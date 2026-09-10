package standards

import (
	"slices"
	"testing"
)

// A share alone is not evidence, so the floor has two numbers.
//
// Three sites out of three is 100% and says nothing about what a repository
// decided. The site count is what separates a convention from a coincidence,
// and it is the half a reader skims past.
func TestTheFloorNeedsBothAShareAndACount(t *testing.T) {
	f := Floor{MinShare: 0.85, MinSites: 12}
	for _, tc := range []struct {
		name       string
		conforming int
		total      int
		want       Standing
	}{
		{"enough sites, high enough share", 12, 12, StandingStandard},
		{"enough sites, exactly at the share", 17, 20, StandingStandard},
		{"enough sites, just under the share", 16, 20, StandingContested},
		{"perfect share, too few sites", 11, 11, StandingContested},
		{"nothing to look at", 0, 0, StandingUnseen},
	} {
		got := f.standing(Result{Conforming: tc.conforming, Total: tc.total})
		if got != tc.want {
			t.Errorf("%s: %d/%d stands as %q, want %q",
				tc.name, tc.conforming, tc.total, got, tc.want)
		}
	}
}

// A probe that looked at nothing reports that, and not a share.
//
// docs/measurement.md Rule 10: zero findings and "nothing ran" are
// indistinguishable in output and opposite in meaning. Share returns a bool
// for the same reason CostRow.Recall does, and a caller that ignores it prints
// 0% over a language no probe reads.
func TestAProbeWithNoSitesReportsNothingRatherThanZero(t *testing.T) {
	r := Result{ID: "go-error-wrap", Total: 0}
	if _, ok := r.Share(); ok {
		t.Error("Share reported a number over zero sites; 0% and 'not measured' are opposite readings")
	}
	if got := DefaultFloor().standing(r); got != StandingUnseen {
		t.Errorf("standing = %q, want %q", got, StandingUnseen)
	}
}

// A tree the probes cannot read says so, by language.
func TestAnUnprobedLanguageIsNamed(t *testing.T) {
	rep := Measure([]File{
		{Path: "a.go", Src: []byte("package p\n")},
		{Path: "b.ts", Src: []byte("export const x = 1\n")},
		{Path: "c.py", Src: []byte("x = 1\n")},
	}, Options{})

	if !slices.Contains(rep.Unprobed, "typescript") || !slices.Contains(rep.Unprobed, "python") {
		t.Errorf("unprobed = %v, want typescript and python named", rep.Unprobed)
	}
	if slices.Contains(rep.Unprobed, "go") {
		t.Error("go is named unprobed, and six probes read it")
	}
	if rep.Files["typescript"] != 1 {
		t.Errorf("files = %v, want one typescript file counted", rep.Files)
	}
}

// A disabled probe is absent, not zero.
//
// Present at 0/0 it renders as a rule the repository fails, which is the
// opposite of what switching it off means.
func TestADisabledProbeIsAbsentRatherThanFailing(t *testing.T) {
	rep := Measure([]File{{Path: "a.go", Src: []byte("package p\n")}},
		Options{Disabled: []string{"go-error-wrap"}})
	for _, r := range rep.Results {
		if r.ID == "go-error-wrap" {
			t.Fatalf("a disabled probe is in the report as %d/%d", r.Conforming, r.Total)
		}
	}
	if len(rep.Results) != len(Probes)-1 {
		t.Errorf("results = %d, want %d", len(rep.Results), len(Probes)-1)
	}
}

// Standards come back best-evidence first, and only the ones the floor
// supports.
func TestOnlyWhatTheFloorSupportsIsWrittenDown(t *testing.T) {
	rep := Report{
		Floor: DefaultFloor(),
		Results: []Result{
			{ID: "weak", Conforming: 5, Total: 10, Standing: StandingContested},
			{ID: "good", Conforming: 90, Total: 100, Standing: StandingStandard},
			{ID: "perfect", Conforming: 20, Total: 20, Standing: StandingStandard},
			{ID: "quiet", Standing: StandingUnseen},
		},
	}
	got := rep.Standards()
	if len(got) != 2 {
		t.Fatalf("standards = %d, want 2", len(got))
	}
	if got[0].ID != "perfect" || got[1].ID != "good" {
		t.Errorf("order = %s, %s; want the stronger share first", got[0].ID, got[1].ID)
	}
}

const scored = `package p

import "fmt"

func a(err error) error { return fmt.Errorf("read: %w", err) }
func b(err error) error { return fmt.Errorf("read: %v", err) }
func c(err error) error { return fmt.Errorf("read: %v", err) }
`

// A change answers for the lines it touched.
//
// Counting the whole file bills an author for a convention that arrived after
// the file did, which is how a gate teaches people to switch it off.
func TestAChangeIsScoredOnTheLinesItTouched(t *testing.T) {
	files := []File{{Path: "p.go", Src: []byte(scored)}}
	base := Report{Results: []Result{{ID: "go-error-wrap", Standing: StandingStandard}}}

	// Line 6 is the %v call; lines 5 and 7 are not this change's.
	got := Score(files, base, TouchedLines(map[string][]int{"p.go": {6}}), Options{})
	a := got["go-error-wrap"]
	if a.Total != 1 || a.Conforming != 0 {
		t.Fatalf("adherence = %d/%d, want 0/1: one touched site, and it does not conform", a.Conforming, a.Total)
	}
	if len(a.Off) != 1 || a.Off[0].Line != 6 {
		t.Errorf("off = %v, want the site on line 6", a.Off)
	}

	// And the line above it, which wraps.
	if a := Score(files, base, TouchedLines(map[string][]int{"p.go": {5}}), Options{})["go-error-wrap"]; a.Conforming != 1 || a.Total != 1 {
		t.Errorf("adherence on the wrapping line = %d/%d, want 1/1", a.Conforming, a.Total)
	}

	// A file the change did not touch contributes nothing.
	if got := Score(files, base, TouchedLines(map[string][]int{"other.go": {1}}), Options{}); len(got) != 0 {
		t.Errorf("an untouched file scored %v", got)
	}
}

// A contested probe scores nothing.
//
// A proposal is not something to fail an author against, and a gate that does
// it once is a gate somebody disables.
func TestAContestedProbeDoesNotScoreAChange(t *testing.T) {
	files := []File{{Path: "p.go", Src: []byte(scored)}}
	base := Report{Results: []Result{{ID: "go-error-wrap", Standing: StandingContested}}}

	got := Score(files, base, TouchedLines(map[string][]int{"p.go": {5, 6, 7}}), Options{})
	if len(got) != 0 {
		t.Errorf("a contested probe scored the change: %v", got)
	}
}
