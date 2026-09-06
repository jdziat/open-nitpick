package evals

import (
	"context"
	"io"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/review"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// TestInfoCorpusIsWellFormed is the ground truth for the info corpus, on
// the multi-file corpus's terms: anchors on added lines, self-credit, no
// keyword that is a token of the change, and every plant at info.
func TestInfoCorpusIsWellFormed(t *testing.T) {
	fixtures := InfoFixtures()
	var clean, planted int
	for _, f := range fixtures {
		if f.Clean() {
			clean++
		} else {
			planted++
		}
	}
	if planted < 10 || clean < 2 {
		t.Errorf("info corpus has %d plants and %d clean controls; it needs ten and two", planted, clean)
	}

	for _, f := range fixtures {
		if !Info(f.Name) || HeldOut(f.Name) || MultiFile(f.Name) {
			t.Errorf("%s: must be in the info corpus and no other", f.Name)
		}
		dir := t.TempDir()
		if err := buildRepo(dir, f); err != nil {
			t.Fatalf("%s: %v", f.Name, err)
		}
		raw, err := vcs.NewLocal(dir, io.Discard).Diff(context.Background(), vcs.Ref{})
		if err != nil {
			t.Fatal(err)
		}
		files, err := diff.Parse(raw)
		if err != nil || len(files) == 0 {
			t.Fatalf("%s: no diff (%v)", f.Name, err)
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
			if d.WantSeverity != config.SeverityInfo {
				t.Errorf("%s: plants %s; this corpus is the info band", f.Name, d.WantSeverity)
			}
			file := files.Find(d.Path)
			if file == nil || !file.IsChangedLine(d.Line) {
				t.Errorf("%s: %s:%d is not a line the change added", f.Name, d.Path, d.Line)
			}
			if !matches(review.Finding{Path: d.Path, Line: d.Line, Rationale: d.Why}, d) {
				t.Errorf("%s: the plant's own Why is not credited by its keywords", f.Name)
			}
			for _, k := range d.Keywords {
				if len(regexp.MustCompile(`[a-z0-9]`).FindAllString(strings.ToLower(k), -1)) < 5 {
					t.Errorf("%s: keyword %q is a substring hazard", f.Name, k)
				}
				if strings.Contains(added[d.Path], strings.ToLower(k)) {
					t.Errorf("%s: keyword %q is a token of the change", f.Name, k)
				}
			}
		}
	}
}

// TestTheMakefileNamesTheWholeInfoCorpus pins the INFO line to InfoFixtures.
func TestTheMakefileNamesTheWholeInfoCorpus(t *testing.T) {
	src, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	var line string
	for _, l := range strings.Split(string(src), "\n") {
		if strings.HasPrefix(l, "INFO :=") {
			line = strings.TrimSpace(strings.TrimPrefix(l, "INFO :="))
		}
	}
	named := map[string]bool{}
	for _, n := range strings.Split(line, ",") {
		named[strings.TrimSpace(n)] = true
	}
	for _, f := range InfoFixtures() {
		if !named[f.Name] {
			t.Errorf("%s is not in the Makefile's INFO line", f.Name)
		}
	}
	for n := range named {
		if !Info(n) {
			t.Errorf("INFO names %q, which is not an info fixture", n)
		}
	}
}
