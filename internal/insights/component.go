package insights

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nitpick-oss/nitpick/internal/server"
)

// Role is what a machine token may do, and the one role a person needs to open
// the pages.
type Role string

// RoleWrite posts reviews and reads. RoleRead only reads. A signed-in person
// needs RoleView, which only reads.
const (
	RoleWrite Role = "write"
	RoleRead  Role = "read"
	RoleView  Role = "insights"
)

// maxBody bounds one posted review: MaxFindings findings at their field limits
// fit well inside it.
const maxBody = 8 << 20

// Token is one machine credential, held as a hash.
type Token struct {
	Name string
	Role Role
	hash [32]byte
}

// NewToken builds a token from its secret.
func NewToken(name string, role Role, secret string) Token {
	return Token{Name: name, Role: role, hash: sha256.Sum256([]byte(secret))}
}

// TokenFromHash builds a token from the hex SHA-256 of its secret.
func TokenFromHash(name string, role Role, hexHash string) (Token, error) {
	raw, err := hex.DecodeString(hexHash)
	if err != nil || len(raw) != sha256.Size {
		return Token{}, errors.New("a token hash must be 64 hex characters")
	}
	t := Token{Name: name, Role: role}
	copy(t.hash[:], raw)
	return t, nil
}

// Component is the insights component of the nitpick server.
type Component struct {
	store  *Store
	tokens []Token
	log    *slog.Logger
	now    func() time.Time
}

// NewComponent builds the component. With no tokens, every machine request is
// refused; people reach the pages and the read routes through a session.
func NewComponent(store *Store, tokens []Token, log *slog.Logger) *Component {
	if log == nil {
		log = slog.Default()
	}
	return &Component{store: store, tokens: tokens, log: log, now: time.Now}
}

// Name implements server.Component.
func (c *Component) Name() string { return "insights" }

// Roles implements server.RoleDeclarer.
func (c *Component) Roles() []server.Role { return []server.Role{server.Role(RoleView)} }

// Prefixes implements server.Component.
func (c *Component) Prefixes() []string { return []string{"/v1/reviews/", "/v1/insights/", "/mcp/"} }

// Health implements server.Component. The store answers from memory, so it is
// healthy while the process is.
func (c *Component) Health(context.Context) error { return nil }

// Routes implements server.Component. Each route takes its own machine
// authentication, and a session as the other way in.
func (c *Component) Routes() []server.Route {
	h := func(f http.HandlerFunc) http.Handler { return f }
	return []server.Route{
		{Pattern: "POST /v1/reviews", Handler: h(c.post)},
		{Pattern: "GET /v1/reviews", Handler: h(c.list)},
		{Pattern: "GET /v1/reviews/{id}", Handler: h(c.one)},
		{Pattern: "GET /v1/insights/overview", Handler: h(c.overview)},
		{Pattern: "GET /v1/insights/hotspots", Handler: h(c.hotspots)},
		{Pattern: "GET /v1/insights/repos", Handler: h(c.repos)},
		{Pattern: "GET /v1/insights/facets", Handler: h(c.facets)},
		{Pattern: "GET /v1/insights/findings", Handler: h(c.findings)},
		{Pattern: "/mcp/", Handler: c.mcpHandler()},
		{Pattern: "/mcp", Handler: c.mcpHandler()},
	}
}

// caller is who a request is: a token's name and role, or a signed-in person.
type caller struct {
	Name  string
	Write bool
}

func (c *Component) authenticate(r *http.Request) (caller, bool) {
	if secret, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok && secret != "" {
		sum := sha256.Sum256([]byte(secret))
		var who caller
		found := false
		for _, t := range c.tokens { // no early exit: time does not say which token matched
			if subtle.ConstantTimeCompare(t.hash[:], sum[:]) == 1 {
				who, found = caller{Name: t.Name, Write: t.Role == RoleWrite}, true
			}
		}
		return who, found
	}
	if id, ok := server.IdentityFrom(r.Context()); ok && id.Has(server.Role(RoleView)) {
		return caller{Name: id.Display()}, true
	}
	return caller{}, false
}

