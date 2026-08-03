package evals

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/review"
)

// severityFixture is a hand-built corpus with one defect at each of three
// severities, far enough apart that no finding can match two of them.
//
// Hand-built rather than borrowed from Fixtures(): these tests pin the SCORER,
// and reading ground truth out of the corpus under measurement would make them
// pass again the moment someone edits a fixture's WantSeverity.
func severityFixture() Fixture {
	return Fixture{
		Name: "hand-built",
		Defects: []Defect{
			{
				Path:         "a.go",
				Line:         10,
				Keywords:     []string{"hardcod", "secret"},
				WantSeverity: config.SeverityCritical,
				Why:          "a live credential is committed to source",
			},
			{
				Path:         "a.go",
				Line:         30,
				Keywords:     []string{"race"},
				WantSeverity: config.SeverityError,
				Why:          "the counter is incremented without synchronization",
			},
			{
				Path:         "a.go",
				Line:         50,
				Keywords:     []string{"capacity"},
				WantSeverity: config.SeverityNit,
				Why:          "the capacity hint is one short, forcing a reallocation",
			},
		},
	}
}

// TestSeverityIsScoredAgainstPlantedGroundTruth pins all three directions.
//
// The understated case is the real one that motivated this: Incumbent reported
// the hardcoded secret as "error" against a planted "critical", and the LLM
// judge scored it as understating nothing at all. If this test ever reports
// zero understated for that pair, the objective scorer has regressed into the
// judge's blind spot and severity tuning is measuring nothing again.
func TestSeverityIsScoredAgainstPlantedGroundTruth(t *testing.T) {
	findings := []review.Finding{
		{Path: "a.go", Line: 30, Severity: "error", Title: "data race on the counter"},
		{Path: "a.go", Line: 50, Severity: "critical", Title: "capacity hint is one short"},
		{Path: "a.go", Line: 10, Severity: "error", Title: "hardcoded credential"},
		{Path: "a.go", Line: 70, Severity: "warning", Title: "this name could be clearer"},
	}

	got := ScoreSeverity(severityFixture(), findings)

	if got.Accurate != 1 {
		t.Errorf("accurate = %d, want 1: error on a planted error is accurate", got.Accurate)
	}
	if got.Inflated != 1 {
		t.Errorf("inflated = %d, want 1: critical on a planted nit is inflated", got.Inflated)
	}
	if got.Understated != 1 {
		t.Errorf("understated = %d, want 1: error on a planted critical is understated, "+
			"and this is the direction the judge could not see", got.Understated)
	}

	// The unmatched finding is not gradeable: nothing planted says what its
	// severity should have been. Counting it accurate would let a reviewer bury
	// its severity errors under its own false positives.
	if got.Graded() != 3 {
		t.Errorf("graded = %d, want 3: the finding matching no defect must not be scored", got.Graded())
	}
	if len(got.Calls) != 3 {
		t.Fatalf("calls = %d, want 3", len(got.Calls))
	}

	// Each call must name the defect it was scored against, or a report cannot
	// say WHICH finding was inflated.
	byTitle := map[string]SeverityCall{}
	for _, c := range got.Calls {
		byTitle[c.Finding.Title] = c
	}

	want := map[string]struct {
		verdict string
		planted config.Severity
	}{
		"data race on the counter":   {SevAccurate, config.SeverityError},
		"capacity hint is one short": {SevInflated, config.SeverityNit},
		"hardcoded credential":       {SevUnderstated, config.SeverityCritical},
	}

	for title, w := range want {
		c, ok := byTitle[title]
		if !ok {
			t.Errorf("no call recorded for %q", title)
			continue
		}
		if c.Verdict != w.verdict {
			t.Errorf("%q: verdict %q, want %q", title, c.Verdict, w.verdict)
		}
		if c.Defect.WantSeverity != w.planted {
			t.Errorf("%q: scored against planted %q, want %q", title, c.Defect.WantSeverity, w.planted)
		}
	}
}

// TestSeverityIgnoresFindingsAnchoredTooFarAway keeps the objective severity
// score aligned with detection.
//
// A finding that describes a planted defect but points somewhere else is not
// counted as detecting it, so grading its severity against that defect would
// credit a reviewer for the severity of a bug it did not actually locate.
func TestSeverityIgnoresFindingsAnchoredTooFarAway(t *testing.T) {
	findings := []review.Finding{
		{Path: "a.go", Line: 10 + anchorTolerance + 1, Severity: "nit", Title: "hardcoded secret"},
	}

	if got := ScoreSeverity(severityFixture(), findings); got.Graded() != 0 {
		t.Errorf("graded = %d, want 0: the finding sits outside the anchor tolerance "+
			"and is not credited with detecting the defect either", got.Graded())
	}
}

