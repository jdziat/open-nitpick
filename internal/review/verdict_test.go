package review

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// verdictsFor turns findings a test wants triage to publish into the verdicts
// triage now returns.
//
// Numbered over dedupe(findings), because that is the list the engine renders
// and therefore the numbering triage answers against. Numbering the raw slice
// instead silently shifts every verdict by one for any test whose findings
// contain a duplicate, which is how this helper first went wrong.
func verdictsFor(findings []Finding) []Verdict {
	findings = dedupe(findings)
	out := make([]Verdict, 0, len(findings))
	for i, f := range findings {
		out = append(out, Verdict{
			Number:     i + 1,
			Severity:   f.Severity,
			Class:      f.Class,
			Line:       f.Line,
			Title:      f.Title,
			Rationale:  f.Rationale,
			Suggestion: f.Suggestion,
		})
	}
	return out
}

// triageWith runs one triage pass over findings with a scripted verdict reply.
func triageWith(t *testing.T, before []Finding, reply TriageResult) ([]Finding, []Overruled) {
	t.Helper()
	return triageWithConfig(t, before, reply, nil)
}

func triageWithConfig(t *testing.T, before []Finding, reply TriageResult, tune func(*config.Config)) ([]Finding, []Overruled) {
	t.Helper()
	model := &scriptedLLM{byPrompt: map[string]string{
		"Review the following changes": mustJSON(t, Result{}),
		"triaging findings":            mustJSON(t, reply),
	}}
	engine := newEngine(t, model, &stubProvider{diff: engineDiff}, tune)
	_, kept, withheld, err := engine.triage(context.Background(), &vcs.PullRequest{}, before)
	if err != nil {
		t.Fatalf("triage: %v", err)
	}
	return kept, withheld
}

func gosecFinding(line int, title string) Finding {
	return Finding{
		Path: "app.go", Line: line, Severity: "error", Title: title, Rationale: "why",
		Class: "security", Source: "golangci-lint(gosec)", FromAnalyzer: true,
		RawSeverity: "HIGH", SeverityTranslated: true, Evidence: []string{"go-defer-in-loop"},
	}
}

// The bug in #95, in the two directions it was reported: a rewording and a
// moved anchor each used to break Finding.Key() and take the metadata with it.
func TestMetadataSurvivesARewordingAndAMove(t *testing.T) {
	before := []Finding{gosecFinding(4, "Real finding")}

	for name, v := range map[string]Verdict{
		"reworded":  {Number: 1, Severity: "error", Class: "security", Title: "Reworded finding"},
		"moved":     {Number: 1, Severity: "error", Class: "security", Line: 5},
		"both":      {Number: 1, Severity: "error", Class: "security", Line: 9, Title: "Quite different words"},
		"unchanged": {Number: 1, Severity: "error", Class: "security"},
	} {
		t.Run(name, func(t *testing.T) {
			kept, _ := triageWith(t, before, TriageResult{Verdicts: []Verdict{v}})
			if len(kept) != 1 {
				t.Fatalf("kept %d findings, want 1", len(kept))
			}
			f := kept[0]
			if len(f.Evidence) != 1 || f.Evidence[0] != "go-defer-in-loop" {
				t.Errorf("Evidence = %v, want the reviewer's", f.Evidence)
			}
			if f.Source != "golangci-lint(gosec)" {
				t.Errorf("Source = %q, want the analyzer that found it", f.Source)
			}
			if !f.FromAnalyzer {
				t.Error("FromAnalyzer lost, so linters.max_severity no longer applies to it")
			}
			if f.RawSeverity != "HIGH" || !f.SeverityTranslated {
				t.Errorf("severity provenance = %q/%v, want HIGH/true", f.RawSeverity, f.SeverityTranslated)
			}
			if f.Class != "security" {
				t.Errorf("Class = %q, want the reviewer's security", f.Class)
			}
		})
	}
}

