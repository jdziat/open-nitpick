package evals

import (
	"testing"
)

// The failure this file exists to prevent, recorded at
// docs/findings.md#the-pre-registration-does-not-fit-the-corpus: a threshold
// fixed before the instrument was built, in units the instrument turned out
// not to express. "At least 3 of 12 plants" was registered against a corpus
// that holds six, so the condition as written could not be evaluated and had
// to be read proportionally after the fact, which is the reading a
// pre-registration exists to make unnecessary.
func TestEveryThresholdNamesADenominatorTheArmsCanExpress(t *testing.T) {
	p, err := LoadPreRegistration()
	if err != nil {
		t.Fatalf("LoadPreRegistration: %v", err)
	}
	if len(p.Thresholds) == 0 {
		t.Fatal("the pre-registration names no thresholds")
	}

	total := p.Arms.PreFix + p.Arms.Repaired + p.Arms.Clean
	valid := map[int]string{
		p.Arms.PreFix:   "pre_fix",
		p.Arms.Repaired: "repaired",
		p.Arms.Clean:    "clean",
		total:           "every snapshot",
	}

	for _, th := range p.Thresholds {
		if th.Denominator == 0 {
			t.Errorf("threshold %q names no denominator", th.Name)
			continue
		}
		if _, ok := valid[th.Denominator]; !ok {
			t.Errorf("threshold %q is read against %d, which is no arm: %d pre-fix, %d repaired, %d clean, %d total. "+
				"A threshold in units the instrument cannot express is the error recorded in findings.md.",
				th.Name, th.Denominator, p.Arms.PreFix, p.Arms.Repaired, p.Arms.Clean, total)
		}
		if th.AtLeast == nil && th.AtMost == nil {
			t.Errorf("threshold %q sets no bound, so nothing can fail it", th.Name)
		}
		if th.AtLeast != nil && th.AtMost != nil {
			t.Errorf("threshold %q sets both bounds; say which direction is the failure", th.Name)
		}
	}
}

// A threshold has to be reachable and refusable. A bound at or beyond the arm
// size is a bar that cannot fail, which docs/measurement.md already records as
// a way to write a condition that constrains nothing.
func TestNoThresholdIsUnfailable(t *testing.T) {
	p, err := LoadPreRegistration()
	if err != nil {
		t.Fatalf("LoadPreRegistration: %v", err)
	}
	for _, th := range p.Thresholds {
		if th.AtLeast != nil && *th.AtLeast > float64(th.Denominator) {
			t.Errorf("threshold %q asks for %g of %d, which cannot be reached", th.Name, *th.AtLeast, th.Denominator)
		}
		if th.AtMost != nil && *th.AtMost >= float64(th.Denominator) {
			t.Errorf("threshold %q allows %g of %d, which cannot be exceeded", th.Name, *th.AtMost, th.Denominator)
		}
	}
}

// The holdout is named before selection, and it is a repository rather than a
// sample of one: reserving rows lets a defect family leak across the split,
// and #84 asks for whole repositories or defect families.
func TestTheHoldoutIsNamedAndIsOneOfTheCorpora(t *testing.T) {
	p, err := LoadPreRegistration()
	if err != nil {
		t.Fatalf("LoadPreRegistration: %v", err)
	}
	if len(p.HoldoutRepos) == 0 {
		t.Fatal("no holdout repository is reserved; every corpus would be available for tuning")
	}
	named := map[string]bool{}
	for _, c := range p.Corpora {
		named[c.Repo] = true
	}
	for _, h := range p.HoldoutRepos {
		if !named[h] {
			t.Errorf("holdout %q is not one of the corpora", h)
		}
	}
	if len(p.HoldoutRepos) >= len(p.Corpora) {
		t.Error("every corpus is a holdout, so nothing is left to tune against")
	}
}

// The commitments that make a null result publishable, and keep the feature
// opt-in whatever the run returns.
func TestTheCommitmentsAreRecorded(t *testing.T) {
	p, err := LoadPreRegistration()
	if err != nil {
		t.Fatalf("LoadPreRegistration: %v", err)
	}
	if !p.PublishNegative {
		t.Error("the pre-registration does not commit to publishing a negative result")
	}
	if p.PromotesDefault {
		t.Error("the pre-registration promotes the default on its own; #84 makes that a separate decision")
	}
	if p.BudgetUSD <= 0 {
		t.Error("no spend ceiling is recorded")
	}
	if p.Repeats < 2 {
		t.Errorf("repeats = %d; one run cannot separate an effect from run-to-run variance", p.Repeats)
	}
	if p.Snapshots != "parent-to-commit" {
		t.Errorf("snapshots = %q, want parent-to-commit: a synthetic diff that re-adds every file "+
			"is a different measurement wearing this one's name", p.Snapshots)
	}
}

// The selection is frozen before it is used, and it matches what was
// registered. Empty and unfrozen is the shipped state: the schema and these
// guards land before the rows so the corpus cannot be assembled while
// retrieval is being tuned against it.
func TestTheSelectionMatchesWhatWasRegistered(t *testing.T) {
	p, err := LoadPreRegistration()
	if err != nil {
		t.Fatalf("LoadPreRegistration: %v", err)
	}
	s, err := LoadSelection()
	if err != nil {
		t.Fatalf("LoadSelection: %v", err)
	}

	if !s.Frozen {
		if len(s.Snapshots) != 0 {
			t.Error("the selection holds snapshots and is not marked frozen; freeze it before it is run")
		}
		t.Skip("the selection is empty and unfrozen, which is the shipped state")
	}

	arms := s.Arms()
	for name, want := range map[string]int{
		"pre_fix": p.Arms.PreFix, "repaired": p.Arms.Repaired, "clean": p.Arms.Clean,
	} {
		if arms[name] != want {
			t.Errorf("the %s arm holds %d snapshots, the pre-registration says %d", name, arms[name], want)
		}
	}

	seen := map[string]bool{}
	for _, snap := range s.Snapshots {
		if seen[snap.ID] {
			t.Errorf("duplicate snapshot id %q", snap.ID)
		}
		seen[snap.ID] = true
		switch {
		case snap.Commit == "" || snap.Parent == "":
			t.Errorf("%s names no commit or parent, so it cannot be reproduced", snap.ID)
		case snap.Arm != "clean" && snap.Defect == "":
			t.Errorf("%s names no defect; a row whose defect cannot be written from the fix's own discussion does not belong here", snap.ID)
		case snap.AdjudicatedBy == "":
			t.Errorf("%s records no adjudicator", snap.ID)
		}
	}
}
