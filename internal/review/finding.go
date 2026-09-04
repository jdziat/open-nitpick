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

	// EndLine is the last line of a multi-line anchor. Zero means the finding
	// is anchored to Line alone, which is what open-nitpick's own reviews
	// always produce: the prompt's contract is to anchor AT the defect, and the
	// hand-authored schema does not offer a model this field.
	//
	// It exists for reviewers that report a region instead. Scoring one of
	// those by the start of its span alone is simply wrong — "the credential is
	// on lines 11-12" has identified a defect on line 12 — and the error is not
	// even stable: the same reviewer anchored the same defect at 7 on one run
	// and 11-12 on the next, which start-only scoring turns from a reviewer's
	// variance into a flipped benchmark result.
	EndLine int `json:"end_line,omitempty"`

	// AlsoAt are further regions the same finding covers.
	//
	// One finding can legitimately name several places: a reviewer that reports
	// a repeated pattern once, or a triage pass that merged duplicates across
	// batches, both produce a single finding with more than one location.
	//
	// Ignoring them misreads the reviewer. Incumbent reported the SQL
	// injection fixture as "store.go:3-6" — the import block, because its
	// proposed fix deletes the fmt import — and then "Also applies to: 15-18",
	// which is where the interpolation actually is. Reading only the primary
	// anchor scores that as missing a defect it explicitly located, and a
	// benchmark that reports a competitor missing something it found is worse
	// than one that does not run.
	//
	// open-nitpick's own reviews leave this empty: the schema does not offer it
	// and one finding gets one anchor.
	AlsoAt []LineSpan `json:"also_at,omitempty"`

	// Severity is one of nit, info, warning, error, critical.
	Severity string `json:"severity"`

	// SeverityTranslated records that Severity above is THIS PROJECT'S word
	// rather than the reporter's own — because an adapter mapped a foreign
	// vocabulary onto our five levels, because the reporter published no severity
	// at all and we assigned one, or because the reporter published a word we did
	// not recognize and normalized away.
	//
	// It is a separate field from RawSeverity because the two facts are
	// independent and "" cannot carry both. "Nobody translated this, so Severity
	// is the reporter's own word" and "somebody translated it and the original did
	// not survive" are opposite claims about the same finding, and an empty
	// RawSeverity is the state of both. Reporting the second as the first is what
	// quotes a reviewer as having said a word we chose for it.
	//
	// Whoever sets Severity to something the reporter did not write MUST set this,
	// and the invariant is that RawSeverity is never populated without it.
	SeverityTranslated bool `json:"-"`

	// RawSeverity is the severity word the REPORTER itself printed, kept when
	// Severity above is this project's translation of it rather than the
	// reporter's own word.
	//
	// It is empty when nothing was translated, which is the ordinary case: a
	// model writes Severity itself, so for its findings that field already IS
	// the word it said. Only an adapter for a reviewer with a different
	// vocabulary fills this in, and it must, because the alternative is that the
	// original is destroyed at parse time and every downstream report describes
	// the reviewer using words the reviewer never used.
	//
	// That is not hypothetical, and it happened on BOTH sides. internal/evals
	// published a block captioned "what each contender called the defects it
	// located" that read "critical x4, warning x3" for a reviewer which had
	// printed "critical" and "major" — our translation, presented as their
	// vocabulary, and a function of a constant we are free to change. That was
	// fixed for the incumbent and reintroduced for our own contenders: the review
	// engine rewrites a model's severity and used to set nothing here, and the
	// eval report answered "nothing was translated" for every model we ship,
	// quoting each of them as having said whatever we had substituted. Empty here
	// therefore means "not recovered", and a reader is told that, rather than
	// being shown the translation as though it were a quotation.
	//
	// It is not serialized: a finding read back from JSON has lost the word, and
	// claiming otherwise would put the same substitution back one layer down.
	// SeverityTranslated is not serialized either, for the same reason — a
	// consumer that recovered the flag without the word would be told a word
	// exists and shown ours.
	RawSeverity string `json:"-"`

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

	// Suggestion is optional replacement code for the anchored line, or for
	// the lines Line through FixEndLine when FixEndLine is set.
	Suggestion string `json:"suggestion"`

	// FixEndLine is the last line, inclusive, that Suggestion replaces. Zero
	// means the single anchored line. The engine validates the range against
	// the diff before a multi-line suggestion is rendered as committable
	// (see validateSuggestions); a range that fails is rendered as a
	// described change instead, never as one click.
	FixEndLine int `json:"fix_end_line,omitempty"`

	// FixValidated is set by the engine when a multi-line suggestion's range
	// passed validation. Not serialized: a model does not get to claim it.
	FixValidated bool `json:"-"`

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

	// FromAnalyzer records that a deterministic analyzer reported this finding
	// rather than a model. Source already names WHICH one, but Source is free
	// text a later pass may have rewritten, and two policies need the fact
	// itself rather than a string to pattern-match: linters.max_severity caps
	// what an analyzer's finding is acted on at, and the severity provenance
	// rules below are different for a tool that writes in its own vocabulary
	// than for a model that was handed ours.
	//
	// It is not serialized, for the same reason RawSeverity is not: every pass
	// that decodes a finding from a model's JSON gets it back zeroed, and a
	// consumer that recovered "reported by an analyzer" from a model's output
	// would be recovering the model's claim rather than the fact. Engine.triage
	// restores it from the pre-triage set instead.
	FromAnalyzer bool `json:"-"`
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

// LineSpan is a contiguous run of lines, inclusive. EndLine of zero, or below
// Line, means the span is the single line Line.
type LineSpan struct {
	Line    int `json:"line"`
	EndLine int `json:"end_line,omitempty"`
}

// Result is the structured output of a review call. Summary is only populated
// by the triage pass.
type Result struct {
	Findings []Finding `json:"findings"`
	Summary  string    `json:"summary"`

	// Dropped is triage's account of what it did not publish, by list
	// number, each with a reason. Every finding triage was given is either
	// published, listed here, or restored by the engine: a finding that
	// simply vanishes is a bug that looks like quality.
	Dropped []Drop `json:"dropped,omitempty"`
}

// Drop is one finding triage withheld, and why.
type Drop struct {
	Number int    `json:"number"`
	Reason string `json:"reason"`
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
