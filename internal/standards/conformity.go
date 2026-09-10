package standards

import (
	"sort"
	"strings"
)

// The floor a rule clears before it is written down as a standard.
//
// Files, not sites. A linter reports violations and never says out of what. The
// only denominator every analyzer can supply is the files it read, and counting
// files keeps one bad file with forty violations from reading as forty broken
// conventions. See docs/findings.md for why the share is 95 here against 85
// over sites, and what would move either.
const (
	DefaultCleanShare = 0.95
	DefaultMinFiles   = 12
)

// Conformity is one rule's reading over the files a source examined.
type Conformity struct {
	Rule   string `json:"rule"`
	Source string `json:"source"`

	// Clean is files with no observation of this rule; Covered is files the
	// source read. Covered is never files on disk: a source that did not run
	// contributes no denominator at all.
	Clean   int `json:"clean"`
	Covered int `json:"covered"`

	// Violations and Lines carry the rate the report prints beside the share.
	// A share says how much of the tree is untouched by a rule and a rate says
	// how loudly the rest of it is, and neither answers for the other.
	Violations int `json:"violations"`
	Lines      int `json:"lines"`

	Standing Standing `json:"standing"`
}

// Share is the fraction of read files carrying no violation, and false when
// nothing was read.
//
// The bool rather than a zero, for the reason Result.Share returns one: zero
// and "not measured" are opposite readings of the same digit.
func (c Conformity) Share() (float64, bool) {
	if c.Covered == 0 {
		return 0, false
	}
	return float64(c.Clean) / float64(c.Covered), true
}

// PerKLOC is violations per thousand lines read, and false when nothing was.
func (c Conformity) PerKLOC() (float64, bool) {
	if c.Lines == 0 {
		return 0, false
	}
	return float64(c.Violations) / float64(c.Lines) * 1000, true
}

// CleanFloor is the evidence a rule needs before it is written down.
type CleanFloor struct {
	CleanShare float64
	MinFiles   int
}

// DefaultCleanFloor is the floor applied when configuration names none.
func DefaultCleanFloor() CleanFloor {
	return CleanFloor{CleanShare: DefaultCleanShare, MinFiles: DefaultMinFiles}
}

// standing reads a conformity against this floor.
func (f CleanFloor) standing(c Conformity) Standing {
	share, ok := c.Share()
	switch {
	case !ok:
		return StandingUnseen
	case c.Covered >= f.MinFiles && share >= f.CleanShare:
		return StandingStandard
	default:
		return StandingContested
	}
}

// Conform turns what the sources saw into one reading per rule.
//
// A rule appears only when some source both ran and observed it, so a linter
// that is installed and finds nothing contributes no rows rather than a wall of
// perfect scores for rules nobody broke. What that costs is a convention this
// repository follows perfectly and silently, which is a real loss and the price
// of not inventing a denominator: an analyzer cannot be asked how many places
// it considered and let pass.
func Conform(results []SourceResult, floor CleanFloor) []Conformity {
	type acc struct {
		source     string
		dirty      map[string]bool
		violations int
	}
	rules := map[string]*acc{}

	// Covered files and lines are per source, since two sources read different
	// sets and a rule's denominator is the set its own source read.
	covered := map[string]Coverage{}

	for _, r := range results {
		if !r.Coverage.Ran {
			continue
		}
		covered[r.Source] = r.Coverage
		for _, o := range r.Observations {
			a := rules[o.Rule]
			if a == nil {
				a = &acc{source: r.Source, dirty: map[string]bool{}}
				rules[o.Rule] = a
			}
			a.dirty[o.Path] = true
			a.violations++
		}
	}

	out := make([]Conformity, 0, len(rules))
	for rule, a := range rules {
		cov := covered[a.source]

		// Only files this source read count as clean. An observation on a path
		// the coverage does not name is a source reporting outside what it
		// said it looked at, which would make the share exceed one.
		clean := 0
		for path := range cov.Files {
			if !a.dirty[path] {
				clean++
			}
		}
		c := Conformity{
			Rule:       rule,
			Source:     a.source,
			Clean:      clean,
			Covered:    len(cov.Files),
			Violations: a.violations,
			Lines:      cov.Lines(),
		}
		c.Standing = floor.standing(c)
		out = append(out, c)
	}

	sort.Slice(out, func(i, j int) bool {
		si, _ := out[i].Share()
		sj, _ := out[j].Share()
		if si != sj {
			return si > sj
		}
		return out[i].Rule < out[j].Rule
	})
	return out
}

// Standards returns the conformities the floor supports writing down.
func Standards(cs []Conformity) []Conformity {
	var out []Conformity
	for _, c := range cs {
		if c.Standing == StandingStandard {
			out = append(out, c)
		}
	}
	return out
}

// RuleText renders a rule name as a sentence for a conventions file.
//
// An analyzer's rule name is what it is: "revive.exported", "ruff.E501". This
// package cannot invent a sentence for one without asserting something it did
// not measure, so the name is printed and the tool that owns it is named
// beside it. A person writing prose about a rule belongs outside the generated
// block, where nothing rewrites them.
func RuleText(rule string) string {
	source := SourceOf(rule)
	name := strings.TrimPrefix(rule, source+".")
	return name + " (" + source + ")"
}
