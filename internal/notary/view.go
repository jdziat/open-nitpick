package notary

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/nitpick-oss/nitpick/internal/redact"
)

// unusableMark is how the review engine marks a stored response that the caller
// rejected or retried. It is a prefix of the response, and a usable response is a
// JSON object, so the two cannot be mistaken. internal/review keeps the same
// constant, and a test holds the two together.
const unusableMark = "!"

// AnswerView is one stored answer as the web app shows it.
type AnswerView struct {
	Request  string `json:"request"`  // the hash of the request the answer is for
	Slot     string `json:"slot"`     // which answer to that request: identical requests are separate answers
	Text     string `json:"text"`     // what the model answered, scrubbed
	Unusable bool   `json:"unusable"` // a reply the review rejected or retried
}

// RecordView is a record as the web app shows it. It holds what a record holds
// and not the prompts or the stage that asked, which a record does not keep.
type RecordView struct {
	Hash      string       `json:"hash"`
	Tool      string       `json:"tool"`
	Provider  string       `json:"provider"`
	Model     string       `json:"model"`
	Source    string       `json:"source"`
	Effort    string       `json:"effort"`
	Complete  bool         `json:"complete"`
	Created   time.Time    `json:"created"`
	Clears    int          `json:"clears"`
	ClearedBy []string     `json:"cleared_by"`
	Answers   []AnswerView `json:"answers"`
}

// viewRecord serves GET /v1/records/view/{hash}: one record for the web app. What
// a model answered is shown through the scrubber the reviewer applies to what it
// prints, so a credential a model echoed from the diff is not displayed to whoever
// opens the page. The record itself is not changed: replay needs the answer as it
// was given.
func (c *Component) viewRecord(w http.ResponseWriter, r *http.Request) {
	allowed, known := c.authoritative(r)
	switch {
	case !known:
		http.Error(w, "missing or unknown token", http.StatusUnauthorized)
		return
	case !allowed:
		http.Error(w, "only the authoritative role may open records", http.StatusForbidden)
		return
	}
	hash := r.PathValue("hash")
	if !hashRE.MatchString(hash) {
		http.NotFound(w, r)
		return
	}
	rec, found, err := c.store.Get(r.Context(), hash)
	if err != nil {
		storeFault(w, r, err)
		return
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	view := RecordView{
		Hash: rec.Hash, Tool: redact.Secrets(rec.Tool), Provider: redact.Secrets(rec.Provider), Model: redact.Secrets(rec.Model),
		Source: redact.Secrets(rec.Source), Effort: redact.Secrets(rec.Effort), Complete: rec.Complete, Created: rec.Created,
		Clears: rec.Clears, ClearedBy: []string{}, Answers: []AnswerView{},
	}
	for _, by := range rec.ClearedBy {
		view.ClearedBy = append(view.ClearedBy, redact.Secrets(by))
	}
	keys := make([]string, 0, len(rec.Answers))
	for k := range rec.Answers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		text, unusable := strings.CutPrefix(rec.Answers[k], unusableMark)
		request, slot, _ := strings.Cut(k, "#")
		view.Answers = append(view.Answers, AnswerView{Request: redact.Secrets(request), Slot: redact.Secrets(slot), Text: redact.Secrets(text), Unusable: unusable})
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(view)
}
