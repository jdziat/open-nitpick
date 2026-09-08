// Package knowledge holds what a reviewing model was not necessarily taught.
//
// A model knows what its training covered. Antipatterns, standard-library
// contracts and language rules are covered unevenly and dated at whatever the
// cutoff was, so a reviewer can miss a defect that a person with one specific
// fact in hand would catch at a glance. The entries here are that fact,
// written down, retrieved by similarity to the change and put in front of the
// model beside the diff.
//
// Every entry is a claim this repository publishes. That is why each names the
// source it came from and the day that source was read: an entry asserting
// something about Go's memory model is worth what its citation is worth, and a
// reader who doubts it is owed somewhere to go.
package knowledge

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Entry is one thing worth knowing, and where it came from.
type Entry struct {
	// ID is the stable identifier, the file's basename. It appears in logs and
	// in the disclosure a review publishes, and never changes once shipped:
	// a review that says it consulted an entry has to be checkable against it.
	ID string

	// Title is the one line a reranker reads. It states the rule rather than
	// naming the topic, because "a Context belongs in the first argument" can
	// be judged against a diff and "contexts" cannot.
	Title string

	// Languages the entry applies to, as the extensions in a diff resolve to.
	// Empty means every language, which almost nothing should be.
	Languages []string

	// Source is where the claim came from: a specification, a standard
	// library's own documentation, a published advisory.
	Source string

	// Checked is the day Source was read. An entry is only as current as this,
	// and a reader comparing it to today knows how much to trust it.
	Checked time.Time

	// Body is the entry itself, in markdown, without the front matter.
	Body string
}

// frontMatter splits a YAML-ish header from the body.
//
// A hand-rolled parser rather than a yaml dependency for the same reason the
// expert prompts are files: the header is five keys, the corpus is checked by
// a test that reads every entry, and a malformed one fails the build rather
// than reaching a model.
var frontMatter = regexp.MustCompile(`(?s)\A---\n(.*?)\n---\n(.*)\z`)

// parseEntry reads one corpus file.
func parseEntry(id, raw string) (Entry, error) {
	m := frontMatter.FindStringSubmatch(raw)
	if m == nil {
		return Entry{}, fmt.Errorf("knowledge: %s: no front matter", id)
	}

	e := Entry{ID: id, Body: strings.TrimSpace(m[2])}
	for _, line := range strings.Split(m[1], "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return Entry{}, fmt.Errorf("knowledge: %s: %q is not key: value", id, line)
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)

		switch key {
		case "title":
			e.Title = value
		case "languages":
			e.Languages = splitList(value)
		case "source":
			e.Source = value
		case "checked":
			t, err := time.Parse("2006-01-02", value)
			if err != nil {
				return Entry{}, fmt.Errorf("knowledge: %s: checked: %w", id, err)
			}
			e.Checked = t
		default:
			return Entry{}, fmt.Errorf("knowledge: %s: unknown key %q", id, key)
		}
	}
	return e, e.validate()
}

// splitList reads "[go, rust]" or "go, rust".
func splitList(v string) []string {
	v = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(v, "["), "]"))
	if v == "" {
		return nil
	}
	out := strings.Split(v, ",")
	for i := range out {
		out[i] = strings.TrimSpace(out[i])
	}
	return out
}

// validate refuses an entry that cannot carry its own weight.
//
// The source and the date are not bookkeeping. An entry without them is an
// assertion with nobody behind it, put in front of a model that will treat it
// as true, and this corpus is the one place in the tool where prose becomes
// evidence rather than instruction.
func (e Entry) validate() error {
	switch {
	case e.Title == "":
		return fmt.Errorf("knowledge: %s: no title", e.ID)
	case e.Source == "":
		return fmt.Errorf("knowledge: %s: no source", e.ID)
	case e.Checked.IsZero():
		return fmt.Errorf("knowledge: %s: no checked date", e.ID)
	case e.Body == "":
		return fmt.Errorf("knowledge: %s: no body", e.ID)
	case len(e.Languages) == 0:
		return fmt.Errorf("knowledge: %s: no languages; an entry for every language is a claim about every language", e.ID)
	}
	return nil
}

// Text is what gets embedded: the title and the body, without the citation.
//
// The source and date are for a reader rather than a nearest-neighbour search,
// and a URL in the embedded text pulls entries together by the host they cite
// rather than by what they say.
func (e Entry) Text() string { return e.Title + "\n\n" + e.Body }
