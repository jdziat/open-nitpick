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

	"github.com/jdziat/open-nitpick/internal/config"
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
	//
	// The literal "any" is the only way to write a rule that crosses
	// languages, and it has to be typed: an empty list used to mean the same
	// thing, which made a forgotten key indistinguishable from a deliberate
	// claim about every language. See AnyLanguage.
	Languages []string

	// Classes the entry is about, from the review taxonomy.
	//
	// What routes it. A style rule handed to the defect pass is the dilution
	// the generation scope exists to prevent, arriving as reference material
	// instead of as a prompt, and a correctness rule handed to the style pass
	// is an embedding call spent on a reviewer that cannot act on it.
	Classes []config.Class

	// Frameworks and Versions are applicability a reader and the model judge
	// for themselves. Neither filters anything today: no caller knows the
	// versions a change runs under, and a filter fed a guess would silence
	// entries on the strength of it. They are rendered beside the entry so
	// the model can decline it, which is what the prompt already asks for.
	Frameworks []string
	Versions   string

	// Applies is the machine-checkable half of Versions: clauses that must
	// hold for the entry to be offered at all.
	//
	// Versions stays prose the model judges, because most applicability is
	// prose ("only when the handler is registered before Serve"). This is for
	// the part a repository can be asked: a claim about time.After's timers is
	// about Go before 1.23, and go.mod says which side of that a repository
	// is on. Empty means the entry is offered to everything, which is what
	// every entry did before this existed.
	Applies []Constraint

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
		case "classes":
			for _, c := range splitList(value) {
				class, ok := config.Class(c).Normalize()
				if !ok {
					return Entry{}, fmt.Errorf("knowledge: %s: %q is not a review class; one of %s",
						id, c, strings.Join(config.ClassNames(), ", "))
				}
				e.Classes = append(e.Classes, class)
			}
		case "frameworks":
			e.Frameworks = splitList(value)
		case "versions":
			e.Versions = value
		case "applies":
			c, err := parseApplies(id, value)
			if err != nil {
				return Entry{}, err
			}
			e.Applies = c
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
		return fmt.Errorf("knowledge: %s: no languages; write `languages: [any]` if the rule really crosses all of them", e.ID)
	case len(e.Classes) == 0:
		return fmt.Errorf("knowledge: %s: no classes; an entry nothing routes is an entry nothing retrieves", e.ID)
	}
	return nil
}

// AnyLanguage is the language list of a rule that crosses languages.
//
// Spelled rather than implied. An empty list meant this once, which made a
// forgotten `languages:` key and a deliberate claim about every language the
// same entry, and the corpus is the one place here where prose becomes
// evidence: a claim that broad should cost someone typing it.
const AnyLanguage = "any"

// Generic reports whether the entry claims to cross languages.
func (e Entry) Generic() bool {
	for _, l := range e.Languages {
		if strings.EqualFold(l, AnyLanguage) {
			return true
		}
	}
	return false
}

// InClasses reports whether the entry is about any of these classes.
func (e Entry) InClasses(allowed map[config.Class]bool) bool {
	for _, c := range e.Classes {
		if allowed[c] {
			return true
		}
	}
	return false
}

// Text is what gets embedded: the title and the body, without the citation.
//
// The source and date are for a reader rather than a nearest-neighbour search,
// and a URL in the embedded text pulls entries together by the host they cite
// rather than by what they say.
func (e Entry) Text() string { return e.Title + "\n\n" + e.Body }
