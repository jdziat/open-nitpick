package evals

import (
	"errors"
	"testing"
	"time"

	"github.com/nitpick-oss/nitpick/internal/config"
	"github.com/nitpick-oss/nitpick/internal/review"
)

func TestFailedReviewCountsPlantsAndRetainsItsCost(t *testing.T) {
	f := Fixtures()[0]
	var cell evaluationCell
	cell.record(f, nil, errors.New("provider failed"), Cost{USD: 0.25, Known: true},
		RunResult{Duration: 2 * time.Second, Usage: TokenUsage{Failed: 1}})
	if cell.reviews != 1 || cell.lost != 1 || cell.plants != len(PersonaDefects(f)) || cell.located != 0 {
		t.Fatalf("failure excluded from scheduled denominator: %+v", cell)
	}
	if cell.usd != 0.25 || cell.seconds != 2 || cell.timed != 1 || cell.failedAttempts != 1 {
		t.Fatalf("failed review lost its cost or duration: %+v", cell)
	}
	for _, defect := range PersonaDefects(f) {
		if cell.byBand[defect.WantSeverity][1] == 0 {
			t.Fatalf("failed review lost %s band denominator", defect.WantSeverity)
		}
	}
}

func TestSilentReviewIsCompleteOnlyOnSuccessfulExecution(t *testing.T) {
	f := Fixtures()[0]
	var cell evaluationCell
	cell.record(f, nil, nil, Cost{}, RunResult{})
	cell.record(f, nil, errors.New("interrupted"), Cost{}, RunResult{})
	if cell.reviews != 2 || cell.lost != 1 || cell.plants != 2*len(PersonaDefects(f)) {
		t.Fatalf("silence and failure conflated: %+v", cell)
	}
	if cell.perFix[f.Name] == "lost" {
		t.Fatal("later failure overwrote earlier silence")
	}
}

func TestExplicitPersonaOverridesTheEnvironmentForReplay(t *testing.T) {
	f := Fixture{Name: "mixed", Defects: []Defect{
		{Path: "a.go", Line: 3, Keywords: []string{"unchecked"}, Class: config.ClassCorrectness, WantSeverity: config.SeverityError, Why: "unchecked result"},
		{Path: "a.go", Line: 9, Keywords: []string{"injection"}, Class: config.ClassSecurity, WantSeverity: config.SeverityError, Why: "sql injection"},
	}}
	t.Setenv("NITPICK_EVAL_SECURITY", "")
	security := true
	cell := evaluationCell{persona: &security}
	cell.record(f, nil, nil, Cost{}, RunResult{})
	if cell.plants != 1 {
		t.Fatalf("explicit security persona scored %d plants, want 1", cell.plants)
	}
	review := false
	cell = evaluationCell{persona: &review}
	cell.record(f, nil, nil, Cost{}, RunResult{})
	if cell.plants != 2 {
		t.Fatalf("explicit review persona scored %d plants, want 2", cell.plants)
	}
}

func TestProductionVisibilityDropsNitsWithoutChangingBenchmarkRecall(t *testing.T) {
	f := Fixture{Name: "nit", Defects: []Defect{
		{Path: "a.go", Line: 3, Keywords: []string{"unchecked"}, Class: config.ClassCorrectness, WantSeverity: config.SeverityNit, Why: "unchecked result"},
	}}
	finding := review.Finding{Path: "a.go", Line: 3, Title: "unchecked result", Class: string(config.ClassCorrectness), Severity: string(config.SeverityNit)}
	var cell evaluationCell
	cell.record(f, []review.Finding{finding}, nil, Cost{}, RunResult{})
	if cell.located != 1 {
		t.Fatalf("benchmark visibility located %d, want 1", cell.located)
	}
	if cell.productionLocated != 0 {
		t.Fatalf("a nit survived the published floor: %d", cell.productionLocated)
	}
	finding.Severity = string(config.SeverityWarning)
	cell = evaluationCell{}
	cell.record(f, []review.Finding{finding}, nil, Cost{}, RunResult{})
	if cell.productionLocated != 1 {
		t.Fatalf("a warning was dropped at the published floor: %d", cell.productionLocated)
	}
}

func TestProductionVisibilityKeepsUnrecognisedSeverity(t *testing.T) {
	kept := atProductionVisibility([]review.Finding{{Severity: "mystery"}, {Severity: string(config.SeverityNit)}})
	if len(kept) != 1 || kept[0].Severity != "mystery" {
		t.Fatalf("unrecognised severity was dropped or a nit kept: %+v", kept)
	}
}

func TestFailedReviewHasNoProductionCredit(t *testing.T) {
	f := Fixtures()[0]
	var cell evaluationCell
	cell.record(f, nil, errors.New("provider failed"), Cost{}, RunResult{})
	if cell.productionLocated != 0 {
		t.Fatalf("a failed review earned production credit: %d", cell.productionLocated)
	}
}
