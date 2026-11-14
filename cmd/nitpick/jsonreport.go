package main

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/jdziat/open-nitpick/internal/review"
)

// jsonReport is what `review -format json` prints: the result and every
// published finding, with the fields a program needs and nothing rendered.
//
// It exists so a measurement reads findings rather than parsing the review's
// markdown, which drifts with every change to how a comment is worded. Result
// and Complete are always present, so an empty findings list can be told from
// a run that published nothing because it did not finish.
type jsonReport struct {
	Result       string        `json:"result"`
	Complete     bool          `json:"complete"`
	FailedStages []string      `json:"failed_stages"`
	Findings     []jsonFinding `json:"findings"`
}

// jsonFinding is one published finding.
type jsonFinding struct {
	Path       string   `json:"path"`
	Line       int      `json:"line"`
	EndLine    int      `json:"end_line,omitempty"`
	Severity   string   `json:"severity"`
	Class      string   `json:"class"`
	Category   string   `json:"category,omitempty"`
	Title      string   `json:"title"`
	Rationale  string   `json:"rationale"`
	Suggestion string   `json:"suggestion,omitempty"`
	Confidence *float64 `json:"confidence,omitempty"`
	Citation   string   `json:"citation,omitempty"`
	Source     string   `json:"source,omitempty"`
}

// writeJSONReport prints a finished review as one JSON object.
func writeJSONReport(w io.Writer, result actionResult, report *review.Report) error {
	out := jsonReport{Result: string(result), FailedStages: []string{}, Findings: []jsonFinding{}}
	if report != nil {
		out.Complete = report.PipelineComplete() && result != resultLimit
		out.FailedStages = append(out.FailedStages, report.FailedStages()...)
		for _, f := range report.Findings {
			out.Findings = append(out.Findings, jsonFinding{
				Path: f.Path, Line: f.Line, EndLine: f.EndLine,
				Severity: f.Sev().String(), Class: f.Class, Category: f.Category,
				Title: f.Title, Rationale: f.Rationale, Suggestion: f.Suggestion,
				Confidence: f.Confidence, Citation: f.Citation, Source: f.Source,
			})
		}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return fmt.Errorf("write the json report: %w", err)
	}
	return nil
}