// Two adjacent findings and a triage reply in the other order. Under a matcher
// this is where one finding's attribution lands on the other; under a number
// it cannot, and that is the property worth pinning.
func TestAdjacentFindingsKeepTheirOwnAttribution(t *testing.T) {
	before := []Finding{gosecFinding(10, "From the analyzer"), {
		Path: "app.go", Line: 12, Severity: "warning", Title: "From the model", Rationale: "why",
		Class: "correctness", Source: "review model",
	}}

	// Reordered, reworded, and both moved onto the other's line.
	kept, _ := triageWith(t, before, TriageResult{Verdicts: []Verdict{
		{Number: 2, Severity: "warning", Class: "correctness", Line: 10, Title: "Second, reworded"},
		{Number: 1, Severity: "error", Class: "security", Line: 12, Title: "First, reworded"},
	}})

	if len(kept) != 2 {
		t.Fatalf("kept %d findings, want 2", len(kept))
	}
	for _, f := range kept {
		switch {
		case strings.HasPrefix(f.Title, "First"):
			if f.Source != "golangci-lint(gosec)" || !f.FromAnalyzer {
				t.Errorf("the analyzer finding published as %q analyzer=%v", f.Source, f.FromAnalyzer)
			}
		case strings.HasPrefix(f.Title, "Second"):
			if f.Source != "review model" || f.FromAnalyzer {
				t.Errorf("the model finding published as %q analyzer=%v", f.Source, f.FromAnalyzer)
			}
		default:
			t.Errorf("unexpected finding %q", f.Title)
		}
	}
}

// A merge names both reporters, because that is the true sentence about who
// found the defect. Picking one destroys a fact.
func TestAMergeUnionsAttributionAndEvidence(t *testing.T) {
	before := []Finding{gosecFinding(10, "From the analyzer"), {
		Path: "app.go", Line: 11, Severity: "warning", Title: "Same defect, model's words",
		Rationale: "why", Class: "correctness", Source: "review model",
		Evidence: []string{"http-body-close-leak"},
	}}

	kept, withheld := triageWith(t, before, TriageResult{
		Verdicts: []Verdict{{Number: 1, Severity: "error", Class: "security", Title: "The merged one"}},
		Dropped:  []Drop{{Number: 2, DuplicateOf: 1, Reason: "same defect"}},
	})

	if len(kept) != 1 || len(withheld) != 1 {
		t.Fatalf("kept %d, withheld %d; want 1 and 1", len(kept), len(withheld))
	}
	f := kept[0]
	if !strings.Contains(f.Source, "gosec") || !strings.Contains(f.Source, "review model") {
		t.Errorf("Source = %q, want both reporters named", f.Source)
	}
	if !f.FromAnalyzer {
		t.Error("the survivor of a merge with an analyzer finding is still capped by linters.max_severity")
	}
	if len(f.Evidence) != 2 {
		t.Errorf("Evidence = %v, want both reviewers' entries", f.Evidence)
	}
}

// A number that names nothing is ignored rather than guessed at, and the
// finding it would have edited comes back as the reviewer wrote it.
func TestAnUnusableNumberLeavesTheFindingAlone(t *testing.T) {
	before := []Finding{gosecFinding(4, "Real finding")}

	for name, verdicts := range map[string][]Verdict{
		"out of range": {{Number: 7, Severity: "nit", Class: "style", Title: "Should not apply"}},
		"zero":         {{Number: 0, Severity: "nit", Class: "style", Title: "Should not apply"}},
		"twice":        {{Number: 1, Severity: "warning", Class: "security"}, {Number: 1, Severity: "nit", Class: "style"}},
	} {
		t.Run(name, func(t *testing.T) {
			expectUnusable = true
			defer func() { expectUnusable = false }()
			kept, _ := triageWith(t, before, TriageResult{Verdicts: verdicts})
			if len(kept) != 1 {
				t.Fatalf("kept %d findings, want the finding restored", len(kept))
			}
			if got := kept[0].Title; got == "Should not apply" {
				t.Error("a verdict naming no finding was applied to one anyway")
			}
			if kept[0].Source != "golangci-lint(gosec)" {
				t.Errorf("Source = %q, want it untouched", kept[0].Source)
			}
			if name == "twice" && kept[0].Severity != "warning" {
				t.Errorf("Severity = %q, want the first verdict to win", kept[0].Severity)
			}
		})
	}
}

