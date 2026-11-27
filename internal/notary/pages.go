package notary

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/nitpick-oss/nitpick/internal/redact"
	"github.com/nitpick-oss/nitpick/internal/server"
	"github.com/nitpick-oss/nitpick/internal/wire"
)

// Pages implements server.Component: the record list and the record view. Both
// need the authoritative role, and both only read: the pages never change or
// delete a record.
func (c *Component) Pages() []server.Page {
	need := server.Role(RoleAuthoritative)
	return []server.Page{
		{Path: "", Title: "Records", Nav: true, Need: need, Body: listBody, Data: c.listPage},
		{Path: "records/{hash}", Title: "Record", Need: need, Body: recordBody, Data: c.recordPage},
	}
}

const listBody = `<form class="filters" method="get" action="/ui/notary/">
<label>Model <input name="model" value="{{.Data.Model}}"></label>
<label>Version <input name="version" value="{{.Data.Version}}"></label>
<label>Since <input name="since" value="{{.Data.Since}}" placeholder="2026-01-02T15:04:05Z"></label>
<label>Until <input name="until" value="{{.Data.Until}}" placeholder="2026-01-03T00:00:00Z"></label>
<button type="submit">Filter</button>
</form>
{{if .Data.Error}}<p class="error" role="alert">{{.Data.Error}}</p>{{end}}
{{if .Data.Records}}
<table class="records">
<thead><tr><th>Created</th><th>Model</th><th>Version</th><th>Status</th><th>Answers</th><th>Source</th><th>Record</th></tr></thead>
<tbody>
{{range .Data.Records}}<tr>
<td><time datetime="{{.Created}}">{{.Created}}</time></td><td>{{.Model}}</td><td>{{.Tool}}</td>
<td>{{.Status}}</td><td>{{.Answers}} of {{.Batches}}</td><td>{{.Source}}</td>
<td><a href="/ui/notary/records/{{.Hash}}"><code>{{.Short}}</code></a></td>
</tr>{{end}}
</tbody>
</table>
{{else}}<p>{{if .Data.Filtered}}No record matches this filter.{{else}}The notary holds no records.{{end}}</p>{{end}}
{{if .Data.Next}}<p><a href="{{.Data.NextHref}}">Older records</a></p>{{end}}`

const recordBody = `<dl class="facts">
<dt>Record</dt><dd><code>{{.Data.Hash}}</code></dd>
<dt>Status</dt><dd>{{.Data.Status}}</dd>
<dt>Model</dt><dd>{{.Data.Provider}} / {{.Data.Model}}</dd>
<dt>Reasoning level</dt><dd>{{if .Data.Effort}}{{.Data.Effort}}{{else}}not recorded{{end}}</dd>
<dt>Tool version</dt><dd>{{.Data.Tool}}</dd>
<dt>Created</dt><dd><time datetime="{{.Data.Created}}">{{.Data.Created}}</time></dd>
<dt>Written by</dt><dd>{{.Data.Source}}</dd>
<dt>Cleared</dt><dd>{{.Data.Clears}} time{{if ne .Data.Clears 1}}s{{end}}{{if .Data.ClearedBy}}, by {{range $i, $by := .Data.ClearedBy}}{{if $i}}, {{end}}{{$by}}{{end}}{{end}}</dd>
</dl>
<p class="muted">The record holds the model's answers, keyed by a hash of the request they answer and a slot. It does not hold the prompts or which stage asked, so those are not shown. An answer marked unusable was a reply the review rejected or retried; it is served only from a complete record.</p>
<h2>Answers ({{len .Data.Answers}})</h2>
{{range .Data.Answers}}<section class="answer">
<h3>Request <code>{{.Request}}</code>{{if .Slot}}, answer {{.Slot}}{{end}}{{if .Unusable}} <span class="tag">unusable</span>{{end}}</h3>
<pre>{{.Text}}</pre>
</section>{{else}}<p>The record holds no answers.</p>{{end}}`

