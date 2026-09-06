package review

import (
	"math"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/jdziat/open-nitpick/internal/diff"
)

// Complexity ranks how much review attention a changed file warrants.
//
// It exists for one job: when a spending ceiling cannot pay for the whole
// diff, something has to choose which files are reviewed, and choosing by
// filename order would review whatever sorts first. The score is deterministic
// and computed from the diff alone, with no model call, because a ranking that
// costs a model call to produce is a ranking that spends the budget it exists
// to protect.
//
// It is a PRIORITY, not a claim about defect density. Nothing here knows which
// file holds the bug. What it knows is which changes have historically been
// worth a careful read: control flow, error handling, many separate edits, new
// code rather than deletions. A team that disagrees can turn the ceiling off
// and review everything, which is the default.
type Complexity struct {
	Path  string
	Score float64

	// Why names the components that contributed, largest first, so a report
	// can say why a file was reviewed ahead of another.
	Why []string
}

// branchy matches the tokens that make code worth reading twice: a decision, a
// loop, an error path, a concurrency primitive. It is intentionally
// cross-language and intentionally crude. A word inside a string literal or a
// comment counts, and that is accepted: the alternative is parsing every
// language in the catalog to rank a file nobody has reviewed yet.
var branchy = regexp.MustCompile(`\b(if|else|for|while|switch|case|catch|except|rescue|try|finally|return|throw|raise|panic|recover|defer|go|async|await|goroutine|select|lock|mutex|atomic|retry|timeout|context|cancel|nil|null|none|undefined|err|error|Err|Error|exception)\b|&&|\|\||\?\?|\?\.`)

// riskyPath matches the parts of a tree where a mistake is expensive.
var riskyPath = regexp.MustCompile(`(?i)(auth|login|session|token|secret|credential|crypto|password|payment|billing|charge|invoice|migration|schema|permission|acl|admin|security|sanitiz|escape|sql|query)`)

// testPath matches files whose defects cost less than a defect in the code
// they cover.
var testPath = regexp.MustCompile(`(?i)(^|/)(tests?|spec|__tests__|testdata|fixtures?)(/|$)|_test\.|\.test\.|\.spec\.|(^|/)test_`)

// Rank scores files and returns them highest first. Ties break on path, so the
// order is stable across runs and reviewable in a diff.
func Rank(files []*diff.File) []Complexity {
	out := make([]Complexity, 0, len(files))
	for _, f := range files {
		out = append(out, Score(f))
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// Score rates one file.
func Score(f *diff.File) Complexity {
	if f == nil {
		return Complexity{}
	}
	c := Complexity{Path: f.Path}

	var added, removed, branches int
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			switch l.Kind {
			case diff.LineAdded:
				added++
				branches += len(branchy.FindAllString(l.Content, -1))
			case diff.LineRemoved:
				removed++
			}
		}
	}

	add := func(points float64, why string) {
		if points <= 0 {
			return
		}
		c.Score += points
		c.Why = append(c.Why, why)
	}

	// Size, damped hard. A 40-line change is more work than a 20-line one, and
	// a 4,000-line generated update is nowhere near two hundred times the work
	// of the 20-line one. Logarithmic rather than square root because square
	// root still let one mechanical file outrank a small dense change in a
	// sensitive place, which is the ordering this ranking exists to get right:
	// sqrt(4000) is 63 and beats anything the other components can add.
	changed := added + removed
	add(1.5*math.Log2(1+float64(changed)), "changed lines")

	// Edits scattered across a file are harder to hold in the head than the
	// same number of lines in one place.
	if len(f.Hunks) > 1 {
		add(math.Log2(float64(len(f.Hunks)))*2, "scattered edits")
	}

	// Control flow and error handling per added line, not in total, so a long
	// mechanical change does not outrank a short dense one.
	if added > 0 {
		add((float64(branches)/float64(added))*10, "control flow")
	}

	// New code has no prior review. A deletion mostly needs its callers
	// checked, which the related-context walk does rather than this file.
	switch f.Kind {
	case diff.ChangeAdded:
		add(4, "new file")
	case diff.ChangeDeleted:
		c.Score *= 0.5
		c.Why = append(c.Why, "deletion")
	}

	if riskyPath.MatchString(f.Path) {
		add(8, "sensitive path")
	}

	// Prose and data are not where defects of the kind this tool reports live.
	// Halved rather than excluded, for the same reason tests are: the slop
	// class reads documentation, and a changed fixture can still break a test.
	if proseOrData(f.Path) {
		c.Score *= 0.5
		c.Why = append(c.Why, "prose or data")
	}

	// A test is worth reviewing and worth reviewing last. Halved rather than
	// excluded: a test that asserts nothing is a real finding, and this tool
	// reports it.
	if testPath.MatchString(f.Path) || testPath.MatchString(path.Base(f.Path)) {
		c.Score *= 0.5
		c.Why = append(c.Why, "test")
	}

	return c
}

// proseOrData reports whether a path holds documentation or data rather than
// code that runs.
func proseOrData(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".md", ".markdown", ".rst", ".txt", ".adoc", ".json", ".yaml", ".yml",
		".toml", ".ini", ".csv", ".lock", ".svg", ".html":
		return true
	}
	return false
}

// Reasons renders Why for a report line.
func (c Complexity) Reasons() string { return strings.Join(c.Why, ", ") }