// A merge into a finding that is itself merged, and a merge cycle. Both used
// to publish a survivor stripped of the analyzer attribution, or to delete
// both findings outright.
func TestAChainedOrCircularMergeKeepsEveryFinding(t *testing.T) {
	before := []Finding{
		gosecFinding(4, "First"),
		{Path: "app.go", Line: 5, Severity: "warning", Title: "Second", Rationale: "why", Class: "correctness", Source: "review model"},
		gosecFinding(6, "Third"),
	}

	for name, tc := range map[string]struct {
		dropped []Drop
		want    int
	}{
		// 3 into 2 is refused because 2 does not survive; 2 into 1 is an
		// ordinary merge and stands, which is what the old accounting did.
		"chained": {[]Drop{{Number: 3, DuplicateOf: 2, Reason: "same"}, {Number: 2, DuplicateOf: 1, Reason: "same"}}, 2},
		// Neither half of a cycle survives, so neither merge happens and all
		// three findings publish.
		"cycle": {[]Drop{{Number: 1, DuplicateOf: 2, Reason: "same"}, {Number: 2, DuplicateOf: 1, Reason: "same"}}, 3},
	} {
		t.Run(name, func(t *testing.T) {
			kept, _ := triageWith(t, before, TriageResult{
				Verdicts: []Verdict{{Number: 1, Severity: "error", Class: "security"}},
				Dropped:  tc.dropped,
			})
			if len(kept) != tc.want {
				t.Fatalf("kept %d findings, want %d; a merge chain is not a way to delete one", len(kept), tc.want)
			}
			for _, f := range kept {
				if f.Title == "Third" && !f.FromAnalyzer {
					t.Error("the analyzer finding lost its marker, so linters.max_severity no longer reaches it")
				}
			}
		})
	}
}

// The two guarantees the deleted no-new-claims matcher used to hold, now held
// by construction: a verdict cannot reach code the reviewer never read, and a
// suggestion cannot follow a moved anchor onto lines nobody wrote it for.
func TestAVerdictCannotMoveAFindingOntoUnreadCode(t *testing.T) {
	before := []Finding{{
		Path: "app.go", Line: 4, Severity: "error", Class: "correctness", Title: "Real finding",
		Rationale: "why", Suggestion: "\treturn err", FixEndLine: 5, EndLine: 5,
	}}

	far, _ := triageWith(t, before, TriageResult{Verdicts: []Verdict{
		{Number: 1, Severity: "error", Class: "correctness", Line: 400},
	}})
	if far[0].Line != 4 {
		t.Errorf("line = %d, want the reviewer's 4: a verdict 396 lines away is a claim about unread code", far[0].Line)
	}
	if far[0].Suggestion == "" {
		t.Error("the suggestion was dropped although the finding never moved")
	}

	near, _ := triageWith(t, before, TriageResult{Verdicts: []Verdict{
		{Number: 1, Severity: "error", Class: "correctness", Line: 7},
	}})
	f := near[0]
	if f.Line != 7 {
		t.Errorf("line = %d, want triage's 7: a merge may move an anchor a few lines", f.Line)
	}
	if f.Suggestion != "" || f.FixEndLine != 0 {
		t.Errorf("a relocated finding kept its patch (suggestion=%q fixend=%d); it would commit over code "+
			"the reviewer did not write it for", f.Suggestion, f.FixEndLine)
	}
	if f.EndLine != 8 {
		t.Errorf("EndLine = %d, want it moved with Line to 8; a span that ends before it starts prints one line", f.EndLine)
	}
}

// A model's own severity word goes stale when triage re-rates it. An
// analyzer's does not: HIGH is what semgrep printed whatever we publish at.
func TestAStaleSeverityWordIsDroppedOnlyForAModelFinding(t *testing.T) {
	model := Finding{Path: "app.go", Line: 4, Severity: "info", Class: "correctness", Title: "From the model",
		Rationale: "why", Source: "review model", RawSeverity: "P1", SeverityTranslated: true}

	kept, _ := triageWith(t, []Finding{model}, TriageResult{Verdicts: []Verdict{
		{Number: 1, Severity: "warning", Class: "correctness"},
	}})
	if kept[0].RawSeverity != "" || kept[0].SeverityTranslated {
		t.Errorf("raw=%q translated=%v after a re-rate; the report would quote the model against a level it did not choose",
			kept[0].RawSeverity, kept[0].SeverityTranslated)
	}

	kept, _ = triageWith(t, []Finding{gosecFinding(4, "From the analyzer")}, TriageResult{Verdicts: []Verdict{
		{Number: 1, Severity: "warning", Class: "security"},
	}})
	if kept[0].RawSeverity != "HIGH" || !kept[0].SeverityTranslated {
		t.Errorf("raw=%q translated=%v; HIGH is what the analyzer printed and a re-rate is not a retraction of it",
			kept[0].RawSeverity, kept[0].SeverityTranslated)
	}
}

