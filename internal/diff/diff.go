// Package diff parses unified diffs into files, hunks, and lines, and maps
// new-file line numbers onto the positions review APIs require.
//
// The mapping matters more than it looks: GitHub accepts inline comments only
// on lines that appear in the diff, and a comment anchored to the wrong line is
// worse than no comment at all. Everything here is deterministic and tested
// without a model in the loop.
package diff

import (
	"fmt"
	"strings"
)

// ChangeKind classifies what happened to a file.
type ChangeKind string

// File change kinds.
const (
	ChangeAdded    ChangeKind = "added"
	ChangeModified ChangeKind = "modified"
	ChangeDeleted  ChangeKind = "deleted"
	ChangeRenamed  ChangeKind = "renamed"
)

// LineKind classifies a single diff line.
type LineKind string

// Diff line kinds.
const (
	// LineContext is unchanged and present on both sides.
	LineContext LineKind = "context"
	// LineAdded exists only in the new file.
	LineAdded LineKind = "added"
	// LineRemoved existed only in the old file.
	LineRemoved LineKind = "removed"
)

// File is one file's changes within a diff.
type File struct {
	// Path names the file after the change, or before it when the change
	// deletes.
	Path string

	// OldPath is set for renames and deletions.
	OldPath string

	Kind ChangeKind

	// Binary marks a file whose contents the diff does not carry.
	Binary bool

	Hunks []Hunk
}

// Hunk is one @@ section of a file's diff.
type Hunk struct {
	OldStart, OldLines int
	NewStart, NewLines int

	// Header is the text following the @@ marker, usually the enclosing
	// function. It is useful context for a reviewer.
	Header string

	Lines []Line
}

// Line is a single line within a hunk.
type Line struct {
	Kind LineKind

	// Content excludes the leading +/-/space marker.
	Content string

	// OldLine is the 1-based line number in the old file, or 0 when the line
	// does not exist there.
	OldLine int

	// NewLine is the 1-based line number in the new file, or 0 when the line
	// does not exist there.
	NewLine int

	// Position is the legacy diff anchor: the 1-based offset of this line
	// below the file's first @@ header, counting every subsequent hunk
	// header, blank line, and "\ No newline" marker.
	//
	// GitHub is closing this parameter down in favor of line+side. It is kept
	// for older or alternative forges, but Side/NewLine is what publishing
	// should prefer.
	Position int
}

// Side identifies which side of a split diff a line belongs to, matching
// GitHub's LEFT/right review-comment parameter.
type Side string

// Diff sides. Deletions live on the left; additions and unchanged lines on the
// right.
const (
	SideLeft  Side = "LEFT"
	SideRight Side = "RIGHT"
)

// Side reports which side of the diff this line should be commented on.
func (l Line) Side() Side {
	if l.Kind == LineRemoved {
		return SideLeft
	}
	return SideRight
}

// AnchorLine returns the line number to pair with Side when publishing a
// comment: the old-file number for deletions, the new-file number otherwise.
func (l Line) AnchorLine() int {
	if l.Kind == LineRemoved {
		return l.OldLine
	}
	return l.NewLine
}

// Stats summarizes a file's churn.
type Stats struct {
	Added   int
	Removed int
}

// Stats counts changed lines in the file.
func (f *File) Stats() Stats {
	var s Stats
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			switch l.Kind {
			case LineAdded:
				s.Added++
			case LineRemoved:
				s.Removed++
			case LineContext:
			}
		}
	}
	return s
}

// ChangedLines returns the new-file line numbers this file adds or modifies, in
// ascending order. Deletions have no new-file line and are excluded, since a
// comment cannot be anchored to a line that no longer exists.
func (f *File) ChangedLines() []int {
	var out []int
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			if l.Kind == LineAdded {
				out = append(out, l.NewLine)
			}
		}
	}
	return out
}

// IsChangedLine reports whether the given new-file line was added by this diff.
// Findings on unchanged lines are typically dropped, since a pull request
// review should discuss what the pull request did.
func (f *File) IsChangedLine(newLine int) bool {
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			if l.Kind == LineAdded && l.NewLine == newLine {
				return true
			}
		}
	}
	return false
}

// Position returns the diff position for a new-file line, suitable for a
// position-anchored review comment. The bool is false when the line is not
// present in the diff at all.
//
// Context lines resolve too: a model commenting on a line it was shown as
// context still produces a placeable comment.
func (f *File) Position(newLine int) (int, bool) {
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			if l.NewLine == newLine && l.Kind != LineRemoved {
				return l.Position, true
			}
		}
	}
	return 0, false
}

// NearestCommentableLine snaps a line number to the closest added line in the
// same file, within maxDistance. Models routinely anchor a finding a line or
// two off, to a closing brace, or to the line after the one they mean, and
// snapping recovers those comments instead of discarding them.
//
// Ties prefer the earlier line, which reads as the start of the construct being
// discussed. It returns false when nothing is close enough.
func (f *File) NearestCommentableLine(newLine, maxDistance int) (int, bool) {
	best, bestDist := 0, maxDistance+1

	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			if l.Kind != LineAdded {
				continue
			}

			dist := l.NewLine - newLine
			if dist < 0 {
				dist = -dist
			}
			if dist < bestDist || (dist == bestDist && l.NewLine < best) {
				best, bestDist = l.NewLine, dist
			}
		}
	}

	if bestDist > maxDistance {
		return 0, false
	}
	return best, true
}

// Files indexes parsed files by path.
type Files []*File

// Find returns the file with the given path, or nil.
func (fs Files) Find(path string) *File {
	for _, f := range fs {
		if f.Path == path {
			return f
		}
	}
	return nil
}

// Paths returns every file path in diff order.
func (fs Files) Paths() []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.Path)
	}
	return out
}

// String renders the hunk the way a reviewer reads it, with new-file line
// numbers in the margin. Line numbers are what let a model cite a location
// precisely, so they are part of the prompt rather than a debugging aid.
func (h *Hunk) String() string {
	var b strings.Builder

	fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@", h.OldStart, h.OldLines, h.NewStart, h.NewLines)
	if h.Header != "" {
		b.WriteString(" " + h.Header)
	}
	b.WriteByte('\n')

	for _, l := range h.Lines {
		switch l.Kind {
		case LineAdded:
			fmt.Fprintf(&b, "%6d + %s\n", l.NewLine, l.Content)
		case LineRemoved:
			fmt.Fprintf(&b, "%6s - %s\n", "", l.Content)
		case LineContext:
			fmt.Fprintf(&b, "%6d   %s\n", l.NewLine, l.Content)
		}
	}

	return b.String()
}

// String renders every hunk of a file.
func (f *File) String() string {
	var b strings.Builder
	for i, h := range f.Hunks {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(h.String())
	}
	return b.String()
}
