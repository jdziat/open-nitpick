package review

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jdziat/open-nitpick/internal/config"
)

// Finding is one issue reported about the change.
//
// Every field is a plain scalar or slice of scalars: the SDK derives the JSON
// Schema for structured output by reflection, and maps or interface fields
// would produce an unconstrained schema that strict validators reject.
type Finding struct {
	// Path is the repository-relative file the finding is about.
	Path string `json:"path"`

	// Line is a line number in the file after the change.
	Line int `json:"line"`

	// Severity is one of nit, info, warning, error, critical.
	Severity string `json:"severity"`

	// Category groups findings, for example correctness or security. It is
	// free text and is shown to readers.
	Category string `json:"category"`

	// Class is the closed taxonomy the nitpick filter operates on. Category is
	// prose for humans; Class is machine-readable policy.
	Class string `json:"class"`

	// Title is a one-line statement of the defect.
	Title string `json:"title"`

	// Rationale explains the consequence and the reasoning.
	Rationale string `json:"rationale"`

	// Suggestion is optional replacement code for the anchored lines.
	Suggestion string `json:"suggestion"`

	// Source names where the finding came from: a linter's rule id, or the
	// reviewing model. It is not part of the model-facing schema — the model
	// does not get to claim provenance — but it IS shown to the reader.
	//
	// "flagged by golangci-lint(gosec), triaged by claude" is the sentence this
	// tool exists to be able to write. A deterministic analyzer found it, a
	// model judged whether it mattered here, and the reader can see both. That
	// is the whole differentiated claim, and it was previously destroyed in
	// triage and never rendered.
	Source string `json:"-"`

	// Triager records which model triaged the finding, so Source can keep
	// naming the original reporter.
	Triager string `json:"-"`
}

// Sev returns the parsed severity.
func (f Finding) Sev() config.Severity { return config.Severity(f.Severity) }

// Cls returns the parsed class.
func (f Finding) Cls() config.Class { return config.Class(f.Class) }

// Key identifies a finding for deduplication. Title is normalized because two
// reviewers rarely word the same defect identically, and the path plus line is
// what makes two reports the same report.
func (f Finding) Key() string {
	return fmt.Sprintf("%s:%d:%s", f.Path, f.Line, normalizeTitle(f.Title))
}

// normalizeTitle reduces a title to a comparable form.
func normalizeTitle(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// Valid reports whether a finding is complete enough to publish. Models
// occasionally emit an empty finding to satisfy a schema; publishing it would
// produce an empty comment on a random line.
func (f Finding) Valid() bool {
	return strings.TrimSpace(f.Path) != "" &&
		strings.TrimSpace(f.Title) != "" &&
		f.Line > 0
}

// Result is the structured output of a review call. Summary is only populated
// by the triage pass.
type Result struct {
	Findings []Finding `json:"findings"`
	Summary  string    `json:"summary"`
}

// sortFindings orders findings most severe first, then by path and line, so
// output is stable across runs. Unstable ordering makes reviews impossible to
// diff against each other.
func sortFindings(findings []Finding) {
	sort.SliceStable(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]

		if ra, rb := a.Sev().Rank(), b.Sev().Rank(); ra != rb {
			return ra > rb
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Line < b.Line
	})
}

// dedupe removes findings that report the same defect at the same place,
// keeping the first occurrence.
func dedupe(findings []Finding) []Finding {
	seen := make(map[string]struct{}, len(findings))
	out := make([]Finding, 0, len(findings))

	for _, f := range findings {
		key := f.Key()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, f)
	}

	return out
}

// Counts summarizes findings by severity, for the run's exit report.
type Counts map[config.Severity]int

// counts tallies findings by severity.
func counts(findings []Finding) Counts {
	out := Counts{}
	for _, f := range findings {
		out[f.Sev()]++
	}
	return out
}

// String renders counts most severe first, omitting empty levels.
func (c Counts) String() string {
	order := []config.Severity{
		config.SeverityCritical, config.SeverityError,
		config.SeverityWarning, config.SeverityInfo, config.SeverityNit,
	}

	var parts []string
	for _, s := range order {
		if n := c[s]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, s))
		}
	}

	if len(parts) == 0 {
		return "no findings"
	}
	return strings.Join(parts, ", ")
}
