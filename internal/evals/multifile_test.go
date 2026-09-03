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

// TestMultiFileCorpusIsWellFormed is the ground truth for the multi-file
// corpus, which lives outside AllFixtures (see EveryFixture for why).
//
// It checks the properties the corpus's own comment claims and nothing the
// registries in groundtruth_test.go would: every plant sits on a line the
// change added, every plant's Why is credited by its own keywords, no keyword
// is a token a reviewer would type by quoting the change, and the file that
// states the contract is byte-identical in Base and Head and is imported by
// the file that breaks it.
func TestMultiFileCorpusIsWellFormed(t *testing.T) {
	fixtures := MultiFileFixtures()
	if len(fixtures) < 8 {
		t.Fatalf("multi-file corpus has %d fixtures; it needs planted AND clean fixtures to measure precision", len(fixtures))
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
		t.Errorf("only %d clean fixture(s): a corpus that plants a defect in every change cannot see the precision cost of extra context", clean)
	}
	if planted < 6 {
		t.Errorf("only %d planted fixture(s)", planted)
	}

	seen := map[string]bool{}
	for _, f := range fixtures {
		if seen[f.Name] {
			t.Errorf("%s: duplicate fixture name", f.Name)
		}
		seen[f.Name] = true
		if MultiFile(f.Name) != true || HeldOut(f.Name) {
			t.Errorf("%s: must be in the multi-file corpus and no other", f.Name)
		}
		for _, other := range AllFixtures() {
			if other.Name == f.Name {
				t.Errorf("%s: also in AllFixtures; a multi-file fixture is in exactly one corpus", f.Name)
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

		// The contract file: in both states, unchanged, and imported by a
		// changed file. Every fixture in this corpus must have at least one.
		unchanged := map[string]bool{}
		for p, base := range f.Base {
			if head, ok := f.Head[p]; ok && head == base && files.Find(p) == nil {
				unchanged[p] = true
			}
		}
		imported := false
		for _, file := range files {
			head := f.Head[file.Path]
			for p := range unchanged {
				if importsFile(head, p) {
					imported = true
				}
			}
		}
		if !imported {
			t.Errorf("%s: no changed file imports a file that is unchanged between Base and Head, so nothing here is cross-file", f.Name)
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

// importsFile reports whether source imports the repository file p, in any
// of the three languages the corpus uses.
func importsFile(source, p string) bool {
	stem := strings.TrimSuffix(strings.TrimSuffix(p, ".go"), ".py")
	stem = strings.TrimSuffix(strings.TrimSuffix(stem, ".ts"), ".tsx")
	stem = strings.TrimSuffix(strings.TrimSuffix(stem, ".rb"), "/__init__")
	dotted := strings.ReplaceAll(stem, "/", ".")
	base := stem[strings.LastIndex(stem, "/")+1:]
	dir := stem[:strings.LastIndex(stem, "/")+1]

	// A Rails constant needs no require: the camelised basename is the use.
	camel := ""
	for part := range strings.SplitSeq(base, "_") {
		if part != "" {
			camel += strings.ToUpper(part[:1]) + part[1:]
		}
	}
	// TS: a tsconfig alias (@/* -> src/*) to a barrel or a module.
	aliased := strings.TrimPrefix(dir, "src/")
	alias := `from "@/` + strings.TrimSuffix(aliased, "/") + `"`
	if base != "index" {
		alias = `from "@/` + aliased + base + `"`
	}
	for _, needle := range []string{
		"/" + dir[:max(len(dir)-1, 0)] + `"`, // Go: "module/pkg/dir"
		alias, camel + ".",
		"from " + dotted + " import", // Python absolute
		"from ." + base + " import",  // Python relative
		"import " + dotted,
		`from "./` + base + `"`, `from "../` + base + `"`, // TS relative
		`from "./` + base + `.js"`,
	} {
		if needle != `/"` && strings.Contains(source, needle) {
			return true
		}
	}
	return false
}

// TestTheMakefileNamesTheWholeTuningCorpus pins the Makefile's TUNING line,
// which `make quick` spends, to Fixtures(), for
// TestTheMakefileSpendsTheWholeHeldOutCorpus's reasons.
func TestTheMakefileNamesTheWholeTuningCorpus(t *testing.T) {
	src, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	var line string
	for _, l := range strings.Split(string(src), "\n") {
		if strings.HasPrefix(l, "TUNING :=") {
			line = strings.TrimSpace(strings.TrimPrefix(l, "TUNING :="))
		}
	}
	if line == "" {
		t.Fatal("the Makefile has no TUNING line")
	}
	named := map[string]bool{}
	for _, n := range strings.Split(line, ",") {
		named[strings.TrimSpace(n)] = true
	}
	tuning := map[string]bool{}
	for _, f := range Fixtures() {
		tuning[f.Name] = true
		if !named[f.Name] {
			t.Errorf("%s is in Fixtures() and not in the Makefile's TUNING line", f.Name)
		}
	}
	for n := range named {
		if !tuning[n] {
			t.Errorf("the Makefile's TUNING line names %q, which is not a tuning fixture", n)
		}
	}
}
