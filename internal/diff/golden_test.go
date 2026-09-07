package diff

import (
	"os"
	"strings"
	"testing"
)

// The fixture is real `git diff --cached` output covering, in one file: a
// modification, a rename with content change, a deletion, a creation, and a
// file with no trailing newline. Hand-written fixtures can encode the same
// misunderstanding as the parser; this one cannot.
const fixturePath = "testdata/git-mixed.diff"

func loadFixture(t *testing.T) []byte {
	t.Helper()

	data, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return data
}

func TestParseRealGitDiff(t *testing.T) {
	files, err := Parse(loadFixture(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	want := []struct {
		path    string
		oldPath string
		kind    ChangeKind
		added   int
		removed int
	}{
		{path: "a.go", oldPath: "a.go", kind: ChangeModified, added: 3, removed: 1},
		{path: "c.txt", oldPath: "b.txt", kind: ChangeRenamed, added: 1, removed: 0},
		{path: "gone.txt", oldPath: "gone.txt", kind: ChangeDeleted, added: 0, removed: 1},
		{path: "new.go", kind: ChangeAdded, added: 1, removed: 0},
	}

	if len(files) != len(want) {
		t.Fatalf("files = %v, want %d entries", files.Paths(), len(want))
	}

	for i, w := range want {
		f := files[i]
		if f.Path != w.path {
			t.Errorf("file %d path = %q, want %q", i, f.Path, w.path)
		}
		if f.Kind != w.kind {
			t.Errorf("%s kind = %q, want %q", f.Path, f.Kind, w.kind)
		}
		if w.oldPath != "" && f.OldPath != w.oldPath {
			t.Errorf("%s old path = %q, want %q", f.Path, f.OldPath, w.oldPath)
		}

		stats := f.Stats()
		if stats.Added != w.added || stats.Removed != w.removed {
			t.Errorf("%s stats = +%d/-%d, want +%d/-%d", f.Path, stats.Added, stats.Removed, w.added, w.removed)
		}
	}
}

// TestPositionsMatchRawDiffOffsets recomputes every position directly from the
// raw text and compares. The two implementations share no code, so an
// off-by-one in the parser cannot hide.
func TestPositionsMatchRawDiffOffsets(t *testing.T) {
	data := loadFixture(t)

	files, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	want := rawPositions(string(data))

	checked := 0
	for _, f := range files {
		perFile, ok := want[f.Path]
		if !ok {
			t.Errorf("no raw positions recorded for %s", f.Path)
			continue
		}

		for _, h := range f.Hunks {
			for _, l := range h.Lines {
				raw := rawForm(l)

				expected, ok := perFile[raw]
				if !ok {
					t.Errorf("%s: parsed line %q has no counterpart in the raw diff", f.Path, raw)
					continue
				}
				if l.Position != expected {
					t.Errorf("%s: line %q position = %d, want %d", f.Path, raw, l.Position, expected)
				}
				checked++
			}
		}
	}

	if checked == 0 {
		t.Fatal("no positions were compared; the cross-check is not running")
	}
}

// TestNoNewlineMarkerShiftsPositionsInRealDiff pins the specific case that is
// easy to get wrong: the fixture's renamed file ends with a "\ No newline"
// marker, which occupies a diff position without being a diff line.
func TestNoNewlineMarkerShiftsPositionsInRealDiff(t *testing.T) {
	files, err := Parse(loadFixture(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	f := files.Find("c.txt")
	if f == nil {
		t.Fatal("c.txt missing from fixture")
	}

	lines := f.Hunks[0].Lines
	last := lines[len(lines)-1]
	if last.Content != "four" || last.Kind != LineAdded {
		t.Fatalf("last line = %+v, want the added 'four'", last)
	}
	// three context lines then the addition, so position 4; the marker follows.
	if last.Position != 4 {
		t.Errorf("position = %d, want 4", last.Position)
	}
	if last.NewLine != 4 {
		t.Errorf("new line = %d, want 4", last.NewLine)
	}
}

// rawForm renders a parsed line back to its raw diff text.
func rawForm(l Line) string {
	switch l.Kind {
	case LineAdded:
		return "+" + l.Content
	case LineRemoved:
		return "-" + l.Content
	default:
		return " " + l.Content
	}
}

// rawPositions independently computes, per file, the physical offset of each
// diff line below that file's first @@ header. Duplicate lines keep their
// first occurrence, which is enough for the fixture and keeps this deliberately
// simple: it is the reference implementation, so it must be obviously correct.
func rawPositions(text string) map[string]map[string]int {
	out := map[string]map[string]int{}

	var (
		path     string
		pos      int
		seenHunk bool
	)

	for _, line := range strings.Split(text, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			path, pos, seenHunk = "", 0, false

		// Header arms only apply before the first hunk. Inside a hunk these
		// prefixes are ordinary content: an added line reading "++ x" renders
		// as "+++ x", and a removed "-- x" renders as "--- x".
		case !seenHunk && strings.HasPrefix(line, "--- "):
			if p := strings.TrimPrefix(line, "--- "); p != "/dev/null" {
				path = strings.TrimPrefix(p, "a/")
			}

		case !seenHunk && strings.HasPrefix(line, "+++ "):
			// The new path wins when there is one; a deletion keeps the old.
			if p := strings.TrimPrefix(line, "+++ "); p != "/dev/null" {
				path = strings.TrimPrefix(p, "b/")
			}
			out[path] = map[string]int{}

		case strings.HasPrefix(line, "@@"):
			if seenHunk {
				pos++
			}
			seenHunk = true

		case !seenHunk:
			// File metadata before the first hunk.

		default:
			// Every remaining physical line counts, including the
			// "\ No newline at end of file" marker.
			pos++
			if out[path] == nil {
				out[path] = map[string]int{}
			}
			if _, ok := out[path][line]; !ok {
				out[path][line] = pos
			}
		}
	}

	return out
}
