package review

import (
	"github.com/jdziat/open-nitpick/internal/bundle"
	"slices"
	"strings"
)

// Triage answers against the numbered list it was given, not with findings of
// its own.
//
// It used to return whole findings, and the engine matched them back to the
// originals by Finding.Key(): path, line and normalized title. Triage is
// allowed to reword and to move a line to the clearer duplicate's, so the key
// it was matched by is made of the two fields it is allowed to change. When it
// used that permission the match failed and the finding published with its
// Class, Source, Evidence, FromAnalyzer, RawSeverity and SeverityTranslated
// gone: the attribution chain, the retrieved entries, and the analyzer cap,
// silently. renderForTriage's own comment named this in 2026, one layer down.
//
// A verdict names the finding by the number it had in the list. The engine
// keeps the originals and applies the verdict to them, so none of that
// metadata leaves Go and there is nothing to restore, no matcher, and no
// tolerance. A number that is wrong costs a severity on one finding, which a
// reader can see and an operator can reverse; a match that was wrong put one
// reviewer's attribution on another reviewer's words.

// Verdict is triage's decision about one numbered finding.
type Verdict struct {
	// Number is the finding's position in the list triage was shown, from 1.
	// It is the identity: everything else here is an edit to that finding.
	Number int `json:"number"`

	// Severity and Class are triage's re-rating. Class is still checked
	// against the reviewer's, which is the policy triage may not re-author.
	Severity string `json:"severity"`
	Class    string `json:"class"`

	// Line moves the anchor to the clearer duplicate's, which triage.md rule 1
	// asks for on a merge. Zero leaves it where the reviewer put it.
	Line int `json:"line,omitempty"`

	// Title, Rationale and Suggestion are the rewording, empty to keep the
	// reviewer's. Ignored entirely under review.triage_no_new_claims, which
	// already put the reviewer's words back by another route.
	Title      string `json:"title,omitempty"`
	Rationale  string `json:"rationale,omitempty"`
	Suggestion string `json:"suggestion,omitempty"`
}

// TriageResult is what the triage pass returns.
type TriageResult struct {
	// Verdicts decode from "findings" so the prompt's own noun still matches
	// the field, and so a model that has seen the older contract is not being
	// asked to learn a new key at the same time as a new shape.
	Verdicts []Verdict `json:"findings"`
	Summary  string    `json:"summary"`

	// Dropped is triage's account of what it merged, by list number. It is the
	// only place a finding may go missing from Verdicts, and the engine
	// restores anything absent from both.
	Dropped []Drop `json:"dropped,omitempty"`
}

// anchorTolerance is how far a verdict may move a finding's line.
//
// A merge moves an anchor onto the clearer duplicate's line, which is a few
// lines. A verdict naming a line far from the reviewer's is not that: it is a
// claim about code the reviewer never read, about to publish under the
// reviewer's attribution.
const anchorTolerance = 10

// applyVerdict edits a finding with triage's decision about it.
//
// The finding is the reviewer's own object, so Class, Source, Evidence,
// FromAnalyzer, RawSeverity and SeverityTranslated are already on it and are
// not touched here. Only what triage is allowed to decide is written.
func (e *Engine) applyVerdict(f *Finding, v Verdict) {
	moved := false
	if strings.TrimSpace(v.Severity) != "" && !strings.EqualFold(v.Severity, f.Severity) {
		moved = true
		f.Severity = v.Severity
		e.recordSeverity(f)
	}

	// Class is checked, not taken. Triage is a filtering pass and may not
	// re-author policy: a finding the reviewer classed `security` coming back
	// `style` is a real defect disappearing at the default level because a
	// summarizer guessed.
	if class := strings.TrimSpace(v.Class); class != "" && f.Class == "" {
		f.Class = class
		f.Class = e.normalizeClass(*f)
	} else if class != "" && !strings.EqualFold(class, f.Class) {
		e.log().Debug("keeping the review-pass class over triage's",
			"path", f.Path, "triage", class, "review", f.Class)
	}

	// The severity word the reporter used goes stale when triage moves the
	// level, unless an analyzer printed it. "HIGH" is what semgrep said and
	// stays true whatever we publish at; a model's own "P1" described a rating
	// nobody holds any more, and leaving it makes the report quote the model
	// against a level it did not choose.
	if moved && !f.FromAnalyzer {
		f.RawSeverity = ""
		f.SeverityTranslated = false
	}

	// The anchor moves only within the finding's own file, which is free here:
	// the verdict names the finding by number, so there is no path to disagree
	// about. How FAR it may move is not free.
	//
	// Under the no-new-claims contract a move that would cost the reviewer
	// their suggestion is refused instead. Moving the line and dropping the
	// patch publishes triage's anchor and one less of the reviewer's words,
	// under a setting whose whole purpose is the opposite; the deleted
	// noclaims.restore reverted the line for the same reason.
	if e.Config != nil && e.Config.Review.TriageNoNewClaims && f.Suggestion != "" && v.Line > 0 && v.Line != f.Line {
		e.log().Debug("keeping the reviewer's line under the no-new-claims contract",
			"path", f.Path, "line", f.Line, "triage", v.Line)
	} else {
		e.moveAnchor(f, v.Line)
	}

	// The rewording, unless the no-new-claims contract is on, which exists to
	// publish the reviewer's own words and already refuses triage's.
	if e.Config != nil && e.Config.Review.TriageNoNewClaims {
		return
	}

	if strings.TrimSpace(v.Title) != "" {
		f.Title = v.Title
	}
	if strings.TrimSpace(v.Rationale) != "" {
		f.Rationale = v.Rationale
	}
	// Taken verbatim. A suggestion is committable code and its leading
	// whitespace is part of it: trimming one turned a replacement of two lines
	// with themselves into a patch that no longer matched the file, so the
	// no-op check stopped firing and a one-click commit appeared for a change
	// that changes nothing. The emptiness test trims; the value never does.
	if strings.TrimSpace(v.Suggestion) != "" {
		f.Suggestion = v.Suggestion
	}
}

