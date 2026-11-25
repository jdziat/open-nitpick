package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nitpick-oss/nitpick/internal/config"
	"github.com/nitpick-oss/nitpick/internal/practices"
	"github.com/nitpick-oss/nitpick/internal/review"
)

func TestEngineeringTriageFailureCannotReportClean(t *testing.T) {
	report := &review.Report{Practices: greenEngineeringEvidence(), Stages: []review.StageStatus{{Stage: "triage", Reason: "malformed-output"}}}
	result := classify(report, config.SeverityWarning, false)
	if result != resultError {
		t.Fatalf("failed engineering triage classified as %s", result)
	}
	var output bytes.Buffer
	if err := writeJSONReport(&output, result, report); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Result   string
		Complete bool
	}
	if err := json.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Complete || got.Result != "error" {
		t.Fatalf("failed engineering triage JSON: %s", output.String())
	}
	text := reviewText(ReviewOut{Complete: false, FailedStages: []string{"triage"}, FailOn: "none"})
	if !strings.Contains(text, "incomplete") || !strings.Contains(text, "Failed stages: triage") {
		t.Fatalf("MCP text hid failed stage: %s", text)
	}
}

func TestEngineeringModelFindingHonorsExplicitSeverityGate(t *testing.T) {
	report := &review.Report{Practices: greenEngineeringEvidence(), Findings: []review.Finding{{Severity: "warning", Title: "real warning"}}}
	if got := classify(report, config.SeverityWarning, false); got != resultFindings {
		t.Fatalf("warning gate returned %s", got)
	}
	if got := classify(report, config.SeverityNone, false); got != resultClean {
		t.Fatalf("explicit no gate returned %s", got)
	}
}

func TestRoundLimitHasAnExplicitExitError(t *testing.T) {
	if err := exitFor(resultLimit); err == nil {
		t.Fatal("round limit exited successfully")
	}
}

func greenEngineeringEvidence() *practices.Report {
	target := practices.Target{Kind: practices.FileTarget, ID: "a.go"}
	return &practices.Report{SchemaVersion: practices.SchemaVersion, Profile: "engineering", Revision: "head", PolicySource: "operator", PolicyDigest: "digest", Checks: []practices.Check{{ID: "conventions", Version: "1", Instrument: practices.Deterministic, State: practices.Completed, Planned: []practices.Target{target}, Examined: []practices.Target{target}}}}
}
