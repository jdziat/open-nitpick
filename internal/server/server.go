package server

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/nitpick-oss/nitpick/internal/database"
	"github.com/nitpick-oss/nitpick/internal/server/state"
)

// Config is everything a server is built from.
type Config struct {
	// Components are the enabled components. A component that is not here mounts
	// nothing, so its routes are 404.
	Components []Component

	// Providers are the ways to sign in. With none, there is no UI: /ui/ and
	// /auth/ are 404, and only the components' own authentication works.
	Providers []Provider

	// Roles maps an identity to the roles it holds.
	Roles *RoleMap

	Sessions SessionConfig
	Logger   *slog.Logger

	// Assets is the built UI. Nil uses the assets embedded in the binary, which
	// are present only when the UI was built.
	Assets fs.FS

	// State is the memory the server's replicas share: sessions, the CSRF key, and what
	// sign-in providers keep between a redirect and its answer. A server with none has no
	// way to hold a login, and New refuses it. Nil builds one over DB.
	State *state.Store
	DB    *database.DB
}

// Models are the tables the server keeps, for the migration.
func Models() []any { return state.Models() }

// Server is the composed handler.
type Server struct {
	components []Component
	providers  []Provider
	roles      *RoleMap
	sessions   *Sessions
	state      *state.Store
	csrf       *CSRF
	log        *slog.Logger
	mux        *http.ServeMux
	assets     *assets
	crossSite  map[string]bool
	tmpl       *template.Template
}

// New validates the configuration and builds the handler. It refuses what would
// otherwise fail at the first request: a route outside its component's
// prefixes, two components on one path, a role rule that matches everyone.
func New(cfg Config) (*Server, error) {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Roles == nil {
		cfg.Roles = &RoleMap{}
	}
	if cfg.State == nil {
		if cfg.DB == nil {
			return nil, errors.New("a server needs a database to keep its sessions in")
		}
		cfg.State = state.New(cfg.DB)
	}
	csrf, err := NewCSRF(context.Background(), cfg.State)
	if err != nil {
		return nil, fmt.Errorf("the CSRF key: %w", err)
	}
	s := &Server{
		components: cfg.Components,
		providers:  cfg.Providers,
		roles:      cfg.Roles,
		sessions:   NewSessions(cfg.State, cfg.Sessions),
		state:      cfg.State,
		csrf:       csrf,
		log:        cfg.Logger,
		mux:        http.NewServeMux(),
		crossSite:  map[string]bool{},
	}
	var declared []Role
	for _, c := range cfg.Components {
		if d, ok := c.(RoleDeclarer); ok {
			declared = append(declared, d.Roles()...)
		}
	}
	if err := cfg.Roles.Check(declared); err != nil {
		return nil, err
	}
	s.sessions.OnError = func(op string, err error) { s.log.Error("sessions: "+op, "error", err) }
	s.assets = loadAssets(cfg.Assets)
	if err := s.mount(); err != nil {
		return nil, err
	}
	return s, nil
}

// Sessions is the session store, for a test or an operator tool to inspect.
func (s *Server) Sessions() *Sessions { return s.sessions }

// State is the shared memory sign-in providers keep their in-progress sign-ins in.
func (s *Server) State() *state.Store { return s.state }

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.protect(s.mux).ServeHTTP(w, r)
}