// need answers 401 or 403 and reports false when the request may not proceed.
func (c *Component) need(w http.ResponseWriter, r *http.Request, write bool) (caller, bool) {
	who, ok := c.authenticate(r)
	switch {
	case !ok:
		w.Header().Set("WWW-Authenticate", `Bearer realm="nitpick insights"`)
		http.Error(w, "missing or unknown token", http.StatusUnauthorized)
		return caller{}, false
	case write && !who.Write:
		http.Error(w, "this token may read but not post reviews", http.StatusForbidden)
		return caller{}, false
	}
	return who, true
}

// Submission is what `nitpick review -insights` posts: where the review ran, and
// the report `-format json` prints.
type Submission struct {
	Repo   string    `json:"repo"`
	Ref    string    `json:"ref,omitempty"`
	Commit string    `json:"commit,omitempty"`
	At     time.Time `json:"at,omitempty"`
	Report Report    `json:"report"`
}

// Report is the part of the JSON report this component reads. Fields it does not
// read are ignored, so the report can grow without breaking a poster.
type Report struct {
	Result   string          `json:"result"`
	Complete bool            `json:"complete"`
	Findings []Finding       `json:"findings"`
	Withheld json.RawMessage `json:"withheld,omitempty"`
}

// Review turns a submission into a review for the store.
func (s Submission) Review(source string) Review {
	withheld := 0
	var w []json.RawMessage
	if json.Unmarshal(s.Report.Withheld, &w) == nil {
		withheld = len(w)
	}
	return Review{
		Repo: s.Repo, Ref: s.Ref, Commit: s.Commit, At: s.At,
		Result: s.Report.Result, Complete: s.Report.Complete, Findings: s.Report.Findings,
		Withheld: withheld, Source: source,
	}
}

