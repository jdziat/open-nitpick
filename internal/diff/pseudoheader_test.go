package diff

import (
	"os"
	"testing"
)

// These fixtures are real `git diff` output for content that renders as
// something the parser could mistake for structure:
//
//   - an added line whose content starts with "++ " renders as "+++ ..."
//     (a nested markdown bullet does this)
//   - a removed line whose content starts with "-- " renders as "--- ..."
//   - a file whose content is literally a diff header
//
// Getting these wrong silently misroutes or shifts every comment in the file,
// which is why they are checked against output git actually produced rather
// than against a hand-written string.
const (
	pseudoHeaderFixture = "testdata/pseudo-headers.diff"
	removalFixture      = "testdata/removals.diff"
)

func parseFixture(t *testing.T, path string) Files {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	files, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse(%s): %v", path, err)
	}
	return files
}

func TestAddedLineIsNotMistakenForAFileHeader(t *testing.T) {
	files := parseFixture(t, pseudoHeaderFixture)

	f := files.Find("notes.md")
	if f == nil {
		t.Fatalf("notes.md missing; parsed paths = %v (a content line hijacked the path)", files.Paths())
	}

	// The bullet is content, not a header.
	var sawBullet, sawReal bool
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			switch l.Content {
			case "++ nested bullet":
				sawBullet = true
				if l.Kind != LineAdded {
					t.Errorf("bullet line kind = %q, want added", l.Kind)
				}
				if l.NewLine != 2 {
					t.Errorf("bullet at new line %d, want 2", l.NewLine)
				}
			case "real added line":
				sawReal = true
				// The line after it must not be shifted.
				if l.NewLine != 3 {
					t.Errorf("following line at new line %d, want 3 — line numbers shifted", l.NewLine)
				}
			}
		}
	}

	if !sawBullet {
		t.Error("the '++ nested bullet' line was swallowed instead of parsed as content")
	}
	if !sawReal {
		t.Error("the line after the pseudo-header went missing")
	}
}

func TestRemovedLineIsNotMistakenForAFileHeader(t *testing.T) {
	files := parseFixture(t, removalFixture)

	f := files.Find("dash.md")
	if f == nil {
		t.Fatalf("dash.md missing; parsed paths = %v", files.Paths())
	}
	if f.OldPath != "dash.md" {
		t.Errorf("OldPath = %q, want dash.md — a content line was parsed as the --- header", f.OldPath)
	}

	stats := f.Stats()
	if stats.Removed != 1 {
		t.Errorf("removed = %d, want 1 — the removed line was erased by header parsing", stats.Removed)
	}

	var saw bool
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			if l.Content == "-- removed dash line" {
				saw = true
				if l.Kind != LineRemoved {
					t.Errorf("kind = %q, want removed", l.Kind)
				}
			}
		}
	}
	if !saw {
		t.Error("the '-- removed dash line' line was swallowed")
	}
}

func TestDiffHeaderAsFileContentIsNotStructure(t *testing.T) {
	// tricky.txt's contents are literally a diff header. As added lines they
	// carry a '+' marker, so they are content — but a parser that scanned for
	// "diff --git" anywhere would split the file in two.
	files := parseFixture(t, pseudoHeaderFixture)

	f := files.Find("tricky.txt")
	if f == nil {
		t.Fatalf("tricky.txt missing; parsed paths = %v", files.Paths())
	}
	if f.Kind != ChangeAdded {
		t.Errorf("Kind = %q, want added", f.Kind)
	}
	if got := f.Stats().Added; got != 2 {
		t.Errorf("added = %d, want 2 (both content lines)", got)
	}
}

func TestOrdinaryFilesInTheSameDiffAreUnaffected(t *testing.T) {
	// The pseudo-header files must not corrupt their neighbors.
	files := parseFixture(t, pseudoHeaderFixture)

	if got := len(files); got != 4 {
		t.Fatalf("files = %d (%v), want 4", got, files.Paths())
	}

	a := files.Find("a.go")
	if a == nil {
		t.Fatal("a.go missing")
	}
	if s := a.Stats(); s.Added != 1 || s.Removed != 1 {
		t.Errorf("a.go stats = +%d/-%d, want +1/-1", s.Added, s.Removed)
	}

	doc := files.Find("doc.md")
	if doc == nil {
		t.Fatal("doc.md missing")
	}
	if s := doc.Stats(); s.Added != 1 {
		t.Errorf("doc.md added = %d, want 1", s.Added)
	}
}

// TestPositionsStillMatchRawOffsets re-runs the independent cross-check over
// the new corpora, so the switch reordering cannot quietly break position
// arithmetic.
func TestPositionsStillMatchRawOffsets(t *testing.T) {
	for _, path := range []string{pseudoHeaderFixture, removalFixture} {
		t.Run(path, func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			files, err := Parse(data)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}

			want := rawPositions(string(data))

			checked := 0
			for _, f := range files {
				for _, h := range f.Hunks {
					for _, l := range h.Lines {
						expected, ok := want[f.Path][rawForm(l)]
						if !ok {
							continue
						}
						if l.Position != expected {
							t.Errorf("%s: %q position = %d, want %d", f.Path, rawForm(l), l.Position, expected)
						}
						checked++
					}
				}
			}
			if checked == 0 {
				t.Error("no positions compared")
			}
		})
	}
}