// overlappingFixture is the multi-defect shape: two defects on one line with
// different WantSeverity, which a single merged comment can match.
func overlappingFixture() Fixture {
	return Fixture{
		Name: "overlapping",
		Defects: []Defect{
			{Path: "h.go", Line: 19, Keywords: []string{"traversal"}, WantSeverity: config.SeverityCritical, Why: "traversal"},
			{Path: "h.go", Line: 19, Keywords: []string{"leak"}, WantSeverity: config.SeverityError, Why: "descriptor leak"},
		},
	}
}

// TestMergedCommentIsGradedAgainstEveryDefectItCovers pins the direction the
// previous scorer had backwards.
//
// It let the FINDING choose which of two overlapping plants it was graded
// against, on the grounds that the ambiguity was the corpus's rather than the
// reviewer's. The consequence was worse than the problem: on plants of critical
// and error, every severity from error upwards graded accurate, so under-rating
// the critical traversal was unmeasurable, and a reviewer could buy immunity
// from the column being tuned by merging two comments into one. A merged
// comment is now graded once per defect it covers, against each defect's own
// planted level.
func TestMergedCommentIsGradedAgainstEveryDefectItCovers(t *testing.T) {
	f := overlappingFixture()

	// Mentions both, so it is credited with both defects.
	both := "path traversal, and the descriptor leak on the same line"

	for _, tc := range []struct {
		severity                        string
		accurate, inflated, understated int
	}{
		// Carries the worse of the two plants: right about the traversal,
		// over-claiming the leak. Merging is not free in either direction.
		{"critical", 1, 1, 0},
		// The case that used to read as flawless: the 1-step understatement of
		// the critical plant is now visible.
		{"error", 1, 0, 1},
		{"info", 0, 0, 2},
	} {
		t.Run(tc.severity, func(t *testing.T) {
			got := ScoreSeverity(f, []review.Finding{
				{Path: "h.go", Line: 19, Severity: tc.severity, Title: both},
			})

			if got.Graded() != 2 {
				t.Fatalf("graded = %d, want 2: both plants were located, so both are graded", got.Graded())
			}
			if got.Accurate != tc.accurate || got.Inflated != tc.inflated || got.Understated != tc.understated {
				t.Errorf("severity %q against plants critical+error: accurate/inflated/understated = %d/%d/%d, want %d/%d/%d",
					tc.severity, got.Accurate, got.Inflated, got.Understated,
					tc.accurate, tc.inflated, tc.understated)
			}
		})
	}
}

// TestSeparateCommentsAreEachGradedOnTheirOwnPlant is the other half.
//
// A reviewer that rates both defects correctly in two comments must score two
// accurate calls. Without this, grading per defect could be satisfied by always
// picking the first matching finding — which would mark the leak inflated here
// purely because the traversal comment came first in the list.
func TestSeparateCommentsAreEachGradedOnTheirOwnPlant(t *testing.T) {
	got := ScoreSeverity(overlappingFixture(), []review.Finding{
		{Path: "h.go", Line: 19, Severity: "critical", Title: "path traversal in the upload name"},
		{Path: "h.go", Line: 19, Severity: "error", Title: "the file handle is never closed, a descriptor leak"},
	})

	if got.Graded() != 2 || got.Accurate != 2 {
		t.Errorf("accurate = %d of %d graded, want 2 of 2: each defect was reported at its planted level",
			got.Accurate, got.Graded())
	}
}

// TestSeverityIsGradedPerDefectNotPerFinding stops verbosity from paying.
//
// Grading per finding counted one planted defect once per comment that happened
// to match it, so a reviewer restating a correct call four ways earned four
// times the accuracy of one that said it once — and a comment about an entirely
// different bug that tripped a keyword was graded against the plant's severity
// as though it had reported it.
func TestSeverityIsGradedPerDefectNotPerFinding(t *testing.T) {
	f := Fixture{
		Name: "one-plant",
		Defects: []Defect{{
			Path: "a.go", Line: 10, Keywords: []string{"secret"},
			WantSeverity: config.SeverityCritical, Why: "a live credential is committed",
		}},
	}

	got := ScoreSeverity(f, []review.Finding{
		{Path: "a.go", Line: 10, Severity: "critical", Title: "hardcoded secret"},
		{Path: "a.go", Line: 11, Severity: "critical", Title: "the secret is committed"},
		{Path: "a.go", Line: 12, Severity: "critical", Title: "secret in source"},
		{Path: "a.go", Line: 12, Severity: "critical", Title: "remove this secret"},
	})

	if got.Graded() != 1 {
		t.Errorf("graded = %d for one planted defect: severity is counted per defect, "+
			"or saying the same true thing four times scores four times as honest", got.Graded())
	}
}

