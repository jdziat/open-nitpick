package review

import (
	"slices"
	"strings"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/config"
)

// Verdict is triage's decision about one numbered finding.
// Finding numbers keep provenance stable when triage edits a title or anchor.
type Verdict struct {
	// Number is the finding's position in the list triage was shown, from 1.
	// Other fields may change without changing that identity.
	Number int `json:"number"`

	// Severity and Class are triage's re-rating. Class is still checked
	// against the reviewer's, which is the policy triage may not re-author.
	Severity string `json:"severity"`
	Class    string `json:"class"`

	// Line moves the anchor to the clearer duplicate's, which triage.md rule 1
	// asks for on a merge. Zero leaves it where the reviewer put it.
	Line int `json:"line,omitempty"`

	// Title, Rationale and Suggestion are the rewording, empty to keep the
	// reviewer's. Ignored under review.triage_no_new_claims.
	Title      string `json:"title,omitempty"`
	Rationale  string `json:"rationale,omitempty"`
	Suggestion string `json:"suggestion,omitempty"`
}

// Acknowledgement identifies a model nit that reports successful assessment
// without alleging a defect. Quote must repeat its entire original rationale.
type Acknowledgement struct {
	Number int    `json:"number"`
	Quote  string `json:"quote"`
	Reason string `json:"reason"`
}

// TriageResult is what the triage pass returns.
type TriageResult struct {
	// Verdicts retain the JSON name used in the triage prompt.
	Verdicts []Verdict `json:"findings"`
	Summary  string    `json:"summary"`

	// Unaccounted entries are restored; explicit decisions remain in Overruled.
	Dropped          []Drop            `json:"dropped,omitempty"`
	Acknowledgements []Acknowledgement `json:"acknowledgements,omitempty"`
}

// anchorTolerance bounds relocation to nearby duplicate findings.
// A distant location needs its own claim and supporting evidence.
const anchorTolerance = 10

// applyVerdict edits a finding without replacing its reviewer provenance.
func (e *Engine) applyVerdict(f *Finding, v Verdict) {
	moved := false
	if strings.TrimSpace(v.Severity) != "" && !strings.EqualFold(v.Severity, f.Severity) {
		moved = true
		f.Severity = v.Severity
		e.recordSeverity(f)
	}

	// Reclassifying a security defect as style could remove it under policy filters.
	if class := strings.TrimSpace(v.Class); class != "" && f.Class == "" {
		f.Class = class
		f.Class = e.normalizeClass(*f)
	} else if class != "" && !strings.EqualFold(class, f.Class) {
		e.log().Debug("keeping the review-pass class over triage's",
			"path", f.Path, "triage", class, "review", f.Class)
	}

	// Analyzer ratings remain original evidence. A model rating becomes stale
	// when triage replaces it.
	if moved && !f.FromAnalyzer {
		f.RawSeverity = ""
		f.SeverityTranslated = false
	}

	// Moving a suggestion would attach its patch to different code. Preserve
	// the original location when policy requires keeping the reviewer's claim.
	if e.Config != nil && e.Config.Review.TriageNoNewClaims && f.Suggestion != "" && v.Line > 0 && v.Line != f.Line {
		e.log().Debug("keeping the reviewer's line under the no-new-claims contract",
			"path", f.Path, "line", f.Line, "triage", v.Line)
	} else {
		e.moveAnchor(f, v.Line)
	}

	if e.Config != nil && e.Config.Review.TriageNoNewClaims {
		return
	}

	if strings.TrimSpace(v.Title) != "" {
		f.Title = v.Title
	}
	if strings.TrimSpace(v.Rationale) != "" {
		f.Rationale = v.Rationale
	}
	// Leading whitespace affects both patch application and no-op detection.
	if strings.TrimSpace(v.Suggestion) != "" {
		f.Suggestion = v.Suggestion
	}
}

// absorb retains evidence and attribution from both merged findings.
// Analyzer provenance must survive so its severity cap still applies.
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

// moveAnchor relocates a nearby duplicate while preserving its range length.
// Suggestions are cleared because their patches apply at the original location.
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

// acknowledgementDecisions excludes conflicting decisions and substantive evidence
// before allowing triage to classify an entry as an assessment acknowledgement.
func acknowledgementDecisions(findings []Finding, result TriageResult, judged map[int]Verdict) map[int]Acknowledgement {
	conflicted := make(map[int]bool)
	for _, d := range result.Dropped {
		conflicted[d.Number] = true
		conflicted[d.DuplicateOf] = true
	}
	counts := make(map[int]int)
	for _, a := range result.Acknowledgements {
		counts[a.Number]++
	}
	accepted := make(map[int]Acknowledgement)
	for _, a := range result.Acknowledgements {
		if a.Number < 1 || a.Number > len(findings) || counts[a.Number] != 1 || conflicted[a.Number] {
			continue
		}
		if _, ok := judged[a.Number]; ok {
			continue
		}
		f := findings[a.Number-1]
		if f.FromAnalyzer || f.Sev() != config.SeverityNit || strings.TrimSpace(f.Suggestion) != "" {
			continue
		}
		if strings.TrimSpace(a.Reason) == "" || strings.TrimSpace(a.Quote) == "" || a.Quote != f.Rationale {
			continue
		}
		accepted[a.Number] = a
	}
	return accepted
}
