package evals

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/review"
)

// dumpFixtureNames returns two real corpus fixture names.
//
// Taken from the corpus rather than invented, because GroupDump has to resolve
// a name to the change that was reviewed: a made-up name is correctly rejected.
func dumpFixtureNames(t *testing.T) (string, string) {
	t.Helper()

	all := Fixtures()
	if len(all) < 2 {
		t.Fatalf("corpus has %d fixture(s); these tests need two", len(all))
	}
	return all[0].Name, all[1].Name
}

// writeDump records one sample and reads the file back as records, which is
// the exact path a re-judge takes.
func writeDump(t *testing.T, samples ...DumpSample) []DumpRecord {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dump.jsonl")

	dump, err := NewDump(path)
	if err != nil {
		t.Fatalf("new dump: %v", err)
	}
	for _, s := range samples {
		if err := dump.Record(s); err != nil {
			t.Fatalf("record: %v", err)
		}
	}
	if err := dump.Close(); err != nil {
		t.Fatalf("close dump: %v", err)
	}

	records, err := ReadDump(path)
	if err != nil {
		t.Fatalf("read dump: %v", err)
	}
	return records
}

func fixtureByName(t *testing.T, name string) Fixture {
	t.Helper()

	for _, f := range AllFixtures() {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("no fixture named %q", name)
	return Fixture{}
}

// TestGroupDumpKeepsVerdictsOnTheirOwnFindings is the property the whole
// re-judge path rests on.
//
// Verdict.Index is a POSITION, so reconstructing the list in any other order
// hands each new verdict a different finding than the recorded verdict it is
// compared against, and every downstream disagreement then reads as two judges
// disagreeing. The records are deliberately shuffled and two contenders'
// samples interleaved, because that is what a concurrent run writes.
func TestGroupDumpKeepsVerdictsOnTheirOwnFindings(t *testing.T) {
	first, second := dumpFixtureNames(t)

	findings := []review.Finding{
		{Path: "a.go", Line: 10, Severity: "error", Class: "security", Title: "zero", Rationale: "first"},
		{Path: "b.go", Line: 20, Severity: "warning", Class: "correctness", Title: "one", Rationale: "second"},
		{Path: "c.go", Line: 30, Severity: "nit", Class: "style", Title: "two", Rationale: "third"},
	}
	judged := &JudgeResult{Verdicts: []Verdict{
		{Index: 0, Real: true, WorthRaising: true, SeverityVerdict: "accurate", ClassCorrect: true},
		{Index: 1, Real: true, WorthRaising: false, SeverityVerdict: "inflated", ClassCorrect: false},
		{Index: 2, Real: false, WorthRaising: false, SeverityVerdict: "inflated", ClassCorrect: true},
	}}

	records := writeDump(t,
		DumpSample{Model: "m/a", Run: 1, Fixture: fixtureByName(t, first), Findings: findings, Judged: judged},
		DumpSample{Model: "m/b", Run: 1, Fixture: fixtureByName(t, second), Findings: findings[:1], Judged: &JudgeResult{
			Verdicts: []Verdict{{Index: 0, Real: false, WorthRaising: false, SeverityVerdict: "inflated"}},
		}},
	)

	// A concurrent writer interleaves samples and a reader must not depend on
	// file order at all, so the order is destroyed before grouping.
	shuffled := []DumpRecord{records[3], records[2], records[0], records[1]}

	groups, _, err := GroupDump(shuffled)
	if err != nil {
		t.Fatalf("group: %v", err)
	}

	var target *RejudgeGroup
	for i := range groups {
		if groups[i].Model == "m/a" && groups[i].Fixture.Name == first {
			target = &groups[i]
		}
	}
	if target == nil {
		t.Fatalf("group m/a on %s missing from %d group(s)", first, len(groups))
	}

	if len(target.Findings) != len(findings) {
		t.Fatalf("reconstructed %d finding(s) for a review of %d", len(target.Findings), len(findings))
	}
	for i, want := range findings {
		if got := target.Findings[i]; got.Title != want.Title || got.Path != want.Path || got.Line != want.Line {
			t.Errorf("position %d holds %s:%d %q, want %s:%d %q — every verdict from here on is "+
				"attached to the wrong finding", i, got.Path, got.Line, got.Title, want.Path, want.Line, want.Title)
		}
	}

	// The verdicts have to land back on the findings they were made about.
	for _, v := range target.Baseline {
		want := judged.Verdicts[v.Index]
		if v != want {
			t.Errorf("verdict at index %d is %+v, want %+v", v.Index, v, want)
		}
	}
	if len(target.Baseline) != len(judged.Verdicts) {
		t.Errorf("recovered %d verdict(s) of %d", len(target.Baseline), len(judged.Verdicts))
	}
}

// TestGroupDumpCollapsesDuplicateCreditLines pins the one place the dump is
// deliberately not one line per finding.
//
// Dump.Record writes a finding once per planted defect it was credited with,
// the extra lines differing only in the ground-truth fields. Treating those as
// two findings lengthens the reconstructed list and shifts every position after
// it, so verdict 3 would be compared against finding 4 and nothing downstream
// could tell.
//
// The duplicate is built at the record level rather than by planting a finding
// that matches two defects in a real fixture. That route depends on the corpus
// keeping two defects on adjacent lines of one file with overlapping keywords,
// an accident of fixtures.go that an edit there would silently remove, turning
// this into a test that skips instead of one that fails.
func TestGroupDumpCollapsesDuplicateCreditLines(t *testing.T) {
	first, _ := dumpFixtureNames(t)

	findings := []review.Finding{
		{Path: "a.go", Line: 10, EndLine: 14, Severity: "error", Class: "correctness", Title: "covers both"},
		{Path: "z.go", Line: 1, Severity: "nit", Class: "style", Title: "last"},
	}

	records := writeDump(t, DumpSample{
		Model: "m/a", Run: 1, Fixture: fixtureByName(t, first), Findings: findings,
		Judged: &JudgeResult{Verdicts: []Verdict{
			{Index: 0, Real: true, WorthRaising: true, SeverityVerdict: "accurate", ClassCorrect: true},
			{Index: 1, Real: false, WorthRaising: false, SeverityVerdict: "inflated", ClassCorrect: true},
		}},
	})
	if len(records) != len(findings) {
		t.Fatalf("writer emitted %d record(s) for %d finding(s)", len(records), len(findings))
	}

	// The second credit line for index 0, exactly as Dump.Record emits it: the
	// same finding, a different located defect.
	credited := records[0]
	credited.Matched = true
	credited.WantSeverity = "critical"
	credited.SeverityDelta = SevUnderstated
	credited.DefectWhy = "the second defect this one comment covered"

	records[0].Matched = true
	records[0].WantSeverity = "error"
	records[0].SeverityDelta = SevAccurate
	records[0].DefectWhy = "the first defect this one comment covered"

	doubled := []DumpRecord{records[0], credited, records[1]}

	groups, _, err := GroupDump(doubled)
	if err != nil {
		t.Fatalf("group: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("reconstructed %d group(s) from one sample", len(groups))
	}

	if got := len(groups[0].Findings); got != len(findings) {
		t.Fatalf("reconstructed %d finding(s) from %d record(s) for a review of %d: a finding "+
			"credited with two defects was counted twice, and every position after it is shifted",
			got, len(doubled), len(findings))
	}
	if groups[0].Findings[1].Title != "last" {
		t.Errorf("position 1 holds %q, want %q", groups[0].Findings[1].Title, "last")
	}
}

// TestGroupDumpKeepsASilentSample pins that a reviewer who said nothing still
// appears.
//
// Dropping it removes a contender's fixture from one ranking and not the other,
// which is a change of sample wearing a change of judge's clothes.
func TestGroupDumpKeepsASilentSample(t *testing.T) {
	first, second := dumpFixtureNames(t)

	loud := []review.Finding{{Path: "a.go", Line: 10, Severity: "error", Class: "security", Title: "spoke"}}
	verdicts := &JudgeResult{Verdicts: []Verdict{{Index: 0, Real: true, WorthRaising: true, SeverityVerdict: "accurate"}}}

	records := writeDump(t,
		DumpSample{Model: "m/talkative", Run: 1, Fixture: fixtureByName(t, first), Findings: loud, Judged: verdicts},
		DumpSample{Model: "m/talkative", Run: 1, Fixture: fixtureByName(t, second), Findings: loud, Judged: verdicts},
		DumpSample{Model: "m/quiet", Run: 1, Fixture: fixtureByName(t, first), Findings: loud, Judged: verdicts},
		// m/quiet reported nothing on `second`. That is a RESULT, so the writer
		// records it; it used to write no line and be reconstructed by guessing
		// the matrix back from the records present.
		DumpSample{Model: "m/quiet", Run: 1, Fixture: fixtureByName(t, second), Judged: &JudgeResult{}},
	)

	groups, warnings, err := GroupDump(records)
	if err != nil {
		t.Fatalf("group: %v", err)
	}

	var found *RejudgeGroup
	for i := range groups {
		if groups[i].Model == "m/quiet" && groups[i].Fixture.Name == second {
			found = &groups[i]
		}
	}
	if found == nil {
		t.Fatalf("the silent sample m/quiet on %s is absent from %d group(s); that contender is "+
			"judged on one fixture under the new judge and two under the old", second, len(groups))
	}
	if !found.Silent {
		t.Error("the reconstructed empty sample is not marked Silent, so a report cannot tell it " +
			"apart from a review that produced findings")
	}
	if len(found.Findings) != 0 || len(found.Baseline) != 0 {
		t.Errorf("the silent sample carries %d finding(s) and %d verdict(s)",
			len(found.Findings), len(found.Baseline))
	}
	if found.Fixture.Name != second || len(found.Fixture.Head) == 0 {
		t.Error("the silent sample carries no fixture source, so it cannot be re-judged")
	}

	// Silence and a failed review are indistinguishable in a dump, and a report
	// that does not say so invites reading one as the other.
	if !strings.Contains(strings.Join(warnings, "\n"), "EMPTY finding list") {
		t.Errorf("no warning that %d group(s) were reconstructed from an absence: %v",
			1, warnings)
	}
}

// TestGroupDumpRefusesAHoleInTheJudgedList pins that an unrecoverable file
// fails loudly.
//
// A missing index cannot be reconstructed: rebuilding the list one short moves
// every later finding onto its neighbour's verdict, and the result is a full
// set of plausible, wrong pairings that nothing downstream can detect.
func TestGroupDumpRefusesAHoleInTheJudgedList(t *testing.T) {
	first, _ := dumpFixtureNames(t)

	findings := []review.Finding{
		{Path: "a.go", Line: 10, Severity: "error", Class: "security", Title: "zero"},
		{Path: "b.go", Line: 20, Severity: "warning", Class: "correctness", Title: "one"},
		{Path: "c.go", Line: 30, Severity: "nit", Class: "style", Title: "two"},
	}

	records := writeDump(t, DumpSample{
		Model: "m/a", Run: 1, Fixture: fixtureByName(t, first), Findings: findings,
		Judged: &JudgeResult{Verdicts: []Verdict{{Index: 0, Real: true, WorthRaising: true}}},
	})

	// Delete the middle finding's line, exactly as a partially-written or
	// hand-filtered file would.
	var holed []DumpRecord
	for _, r := range records {
		if r.Index == 1 {
			continue
		}
		holed = append(holed, r)
	}

	if _, _, err := GroupDump(holed); err == nil {
		t.Fatal("a dump missing index 1 of 3 reconstructed without error; every finding after " +
			"the hole is now paired with the previous finding's verdict")
	}
}

// TestGroupDumpRefusesTwoRunsUnderOneKey pins the concatenated-file case.
//
// The writer truncates on open, so one key holding two different findings means
// the file is two runs stapled together. Reconstructing takes one run's finding
// and the other run's verdict, which is worse than refusing.
func TestGroupDumpRefusesTwoRunsUnderOneKey(t *testing.T) {
	first, _ := dumpFixtureNames(t)

	a := writeDump(t, DumpSample{
		Model: "m/a", Run: 1, Fixture: fixtureByName(t, first),
		Findings: []review.Finding{{Path: "a.go", Line: 10, Severity: "error", Class: "security", Title: "one run"}},
	})
	b := writeDump(t, DumpSample{
		Model: "m/a", Run: 1, Fixture: fixtureByName(t, first),
		Findings: []review.Finding{{Path: "a.go", Line: 10, Severity: "error", Class: "security", Title: "another run"}},
	})

	if _, _, err := GroupDump(append(a, b...)); err == nil {
		t.Fatal("two runs under one key reconstructed without error")
	}
}

// TestRejudgeReportShowsBothRankingsAndTheVendorCohort pins what the output has
// to make obvious: whether rank moves, and whether the judge's own vendor is
// where it moved.
func TestRejudgeReportShowsBothRankingsAndTheVendorCohort(t *testing.T) {
	first, _ := dumpFixtureNames(t)
	fixture := fixtureByName(t, first)

	findings := []review.Finding{
		{Path: "a.go", Line: 10, Severity: "error", Class: "security", Title: "zero"},
		{Path: "b.go", Line: 20, Severity: "warning", Class: "correctness", Title: "one"},
	}

	// The same-vendor contender is worth-raising 2/2 under the baseline judge
	// and 0/2 under the new one; the other-vendor contender is the reverse. The
	// ranking must therefore flip.
	outcome := func(model string, before, after []Verdict) RejudgeOutcome {
		return RejudgeOutcome{
			Group: RejudgeGroup{
				Model: model, Run: 1, Fixture: fixture,
				Findings: findings, Baseline: before,
			},
			Result: &JudgeResult{Verdicts: after},
		}
	}
	yes := func(i int) Verdict {
		return Verdict{Index: i, Real: true, WorthRaising: true, SeverityVerdict: "accurate", ClassCorrect: true}
	}
	no := func(i int) Verdict {
		return Verdict{Index: i, Real: false, WorthRaising: false, SeverityVerdict: "inflated", ClassCorrect: false}
	}

	outcomes := []RejudgeOutcome{
		outcome("nitpick/openai/gpt-5.6-sol", []Verdict{yes(0), yes(1)}, []Verdict{no(0), no(1)}),
		outcome("nitpick/anthropic/claude-opus-5", []Verdict{no(0), no(1)}, []Verdict{yes(0), yes(1)}),
		// Silent everywhere: it must still have a row, or a contender that
		// stayed quiet disappears from the comparison entirely.
		{Group: RejudgeGroup{Model: "incumbent/cli", Run: 1, Fixture: fixture, Silent: true},
			Result: &JudgeResult{}},
	}

	report := RejudgeReport("openai/gpt-5.6-terra", "anthropic/claude-opus-5", outcomes, nil)

	for _, want := range []string{
		"nitpick/openai/gpt-5.6-sol",
		"nitpick/anthropic/claude-opus-5",
		"incumbent/cli",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report omits contender %q:\n%s", want, report)
		}
	}

	// The cohort split is the entire question, and it is drawn on the vendor
	// INSIDE the "nitpick/" prefix.
	if !strings.Contains(report, "SAME VENDOR AS THE BASELINE JUDGE (openai)") {
		t.Errorf("report has no openai cohort heading:\n%s", report)
	}
	cohort := section(report, "SAME VENDOR AS THE BASELINE JUDGE (openai)")
	if !strings.Contains(cohort, "nitpick/openai/gpt-5.6-sol") {
		t.Errorf("the openai contender is not in the openai cohort:\n%s", cohort)
	}
	if strings.Contains(cohort, "nitpick/anthropic/claude-opus-5") {
		t.Errorf("an anthropic contender is inside the openai cohort:\n%s", cohort)
	}

	// Both precisions must be printed, and they must differ: one column would
	// make the whole exercise unreadable.
	if !strings.Contains(report, "PREC-A") || !strings.Contains(report, "PREC-B") {
		t.Errorf("report does not print both judges' precision:\n%s", report)
	}

	openai := line(report, "nitpick/openai/gpt-5.6-sol")
	if !strings.Contains(openai, "1.00") || !strings.Contains(openai, "0.00") {
		t.Errorf("the openai row does not show precision falling from 1.00 to 0.00: %q", openai)
	}
	// It was first under the baseline judge and must not be first under the new
	// one, or the report is hiding the only thing it exists to show.
	if !strings.Contains(openai, "-1") {
		t.Errorf("the openai row shows no rank movement after its precision inverted: %q", openai)
	}

	if !strings.Contains(report, "MISSED") {
		t.Errorf("report does not say that MISSED has no baseline in a dump:\n%s", report)
	}
}

// TestRejudgeReportDropsAFailedGroupFromBothSides pins that the two rankings
// are always over the same sample.
//
// Keeping a group's recorded verdicts when the new judge could not assess it
// would put the baseline column over more findings than the new one, a
// difference of sample presented as a difference of judge, which is the exact
// confound this path exists to remove.
func TestRejudgeReportDropsAFailedGroupFromBothSides(t *testing.T) {
	first, second := dumpFixtureNames(t)

	findings := []review.Finding{{Path: "a.go", Line: 10, Severity: "error", Class: "security", Title: "zero"}}
	worth := []Verdict{{Index: 0, Real: true, WorthRaising: true, SeverityVerdict: "accurate", ClassCorrect: true}}
	junk := []Verdict{{Index: 0, Real: false, WorthRaising: false, SeverityVerdict: "inflated", ClassCorrect: false}}

	outcomes := []RejudgeOutcome{
		{
			Group: RejudgeGroup{Model: "m/a", Run: 1, Fixture: fixtureByName(t, first),
				Findings: findings, Baseline: junk},
			Result: &JudgeResult{Verdicts: junk},
		},
		{
			// The new judge failed here. Its recorded verdict is worth-raising,
			// so counting it would lift the baseline column alone.
			Group: RejudgeGroup{Model: "m/a", Run: 1, Fixture: fixtureByName(t, second),
				Findings: findings, Baseline: worth},
			Err: errFailedRejudge,
		},
	}

	report := RejudgeReport("openai/gpt-5.6-terra", "openai/gpt-5.6-terra", outcomes, nil)

	row := line(report, "m/a")
	// One verdict on each side, not two against one.
	if !strings.Contains(row, "0.00") || strings.Contains(row, "0.50") {
		t.Errorf("the failed group leaked into the baseline column: %q\n%s", row, report)
	}
	if !strings.Contains(report, "EXCLUDED FROM BOTH SIDES") {
		t.Errorf("the excluded group is not reported:\n%s", report)
	}

	// Both judges are the same vendor here, which cannot answer the vendor
	// question at all. Saying so is the difference between a noise measurement
	// and a bias measurement that quietly is not one.
	if !strings.Contains(report, "BOTH JUDGES ARE OPENAI") {
		t.Errorf("a same-vendor judge pair is not called out:\n%s", report)
	}
}

// errFailedRejudge stands in for a provider error.
var errFailedRejudge = errRejudge("judge call failed")

type errRejudge string

func (e errRejudge) Error() string { return string(e) }

// section returns the report text following a heading, up to the next blank
// line, so a cohort's membership can be asserted rather than its mere presence.
func section(report, heading string) string {
	_, rest, ok := strings.Cut(report, heading)
	if !ok {
		return ""
	}
	if end := strings.Index(rest, "\n\n"); end >= 0 {
		return rest[:end]
	}
	return rest
}

// line returns the first line containing s.
func line(report, s string) string {
	for _, l := range strings.Split(report, "\n") {
		if strings.Contains(l, s) {
			return l
		}
	}
	return ""
}

// TestGroupDumpRefusesATruncatedTail pins the hole that used to be silent.
//
// A hole in the MIDDLE was already refused. A hole at the END was not: the
// list's length was inferred from the largest index present, so a file missing
// its last line rebuilt SHORT with no error and no warning. That is the worse
// of the two. The new judge is shown a shorter review than the recorded
// verdicts were made about, and the baseline precision then silently disagrees
// with the published table it is printed beside. A killed run, a `head -n` or a
// jq filter all produce exactly this file.
func TestGroupDumpRefusesATruncatedTail(t *testing.T) {
	first, _ := dumpFixtureNames(t)

	findings := []review.Finding{
		{Path: "a.go", Line: 10, Severity: "error", Class: "security", Title: "zero"},
		{Path: "b.go", Line: 20, Severity: "warning", Class: "correctness", Title: "one"},
		{Path: "c.go", Line: 30, Severity: "nit", Class: "style", Title: "two"},
	}
	records := writeDump(t, DumpSample{
		Model: "m/a", Run: 1, Fixture: fixtureByName(t, first), Findings: findings,
		Judged: &JudgeResult{Verdicts: []Verdict{
			{Index: 0, Real: true, WorthRaising: true},
			{Index: 1, Real: true, WorthRaising: true},
			{Index: 2, Real: false, WorthRaising: false},
		}},
	})
	if len(records) != 3 {
		t.Fatalf("dump wrote %d record(s) for 3 findings", len(records))
	}

	groups, _, err := GroupDump(records[:len(records)-1])
	if err == nil {
		t.Fatalf("a dump truncated after index 1 rebuilt %d finding(s) without error; the recorded "+
			"verdicts were made about 3", len(groups[0].Findings))
	}
}

// TestGroupDumpRefusesTwoJudgementsOfOneFinding pins the concatenation case the
// finding comparison cannot see.
//
// Two runs of a deterministic reviewer produce IDENTICAL findings, so the
// two-different-findings guard passes them; only the verdicts differ. That is
// not an exotic file, Summary.Stable's own definition says identical inputs at
// temperature 0 should produce identical findings, and it is precisely the
// judge noise this whole path exists to measure. Resolving it by keeping
// whichever line came last makes the surviving judgement a function of file
// order, so it is refused in both orders.
func TestGroupDumpRefusesTwoJudgementsOfOneFinding(t *testing.T) {
	first, _ := dumpFixtureNames(t)

	finding := []review.Finding{{Path: "a.go", Line: 10, Severity: "error", Class: "security", Title: "same"}}
	sample := func(real bool) DumpSample {
		return DumpSample{
			Model: "m/a", Run: 1, Fixture: fixtureByName(t, first), Findings: finding,
			Judged: &JudgeResult{Verdicts: []Verdict{{Index: 0, Real: real, WorthRaising: real}}},
		}
	}

	yes := writeDump(t, sample(true))
	no := writeDump(t, sample(false))

	for _, tc := range []struct {
		name    string
		records []DumpRecord
	}{
		{"worth-raising first", append(append([]DumpRecord{}, yes...), no...)},
		{"worth-raising second", append(append([]DumpRecord{}, no...), yes...)},
	} {
		groups, _, err := GroupDump(tc.records)
		if err == nil {
			t.Errorf("%s: two judgements of one finding reconstructed without error, keeping %+v — "+
				"the recorded verdict is now chosen by which file was catted first",
				tc.name, groups[0].Baseline)
		}
	}
}

// TestGroupDumpRefusesAnEditedFixture pins the confound that arrives through the
// corpus rather than through the judge.
//
// A dump names its fixture and the re-judge resolves that name against the
// corpus as it stands NOW. Edit a fixture's Head between collecting the dump
// and re-judging it, and the new judge reads a different change than the one
// the recorded verdicts were made about, which is exactly the "the findings
// And the judge both moved" confound this path exists to eliminate, restored
// silently. The fixture NAME surviving is not evidence its source did.
func TestGroupDumpRefusesAnEditedFixture(t *testing.T) {
	first, _ := dumpFixtureNames(t)

	records := writeDump(t, DumpSample{
		Model: "m/a", Run: 1, Fixture: fixtureByName(t, first),
		Findings: []review.Finding{{Path: "a.go", Line: 10, Severity: "error", Class: "security", Title: "one"}},
		Judged:   &JudgeResult{Verdicts: []Verdict{{Index: 0, Real: true, WorthRaising: true}}},
	})

	if records[0].FixtureHash == "" {
		t.Fatal("the dump records no fixture hash, so an edited fixture cannot be detected at all")
	}

	// What the file would hold had it been collected before an edit to the
	// fixture's source. The hash is the only thing that changes: the name, the
	// findings and the verdicts are all still valid.
	records[0].FixtureHash = "0000000000000000000000000000000000000000000000000000000000000000"

	if _, _, err := GroupDump(records); err == nil {
		t.Fatal("a dump collected against different fixture source was re-judged without error; the " +
			"change under review moved as well as the judge, and the difference is attributed to the judge")
	}
}

// TestRejudgeReportRanksEachVariantSeparately pins the merge that made the
// persona and nitpick axes unreadable.
//
// GroupDump keys on (model, variant) precisely because the nitpick axis derives
// all four levels from ONE review: a finding that survives every level is
// written four times, under four variants. The report keyed its rows on the
// model alone and merged them back, so that one comment was counted four times
// in one row, the exact thing DumpRecord.Variant's own doc forbids, and the
// four levels, which are the whole point of such a dump, could not be told
// apart.
func TestRejudgeReportRanksEachVariantSeparately(t *testing.T) {
	first, _ := dumpFixtureNames(t)

	levels := []string{"nitpick=off", "nitpick=minimal", "nitpick=normal", "nitpick=pedantic"}
	finding := []review.Finding{{Path: "a.go", Line: 10, Severity: "error", Class: "security", Title: "survives every level"}}
	judged := &JudgeResult{Verdicts: []Verdict{{Index: 0, Real: true, WorthRaising: true}}}

	var samples []DumpSample
	for _, level := range levels {
		samples = append(samples, DumpSample{
			Model: "m/a", Variant: level, Run: 1, Fixture: fixtureByName(t, first),
			Findings: finding, Judged: judged,
		})
	}

	groups, warnings, err := GroupDump(writeDump(t, samples...))
	if err != nil {
		t.Fatalf("group: %v", err)
	}

	outcomes := make([]RejudgeOutcome, 0, len(groups))
	for _, g := range groups {
		outcomes = append(outcomes, RejudgeOutcome{Group: g, Result: judged})
	}
	report := RejudgeReport("openai/base", "openai/new", outcomes, warnings)

	for _, level := range levels {
		row := line(report, level)
		if row == "" {
			t.Errorf("no row for variant %q: the axis this dump exists to compare is not in the table", level)
			continue
		}
		// One review, one finding, one verdict, under each level.
		if !strings.Contains(row, " 1 ") {
			t.Errorf("row for %q counts something other than one verdict: %q", level, strings.TrimSpace(row))
		}
	}
}

// TestRejudgeReportCountsBothJudgesByTheSameRule pins the property the whole
// report claims: that it changes exactly one thing.
//
// The recorded baseline has already been reduced to one verdict per finding
// position, because that is all the dump can carry. The new judge's list had
// not been, so a duplicate or out-of-range index counted on one side and not
// the other. Handing a judge's own answer back to it as the "new" judgement
// then produced a non-zero DELTA and could move a rank, with no judge having
// changed at all.
func TestRejudgeReportCountsBothJudgesByTheSameRule(t *testing.T) {
	first, _ := dumpFixtureNames(t)

	findings := []review.Finding{
		{Path: "a.go", Line: 10, Severity: "error", Class: "security", Title: "zero"},
		{Path: "b.go", Line: 20, Severity: "warning", Class: "correctness", Title: "one"},
	}
	// The malformed shapes Aggregate.Add already reports as suspect: index 0
	// answered twice, and an index with no finding behind it.
	raw := []Verdict{
		{Index: 0, Real: true, WorthRaising: true},
		{Index: 1, Real: true, WorthRaising: false},
		{Index: 0, Real: true, WorthRaising: true},
		{Index: 7, Real: true, WorthRaising: true},
	}

	groups, warnings, err := GroupDump(writeDump(t, DumpSample{
		Model: "m/a", Run: 1, Fixture: fixtureByName(t, first), Findings: findings,
		Judged: &JudgeResult{Verdicts: raw},
	}))
	if err != nil {
		t.Fatalf("group: %v", err)
	}

	// The same judgement, handed back as the new judge's answer.
	outcomes := []RejudgeOutcome{{Group: groups[0], Result: &JudgeResult{Verdicts: raw}}}
	report := RejudgeReport("openai/base", "openai/new", outcomes, warnings)

	row := line(report, "m/a ")
	if !strings.Contains(row, "+0.00") {
		t.Errorf("re-judging a dump with its OWN recorded verdicts reports a precision delta: %q. "+
			"The two sides are counted by different rules, so the report attributes to the judge a "+
			"difference the judge did not make", strings.TrimSpace(row))
	}
	if !strings.Contains(row, "+0\n") && !strings.HasSuffix(strings.TrimSpace(row), "+0") {
		t.Errorf("rank moved on an unchanged judgement: %q", strings.TrimSpace(row))
	}

	// The verdicts the dump could not carry are not the original judge saying
	// less, and a reader comparing PREC-A against the published table has to be
	// told which of the two it is.
	if !strings.Contains(report, "SHORTER") {
		t.Error("the report does not say the recorded baseline is shorter than the judgement it came " +
			"from, so PREC-A silently disagrees with the published number for the same run")
	}
}

// TestRejudgeReportKeepsPhantomVerdictsOutOfPrecision pins the one asymmetry a
// silent group creates.
//
// A silent review records no verdicts. There were no findings to judge, so its
// baseline is structurally zero. The new judge IS still asked, deliberately,
// because a judge that answers an empty list is a failure mode worth showing.
// Counting what it invents would let that failure raise the contender's
// new-judge precision and its rank against a baseline that could never have a
// counterpart, which is a difference in what was counted dressed as a
// difference between judges.
func TestRejudgeReportKeepsPhantomVerdictsOutOfPrecision(t *testing.T) {
	outcomes := []RejudgeOutcome{{
		Group: RejudgeGroup{Model: "nitpick/x/y", Run: 1, Fixture: fixtureByName(t, "clean-refactor"), Silent: true},
		Result: &JudgeResult{Verdicts: []Verdict{
			{Index: 0, Real: true, WorthRaising: true},
			{Index: 1, Real: true, WorthRaising: true},
		}},
	}}

	report := RejudgeReport("openai/base", "openai/new", outcomes, nil)

	// Read by column rather than by substring: "n/a" appears three times in
	// this row, so a Contains check passes whether or not PREC-B is the cell
	// holding it. The contender name carries no variant here, so the row splits
	// into exactly the header's columns.
	cols := strings.Fields(line(report, "nitpick/x/y"))
	want := strings.Fields(precisionHeader)
	if len(cols) != len(want) {
		t.Fatalf("row has %d column(s) for a %d-column header: %q", len(cols), len(want), cols)
	}
	cell := func(name string) string {
		for i, h := range want {
			if h == name {
				return cols[i]
			}
		}
		t.Fatalf("no %s column in %q", name, precisionHeader)
		return ""
	}

	if got := cell("PREC-B"); got != "n/a" {
		t.Errorf("PREC-B is %q: a judge that invented 2 findings on an EMPTY list was given a "+
			"new-judge precision, against a baseline that structurally cannot have one", got)
	}
	if got := cell("V-B"); got != "0" {
		t.Errorf("V-B is %q, want 0: verdicts about findings that do not exist are counted as "+
			"verdicts", got)
	}

	// Excluded from the ranked numbers but not from the reader's view: being
	// able to show this is the whole reason a silent group is submitted at all.
	if got := cell("PHAN"); got != "2" {
		t.Errorf("PHAN is %q, want 2: the invented verdicts are counted nowhere in the row, so the "+
			"failure is visible only to a reader who scrolls to the notes", got)
	}
	if !strings.Contains(report, "about a finding that does not exist") {
		t.Error("no note explains the PHAN cell, so a reader cannot tell an inventing judge from a " +
			"formatting artefact")
	}
}

// TestRejudgeReportSaysItsRankingIsNotThePublishedOne pins a caveat, because
// the omission is what makes the number misleading.
//
// RANK-A/RANK-B rank by precision alone. The published table ranks by the
// judge's GRADE first and uses precision only to break ties, and the dump
// records no grade, so that ranking cannot be reproduced here at all. A reader
// interrogating the published ranking reads MOVE +0 as "it held" unless the
// report says otherwise.
func TestRejudgeReportSaysItsRankingIsNotThePublishedOne(t *testing.T) {
	outcomes := []RejudgeOutcome{{
		Group: RejudgeGroup{
			Model: "m/a", Run: 1, Fixture: fixtureByName(t, "go-nil-deref"),
			Findings: []review.Finding{{Path: "a.go", Line: 10, Severity: "error", Title: "one"}},
			Baseline: []Verdict{{Index: 0, Real: true, WorthRaising: true}},
		},
		Result: &JudgeResult{Verdicts: []Verdict{{Index: 0, Real: true, WorthRaising: true}}},
	}}

	report := RejudgeReport("openai/base", "openai/new", outcomes, nil)
	if !strings.Contains(report, "PRECISION ALONE") {
		t.Error("the report prints RANK columns without saying they rank by precision alone, while " +
			"the table they invite comparison with ranks by grade first")
	}
}

// TestGroupDumpDoesNotInventGroupsAcrossCorpora pins what a legacy dump may and
// may not guess.
//
// Before silence was recorded, the matrix was reconstructed as the cross product
// of every contender against every fixture in the file, so a dump holding both
// corpora credited a tuning contender with silence on a held-out fixture it was
// never given, and each fabricated group cost a real judge call on an empty
// list. DumpRecord.HeldOut exists to keep the two corpora apart in a shared
// file, and the reconstruction has to read it.
func TestGroupDumpDoesNotInventGroupsAcrossCorpora(t *testing.T) {
	tuning := Fixtures()[0].Name
	held := HeldOutFixtures()[0].Name

	finding := []review.Finding{{Path: "a.go", Line: 10, Severity: "error", Class: "security", Title: "one"}}
	judged := &JudgeResult{Verdicts: []Verdict{{Index: 0, Real: true, WorthRaising: true}}}

	// Hand-built to be a LEGACY dump: no Findings count and no Silent marker,
	// which is what forces the inference path.
	records := []DumpRecord{
		{Model: "m/tuner", Fixture: tuning, Run: 1, Index: 0, Path: "a.go", Line: 10,
			Severity: "error", Class: "security", Title: "one",
			Verdict: &Verdict{Index: 0, Real: true, WorthRaising: true}},
		{Model: "m/heldout", Fixture: held, HeldOut: true, Run: 1, Index: 0, Path: "a.go", Line: 10,
			Severity: "error", Class: "security", Title: "one",
			Verdict: &Verdict{Index: 0, Real: true, WorthRaising: true}},
	}
	_, _ = finding, judged

	groups, warnings, err := GroupDump(records)
	if err != nil {
		t.Fatalf("group: %v", err)
	}

	for _, g := range groups {
		if g.Model == "m/tuner" && HeldOut(g.Fixture.Name) {
			t.Errorf("m/tuner was credited with a review of the held-out fixture %s, which it was "+
				"never given; re-judging it costs a judge call on a fabricated sample", g.Fixture.Name)
		}
		if g.Model == "m/heldout" && !HeldOut(g.Fixture.Name) {
			t.Errorf("m/heldout was credited with a review of the tuning fixture %s", g.Fixture.Name)
		}
	}

	// A guess has to announce itself as one, including the direction it cannot
	// see: a contender silent on every fixture in a run leaves no trace of that
	// run at all.
	if len(groups) != len(records) && !strings.Contains(strings.Join(warnings, "\n"), "INFERRED") {
		t.Errorf("the matrix was guessed and no warning says so: %v", warnings)
	}
}

// fakeJudge records what it was asked and answers deterministically, so
// Rejudge's two stated properties can be checked without a network run.
type fakeJudge struct {
	mu    sync.Mutex
	seen  []string
	delay func(fixture string) time.Duration
}

func (f *fakeJudge) Judge(_ context.Context, fx Fixture, _ config.Persona, findings []review.Finding) (*JudgeResult, error) {
	if f.delay != nil {
		time.Sleep(f.delay(fx.Name))
	}

	f.mu.Lock()
	f.seen = append(f.seen, fmt.Sprintf("%s/%d", fx.Name, len(findings)))
	f.mu.Unlock()

	verdicts := make([]Verdict, 0, len(findings))
	for i := range findings {
		verdicts = append(verdicts, Verdict{Index: i, Real: true, WorthRaising: true})
	}
	return &JudgeResult{Verdicts: verdicts, Grade: "B"}, nil
}

// TestRejudgeKeepsOutcomesInGroupOrder pins that concurrency does not reorder
// the result.
//
// The outcomes are read positionally by every caller and the report prints them
// in the order it receives them, so a slice filled in completion order would
// attach each verdict set to a different group, and, since every group carries
// its own Model and Fixture, the mismatch would look like an ordinary
// disagreement rather than like a bug. The delay is deliberately inverted so
// completion order cannot coincide with submission order.
func TestRejudgeKeepsOutcomesInGroupOrder(t *testing.T) {
	names := []string{"go-nil-deref", "go-sql-injection", "go-hardcoded-secret", "multi-defect"}

	groups := make([]RejudgeGroup, 0, len(names))
	for i, name := range names {
		groups = append(groups, RejudgeGroup{
			Model: "m/a", Run: 1, Fixture: fixtureByName(t, name),
			Findings: []review.Finding{{Path: "a.go", Line: 10 + i, Severity: "error", Title: name}},
			Baseline: []Verdict{{Index: 0, Real: true, WorthRaising: true}},
		})
	}

	// The first group submitted finishes last.
	judge := &fakeJudge{delay: func(fixture string) time.Duration {
		for i, name := range names {
			if name == fixture {
				return time.Duration(len(names)-i) * 20 * time.Millisecond
			}
		}
		return 0
	}}

	outcomes := Rejudge(context.Background(), judge, config.DefaultPersona(), groups, 4)

	if len(outcomes) != len(groups) {
		t.Fatalf("%d outcome(s) for %d group(s)", len(outcomes), len(groups))
	}
	for i, o := range outcomes {
		if o.Group.Fixture.Name != names[i] {
			t.Fatalf("outcome %d carries %s, want %s: the results are in completion order, so every "+
				"verdict set is attached to another group's findings",
				i, o.Group.Fixture.Name, names[i])
		}
		if o.Err != nil || o.Result == nil {
			t.Fatalf("outcome %d has no result: %v", i, o.Err)
		}
	}
}

// TestRejudgeSubmitsSilentGroups pins that a review with no findings is still
// put in front of the judge.
//
// Assuming it produces nothing would be a self-fulfilling measurement: a judge
// that answers an empty finding list with verdicts is a real failure mode, and
// this path is the only place it can be observed. Skipping the call would also
// drop the group from one side of a comparison whose entire premise is that
// both sides see the same sample.
func TestRejudgeSubmitsSilentGroups(t *testing.T) {
	groups := []RejudgeGroup{
		{Model: "m/a", Run: 1, Fixture: fixtureByName(t, "go-nil-deref"),
			Findings: []review.Finding{{Path: "a.go", Line: 10, Severity: "error", Title: "spoke"}}},
		{Model: "m/a", Run: 1, Fixture: fixtureByName(t, "clean-refactor"), Silent: true},
	}

	judge := &fakeJudge{}
	outcomes := Rejudge(context.Background(), judge, config.DefaultPersona(), groups, 2)

	if !slices.Contains(judge.seen, "clean-refactor/0") {
		t.Errorf("the silent group was never submitted to the judge (asked: %v); a judge that "+
			"invents findings on an empty list cannot be observed, and the group is missing from "+
			"one side of the comparison", judge.seen)
	}
	if outcomes[1].Result == nil {
		t.Error("the silent group has no result, so it is dropped from the new judge's side only")
	}
}
