package practices

import (
	"github.com/jdziat/open-nitpick/internal/config"
	"strings"
	"testing"
)

func TestRequiredCoverageCannotPassWithoutExaminedTargets(t *testing.T) {
	for _, state := range []State{Completed, Partial, Unavailable, Failed, NotSelected, NotApplicable, "unknown"} {
		t.Run(string(state), func(t *testing.T) {
			r := fixtureReport(Check{ID: "commits", Version: "1", Instrument: Deterministic, Required: true, State: state, Reason: "no targets"})
			if r.ExitCode() != 2 {
				t.Fatalf("empty %s check passed: %+v", state, r)
			}
		})
	}
}

func TestCompletedViolationsAndMissingCoverageRemainIndependent(t *testing.T) {
	good := Check{ID: "commits", Version: "1", Instrument: Deterministic, Required: true, State: Completed,
		Planned: []Target{{Kind: CommitTarget, ID: "abc"}}, Examined: []Target{{Kind: CommitTarget, ID: "abc"}}}
	r := fixtureReport(good)
	if r.ExitCode() != 0 {
		t.Fatalf("completed clean control: %+v", r)
	}
	r.Checks[0].Findings = []Finding{{Rule: "commits.subject", Target: good.Examined[0], Blocking: true, Title: "bad subject"}}
	if r.ExitCode() != 1 {
		t.Fatal("blocking finding did not fail")
	}
	r.Checks = append(r.Checks, Check{ID: "design", Version: "1", Instrument: Model, Required: true, State: Partial, Reason: "budget"})
	if r.ExitCode() != 2 || len(r.Checks[0].Findings) != 1 {
		t.Fatal("incomplete coverage must win without deleting violations")
	}
}

func TestCoverageRejectsOmittedAndUnplannedTargets(t *testing.T) {
	a, b := Target{Kind: FileTarget, ID: "a.go"}, Target{Kind: FileTarget, ID: "b.go"}
	for _, check := range []Check{
		{Planned: []Target{a, b}, Examined: []Target{a}},
		{Planned: []Target{a}, Examined: []Target{a, b}},
		{Planned: []Target{a}, Examined: []Target{a, a}},
		{Planned: []Target{a}, Examined: []Target{a}, Omitted: []Omission{{Target: b, Reason: "limit"}}},
	} {
		check.ID, check.Version, check.Instrument, check.State, check.Required = "check", "1", Deterministic, Completed, true
		if fixtureReport(check).ExitCode() != 2 {
			t.Errorf("incorrect coverage accepted: %+v", check)
		}
	}
}

func TestAdvisoryFindingsAndOptionalFailuresDoNotBlock(t *testing.T) {
	target := Target{Kind: UnitTarget, ID: "cache"}
	r := fixtureReport(Check{ID: "design", Version: "1", Instrument: Model, Required: true, State: Completed,
		Planned: []Target{target}, Examined: []Target{target}, Findings: []Finding{{Rule: "hidden-state", Target: target, Title: "state ownership"}}},
		Check{ID: "security", Version: "1", Instrument: Deterministic, State: Unavailable, Reason: "not installed"})
	if r.ExitCode() != 0 {
		t.Fatal("advisory findings or optional failure blocked")
	}
	r.Checks[0].FailedStages = []string{"validation"}
	if r.ExitCode() != 2 {
		t.Fatal("failed stage passed")
	}
}

func TestUnpinnedReportsAndDuplicateChecksCannotPass(t *testing.T) {
	target := Target{Kind: FileTarget, ID: "a.go"}
	r := fixtureReport(Check{ID: "x", Version: "1", Instrument: Deterministic, Required: true, State: Completed, Planned: []Target{target}, Examined: []Target{target}})
	if problems := r.Problems(); len(problems) != 0 {
		t.Fatalf("invalid duplicate-test control: %v", problems)
	}
	r.Checks = append(r.Checks, r.Checks[0])
	if len(r.Problems()) == 0 {
		t.Fatal("duplicate check accepted")
	}
	r.Checks = r.Checks[:1]
	r.PolicyDigest = ""
	if len(r.Problems()) == 0 {
		t.Fatal("unidentified policy accepted")
	}
}

func fixtureReport(checks ...Check) Report {
	return Report{SchemaVersion: SchemaVersion, Profile: "engineering", Revision: "head-sha", PolicySource: "defaults", PolicyDigest: "policy-sha", Checks: checks}
}

func TestSnapshotAloneCannotEstablishEngineeringAssessment(t *testing.T) {
	target := Target{Kind: FileTarget, ID: "a.go"}
	r := fixtureReport(Check{ID: "snapshot", Version: "1", Instrument: Deterministic, State: Completed, Planned: []Target{target}, Examined: []Target{target}})
	if r.ExitCode() != 2 {
		t.Fatal("reading source was counted as a substantive engineering check")
	}
}