type listRow struct {
	wire.Summary
	Status string
	Short  string
}

type listView struct {
	Model, Version, Since, Until string
	Filtered                     bool
	Records                      []listRow
	Next, NextHref               string
	Error                        string
}

func status(complete bool) string {
	if complete {
		return "replays"
	}
	return "resumes"
}

func (c *Component) listPage(r *http.Request) (any, error) {
	lister, ok := c.store.(wire.Lister)
	if !ok {
		return listView{Error: "this notary's store cannot list records"}, nil
	}
	q, err := listQuery(r)
	v := r.URL.Query()
	view := listView{Model: v.Get("model"), Version: v.Get("version"), Since: v.Get("since"), Until: v.Get("until")}
	view.Filtered = view.Model != "" || view.Version != "" || view.Since != "" || view.Until != ""
	if err != nil {
		// The person typed it, so the page says what is wrong with it and shows
		// the form again; it is not a server error.
		view.Error = err.Error()
		return view, nil
	}
	page, next, err := lister.List(r.Context(), q)
	if err != nil {
		return nil, err
	}
	for _, s := range page {
		s.Model, s.Tool, s.Provider, s.Source = redact.Secrets(s.Model), redact.Secrets(s.Tool), redact.Secrets(s.Provider), redact.Secrets(s.Source)
		view.Records = append(view.Records, listRow{Summary: s, Status: status(s.Complete), Short: s.Hash[:12]})
	}
	if next != "" {
		view.Next = next
		more := r.URL.Query()
		more.Set("cursor", next)
		view.NextHref = "/ui/notary/?" + more.Encode()
	}
	return view, nil
}

type answerView struct {
	Request  string // the hash of the request the answer is for
	Slot     string // which answer to that request: identical requests are separate answers
	Text     string
	Unusable bool
}

type recordView struct {
	Hash, Tool, Provider, Model, Source, Effort, Status string
	Created                                             time.Time
	Clears                                              int
	ClearedBy                                           []string
	Answers                                             []answerView
}

// unusableMark is how the review engine marks a stored response that the caller
// rejected or retried. It is a prefix of the response, and a usable response is a
// JSON object, so the two cannot be mistaken. internal/review keeps the same
// constant, and a test holds the two together.
const unusableMark = "!"

func (c *Component) recordPage(r *http.Request) (any, error) {
	hash := r.PathValue("hash")
	if !hashRE.MatchString(hash) {
		return nil, server.ErrNotFound
	}
	rec, found, err := c.store.Get(r.Context(), hash)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, server.ErrNotFound
	}
	view := recordView{
		Hash: rec.Hash, Tool: redact.Secrets(rec.Tool), Provider: redact.Secrets(rec.Provider), Model: redact.Secrets(rec.Model),
		Source: redact.Secrets(rec.Source), Effort: redact.Secrets(rec.Effort), Created: rec.Created, Clears: rec.Clears,
	}
	for _, by := range rec.ClearedBy {
		view.ClearedBy = append(view.ClearedBy, redact.Secrets(by))
	}
	if rec.Complete {
		view.Status = "complete: a review with these inputs is replayed from it"
	} else {
		view.Status = "incomplete: a review with these inputs resumes, asking only for the " +
			"answers it lacks"
	}
	keys := make([]string, 0, len(rec.Answers))
	for k := range rec.Answers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		text, unusable := strings.CutPrefix(rec.Answers[k], unusableMark)
		request, slot, _ := strings.Cut(k, "#")
		// What a model answered is shown through the scrubber the reviewer applies to
		// what it prints, so a credential a model echoed from the diff is not
		// displayed to whoever opens the page. The record itself is not changed:
		// replay needs the answer as it was given.
		view.Answers = append(view.Answers, answerView{
			Request: redact.Secrets(request), Slot: redact.Secrets(slot), Text: redact.Secrets(text), Unusable: unusable,
		})
	}
	return view, nil
}
