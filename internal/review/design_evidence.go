package review

import (
	"slices"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/diff"
)

// filterTaskAnchors preserves valid task evidence outside the changed lines.
func (e *Engine) filterTaskAnchors(findings []Finding, files diff.Files) ([]Finding, []LinterDiscard, []Finding) {
	var kept []Finding
	var discarded []LinterDiscard
	var unpublished []Finding
	for _, finding := range findings {
		if finding.TaskContext == nil || finding.FromAnalyzer {
			accepted, dropped := e.filterAnchors([]Finding{finding}, files)
			kept = append(kept, accepted...)
			discarded = append(discarded, dropped...)
			continue
		}
		lines, known := finding.TaskContext.Lines[finding.Path]
		end := finding.EndLine
		if end == 0 {
			end = finding.Line
		}
		visible := known && finding.Line >= 1 && end >= finding.Line && end <= lines
		if !visible {
			visible = bundle.ContainsSourceRange(finding.TaskContext.Spans[finding.Path], finding.Line, end)
		}
		if !visible {
			finding.Unresolved = "finding location is outside the source supplied to its design task"
			unpublished = append(unpublished, finding)
			continue
		}
		file := files.Find(finding.Path)
		finding.SummaryOnly = file == nil || !slices.Contains(file.CommentableLines(), finding.Line)
		kept = append(kept, finding)
	}
	return kept, discarded, unpublished
}