func (c *Component) post(w http.ResponseWriter, r *http.Request) {
	who, ok := c.need(w, r, true)
	if !ok {
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			http.Error(w, "the review is larger than "+strconv.Itoa(maxBody>>20)+" MiB", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "could not read the request", http.StatusBadRequest)
		return
	}
	var sub Submission
	if err := json.Unmarshal(body, &sub); err != nil {
		http.Error(w, "the body is not a review: "+err.Error(), http.StatusBadRequest)
		return
	}
	kept, err := c.store.Add(r.Context(), sub.Review(who.Name))
	switch {
	case errors.Is(err, ErrInvalid):
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	case err != nil:
		c.log.Error("insights: keep a review", "error", err)
		http.Error(w, "the review could not be kept", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": kept.ID, "findings": len(kept.Findings), "truncated": kept.Truncated})
}

// parseQuery reads the window and repo every read route takes.
func (c *Component) parseQuery(r *http.Request) (Query, error) {
	q := Query{Now: c.now().UTC(), Days: 30, Repo: r.URL.Query().Get("repo")}
	if v := r.URL.Query().Get("days"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > MaxDays {
			return q, fmt.Errorf("days must be a whole number from 1 to %d", MaxDays)
		}
		q.Days = n
	}
	return q, nil
}

// MaxDays is the longest window a query may ask for.
const MaxDays = 365

// ReviewRow is a review in a list: what a table shows without the findings.
type ReviewRow struct {
	ID       string    `json:"id"`
	Repo     string    `json:"repo"`
	Ref      string    `json:"ref,omitempty"`
	Commit   string    `json:"commit,omitempty"`
	Result   string    `json:"result"`
	At       time.Time `json:"at"`
	Complete bool      `json:"complete"`
	Findings int       `json:"findings"`
	Errors   int       `json:"errors"`
	Worst    string    `json:"worst,omitempty"`
	Withheld int       `json:"withheld,omitempty"`
}

func reviewRow(rv Review) ReviewRow {
	row := ReviewRow{ID: rv.ID, Repo: rv.Repo, Ref: rv.Ref, Commit: rv.Commit, Result: rv.Result, At: rv.At, Complete: rv.Complete, Findings: len(rv.Findings), Withheld: rv.Withheld}
	for _, f := range rv.Findings {
		if rank(f.Severity) >= rank("error") {
			row.Errors++
		}
		if rank(f.Severity) > rank(row.Worst) {
			row.Worst = f.Severity
		}
	}
	return row
}

var reviewSorts = sortSpec{allowed: []string{"at", "repo", "findings", "errors", "result"}, def: "at", defDesc: true}

// Reviews lists the reviews of a window, filtered, searched, sorted and paged.
// complete is "", "true" or "false".
func (c *Component) reviewList(v *View, q Query, p ListParams, complete string) Paged[ReviewRow] {
	from, _ := q.window()
	var rows []ReviewRow
	for _, rv := range v.Reviews(Filter{Repo: q.Repo, Since: from, Until: q.Now}) {
		row := reviewRow(rv)
		if complete != "" && strconv.FormatBool(row.Complete) != complete {
			continue
		}
		if !contains(p.Q, row.Repo, row.Ref, row.Commit, row.Result) {
			continue
		}
		rows = append(rows, row)
	}
	sortRows(rows, p, map[string]func(a, b ReviewRow) int{
		"at":       func(a, b ReviewRow) int { return a.At.Compare(b.At) },
		"repo":     func(a, b ReviewRow) int { return strings.Compare(a.Repo, b.Repo) },
		"findings": func(a, b ReviewRow) int { return a.Findings - b.Findings },
		"errors":   func(a, b ReviewRow) int { return a.Errors - b.Errors },
		"result":   func(a, b ReviewRow) int { return strings.Compare(a.Result, b.Result) },
	})
	return paginate(rows, p)
}

// view loads the window a query asks about, or answers 500 and says why in the
// log. ok is false when it has answered. A read that fails is an error and not an
// empty page: a dashboard that quietly shows nothing looks like a team with no
// findings.
func (c *Component) view(w http.ResponseWriter, r *http.Request, q Query) (*View, bool) {
	v, err := c.store.View(r.Context(), q)
	if err != nil {
		c.log.Error("insights: read reviews", "error", err)
		http.Error(w, "the reviews could not be read", http.StatusInternalServerError)
		return nil, false
	}
	return v, true
}

// listRequest authenticates a read route and reads the window and the list
// parameters, answering 401, 403 or 400 itself when it cannot. ok is false when it
// has answered.
func (c *Component) listRequest(w http.ResponseWriter, r *http.Request, spec sortSpec) (q Query, p ListParams, ok bool) {
	if _, ok := c.need(w, r, false); !ok {
		return q, p, false
	}
	q, err := c.parseQuery(r)
	if err == nil {
		p, err = parseList(r, spec)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return q, p, false
	}
	return q, p, true
}

func (c *Component) list(w http.ResponseWriter, r *http.Request) {
	q, p, ok := c.listRequest(w, r, reviewSorts)
	if !ok {
		return
	}
	complete := r.URL.Query().Get("complete")
	if complete != "" && complete != "true" && complete != "false" {
		http.Error(w, "complete must be true or false", http.StatusBadRequest)
		return
	}
	v, ok := c.view(w, r, q)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, c.reviewList(v, q, p, complete))
}

func (c *Component) one(w http.ResponseWriter, r *http.Request) {
	if _, ok := c.need(w, r, false); !ok {
		return
	}
	rv, ok, err := c.store.Review(r.Context(), r.PathValue("id"))
	if err != nil {
		c.log.Error("insights: read a review", "error", err)
		http.Error(w, "the review could not be read", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "no such review", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, rv)
}

func (c *Component) overview(w http.ResponseWriter, r *http.Request) {
	if _, ok := c.need(w, r, false); !ok {
		return
	}
	q, err := c.parseQuery(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	v, ok := c.view(w, r, q)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"overview": v.Overview(q),
		"prior":    v.PriorOverview(q),
		"series":   v.Series(q),
	})
}

func (c *Component) facets(w http.ResponseWriter, r *http.Request) {
	if _, ok := c.need(w, r, false); !ok {
		return
	}
	q, err := c.parseQuery(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	v, ok := c.view(w, r, q)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, v.Facets(q))
}

var repoSorts = sortSpec{allowed: []string{"repo", "reviews", "findings", "errors", "last"}, def: "findings", defDesc: true}

func (c *Component) repos(w http.ResponseWriter, r *http.Request) {
	q, p, ok := c.listRequest(w, r, repoSorts)
	if !ok {
		return
	}
	v, ok := c.view(w, r, q)
	if !ok {
		return
	}
	var rows []RepoRow
	for _, row := range v.Repos(q) {
		if contains(p.Q, row.Repo) {
			rows = append(rows, row)
		}
	}
	sortRows(rows, p, map[string]func(a, b RepoRow) int{
		"repo":     func(a, b RepoRow) int { return strings.Compare(a.Repo, b.Repo) },
		"reviews":  func(a, b RepoRow) int { return a.Reviews - b.Reviews },
		"findings": func(a, b RepoRow) int { return a.Findings - b.Findings },
		"errors":   func(a, b RepoRow) int { return a.Errors - b.Errors },
		"last":     func(a, b RepoRow) int { return a.Last.Compare(b.Last) },
	})
	writeJSON(w, http.StatusOK, paginate(rows, p))
}

var hotspotSorts = sortSpec{allowed: []string{"score", "findings", "path", "repo", "delta", "worst", "last"}, def: "score", defDesc: true}

func (c *Component) hotspots(w http.ResponseWriter, r *http.Request) {
	q, p, ok := c.listRequest(w, r, hotspotSorts)
	if !ok {
		return
	}
	trend := Trend(r.URL.Query().Get("trend"))
	if trend != "" && trend != TrendUp && trend != TrendDown && trend != TrendFlat {
		http.Error(w, "trend must be up, down or flat", http.StatusBadRequest)
		return
	}
	floor := r.URL.Query().Get("min_severity")
	if floor != "" && rank(floor) < 0 {
		http.Error(w, "min_severity must be one of "+strings.Join(Severities, ", "), http.StatusBadRequest)
		return
	}
	v, ok := c.view(w, r, q)
	if !ok {
		return
	}
	var rows []Hotspot
	for _, h := range v.Hotspots(q) {
		if trend != "" && h.Trend != trend || rank(h.Worst) < rank(floor) || !contains(p.Q, h.Path, h.Repo) {
			continue
		}
		rows = append(rows, h)
	}
	sortRows(rows, p, map[string]func(a, b Hotspot) int{
		"score":    func(a, b Hotspot) int { return cmpFloat(a.Score, b.Score) },
		"findings": func(a, b Hotspot) int { return a.Findings - b.Findings },
		"path":     func(a, b Hotspot) int { return strings.Compare(a.Path, b.Path) },
		"repo":     func(a, b Hotspot) int { return strings.Compare(a.Repo, b.Repo) },
		"delta":    func(a, b Hotspot) int { return a.Delta - b.Delta },
		"worst":    func(a, b Hotspot) int { return rank(a.Worst) - rank(b.Worst) },
		"last":     func(a, b Hotspot) int { return a.Last.Compare(b.Last) },
	})
	writeJSON(w, http.StatusOK, paginate(rows, p))
}

var findingSorts = sortSpec{allowed: []string{"at", "severity", "path", "repo", "class", "title"}, def: "at", defDesc: true}

func (c *Component) findings(w http.ResponseWriter, r *http.Request) {
	q, p, ok := c.listRequest(w, r, findingSorts)
	if !ok {
		return
	}
	v := r.URL.Query()
	fq := FindingQuery{Query: q, Path: v.Get("path"), Class: v.Get("class"), MinSeverity: v.Get("min_severity"), Q: p.Q}
	if fq.MinSeverity != "" && rank(fq.MinSeverity) < 0 {
		http.Error(w, "min_severity must be one of "+strings.Join(Severities, ", "), http.StatusBadRequest)
		return
	}
	view, ok := c.view(w, r, q)
	if !ok {
		return
	}
	rows := view.AllFindings(fq)
	sortRows(rows, p, map[string]func(a, b FindingRow) int{
		"at":       func(a, b FindingRow) int { return a.At.Compare(b.At) },
		"severity": func(a, b FindingRow) int { return rank(a.Severity) - rank(b.Severity) },
		"path":     func(a, b FindingRow) int { return strings.Compare(a.Path, b.Path) },
		"repo":     func(a, b FindingRow) int { return strings.Compare(a.Repo, b.Repo) },
		"class":    func(a, b FindingRow) int { return strings.Compare(a.Class, b.Class) },
		"title":    func(a, b FindingRow) int { return strings.Compare(a.Title, b.Title) },
	})
	writeJSON(w, http.StatusOK, paginate(rows, p))
}

func cmpFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
