package standards

import (
	"context"
	"errors"
	"testing"
)

// fakeSource answers with whatever it was built with.
type fakeSource struct {
	name string
	obs  []Observation
	cov  Coverage
	err  error
}

func (f fakeSource) Name() string { return f.name }

func (f fakeSource) Observe(context.Context, string, []File) ([]Observation, Coverage, error) {
	return f.obs, f.cov, f.err
}

// covering builds a coverage over n files of 100 lines each.
func covering(n int) Coverage {
	c := Coverage{Files: map[string]int{}, Ran: true}
	for i := range n {
		c.Files[string(rune('a'+i%26))+string(rune('a'+i/26))+".go"] = 100
	}
	return c
}

// A source that did not run contributes no denominator.
//
// This is the whole discipline in one assertion. A linter whose binary is
// missing examined nothing, and counting the files it would have read enters
// every one of them as clean: a tool that is absent reports as a repository
// that conforms. Five variants of this bug have shipped in this package.
func TestASourceThatDidNotRunContributesNoDenominator(t *testing.T) {
	// The absent source carries observations of its own. A source that half
	// ran is the case that matters: with no observations it contributes no
	// rule and no denominator whatever the code does, so a fixture without
	// them passes against the bug it names.
	absent := Coverage{Files: map[string]int{"a.go": 100, "b.go": 100}, Why: "not installed"}
	results := []SourceResult{
		{Source: "absent", Coverage: absent,
			Observations: []Observation{{Rule: "absent.r", Path: "a.go"}}},
		{Source: "present", Observations: []Observation{{Rule: "present.r", Path: "aa.go"}},
			Coverage: covering(20)},
	}

	got := Conform(results, DefaultCleanFloor())
	for _, c := range got {
		if c.Source == "absent" {
			t.Errorf("a source that did not run produced a reading: %+v", c)
		}
	}
	if len(got) != 1 {
		t.Fatalf("conformities = %+v, want only the source that ran", got)
	}
	if got[0].Covered != 20 {
		t.Errorf("covered = %d, want 20: the absent source's files entered the denominator", got[0].Covered)
	}
	if got[0].Clean != 19 {
		t.Errorf("clean = %d, want 19", got[0].Clean)
	}
}

// An erroring source becomes a source that did not run, with the reason.
func TestAnErroringSourceIsRecordedRatherThanLost(t *testing.T) {
	got := Gather(context.Background(), ".", nil, []Source{
		fakeSource{name: "broken", err: errors.New("the binary exploded")},
		fakeSource{name: "fine", cov: covering(3)},
	})
	if len(got) != 2 {
		t.Fatalf("results = %d, want both sources reported", len(got))
	}
	for _, r := range got {
		if r.Source == "broken" {
			if r.Coverage.Ran {
				t.Error("a source that returned an error is recorded as having run")
			}
			if r.Coverage.Why != "the binary exploded" {
				t.Errorf("why = %q, want the error", r.Coverage.Why)
			}
		}
		if r.Source == "fine" && !r.Coverage.Ran {
			t.Error("one source failing stopped another from being recorded")
		}
	}
}

// The floor needs both a share and a count.
//
// A file is a coarse unit and most files touch most rules zero times, so file
// shares sit near the top of the range. At the 85% a site denominator justified,
// nearly every rule any analyzer offers would be a standard, and a floor that
// admits everything is not one.
func TestTheCleanFloorNeedsBothAShareAndACount(t *testing.T) {
	f := CleanFloor{CleanShare: 0.95, MinFiles: 12}
	for _, tc := range []struct {
		name           string
		clean, covered int
		want           Standing
	}{
		{"enough files, at the share", 19, 20, StandingStandard},
		{"enough files, under the share", 18, 20, StandingContested},
		{"perfect share, too few files", 11, 11, StandingContested},
		{"nothing read", 0, 0, StandingUnseen},
	} {
		got := f.standing(Conformity{Clean: tc.clean, Covered: tc.covered})
		if got != tc.want {
			t.Errorf("%s: %d/%d stands as %q, want %q", tc.name, tc.clean, tc.covered, got, tc.want)
		}
	}
}

// A rule with nothing read reports that, not a zero share.
func TestAConformityOverNothingReportsNothing(t *testing.T) {
	c := Conformity{Rule: "x.y"}
	if _, ok := c.Share(); ok {
		t.Error("Share reported a number over zero covered files")
	}
	if _, ok := c.PerKLOC(); ok {
		t.Error("PerKLOC reported a rate over zero lines")
	}
}

// An observation outside a source's own coverage cannot inflate a share.
//
// If it could, a share would exceed one, which is the arithmetic saying the
// denominator is wrong rather than the numerator.
func TestAShareCannotExceedOne(t *testing.T) {
	results := []SourceResult{{
		Source:   "s",
		Coverage: covering(12),
		Observations: []Observation{
			{Rule: "s.r", Path: "aa.go"},
			{Rule: "s.r", Path: "nowhere.go"},
		},
	}}
	got := Conform(results, DefaultCleanFloor())
	if len(got) != 1 {
		t.Fatalf("conformities = %+v", got)
	}
	if got[0].Clean > got[0].Covered {
		t.Errorf("clean %d exceeds covered %d", got[0].Clean, got[0].Covered)
	}
	if got[0].Clean != 11 {
		t.Errorf("clean = %d, want 11: only the in-coverage path is dirty", got[0].Clean)
	}
}
