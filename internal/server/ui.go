package server

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"
)

//go:embed templates/*.html
var templateFS embed.FS

// templates are parsed once, at start-up, so a broken one is an error from New and
// not a 500 for the first person who signs in. Everything that varies is an action
// of html/template and is escaped for the context it appears in. The pages hold no
// inline script and no inline style, which the Content-Security-Policy would
// refuse anyway.
func parseTemplates() (*template.Template, error) {
	return template.ParseFS(templateFS, "templates/*.html")
}

type providerEntry struct {
	ID, Label   string
	Credentials bool
}

// pageData is what the login and shell templates are executed with.
type pageData struct {
	CSS, JS, Theme string // the built files, by their hashed names
	Session        string // the shell's session JSON
	Message        string
	Providers      []providerEntry
	CSRF           string
	Next           string
	NextQuery      string
}

// SessionInfo is what the web app is told about whoever is signed in. It is the
// whole of what the page knows about them: the roles decide what the app offers,
// and the API routes enforce them again on every request.
type SessionInfo struct {
	Name       string   `json:"name"`
	Provider   string   `json:"provider"`
	Roles      []Role   `json:"roles"`
	Components []string `json:"components"`
	CSRF       string   `json:"csrf"`
}

func (s *Server) mountUI() error {
	t, err := parseTemplates()
	if err != nil {
		return fmt.Errorf("templates: %w", err)
	}
	s.tmpl = t

	s.mux.HandleFunc("GET /ui/assets/{name}", s.assets.serve)
	s.mux.HandleFunc("GET /ui/{path...}", s.shell)
	s.mux.HandleFunc("GET /auth/login", s.loginPage)
	s.mux.HandleFunc("POST /auth/logout", s.logout)

	ids := map[string]bool{}
	for _, p := range s.providers {
		if p.ID() == "" || ids[p.ID()] || strings.ContainsAny(p.ID(), "/ ?#") || p.ID() == "login" || p.ID() == "logout" {
			return fmt.Errorf("provider id %q is empty, reserved or repeated", p.ID())
		}
		ids[p.ID()] = true
		p.Mount(s.mux, s)
		for _, path := range p.CrossSitePosts() {
			s.crossSite[path] = true
		}
	}
	return nil
}

// SignIn implements Host.
func (s *Server) SignIn(w http.ResponseWriter, r *http.Request, id Identity, next string) {
	granted := s.roles.Grant(id)
	if err := s.sessions.Start(r.Context(), w, granted); err != nil {
		s.log.Error("start session", "error", err)
		http.Error(w, "could not start a session", http.StatusInternalServerError)
		return
	}
	s.log.Info("signed in", "provider", granted.Provider, "subject", granted.Subject, "roles", granted.Roles)
	http.Redirect(w, r, safeNext(next), http.StatusSeeOther)
}

// CSRF implements Host.
func (s *Server) CSRF() *CSRF { return s.csrf }

// LoginFailed implements Host.
func (s *Server) LoginFailed(w http.ResponseWriter, r *http.Request, status int, message string) {
	s.renderLogin(w, r, status, message, r.FormValue("next"))
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := IdentityFrom(r.Context()); ok {
		http.Redirect(w, r, safeNext(r.URL.Query().Get("next")), http.StatusSeeOther)
		return
	}
	s.renderLogin(w, r, http.StatusOK, "", r.URL.Query().Get("next"))
}

func (s *Server) renderLogin(w http.ResponseWriter, r *http.Request, status int, message, next string) {
	secret, err := s.csrf.PreSession(w, r)
	if err != nil {
		http.Error(w, "could not prepare the form", http.StatusInternalServerError)
		return
	}
	next = safeNext(next)
	data := s.page()
	data.Message, data.Next, data.NextQuery, data.CSRF = message, next, next, s.csrf.Token(secret)
	for _, p := range s.providers {
		data.Providers = append(data.Providers, providerEntry{ID: p.ID(), Label: p.Label(), Credentials: p.Form() == FormCredentials})
	}
	s.write(w, "login.html", status, data)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	// protect already checked CSRF for a session that exists.
	s.sessions.End(w, r)
	http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
}

// shell answers every GET under /ui/ with the web app's page, once the person is
// signed in. The app routes inside it. What a person may see is decided by the API
// routes the app calls, not by this page, which holds no data.
func (s *Server) shell(w http.ResponseWriter, r *http.Request) {
	if !s.assets.built {
		s.stub(w)
		return
	}
	id, ok := IdentityFrom(r.Context())
	if !ok {
		http.Redirect(w, r, "/auth/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
		return
	}
	var csrf string
	if _, secret, ok := s.sessions.Lookup(r); ok {
		csrf = s.csrf.Token(secret)
	}
	info := SessionInfo{Name: id.Display(), Provider: id.Provider, Roles: id.Roles, Components: s.componentNames(), CSRF: csrf}
	if info.Roles == nil {
		info.Roles = []Role{}
	}
	raw, err := json.Marshal(info)
	if err != nil {
		s.log.Error("encode the session", "error", err)
		http.Error(w, "this page could not be produced", http.StatusInternalServerError)
		return
	}
	data := s.page()
	data.Session = string(raw)
	s.write(w, "shell.html", http.StatusOK, data)
}

func (s *Server) componentNames() []string {
	names := make([]string, 0, len(s.components))
	for _, c := range s.components {
		names = append(names, c.Name())
	}
	return names
}

// page is the data every page starts from: the built files it loads.
func (s *Server) page() pageData {
	return pageData{CSS: s.assets.css, JS: s.assets.js, Theme: s.assets.theme}
}

// write executes a template into a buffer first, so a template error is a 500 and
// not half a page, then sends it.
func (s *Server) write(w http.ResponseWriter, name string, status int, data pageData) {
	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		s.log.Error("render", "template", name, "error", err)
		http.Error(w, "this page could not be produced", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

// stub is what the UI pages say when the UI was not built into the binary. It
// loads nothing, since there is nothing built to load.
func (s *Server) stub(w http.ResponseWriter) {
	s.write(w, "stub.html", http.StatusOK, pageData{})
}
