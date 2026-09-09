package knowledge

import (
	"embed"
	"fmt"
	"path"
	"sort"
	"strings"
	"sync"

	"github.com/jdziat/open-nitpick/internal/config"
)

// The corpus is files rather than Go literals for the reason the expert
// prompts are: an entry is prose someone reviews, and adding one should be a
// new file rather than an edit to a slice.
//
//go:embed corpus/*.md
var corpusFS embed.FS

// The committed indexes ship with the binary too. A review that had to fetch
// one would need a network for a file that is already known at build time.
//
// A directory rather than a single file: one index per embedding model, chosen
// at run time by SelectIndex from what models.embed names.
//
//go:embed indexes/*.json
var indexFS embed.FS

const indexDir = "indexes"

const corpusDir = "corpus"

var (
	loadOnce sync.Once
	loaded   []Entry
	loadErr  error
)

// Corpus returns every authored entry, by id.
//
// Parsed once. A malformed entry is an error rather than a skip: a corpus that
// quietly drops what it cannot read would answer a retrieval with silence and
// look the same as a corpus with nothing to say.
func Corpus() ([]Entry, error) {
	loadOnce.Do(func() {
		names, err := corpusFS.ReadDir(corpusDir)
		if err != nil {
			loadErr = fmt.Errorf("knowledge: read corpus: %w", err)
			return
		}
		for _, f := range names {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".md") {
				continue
			}
			raw, err := corpusFS.ReadFile(path.Join(corpusDir, f.Name()))
			if err != nil {
				loadErr = fmt.Errorf("knowledge: read %s: %w", f.Name(), err)
				return
			}
			e, err := parseEntry(strings.TrimSuffix(f.Name(), ".md"), string(raw))
			if err != nil {
				loadErr = err
				return
			}
			loaded = append(loaded, e)
		}
		sort.Slice(loaded, func(i, j int) bool { return loaded[i].ID < loaded[j].ID })
	})
	return loaded, loadErr
}

// ForLanguages narrows the corpus to entries that name one of the languages a
// change touches.
//
// A first cut before any vector is compared. Retrieval by similarity alone
// will happily hand a Go reviewer the entry about Python's default arguments,
// because the two describe the same shape of mistake and sit close together in
// any embedding, and a reviewer offered a rule from another language has been
// given a reason to invent a finding.
func ForLanguages(entries []Entry, langs map[string]bool) []Entry {
	if len(langs) == 0 {
		return nil
	}
	var out []Entry
	for _, e := range entries {
		// A generic entry survives any language the change touches, and still
		// needs the change to touch a language this build recognises: a diff
		// of files nobody can name retrieves nothing, generic entries
		// included, which is the behaviour an unknown extension had before
		// generic entries existed.
		if e.Generic() {
			out = append(out, e)
			continue
		}
		for _, l := range e.Languages {
			if langs[l] {
				out = append(out, e)
				break
			}
		}
	}
	return out
}

// ForClasses narrows a pool to entries about the classes a pass publishes.
//
// After the language cut and before any vector comparison, for the same
// reason: a style rule the defect pass cannot act on is not a near miss to be
// ranked, it is a wrong answer, and letting cosine decide means the closest
// wrong answer displaces a right one.
func ForClasses(entries []Entry, allowed map[config.Class]bool) []Entry {
	if len(allowed) == 0 {
		return nil
	}
	var out []Entry
	for _, e := range entries {
		if e.InClasses(allowed) {
			out = append(out, e)
		}
	}
	return out
}
