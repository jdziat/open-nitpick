package server

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"
)

// layoutText is the frame every page sits in. Everything that varies is an
// action of html/template and is escaped for the context it appears in. There is
// no inline script and no inline style, which the Content-Security-Policy
// would refuse anyway.
const layoutText = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}} · nitpick</title>
{{if .CSS}}<link rel="stylesheet" href="/ui/assets/{{.CSS}}">{{end}}
</head>
<body>
<header class="bar">
<a class="brand" href="/ui/">nitpick</a>
<nav aria-label="Pages">{{range .Nav}}<a href="{{.Href}}"{{if .Current}} aria-current="page"{{end}}>{{.Title}}</a>{{end}}</nav>
{{if .SignedIn}}<form class="who" method="post" action="/auth/logout">
<span>{{.Who}}</span>
<input type="hidden" name="csrf_token" value="{{.CSRF}}">
<button type="submit">Sign out</button>
</form>{{end}}
</header>
<main>
<h1>{{.Title}}</h1>
{{template "body" .View}}
</main>
{{if .JS}}<script src="/ui/assets/{{.JS}}" defer></script>{{end}}
</body>
</html>`

const indexBody = `{{if .Data}}<ul class="pages">{{range .Data}}<li><a href="{{.Href}}">{{.Title}}</a> <span class="muted">{{.Component}}</span></li>{{end}}</ul>
{{else}}<p>No pages are available to this account.</p>{{end}}`

const loginBody = `{{if .Data.Message}}<p class="error" role="alert">{{.Data.Message}}</p>{{end}}
{{range .Data.Providers}}{{if .Credentials}}
<form class="login" method="post" action="/auth/{{.ID}}/login">
<h2>{{.Label}}</h2>
<label>Username <input name="username" autocomplete="username" required></label>
<label>Password <input name="password" type="password" autocomplete="current-password" required></label>
<input type="hidden" name="csrf_token" value="{{$.CSRF}}">
<input type="hidden" name="next" value="{{$.Data.Next}}">
<button type="submit">Sign in</button>
</form>
{{else}}
<p><a class="button" href="/auth/{{.ID}}/login?next={{$.Data.NextQuery}}">Sign in with {{.Label}}</a></p>
{{end}}{{end}}`

const stubBody = `<p>The UI was not built into this binary.</p>
<p>Run <code>make ui</code> before <code>go build</code>, or use a release image, which includes it.</p>`

const forbiddenBody = `<p>This account does not hold the role this page needs.</p>`
const notFoundBody = `<p>There is nothing at this address.</p>`

type pageRoute struct {
	component string
	page      Page
	pattern   string
	tmpl      *template.Template
}

type navItem struct {
	Title   string
	Href    string
	Current bool
}

type indexEntry struct {
	Title, Href, Component string
}

type providerEntry struct {
	ID, Label   string
	Credentials bool
}

type loginData struct {
	Message   string
	Providers []providerEntry
	Next      string
	NextQuery string
}

// layoutData is what the layout is executed with.
type layoutData struct {
	Title    string
	CSS, JS  string
	Nav      []navItem
	SignedIn bool
	Who      string
	CSRF     string
	View     View
}

func (s *Server) mountUI() error {
	layout, err := template.New("layout").Parse(layoutText)
	if err != nil {
		return fmt.Errorf("layout: %w", err)
	}
	s.layout = layout

	s.mux.HandleFunc("GET /ui/assets/{name}", s.assets.serve)
	s.mux.HandleFunc("GET /ui/{$}", s.index)
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

	for _, c := range s.components {
		for _, pg := range c.Pages() {
			if strings.HasPrefix(pg.Path, "/") || strings.Contains(pg.Path, "..") {
				return fmt.Errorf("component %s: page path %q must be relative", c.Name(), pg.Path)
			}
			if pg.Data == nil {
				return fmt.Errorf("component %s: page %q has no Data", c.Name(), pg.Path)
			}
			tmpl, err := s.pageTemplate(pg.Body)
			if err != nil {
				return fmt.Errorf("component %s: page %q: %w", c.Name(), pg.Path, err)
			}
			pattern := "GET /ui/" + c.Name() + "/"
			if pg.Path == "" {
				pattern += "{$}"
			} else {
				pattern += pg.Path
			}
			route := pageRoute{component: c.Name(), page: pg, pattern: pattern, tmpl: tmpl}
			s.pages = append(s.pages, route)
			s.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) { s.servePage(w, r, route) })
		}
	}
	return nil
}

// pageTemplate parses the layout and one body into their own set, so a body can
// not see or redefine another page's.
func (s *Server) pageTemplate(body string) (*template.Template, error) {
	t, err := s.layout.Clone()
	if err != nil {
		return nil, err
	}
	if _, err := t.New("body").Parse(body); err != nil {
		return nil, err
	}
	return t, nil
}

// SignIn implements Host.
func (s *Server) SignIn(w http.ResponseWriter, r *http.Request, id Identity, next string) {
	granted := s.roles.Grant(id)
	if err := s.sessions.Start(w, granted); err != nil {
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
	data := loginData{Message: message, Next: next, NextQuery: next}
	for _, p := range s.providers {
		data.Providers = append(data.Providers, providerEntry{ID: p.ID(), Label: p.Label(), Credentials: p.Form() == FormCredentials})
	}
	tmpl, err := s.pageTemplate(loginBody)
	if err != nil {
		s.log.Error("login template", "error", err)
		http.Error(w, "the login page is broken", http.StatusInternalServerError)
		return
	}
	s.write(w, tmpl, status, layoutData{
		Title: "Sign in", CSS: s.assets.css, JS: s.assets.js,
		View: View{Data: data, CSRF: s.csrf.Token(secret)},
	}, "")
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	// protect already checked CSRF for a session that exists.
	s.sessions.End(w, r)
	http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	if !s.assets.built {
		s.stub(w)
		return
	}
	id, ok := IdentityFrom(r.Context())
	if !ok {
		http.Redirect(w, r, "/auth/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
		return
	}
	var entries []indexEntry
	for _, p := range s.pages {
		if p.page.Need == "" || id.Has(p.page.Need) {
			if p.page.Nav {
				entries = append(entries, indexEntry{Title: p.page.Title, Href: pageHref(p), Component: p.component})
			}
		}
	}
	tmpl, err := s.pageTemplate(indexBody)
	if err != nil {
		http.Error(w, "the page is broken", http.StatusInternalServerError)
		return
	}
	s.write(w, tmpl, http.StatusOK, s.frame(r, id, "Home", View{Data: entries, Identity: id}), r.URL.Path)
}

func (s *Server) servePage(w http.ResponseWriter, r *http.Request, route pageRoute) {
	if !s.assets.built {
		s.stub(w)
		return
	}
	id, ok := IdentityFrom(r.Context())
	if !ok {
		http.Redirect(w, r, "/auth/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
		return
	}
	if route.page.Need != "" && !id.Has(route.page.Need) {
		s.simple(w, r, id, http.StatusForbidden, "Not allowed", forbiddenBody)
		return
	}
	data, err := route.page.Data(r)
	switch {
	case errors.Is(err, ErrNotFound):
		s.simple(w, r, id, http.StatusNotFound, "Not found", notFoundBody)
		return
	case err != nil:
		// The text of the error is for the log. It can name a path, and a page
		// that printed it would show it to whoever asked.
		s.log.Error("page", "component", route.component, "path", r.URL.Path, "error", err)
		http.Error(w, "this page could not be produced", http.StatusInternalServerError)
		return
	}
	view := View{Data: data, Identity: id}
	title := route.page.Title
	if route.page.Heading != nil {
		if h := route.page.Heading(data); h != "" {
			title = h
		}
	}
	s.write(w, route.tmpl, http.StatusOK, s.frame(r, id, title, view), r.URL.Path)
}

func (s *Server) simple(w http.ResponseWriter, r *http.Request, id Identity, status int, title, body string) {
	tmpl, err := s.pageTemplate(body)
	if err != nil {
		http.Error(w, title, status)
		return
	}
	s.write(w, tmpl, status, s.frame(r, id, title, View{Identity: id}), r.URL.Path)
}

// frame fills the layout for a signed-in page.
func (s *Server) frame(r *http.Request, id Identity, title string, view View) layoutData {
	var csrf string
	if _, secret, ok := s.sessions.Lookup(r); ok {
		csrf = s.csrf.Token(secret)
	}
	view.CSRF = csrf
	var nav []navItem
	for _, p := range s.pages {
		if !p.page.Nav || (p.page.Need != "" && !id.Has(p.page.Need)) {
			continue
		}
		href := pageHref(p)
		nav = append(nav, navItem{Title: p.page.Title, Href: href, Current: href == r.URL.Path})
	}
	return layoutData{
		Title: title, CSS: s.assets.css, JS: s.assets.js, Nav: nav,
		SignedIn: true, Who: id.Display(), CSRF: csrf, View: view,
	}
}

// pageHref is a page's address. A page whose path has a wildcard is not
// linkable, and the navigation only holds pages that are.
func pageHref(p pageRoute) string {
	if strings.Contains(p.page.Path, "{") {
		return ""
	}
	return "/ui/" + p.component + "/" + p.page.Path
}

// write executes a page into a buffer first, so a template error is a 500 and
// not half a page, then sends it.
func (s *Server) write(w http.ResponseWriter, tmpl *template.Template, status int, data layoutData, _ string) {
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "layout", data); err != nil {
		s.log.Error("render", "error", err)
		http.Error(w, "this page could not be produced", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

// stub is what the UI pages say when the UI was not built into the binary.
func (s *Server) stub(w http.ResponseWriter) {
	tmpl, err := s.pageTemplate(stubBody)
	if err != nil {
		http.Error(w, "The UI was not built.", http.StatusOK)
		return
	}
	s.write(w, tmpl, http.StatusOK, layoutData{Title: "UI not built"}, "")
}
