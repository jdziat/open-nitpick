package evals

import (
	"context"
	"io"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/review"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// TestCallersCorpusIsWellFormed is the ground truth for the callers corpus,
// which lives outside AllFixtures for the multi-file corpus's reasons.
//
// It holds the INVERSE of TestMultiFileCorpusIsWellFormed's structural rule.
// There, the contract file is unchanged and a changed file imports it. Here,
// the changed file IS the contract, and a file that is byte-identical in Base
// and Head imports it, the caller the change breaks. A fixture that satisfied
// the multi-file rule instead would be a multi-file fixture filed in the wrong
// corpus, and would measure the direction related context already covers.
//
// Every plant sits on a line the change added in the contract file, every
// plant's Why is credited by its own keywords, and no keyword is a token a
// reviewer would type by quoting the change.
func TestCallersCorpusIsWellFormed(t *testing.T) {
	fixtures := CallerFixtures()
	if len(fixtures) < 4 {
		t.Fatalf("callers corpus has %d fixtures; it needs planted AND clean fixtures to measure precision", len(fixtures))
	}

	var clean, planted int
	for _, f := range fixtures {
		if f.Clean() {
			clean++
		} else {
			planted++
		}
	}
	if clean < 2 {
		t.Errorf("only %d clean fixture(s): a corpus that breaks a caller in every change cannot see a reviewer that flags every signature change", clean)
	}
	if planted < 4 {
		t.Errorf("only %d planted fixture(s)", planted)
	}

	seen := map[string]bool{}
	for _, f := range fixtures {
		if seen[f.Name] {
			t.Errorf("%s: duplicate fixture name", f.Name)
		}
		seen[f.Name] = true
		if !Caller(f.Name) || HeldOut(f.Name) || MultiFile(f.Name) || Info(f.Name) {
			t.Errorf("%s: must be in the callers corpus and no other", f.Name)
		}
		for _, other := range AllFixtures() {
			if other.Name == f.Name {
				t.Errorf("%s: also in AllFixtures; a callers fixture is in exactly one corpus", f.Name)
			}
		}

		dir := t.TempDir()
		if err := buildRepo(dir, f); err != nil {
			t.Fatalf("%s: build repo: %v", f.Name, err)
		}
		raw, err := vcs.NewLocal(dir, io.Discard).Diff(context.Background(), vcs.Ref{})
		if err != nil {
			t.Fatalf("%s: diff: %v", f.Name, err)
		}
		files, err := diff.Parse(raw)
		if err != nil || len(files) == 0 {
			t.Fatalf("%s: the change produces no diff (%v)", f.Name, err)
		}

		// The caller: in both states, unchanged, and importing a changed file.
		// Every fixture in this corpus must have at least one.
		unchanged := map[string]string{}
		for p, base := range f.Base {
			if head, ok := f.Head[p]; ok && head == base && files.Find(p) == nil {
				unchanged[p] = head
			}
		}
		imported := false
		for _, file := range files {
			for _, caller := range unchanged {
				if importsFile(caller, file.Path) {
					imported = true
				}
			}
		}
		if !imported {
			t.Errorf("%s: no unchanged file imports a file the change touches, so nothing here has a caller to break", f.Name)
		}

		// And the multi-file rule must not hold, or this is that corpus's
		// fixture in the wrong place.
		for _, file := range files {
			for p := range unchanged {
				if importsFile(f.Head[file.Path], p) {
					t.Errorf("%s: changed file %s imports unchanged %s, which is the multi-file corpus's shape; a callers fixture is the contract, not its user", f.Name, file.Path, p)
				}
			}
		}

		added := map[string]string{}
		for _, file := range files {
			var b strings.Builder
			for _, h := range file.Hunks {
				for _, l := range h.Lines {
					if l.Kind == diff.LineAdded {
						b.WriteString(strings.ToLower(l.Content))
						b.WriteByte('\n')
					}
				}
			}
			added[file.Path] = b.String()
		}

		for _, d := range f.Defects {
			file := files.Find(d.Path)
			if file == nil {
				t.Errorf("%s: plant is on %s, which the diff does not contain", f.Name, d.Path)
				continue
			}
			if !file.IsChangedLine(d.Line) {
				t.Errorf("%s: %s:%d is not a line the change added", f.Name, d.Path, d.Line)
			}
			if !d.WantSeverity.Valid() || d.Class == "" || d.Why == "" {
				t.Errorf("%s: plant at %s:%d is missing a severity, class or Why", f.Name, d.Path, d.Line)
			}

			own := review.Finding{Path: d.Path, Line: d.Line, Rationale: d.Why}
			if !matches(own, d) {
				t.Errorf("%s: the plant's own Why is not credited by its keywords: %q", f.Name, d.Why)
			}

			for _, k := range d.Keywords {
				letters := regexp.MustCompile(`[a-z]`).FindAllString(strings.ToLower(k), -1)
				if len(letters) < 5 {
					t.Errorf("%s: keyword %q has fewer than five letters and is a substring hazard", f.Name, k)
				}
				if strings.Contains(added[d.Path], strings.ToLower(k)) {
					t.Errorf("%s: keyword %q is a token of the change itself, so quoting the diff earns credit", f.Name, k)
				}
			}
		}
	}
}

// TestTheMakefileNamesTheWholeCallersCorpus pins the Makefile's CALLERS line
// to CallerFixtures(), for TestTheMakefileNamesTheWholeTuningCorpus's reasons.
func TestTheMakefileNamesTheWholeCallersCorpus(t *testing.T) {
	src, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	var line string
	for _, l := range strings.Split(string(src), "\n") {
		if strings.HasPrefix(l, "CALLERS :=") {
			line = strings.TrimSpace(strings.TrimPrefix(l, "CALLERS :="))
		}
	}
	if line == "" {
		t.Fatal("the Makefile has no CALLERS line")
	}
	named := map[string]bool{}
	for _, n := range strings.Split(line, ",") {
		named[strings.TrimSpace(n)] = true
	}
	corpus := map[string]bool{}
	for _, f := range CallerFixtures() {
		corpus[f.Name] = true
		if !named[f.Name] {
			t.Errorf("CallerFixtures() has %q and the Makefile's CALLERS line does not", f.Name)
		}
	}
	for n := range named {
		if !corpus[n] {
			t.Errorf("the Makefile's CALLERS line names %q, which is not in CallerFixtures()", n)
		}
	}
}
