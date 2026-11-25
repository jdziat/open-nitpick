package main

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/nitpick-oss/nitpick/internal/review"
)

// jsonReport is what `review -format json` prints: the result and every
// published finding, with the fields a program needs and nothing rendered.
//
// It exists so a measurement reads findings rather than parsing the review's
// markdown, which drifts with every change to how a comment is worded. Result
// and Complete are always present, so an empty findings list can be told from
// a run that published nothing because it did not finish.
type jsonReport struct {
	Result               string                  `json:"result"`
	Complete             bool                    `json:"complete"`
	FailedStages         []string                `json:"failed_stages"`
	Findings             []jsonFinding           `json:"findings"`
	Withheld             []jsonWithheld          `json:"withheld"`
	Usage                usageJSON               `json:"usage"`
	Learnings            review.LearningsStatus  `json:"learnings"`
	Execution            review.ExecutionSummary `json:"execution"`
	ReusedAnalyzerStages int                     `json:"reused_analyzer_stages,omitempty"`
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

// jsonWithheld is a finding the reviewer reported and a later stage held back,
// with the stage that held it and its reason. Listed so a measurement can
// score what was drafted as well as what was posted, from the same run.
type jsonWithheld struct {
	jsonFinding
	WithheldBy string `json:"withheld_by"`
	Reason     string `json:"reason"`

	// Revised is the severity an expert moved the finding to, when that put it
	// below what is published. It is absent for a finding that was refuted or
	// held back by a filter, and it is what tells the two apart: an expert that
	// agreed the defect is real and rated it low is not one that refuted it.
	Revised string `json:"revised,omitempty"`

	// Cited is the knowledge entry the expert named as deciding its verdict.
	Cited string `json:"cited,omitempty"`
}

func toJSONFinding(f review.Finding) jsonFinding {
	return jsonFinding{
		Path: f.Path, Line: f.Line, EndLine: f.EndLine,
		Severity: f.Sev().String(), Class: f.Class, Category: f.Category,
		Title: f.Title, Rationale: f.Rationale, Suggestion: f.Suggestion,
		Confidence: f.Confidence, Citation: f.Citation, Source: f.Source,
	}
}

// writeJSONReport prints a finished review as one JSON object.
func writeJSONReport(w io.Writer, result actionResult, report *review.Report) error {
	out := jsonReport{Result: string(result), FailedStages: []string{}, Findings: []jsonFinding{}, Withheld: []jsonWithheld{}, Usage: usageForJSON(nil)}
	if report != nil {
		out.Usage = usageForJSON(report.ModelCalls)
		out.Learnings = report.Learnings
		out.Execution = report.Execution
		out.ReusedAnalyzerStages = report.ReusedAnalyzerStages
		out.Complete = report.PipelineComplete() && result != resultLimit && result != resultError
		out.FailedStages = append(out.FailedStages, report.FailedStages()...)
		for _, f := range report.Findings {
			out.Findings = append(out.Findings, toJSONFinding(f))
		}
		for _, o := range report.Overruled {
			out.Withheld = append(out.Withheld, jsonWithheld{
				jsonFinding: toJSONFinding(o.Finding), WithheldBy: o.Expert, Reason: o.Reason,
				Revised: string(o.Revised), Cited: o.Cited,
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
