package evals

import "github.com/jdziat/open-nitpick/internal/config"

// KnowledgeFixtures is the knowledge corpus: defects a reviewer catches when
// it holds one specific fact, and plausibly misses without it.
//
// Every plant here has a matching entry in internal/knowledge/corpus. That is
// the point and also the caution: this corpus measures whether retrieval puts
// the right entry in front of the model, not whether the corpus covers the
// defects a repository has. A gain here is a claim about retrieval, and
// nothing more. Rule 15 of docs/measurement.md applies: it is neither tuning
// nor held-out, it is re-runnable, and it is outside AllFixtures.
//
// Six of the twelve are clean controls whose code resembles a plant closely
// enough to attract the same entry. Without them a gain would be
// indistinguishable from a reviewer that reports whatever it was shown, which
// is the failure mode this feature has to be measured against rather than
// assumed past.
func KnowledgeFixtures() []Fixture {
	return []Fixture{
		knowDeferInLoopFixture(),
		knowCleanDeferInFuncFixture(),
		knowTimeAfterLeakFixture(),
		knowCleanTimerStoppedFixture(),
		knowRowsErrFixture(),
		knowCleanRowsErrCheckedFixture(),
		knowMutableDefaultFixture(),
		knowCleanNoneDefaultFixture(),
		knowPipefailFixture(),
		knowCleanPipefailSetFixture(),
		knowNilMapWriteFixture(),
		knowCleanMapMadeFixture(),
	}
}

// Knowledge reports whether a fixture is in the knowledge corpus.
func Knowledge(fixture string) bool {
	for _, f := range KnowledgeFixtures() {
		if f.Name == fixture {
			return true
		}
	}
	return false
}

const knowGoMod = "module example.com/svc\n\ngo 1.22\n"

// A defer inside a loop holds every descriptor until the function returns.
func knowDeferInLoopFixture() Fixture {
	return Fixture{
		Name: "know-go-defer-in-loop",
		Base: map[string]string{"go.mod": knowGoMod, "ingest/read.go": `package ingest

import "os"

// Sizes reports the size of every path.
func Sizes(paths []string) (map[string]int64, error) {
	out := map[string]int64{}
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		out[p] = fi.Size()
	}
	return out, nil
}
`},
		Head: map[string]string{"go.mod": knowGoMod, "ingest/read.go": `package ingest

import (
	"io"
	"os"
)

// Sizes reports the size of every path.
func Sizes(paths []string) (map[string]int64, error) {
	out := map[string]int64{}
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			return nil, err
		}
		defer f.Close()

		n, err := io.Copy(io.Discard, f)
		if err != nil {
			return nil, err
		}
		out[p] = n
	}
	return out, nil
}
`},
		Defects: []Defect{{
			Path: "ingest/read.go", Line: 14,
			Keywords: []string{"until the function returns", "file descriptor",
				"descriptors", "too many open files", "closed until", "defer inside",
				"deferred close", "each iteration"},
			Class:        config.ClassResource,
			WantSeverity: config.SeverityError,
			Why:          "the deferred Close runs at function return, so every file stays open for the whole loop",
		}},
	}
}

// The control: the same work, with the defer given a scope that ends.
func knowCleanDeferInFuncFixture() Fixture {
	return Fixture{
		Name: "know-go-clean-defer-scoped",
		Base: map[string]string{"go.mod": knowGoMod, "ingest/read.go": `package ingest

import "os"

// Sizes reports the size of every path.
func Sizes(paths []string) (map[string]int64, error) {
	out := map[string]int64{}
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		out[p] = fi.Size()
	}
	return out, nil
}
`},
		Head: map[string]string{"go.mod": knowGoMod, "ingest/read.go": `package ingest

import (
	"io"
	"os"
)

// Sizes reports the size of every path.
func Sizes(paths []string) (map[string]int64, error) {
	out := map[string]int64{}
	for _, p := range paths {
		n, err := size(p)
		if err != nil {
			return nil, err
		}
		out[p] = n
	}
	return out, nil
}

// size reads one file, and closes it before returning.
func size(p string) (int64, error) {
	f, err := os.Open(p)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return io.Copy(io.Discard, f)
}
`},
	}
}