func (s *Server) mount() (err error) {
	// ServeMux panics on a pattern it cannot register. A bad component must be
	// an error from New, not a crash of the process.
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("mount: %v", rec)
		}
	}()

	names := map[string]bool{}
	var owned []string
	for _, c := range s.components {
		name := c.Name()
		if name == "" || names[name] {
			return fmt.Errorf("component name %q is empty or repeated", name)
		}
		names[name] = true
		for _, p := range c.Prefixes() {
			if !strings.HasPrefix(p, "/") || !strings.HasSuffix(p, "/") || p == "/" {
				return fmt.Errorf("component %s: prefix %q must begin and end with /, and not be /", name, p)
			}
			for _, o := range owned {
				if strings.HasPrefix(p, o) || strings.HasPrefix(o, p) {
					return fmt.Errorf("component %s: prefix %s overlaps %s", name, p, o)
				}
			}
			owned = append(owned, p)
		}
		for _, r := range c.Routes() {
			if err := s.mountRoute(c, r); err != nil {
				return err
			}
		}
	}

	s.mux.HandleFunc("GET /healthz", s.health)
	s.mux.HandleFunc("HEAD /healthz", s.health)

	if len(s.providers) > 0 {
		if err := s.mountUI(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) mountRoute(c Component, r Route) error {
	if r.Handler == nil {
		return fmt.Errorf("component %s: route %q has no handler", c.Name(), r.Pattern)
	}
	path := r.Pattern
	if _, rest, ok := strings.Cut(r.Pattern, " "); ok {
		path = rest
	}
	// Under a prefix, or the prefix itself without its closing slash: a prefix of
	// "/v1/records/" owns "/v1/records" as well as what is beneath it, as the
	// collection of the things it holds.
	under := false
	for _, p := range c.Prefixes() {
		if strings.HasPrefix(path, p) || path == strings.TrimSuffix(p, "/") {
			under = true
		}
	}
	if !under {
		return fmt.Errorf("component %s: route %q is outside its prefixes %v", c.Name(), r.Pattern, c.Prefixes())
	}
	handler := r.Handler
	if r.Need != "" {
		handler = s.requireRole(r.Need, handler)
	}
	s.mux.Handle(r.Pattern, handler)
	return nil
}

// requireRole is the check a route that names a role gets. The handler does not
// run for a request without a session or without the role.
func (s *Server) requireRole(need Role, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := IdentityFrom(r.Context())
		if !ok {
			http.Error(w, "sign in required", http.StatusUnauthorized)
			return
		}
		if !id.Has(need) {
			http.Error(w, "this account lacks the "+string(need)+" role", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// protect is the layer every request passes through: security headers, the
// session, and CSRF for what changes state.
func (s *Server) protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setSecurityHeaders(w)

		// ServeMux answers a path it would clean with a redirect that keeps the
		// method and the body, so a client that follows it performs the request on
		// a path the server never received. Refusing it here keeps every route's
		// path exactly what the route says, and what a front proxy matched on.
		if unclean(r.URL.Path) {
			http.NotFound(w, r)
			return
		}

		id, secret, signedIn := s.sessions.Lookup(r)
		if signedIn {
			r = r.WithContext(withIdentity(r.Context(), id))
		}

		// A cookie authorises this request, so another site can ask for it with a
		// form. A bearer token cannot be attached by another site, so a request
		// that carries one is not subject to it.
		if signedIn && !safeMethod(r.Method) && r.Header.Get("Authorization") == "" && !s.crossSite[r.URL.Path] {
			if !sameOrigin(r) || !s.csrf.Valid(r, secret) {
				http.Error(w, "the request did not come from this site", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// unclean reports a path with an empty, "." or ".." segment. One trailing slash
// is not unclean: a subtree route ends in it.
func unclean(p string) bool {
	segments := strings.Split(strings.TrimPrefix(p, "/"), "/")
	for i, seg := range segments {
		switch {
		case seg == "." || seg == "..":
			return true
		case seg == "" && i != len(segments)-1:
			return true
		}
	}
	return false
}

// setSecurityHeaders applies to every response, not only pages: a record's JSON
// is as capable of being sniffed as HTML as a page is.
func setSecurityHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Set("Content-Security-Policy", ContentSecurityPolicy)
}

// ContentSecurityPolicy allows nothing but this site's own scripts and styles,
// and no frame, base tag or form target elsewhere. There is no inline script and
// no external request, so text that gets into a page unescaped still cannot run.
const ContentSecurityPolicy = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; font-src 'self'; " +
	"connect-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'"

// health reports every enabled component. The detail of a failure goes to the
// log: it can name a directory or a host, and the probe has no token.
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	ok := true
	lines := make([]string, 0, len(s.components))
	for _, c := range s.components {
		if err := c.Health(ctx); err != nil {
			ok = false
			s.log.Error("component unhealthy", "component", c.Name(), "error", err)
			lines = append(lines, c.Name()+": fail")
			continue
		}
		lines = append(lines, c.Name()+": ok")
	}
	sort.Strings(lines)
	status, head := http.StatusOK, "ok"
	if !ok {
		status, head = http.StatusServiceUnavailable, "unhealthy"
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = fmt.Fprintf(w, "%s\n%s", head, strings.Join(lines, "\n"))
	if len(lines) > 0 {
		_, _ = fmt.Fprintln(w)
	}
}

// safeNext keeps a post-login redirect on this site. Anything that is not a
// path under the UI becomes the UI home: a sign-in that could be sent anywhere is
// an open redirect with a trusted-looking origin. Requiring the "/ui/" prefix
// refuses a scheme, a host, "//" and "/\\"; what is left to refuse is a
// character a header or a parser could read differently.
func safeNext(next string) string {
	const home = "/ui/"
	if !strings.HasPrefix(next, "/ui/") && next != "/ui" {
		return home
	}
	if strings.ContainsFunc(next, func(r rune) bool { return r < 0x20 || r == 0x7f || r == '\\' }) {
		return home
	}
	return next
}