// TestSeverityGradedCountEqualsRecall is the invariant that makes the two
// columns readable side by side.
//
// The severity counts are printed next to RECALL with no divisor of their own.
// That is only legible while their total IS the recall numerator; when it was
// not, the legend printed under the table stated a number the column beside it
// contradicted.
func TestSeverityGradedCountEqualsRecall(t *testing.T) {
	for _, f := range AllFixtures() {
		findings, ok := CachedIncumbent("testdata/incumbent", f)
		if !ok {
			continue
		}

		s := ScoreRun(RunResult{Report: &review.Report{Findings: findings}}, f)
		if s.Severity.Graded() != s.Matched {
			t.Errorf("%s: %d defects located but %d severity calls; the SEV cell no longer adds up to RECALL",
				f.Name, s.Matched, s.Severity.Graded())
		}
	}
}

// TestAggregateKeepsBothSeverityOpinions proves the objective counts are
// carried alongside the judge's and do not overwrite them.
//
// The two disagree by design: this sample is the Incumbent case, an "error" on
// a planted "critical" that the judge called accurate. A report that showed one
// number would be showing whichever instrument happened to win a merge.
func TestAggregateKeepsBothSeverityOpinions(t *testing.T) {
	var a Aggregate

	findings := []review.Finding{
		{Path: "a.go", Line: 10, Severity: "error", Title: "hardcoded secret"},
	}

	if problems := a.Add(&JudgeResult{
		Verdicts: []Verdict{{Index: 0, Real: true, WorthRaising: true, SeverityVerdict: "accurate"}},
		Grade:    "B",
	}, len(findings)); len(problems) > 0 {
		t.Fatalf("judge output reported suspect: %v", problems)
	}
	a.AddSeverity(ScoreSeverity(severityFixture(), findings))

	if a.Understated != 0 {
		t.Errorf("judge understated = %d, want 0: the judge's own verdict was 'accurate' "+
			"and must survive intact", a.Understated)
	}
	if a.SevUnderstated != 1 {
		t.Errorf("objective understated = %d, want 1: error on a planted critical", a.SevUnderstated)
	}
	if a.SevAccurate != 0 || a.SevInflated != 0 {
		t.Errorf("objective accurate/inflated = %d/%d, want 0/0", a.SevAccurate, a.SevInflated)
	}
}