// Under the no-new-claims contract a line move that would cost the reviewer
// their patch is refused. Moving the anchor and dropping the suggestion
// publishes triage's line and one fewer of the reviewer's words, under the one
// setting whose purpose is the opposite.
func TestTheContractKeepsTheReviewersSuggestion(t *testing.T) {
	before := []Finding{{
		Path: "app.go", Line: 4, Severity: "error", Class: "correctness", Title: "Reviewer's title",
		Rationale: "why", Suggestion: "\treturn err", FixEndLine: 5,
	}}
	verdicts := []Verdict{{Number: 1, Severity: "error", Class: "correctness", Line: 6, Title: "Triage's title"}}

	kept, _ := triageWithConfig(t, before, TriageResult{Verdicts: verdicts}, func(c *config.Config) {
		c.Review.TriageNoNewClaims = true
	})
	f := kept[0]
	if f.Suggestion == "" {
		t.Error("the reviewer's suggestion was dropped under the contract that publishes the reviewer's words")
	}
	if f.Line != 4 {
		t.Errorf("line = %d, want the reviewer's 4 when the move would have cost the patch", f.Line)
	}
	if f.Title != "Reviewer's title" {
		t.Errorf("title = %q, want the reviewer's", f.Title)
	}
}

// A triage reply whose verdicts name no finding is what an old-shape fixture
// looks like from inside the engine: every verdict rejected, the review
// publishes what the reviewer wrote, and a test asserting exactly that passes
// having exercised nothing. This makes that state fail loudly for the whole
// package.
// expectUnusable silences the guard for the one test whose whole subject is a
// verdict that names nothing.
var (
	expectUnusable   bool
	unusableVerdicts int
)

func TestMain(m *testing.M) {
	testUnusableVerdicts = func(n int) {
		if expectUnusable {
			return
		}
		unusableVerdicts += n
		if os.Getenv("NITPICK_TRACE_VACUOUS") != "" {
			debug.PrintStack()
		}
	}
	code := m.Run()
	if code == 0 && unusableVerdicts > 0 {
		panic(fmt.Sprintf("%d triage verdicts named no finding: a fixture is still the old shape, "+
			"so the test that scripted it exercised no part of triage", unusableVerdicts))
	}
	os.Exit(code)
}

// A merge, round-tripped through the shipped schema and the JSON decode rather
// than constructed in Go.
//
// duplicate_of was missing from the triage schema while the schema ships with
// strict enforcement, so the model could not emit it, every Drop decoded with
// DuplicateOf zero, and merging never ran. Every test of it built the reply as
// a Go value and skipped the schema that forbade it.
func TestAMergeSurvivesTheShippedSchema(t *testing.T) {
	raw, err := triageSchema(offeredClasses(false))
	if err != nil {
		t.Fatalf("triageSchema: %v", err)
	}
	var schema struct {
		Properties struct {
			Dropped struct {
				Items struct {
					Properties map[string]any `json:"properties"`
					Required   []string       `json:"required"`
				} `json:"items"`
			} `json:"dropped"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := schema.Properties.Dropped.Items.Properties["duplicate_of"]; !ok {
		t.Fatal("duplicate_of is not in the dropped schema, so a strict provider cannot emit it and merging cannot run")
	}
	for _, r := range schema.Properties.Dropped.Items.Required {
		if r == "duplicate_of" {
			t.Error("duplicate_of must not be required: a drop with no merge target is legal and is restored")
		}
	}

	// And the wire shape a model would actually send decodes into a merge.
	var result TriageResult
	if err := json.Unmarshal([]byte(`{"findings":[{"number":1,"severity":"error","class":"security"}],
		"summary":"s","dropped":[{"number":2,"duplicate_of":1,"reason":"same defect"}]}`), &result); err != nil {
		t.Fatalf("decode a model-shaped reply: %v", err)
	}
	if len(result.Dropped) != 1 || result.Dropped[0].DuplicateOf != 1 {
		t.Fatalf("dropped = %+v, want duplicate_of 1", result.Dropped)
	}
}