func TestOptionalPartialCoverageStillRejectsContradictoryOmissions(t *testing.T) {
	a, b := Target{Kind: FileTarget, ID: "a.go"}, Target{Kind: FileTarget, ID: "b.go"}
	good := Check{ID: "commits", Version: "1", Instrument: Deterministic, State: Completed, Planned: []Target{a}, Examined: []Target{a}}
	for _, omissions := range [][]Omission{
		{{Target: a, Reason: "budget"}},
		{{Target: Target{Kind: FileTarget, ID: "unplanned.go"}, Reason: "budget"}},
		{{Target: b, Reason: "budget"}, {Target: b, Reason: "budget"}},
		{{Target: b}},
	} {
		partial := Check{ID: "design", Version: "1", Instrument: Model, State: Partial, Reason: "budget", Planned: []Target{a, b}, Examined: []Target{a}, Omitted: omissions}
		if fixtureReport(good, partial).ExitCode() != 2 {
			t.Fatalf("contradictory optional coverage passed: %+v", partial)
		}
	}
}

func TestConfiguredBoundariesCannotDisappearFromTheReport(t *testing.T) {
	target := Target{Kind: FileTarget, ID: "a.go"}
	r := fixtureReport(Check{ID: "conventions", Version: "1", Instrument: Deterministic, State: Completed, Planned: []Target{target}, Examined: []Target{target}})
	ApplyPolicy(&r, config.Practices{Boundaries: []config.PracticeBoundary{{From: "app/api", Forbid: []string{"app/db"}, Reason: "fixture"}}})
	if len(r.Checks) != 2 || r.Checks[1].ID != "design-boundaries" || !r.Checks[1].Required || r.Checks[1].State != Unavailable || r.ExitCode() != 2 {
		t.Fatalf("configured boundary silently disappeared: %+v", r)
	}
}

func TestReportSchemaDistinguishesOlderCoverageContracts(t *testing.T) {
	target := Target{Kind: CommitTarget, ID: "sha"}
	r := fixtureReport(Check{ID: "commits", Version: "1", Instrument: Deterministic, State: Completed, Planned: []Target{target}, Examined: []Target{target}})
	if len(r.Problems()) != 0 || r.SchemaVersion != 2 {
		t.Fatalf("current report contract invalid: %+v", r)
	}
	r.SchemaVersion = 1
	if !strings.Contains(strings.Join(r.Problems(), ";"), "unsupported report schema version 1; expected 2") {
		t.Fatalf("old contract lacks an explicit version diagnosis: %v", r.Problems())
	}
}

func TestTaskOmissionsRequireValidScopeAndAnExplanation(t *testing.T) {
	source := Target{Kind: FileTarget, ID: "a.go"}
	unit := Target{Kind: UnitTarget, ID: "package:a"}
	commit := Target{Kind: CommitTarget, ID: "sha"}
	for _, omission := range []Omission{
		{Target: source},
		{Target: source, Reason: " \t"},
		{Reason: "budget"},
		{Target: commit, Reason: "budget"},
		{Target: Target{Kind: FileTarget, ID: "unrelated.go"}, Reason: "budget"},
	} {
		r := fixtureReport(Check{ID: "commits", Version: "1", Instrument: Deterministic, State: Completed, Planned: []Target{commit}, Examined: []Target{commit}},
			Check{ID: "design", Version: "1", Instrument: Model, State: Partial, Reason: "budget", Planned: []Target{unit}, Omitted: []Omission{{Target: unit, Reason: "budget"}}, Tasks: []DesignTask{{ID: unit.ID, Source: source, Sources: []Target{source}, Purpose: "review a", Omitted: []Omission{{Target: source, Reason: "budget"}}}}})
		if len(r.Problems()) != 0 {
			t.Fatalf("valid optional omission control failed: %v", r.Problems())
		}
		r.Checks[1].Tasks[0].Omitted[0] = omission
		if len(r.Problems()) == 0 {
			t.Fatalf("invalid task omission accepted: %+v", omission)
		}
	}
}

func TestTaskOmissionsRejectDuplicateEvidenceButRetainDistinctReasons(t *testing.T) {
	source := Target{Kind: FileTarget, ID: "a.go"}
	unit := Target{Kind: UnitTarget, ID: "package:a"}
	commit := Target{Kind: CommitTarget, ID: "sha"}
	r := fixtureReport(Check{ID: "commits", Version: "1", Instrument: Deterministic, State: Completed, Planned: []Target{commit}, Examined: []Target{commit}},
		Check{ID: "design", Version: "1", Instrument: Model, State: Partial, Reason: "budget", Planned: []Target{unit}, Omitted: []Omission{{Target: unit, Reason: "budget"}}, Tasks: []DesignTask{{ID: unit.ID, Source: source, Sources: []Target{source}, Purpose: "review a", Omitted: []Omission{{Target: unit, Reason: "missing source"}, {Target: unit, Reason: "cancelled"}}}}})
	if len(r.Problems()) != 0 {
		t.Fatalf("distinct causes were conflated: %v", r.Problems())
	}
	r.Checks[1].Tasks[0].Omitted[1] = r.Checks[1].Tasks[0].Omitted[0]
	if !strings.Contains(strings.Join(r.Problems(), ";"), "repeats an omission") {
		t.Fatalf("duplicate omission passed: %v", r.Problems())
	}
}