// TestDumpWritesWhatItClaims reads the dump back as a program would.
func TestDumpWritesWhatItClaims(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dump.jsonl")

	dump, err := NewDump(path)
	if err != nil {
		t.Fatalf("new dump: %v", err)
	}

	findings := []review.Finding{
		{Path: "a.go", Line: 10, Severity: "error", Class: "security", Title: "hardcoded secret",
			Rationale: "a live credential is committed"},
		// Judged, matching nothing planted.
		{Path: "a.go", Line: 70, Severity: "warning", Class: "style", Title: "unclear name"},
		// Matched but NOT judged: the judge returned no verdict for index 2.
		{Path: "a.go", Line: 30, Severity: "error", Class: "concurrency", Title: "data race"},
	}

	judged := &JudgeResult{Verdicts: []Verdict{
		{Index: 0, Real: true, WorthRaising: true, SeverityVerdict: "accurate",
			ClassCorrect: true, ExpectedClass: "security", Reasoning: "a committed key is a real problem"},
		{Index: 1, Real: false, WorthRaising: false, SeverityVerdict: "inflated",
			ClassCorrect: false, ExpectedClass: "style", Reasoning: "naming is out of scope"},
	}}

	if err := dump.Record(DumpSample{
		Model:    "nitpick/test-model",
		Variant:  "nitpick=normal",
		Run:      2,
		Fixture:  severityFixture(),
		Findings: findings,
		Judged:   judged,
	}); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := dump.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	records := readDump(t, path)
	if len(records) != len(findings) {
		t.Fatalf("wrote %d records for %d findings", len(records), len(findings))
	}

	first := records[0]
	if first.Model != "nitpick/test-model" || first.Fixture != "hand-built" ||
		first.Run != 2 || first.Variant != "nitpick=normal" {
		t.Errorf("sample identity lost: %+v", first)
	}
	if first.Index != 0 || first.Path != "a.go" || first.Line != 10 ||
		first.Severity != "error" || first.Class != "security" || first.Title != "hardcoded secret" {
		t.Errorf("finding not recorded faithfully: %+v", first)
	}
	if first.Rationale == "" {
		t.Error("rationale dropped; the dump exists to be read next to the code it was about")
	}

	// The judge's own call has to survive, or the dump cannot be used to see
	// why a severity was accepted.
	if first.Verdict == nil {
		t.Fatal("verdict missing for a judged finding")
	}
	if first.Verdict.SeverityVerdict != "accurate" || !first.Verdict.Real ||
		!first.Verdict.WorthRaising || !first.Verdict.ClassCorrect ||
		first.Verdict.Reasoning == "" {
		t.Errorf("judge verdict not recorded faithfully: %+v", first.Verdict)
	}

	// Ground truth beside it, disagreeing with the judge exactly as the tables do.
	if !first.Matched {
		t.Error("matched = false for a finding on a planted defect")
	}
	if first.WantSeverity != string(config.SeverityCritical) {
		t.Errorf("want_severity = %q, want %q", first.WantSeverity, config.SeverityCritical)
	}
	if first.SeverityDelta != SevUnderstated {
		t.Errorf("severity_delta = %q, want %q: error on a planted critical",
			first.SeverityDelta, SevUnderstated)
	}
	if first.DefectWhy == "" {
		t.Error("defect_why dropped; a dump line must be legible without the fixture source")
	}

	// A finding matching nothing planted carries no ground truth at all, rather
	// than a default that would read as agreement.
	unmatched := records[1]
	if unmatched.Matched || unmatched.WantSeverity != "" || unmatched.SeverityDelta != "" {
		t.Errorf("unmatched finding carries ground truth it cannot have: %+v", unmatched)
	}
	if unmatched.Verdict == nil || unmatched.Verdict.SeverityVerdict != "inflated" {
		t.Errorf("verdict lost for the unmatched finding: %+v", unmatched.Verdict)
	}

	// An absent verdict must be absent, not an approving zero value.
	unjudged := records[2]
	if unjudged.Verdict != nil {
		t.Errorf("index 2 has a verdict the judge never returned: %+v", unjudged.Verdict)
	}
	if !unjudged.Matched || unjudged.SeverityDelta != SevAccurate {
		t.Errorf("ground truth lost for an unjudged finding: %+v", unjudged)
	}
}

// TestDumpDisabledCostsNothing pins the no-op path, which is what lets every
// call site drop the record unconditionally.
func TestDumpDisabledCostsNothing(t *testing.T) {
	t.Setenv(EnvDump, "")

	dump, err := OpenDump()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if dump != nil {
		t.Fatalf("dump is enabled with %s unset", EnvDump)
	}

	if err := dump.Record(DumpSample{
		Fixture:  severityFixture(),
		Findings: []review.Finding{{Path: "a.go", Line: 10, Severity: "error", Title: "hardcoded secret"}},
	}); err != nil {
		t.Errorf("record on a disabled dump: %v", err)
	}
	if err := dump.Close(); err != nil {
		t.Errorf("close on a disabled dump: %v", err)
	}
}

// TestOpenDumpHonoursTheEnvironment proves the env var is actually read; a
// diagnostic that silently writes nowhere is worse than none.
func TestOpenDumpHonoursTheEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dump.jsonl")
	t.Setenv(EnvDump, path)

	dump, err := OpenDump()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if dump == nil {
		t.Fatalf("%s=%s produced no dump", EnvDump, path)
	}

	if err := dump.Record(DumpSample{
		Model:    "m",
		Fixture:  severityFixture(),
		Findings: []review.Finding{{Path: "a.go", Line: 30, Severity: "error", Title: "data race"}},
	}); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := dump.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if got := readDump(t, path); len(got) != 1 || got[0].Title != "data race" {
		t.Errorf("dump at %s = %+v, want one record for the data race", path, got)
	}
}

// readDump parses the file one JSON object per line, the way a consumer would.
func readDump(t *testing.T, path string) []DumpRecord {
	t.Helper()

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open dump: %v", err)
	}
	defer func() { _ = f.Close() }()

	var out []DumpRecord

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var rec DumpRecord
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			t.Fatalf("line %d is not JSON: %v\n%s", len(out)+1, err, scanner.Text())
		}
		out = append(out, rec)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read dump: %v", err)
	}

	return out
}
