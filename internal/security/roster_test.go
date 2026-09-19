package security

import (
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/review"
)

func TestRequiredSkipFailedMakesRosterIncomplete(t *testing.T) {
	// Mutation: treating failed/skip as ran would claim complete falsely.
	roster := BuildRoster(
		[]review.LinterStatus{
			{Linter: "osv-scanner", Outcome: review.LinterFailed, State: "binary not found"},
			{Linter: "gitleaks", Outcome: review.LinterRan, State: "isolated"},
		},
		RequiredAlways,
		ModelStatus{Status: ModelSkippedByFlag, Reason: "-no-model"},
		false,
	)
	if roster.Complete {
		t.Fatal("required scanner failed must make Complete false")
	}
	if !hasFailedStage(roster, "osv-scanner") {
		t.Fatalf("failed_stages %v must mention osv-scanner", roster.FailedStages)
	}
}

func TestNonRequiredRanAloneCannotMakeComplete(t *testing.T) {
	// Mutation: len(scanners with ran)>0 ⇒ complete would greenwash.
	roster := BuildRoster(
		[]review.LinterStatus{
			{Linter: "semgrep", Outcome: review.LinterRan, State: "operator config"},
			{Linter: "osv-scanner", Outcome: review.LinterFailed, State: "binary not found"},
			{Linter: "gitleaks", Outcome: review.LinterFailed, State: "binary not found"},
		},
		RequiredAlways,
		ModelStatus{Status: ModelSkippedByFlag},
		false,
	)
	if roster.Complete {
		t.Fatal("non-required ran alone must not make Complete true when required failed")
	}
}

func TestGolangciRanWithoutGosecEnabledIsIncomplete(t *testing.T) {
	// Mutation: accepting golangci ran without gosec:enabled ⇒ exit 0.
	roster := BuildRoster(
		[]review.LinterStatus{
			{Linter: "osv-scanner", Outcome: review.LinterRan, State: "isolated"},
			{Linter: "gitleaks", Outcome: review.LinterRan, State: "isolated"},
			{Linter: "golangci-lint", Outcome: review.LinterRan, State: "isolated"},
		},
		append(RequiredAlways, "golangci-lint"),
		ModelStatus{Status: ModelSkippedByFlag},
		false,
	)
	if roster.Complete {
		t.Fatal("golangci-lint ran without gosec:enabled must be incomplete")
	}
	gl := scannerByID(t, roster, "golangci-lint")
	if gl.Status != StatusFailed || gl.Reason != "gosec not enabled" {
		t.Fatalf("golangci status=%q reason=%q, want failed/gosec not enabled", gl.Status, gl.Reason)
	}
}

func TestModelFailedMakesIncompleteEvenWhenScannersOK(t *testing.T) {
	// Mutation: mapping model fail to clean under fail-on.
	roster := BuildRoster(
		okAlwaysStatuses(),
		RequiredAlways,
		ModelStatus{Status: ModelFailed, Reason: "provider error"},
		false,
	)
	if roster.Complete {
		t.Fatal("model failed must make Complete false when scanners are OK")
	}
	if !hasFailedStage(roster, "model") {
		t.Fatalf("failed_stages %v must mention model", roster.FailedStages)
	}
}

func TestModelSkippedByFlagCompleteWhenScannersOK(t *testing.T) {
	roster := BuildRoster(
		okAlwaysStatuses(),
		RequiredAlways,
		ModelStatus{Status: ModelSkippedByFlag, Reason: "-no-model"},
		false,
	)
	if !roster.Complete {
		t.Fatalf("model skipped_by_flag with scanners OK must be complete; failed=%v", roster.FailedStages)
	}
}

func TestRosterAlwaysIncludesOsvScannerAndGitleaks(t *testing.T) {
	// Mutation: omitting a required id from the roster is a bug (plan test 16).
	roster := BuildRoster(
		[]review.LinterStatus{
			{Linter: "semgrep", Outcome: review.LinterRan, State: "config"},
		},
		nil,
		ModelStatus{Status: ModelSkippedByConfig},
		false,
	)
	for _, id := range RequiredAlways {
		got := scannerByID(t, roster, id)
		if got.ID != id {
			t.Fatalf("roster missing required always id %q", id)
		}
	}
	osv := scannerByID(t, roster, "osv-scanner")
	if osv.Status != StatusFailed || osv.Reason != "required scanner not in roster" {
		t.Fatalf("missing osv status=%q reason=%q", osv.Status, osv.Reason)
	}
}

func TestLinterSkippedMapsToNotApplicable(t *testing.T) {
	roster := BuildRoster(
		[]review.LinterStatus{
			{Linter: "osv-scanner", Outcome: review.LinterRan, State: "isolated"},
			{Linter: "gitleaks", Outcome: review.LinterSkipped, State: "no files it analyzes were selected for review"},
			{Linter: "golangci-lint", Outcome: review.LinterRan, State: "isolated; gosec:enabled"},
		},
		append(RequiredAlways, "golangci-lint"),
		ModelStatus{Status: ModelSkippedByFlag},
		false,
	)
	if !roster.Complete {
		t.Fatalf("skipped→not_applicable should satisfy; failed=%v", roster.FailedStages)
	}
	if got := scannerByID(t, roster, "gitleaks").Status; got != StatusNotApplicable {
		t.Fatalf("gitleaks status=%q, want not_applicable", got)
	}
}

func TestOpaqueSkipOnRequiredAlwaysIsIncomplete(t *testing.T) {
	// Mutation: mapping every LinterSkipped to not_applicable would satisfy
	// RequiredAlways when gitleaks was disabled or aborted without a
	// no-targets reason.
	roster := BuildRoster(
		[]review.LinterStatus{
			{Linter: "osv-scanner", Outcome: review.LinterRan, State: "isolated"},
			{Linter: "gitleaks", Outcome: review.LinterSkipped, State: "disabled by operator"},
		},
		RequiredAlways,
		ModelStatus{Status: ModelSkippedByFlag},
		false,
	)
	if roster.Complete {
		t.Fatal("opaque skip on RequiredAlways must make Complete false")
	}
	got := scannerByID(t, roster, "gitleaks")
	if got.Status != StatusSkipped {
		t.Fatalf("gitleaks status=%q, want skipped", got.Status)
	}
}

func okAlwaysStatuses() []review.LinterStatus {
	return []review.LinterStatus{
		{Linter: "osv-scanner", Outcome: review.LinterRan, State: "isolated"},
		{Linter: "gitleaks", Outcome: review.LinterRan, State: "isolated"},
	}
}

func scannerByID(t *testing.T, roster Roster, id string) ScannerStatus {
	t.Helper()
	for _, s := range roster.Scanners {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("roster missing scanner %q; have %v", id, roster.Scanners)
	return ScannerStatus{}
}

func hasFailedStage(roster Roster, needle string) bool {
	for _, s := range roster.FailedStages {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}
