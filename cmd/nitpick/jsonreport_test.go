package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/review"
)

// A run with nothing to report and a run that did not finish must not print the
// same thing, so the report always carries result and complete, and an empty
// findings list prints as [] rather than null.
func TestTheJSONReportSaysWhetherTheRunFinishedAndCarriesConfidence(t *testing.T) {
	var buf bytes.Buffer
	if err := writeJSONReport(&buf, resultClean, &review.Report{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"findings": []`) || !strings.Contains(buf.String(), `"complete": true`) {
		t.Errorf("empty clean report:\n%s", buf.String())
	}

	buf.Reset()
	c := 0.8
	report := &review.Report{
		Stages:   []review.StageStatus{{Stage: "triage", Reason: "timeout"}},
		Findings: []review.Finding{{Path: "a.go", Line: 3, Severity: "warning", Class: "correctness", Title: "t", Rationale: "r", Confidence: &c, Citation: "x := y"}},
	}
	if err := writeJSONReport(&buf, resultError, report); err != nil {
		t.Fatal(err)
	}
	var got jsonReport
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("not json: %v\n%s", err, buf.String())
	}
	if got.Complete || got.Result != "error" || len(got.FailedStages) != 1 {
		t.Errorf("report = %+v, want an incomplete error run naming its failed stage", got)
	}
	if len(got.Findings) != 1 || got.Findings[0].Confidence == nil || *got.Findings[0].Confidence != 0.8 || got.Findings[0].Citation != "x := y" {
		t.Errorf("findings = %+v, want the confidence and citation carried", got.Findings)
	}
}

func TestAnUnknownOutputFormatIsRefused(t *testing.T) {
	err := runReview(context.Background(), []string{"-repo", t.TempDir(), "-format", "xml"})
	if err == nil || !strings.Contains(err.Error(), "invalid -format") {
		t.Errorf("err = %v, want -format xml refused", err)
	}
}
