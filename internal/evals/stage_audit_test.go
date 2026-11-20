package evals

import (
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/review"
)

func stageAuditFixture() (Fixture, review.Finding) {
	f := Fixture{Name: "stage-audit", Defects: []Defect{{Path: "a.go", Line: 3,
		Keywords: []string{"unchecked"}, Class: config.ClassCorrectness, WantSeverity: config.SeverityError, Why: "unchecked result"}}}
	finding := review.Finding{Path: "a.go", Line: 3, Title: "unchecked result", Class: string(config.ClassCorrectness), Severity: string(config.SeverityError)}
	return f, finding
}

func fullLadder(finding review.Finding, lostAt string) []review.Snapshot {
	var out []review.Snapshot
	lost := false
	for _, stage := range ExpectedStages(TypeReview, nil) {
		if stage == lostAt {
			lost = true
		}
		snap := review.Snapshot{Stage: stage}
		if !lost {
			snap.Findings = []review.Finding{finding}
		}
		out = append(out, snap)
	}
	return out
}

func TestStageAuditSeparatesGenerationMissesFromLaterLoss(t *testing.T) {
	f, finding := stageAuditFixture()
	expected := ExpectedStages(TypeReview, nil)
	audit := AttributeStages(f, fullLadder(finding, "triage"), expected)
	if len(audit) != 1 || !audit[0].Generated || audit[0].Published || audit[0].Unknown || audit[0].FirstUncreditedStage != "triage" {
		t.Fatalf("lost claim attributed to generation: %+v", audit)
	}
	audit = AttributeStages(f, fullLadder(finding, "generation"), expected)
	if audit[0].Generated || audit[0].FirstUncreditedStage != "generation" || audit[0].Unknown {
		t.Fatalf("generation miss attributed downstream: %+v", audit)
	}
	audit = AttributeStages(f, nil, expected)
	if !audit[0].Unknown {
		t.Fatal("missing stage evidence became a generation diagnosis")
	}
}

func TestAttributeStagesUnknownWhenMiddleSnapshotMissing(t *testing.T) {
	f, finding := stageAuditFixture()
	var snaps []review.Snapshot
	for _, snap := range fullLadder(finding, "anchors-after-triage") {
		if snap.Stage == "triage" {
			continue // the evidence that would show whether triage kept the claim
		}
		snaps = append(snaps, snap)
	}
	audit := AttributeStages(f, snaps, ExpectedStages(TypeReview, nil))
	if !audit[0].Unknown {
		t.Fatalf("a missing triage snapshot still named a stage: %+v", audit[0])
	}
	if audit[0].StagePresent["triage"] {
		t.Fatal("triage was recorded present with no snapshot")
	}
}

func TestAttributeStagesNamesStageWhenAllPresent(t *testing.T) {
	f, finding := stageAuditFixture()
	audit := AttributeStages(f, fullLadder(finding, "anchors-after-triage"), ExpectedStages(TypeReview, nil))
	if audit[0].Unknown || audit[0].FirstUncreditedStage != "anchors-after-triage" {
		t.Fatalf("complete evidence did not name the loss: %+v", audit[0])
	}
	for stage, ok := range audit[0].StagePresent {
		if !ok {
			t.Fatalf("stage %s reported absent in a complete ladder", stage)
		}
	}
}

func TestAttributeStagesIgnoresDisabledOptionalStages(t *testing.T) {
	f, finding := stageAuditFixture()
	// validation and verification are off: their absence is not lost evidence.
	audit := AttributeStages(f, fullLadder(finding, "publication-gate"), ExpectedStages(TypeReview, nil))
	if audit[0].Unknown {
		t.Fatalf("disabled validation made a complete audit Unknown: %+v", audit[0])
	}
	enabled := ExpectedStages(TypeReview, []string{StageValidation})
	audit = AttributeStages(f, fullLadder(finding, "publication-gate"), enabled)
	if !audit[0].Unknown {
		t.Fatal("an enabled validation stage with no snapshot was not Unknown")
	}
}

func TestExpectedStagesEmptyForUnregisteredLadder(t *testing.T) {
	if got := ExpectedStages(TypeDeepReview, nil); got != nil {
		t.Fatalf("deep-review ladder = %v, want none until registered from captures", got)
	}
	f, finding := stageAuditFixture()
	audit := AttributeStages(f, fullLadder(finding, "triage"), ExpectedStages(TypeDeepReview, nil))
	if !audit[0].Unknown {
		t.Fatal("an unregistered ladder produced a named loss stage")
	}
}

func TestAttributeStagesUsesExplicitPersonaNotEnvironment(t *testing.T) {
	f := Fixture{Name: "mixed", Defects: []Defect{
		{Path: "a.go", Line: 3, Keywords: []string{"unchecked"}, Class: config.ClassCorrectness, WantSeverity: config.SeverityError, Why: "unchecked result"},
		{Path: "a.go", Line: 9, Keywords: []string{"injection"}, Class: config.ClassSecurity, WantSeverity: config.SeverityError, Why: "sql injection"},
	}}
	if got := len(AttributeStagesForPersona(f, nil, nil, true)); got != 1 {
		t.Fatalf("security persona audited %d plants, want only the security one", got)
	}
	if got := len(AttributeStagesForPersona(f, nil, nil, false)); got != 2 {
		t.Fatalf("review persona audited %d plants, want both", got)
	}
}