// absorb folds a merged finding into the one it was merged into.
//
// Evidence and Source join rather than one replacing the other. "Flagged by
// golangci-lint(gosec), triaged by claude" is a sentence about who found a
// defect, and when two reviewers found it the true sentence names both:
// keeping one destroys a fact rather than choosing between two guesses. The
// analyzer flag is sticky for the same reason, so a merge cannot lift an
// analyzer finding out of linters.max_severity.
func absorb(survivor *Finding, merged Finding) {
	if merged.TaskContext != nil {
		if survivor.TaskContext == nil {
			survivor.TaskContext = merged.TaskContext
		} else if survivor.TaskContext.ID != merged.TaskContext.ID || survivor.TaskContext.Text != merged.TaskContext.Text {
			combined := &TaskContext{ID: survivor.TaskContext.ID + "+" + merged.TaskContext.ID, Text: survivor.TaskContext.Text + "\n\n" + merged.TaskContext.Text, Lines: map[string]int{}, Spans: map[string][]bundle.SourceSpan{}}
			for _, scope := range []*TaskContext{survivor.TaskContext, merged.TaskContext} {
				for name, spans := range scope.Spans {
					combined.Spans[name] = append(combined.Spans[name], spans...)
				}
				for name, lines := range scope.Lines {
					combined.Lines[name] = max(combined.Lines[name], lines)
				}
			}
			for name, spans := range combined.Spans {
				slices.SortFunc(spans, func(a, b bundle.SourceSpan) int {
					if a.Start < b.Start {
						return -1
					}
					if a.Start > b.Start {
						return 1
					}
					return 0
				})
				combined.Spans[name] = spans
			}
			survivor.TaskContext = combined
		}
	}
	survivor.Evidence = capEvidence(union(survivor.Evidence, merged.Evidence))

	switch {
	case merged.Source == "" || survivor.Source == merged.Source:
	case survivor.Source == "":
		survivor.Source = merged.Source
	default:
		survivor.Source += ", " + merged.Source
	}

	survivor.FromAnalyzer = survivor.FromAnalyzer || merged.FromAnalyzer
}

// union appends what b adds to a, keeping a's order.
func union(a, b []string) []string {
	seen := make(map[string]bool, len(a))
	for _, s := range a {
		seen[s] = true
	}
	for _, s := range b {
		if !seen[s] {
			seen[s] = true
			a = append(a, s)
		}
	}
	return a
}

// capEvidence re-applies the cap a union can exceed.
func capEvidence(e []string) []string {
	if len(e) > evidenceCap {
		return e[:evidenceCap]
	}
	return e
}

// moveAnchor takes triage's line, within limits.
//
// Bounded by anchorTolerance, which is what the no-new-claims path used before
// this: a verdict naming a line far from the reviewer's is not a merge moving
// an anchor onto the clearer duplicate, it is a claim about code the reviewer
// never read, published under the reviewer's attribution.
//
// EndLine travels with Line. A span left behind ends before it starts, and
// span() then silently prints one line of a range someone wrote about several.
//
// A suggestion does not travel at all. noclaims.restore says why: a patch
// reattached to a line triage moved offers a one-click commit over the wrong
// code. The finding keeps its words and loses the button.
func (e *Engine) moveAnchor(f *Finding, line int) {
	if line <= 0 || line == f.Line {
		return
	}
	delta := line - f.Line
	if delta > anchorTolerance || delta < -anchorTolerance {
		e.log().Warn("triage moved a finding further than an anchor moves; keeping the reviewer's line",
			"path", f.Path, "from", f.Line, "to", line)
		return
	}

	if f.Suggestion != "" {
		e.log().Debug("dropping a suggestion from a relocated finding",
			"path", f.Path, "from", f.Line, "to", line)
		f.Suggestion = ""
		f.FixEndLine = 0
	}
	if f.EndLine > 0 {
		f.EndLine += delta
	}
	f.Line = line
}
