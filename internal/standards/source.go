package standards

import (
	"context"
	"sort"
	"strings"
)

// Where a measurement's raw material comes from.
//
// The first version of this package wrote its own Go AST probes and measured
// with those. A direct test of six of them against golangci-lint found five
// restating linters that already exist, one of them a linter this repository
// had enabled the whole time. Writing more probes means reimplementing mature
// tools one language at a time, and internal/linters already carries 37 of
// them across Go, Python, TypeScript, Ruby, Rust, Java, Terraform and more.
//
// So the analyzers became the instrument and this package became what turns
// what they say into something retirable. Two things they do not give:
//
//   - A denominator. A linter says "3 violations" and never says out of what.
//     Without one there is no share, no rule that retires itself when the code
//     stops following it, and no conventions file that maintains itself.
//   - Inconsistency. A rule followed everywhere except one corner is a
//     codebase disagreeing with itself, and no analyzer reports it because no
//     analyzer looks at more than the file in front of it.
//
// The interfaces here are deliberately small so that a source can be a linter,
// a prose scan, an AST probe, or a model, and the arithmetic downstream cannot
// tell which it was. What it can tell, and reports, is whether the source ran.

// Observation is one place a source had something to say.
//
// Deliberately not a finding. It carries no severity and no message, because
// what this package does with it is count it, and a source that ranks its own
// output would be voting on the measurement.
type Observation struct {
	// Rule is namespaced by the source, as "revive.exported", "ruff.E501",
	// "slop.em-dash" or "probe.go-test-name-sentence". Namespacing is what
	// keeps two analyzers that both call a rule "unused" from being counted as
	// one convention.
	Rule string `json:"rule"`

	Path string `json:"path"`
	Line int    `json:"line"`
}

// Coverage is what a source looked at.
//
// Returned rather than assumed, and it is the whole denominator. A linter
// whose binary is not installed examined nothing, and counting files-on-disk
// would enter every one of them as clean: a tool that is absent would report
// as a repository that conforms. That is the failure docs/measurement.md Rule
// 10 names, and it is the one this package has now shipped five variants of.
type Coverage struct {
	// Files maps a repository-relative path to the lines the source read
	// there. Lines rather than a set, because the rate a report prints beside
	// each share is violations per thousand lines, over lines somebody read.
	Files map[string]int `json:"files"`

	// Ran records whether the source did its work. False with a Why is a
	// source that could not run; true with an empty Files is a source that ran
	// and found nothing to read. Different facts, and they render the same
	// unless they are kept apart.
	Ran bool   `json:"ran"`
	Why string `json:"why,omitempty"`
}

// Lines totals what a source read.
func (c Coverage) Lines() int {
	n := 0
	for _, l := range c.Files {
		n += l
	}
	return n
}

// Source produces observations over a checked-out tree.
//
// An error is for a source that failed in a way the operator should hear about.
// A source that is unavailable returns none, and a Coverage saying so.
type Source interface {
	Name() string
	Observe(ctx context.Context, root string, files []File) ([]Observation, Coverage, error)
}

// SourceResult pairs what one source saw with what it looked at.
type SourceResult struct {
	Source       string        `json:"source"`
	Observations []Observation `json:"observations"`
	Coverage     Coverage      `json:"coverage"`
}

// Gather runs every source and returns what each one saw.
//
// One source failing does not stop the others: a measurement over four
// analyzers where one is broken is worth more than no measurement, provided
// the report says which four were asked and which three answered. The error a
// source returns becomes its Why rather than the run's failure.
func Gather(ctx context.Context, root string, files []File, sources []Source) []SourceResult {
	out := make([]SourceResult, 0, len(sources))
	for _, s := range sources {
		obs, cov, err := s.Observe(ctx, root, files)
		if err != nil {
			cov = Coverage{Why: err.Error()}
			obs = nil
		}
		out = append(out, SourceResult{Source: s.Name(), Observations: obs, Coverage: cov})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Source < out[j].Source })
	return out
}

// Rules are the distinct rule names in a set of observations, sorted.
func Rules(obs []Observation) []string {
	seen := map[string]bool{}
	for _, o := range obs {
		seen[o.Rule] = true
	}
	out := make([]string, 0, len(seen))
	for r := range seen {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

// SourceOf returns the source half of a namespaced rule name.
func SourceOf(rule string) string {
	if i := strings.IndexByte(rule, '.'); i > 0 {
		return rule[:i]
	}
	return rule
}
