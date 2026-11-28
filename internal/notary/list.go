package notary

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/nitpick-oss/nitpick/internal/redact"
	"github.com/nitpick-oss/nitpick/internal/server"
	"github.com/nitpick-oss/nitpick/internal/wire"
)

// authoritative is who may list and open records: a token with the highest role,
// or a signed-in session that holds it. Provisional and read tokens write and
// read single records by hash, which they have to know; they are not shown what
// else the notary holds.
func (c *Component) authoritative(r *http.Request) (allowed, known bool) {
	if tok, ok := c.auth(r); ok {
		return tok.Role == RoleAuthoritative, true
	}
	if id, ok := server.IdentityFrom(r.Context()); ok {
		return id.Has(server.Role(RoleAuthoritative)), true
	}
	return false, false
}

// list serves GET /v1/records: the summaries as stored, for machines.
func (c *Component) list(w http.ResponseWriter, r *http.Request) { c.listRecords(w, r, false) }

// viewList serves GET /v1/records/view: the same listing with each summary run
// through the scrubber the reviewer applies to what it prints, for the web app. A
// credential that ended up in a model name or a source label is then not shown to
// whoever opens the page.
func (c *Component) viewList(w http.ResponseWriter, r *http.Request) { c.listRecords(w, r, true) }

func (c *Component) listRecords(w http.ResponseWriter, r *http.Request, scrub bool) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	allowed, known := c.authoritative(r)
	switch {
	case !known:
		http.Error(w, "missing or unknown token", http.StatusUnauthorized)
		return
	case !allowed:
		http.Error(w, "only the authoritative role may list records", http.StatusForbidden)
		return
	}
	lister, ok := c.store.(wire.Lister)
	if !ok {
		http.Error(w, "this notary's store cannot list records", http.StatusNotImplemented)
		return
	}
	q, err := listQuery(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	page, next, err := lister.List(r.Context(), q)
	switch {
	case errors.Is(err, wire.ErrBadQuery):
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	case err != nil:
		storeFault(w, r, err)
		return
	}
	if page == nil {
		page = []wire.Summary{} // an empty store is "records": [], not null
	}
	if scrub {
		for i := range page {
			page[i].Model, page[i].Tool = redact.Secrets(page[i].Model), redact.Secrets(page[i].Tool)
			page[i].Provider, page[i].Source = redact.Secrets(page[i].Provider), redact.Secrets(page[i].Source)
			page[i].Effort = redact.Secrets(page[i].Effort)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(struct {
		Records []wire.Summary `json:"records"`
		Next    string         `json:"next_cursor"`
	}{page, next})
}

// listQuery reads the filters and page of a listing from a request. A value it
// cannot read is the caller's mistake, said as one, and not read as "no filter".
func listQuery(r *http.Request) (wire.ListQuery, error) {
	v := r.URL.Query()
	q := wire.ListQuery{Model: v.Get("model"), Tool: v.Get("version"), Cursor: v.Get("cursor")}
	if s := v.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 {
			return q, errors.New("limit must be a positive number")
		}
		q.Limit = n
	}
	for name, dst := range map[string]*time.Time{"since": &q.Since, "until": &q.Until} {
		s := v.Get(name)
		if s == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return q, errors.New(name + " must be an RFC 3339 time, such as 2026-01-02T15:04:05Z")
		}
		*dst = t
	}
	return q, nil
}
