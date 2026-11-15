package review

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	llms "github.com/nocturnium/llm-go-sdk/v6"

	"github.com/jdziat/open-nitpick/internal/vcs"
)

func mustJSONAny(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func finding(title, severity string, confidence float64) Finding {
	c := confidence
	return Finding{Path: "app.go", Line: 4, Severity: severity, Category: "correctness", Class: "correctness",
		Title: title, Rationale: "r", Confidence: &c}
}

// apart puts each finding on its own line, ten apart, so a test about the
// floor or the cap is not also a test of the duplicate rule.
func apart(fs []Finding) []Finding {
	for i := range fs {
		fs[i].Line = 10 * (i + 1)
	}
	return fs
}

func keptTitles(fs []Finding) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Title)
	}
	return out
}

// The floor is inclusive: a finding at the floor is kept and one below it is
// withheld with its confidence named, as is a finding that gave none.
func TestTheDeepFilterWithholdsFindingsBelowTheConfidenceFloor(t *testing.T) {
	none := finding("no confidence", "error", 0)
	none.Confidence = nil
	kept, withheld := deepFilter([]Finding{
		finding("at the floor", "error", 0.70),
		finding("below the floor", "error", 0.69),
		none,
	}, 0.7, 5)
	if got := keptTitles(kept); len(got) != 1 || got[0] != "at the floor" {
		t.Errorf("kept = %v, want only the finding at the floor", got)
	}
	if len(withheld) != 2 || !strings.Contains(withheld[0].Reason, "0.69") || withheld[0].Expert != deepFilterExpert {
		t.Errorf("withheld = %+v, want both, the first naming its confidence", withheld)
	}
}

// The cap holds info and nit findings to the few that rank highest, by
// severity and then confidence, and never touches a warning: there it would be
// a cap on real defects, not on noise.
func TestTheDeepFilterCapsLowPriorityFindingsAndNeverWarnings(t *testing.T) {
	var in []Finding
	for i := range 4 {
		in = append(in, finding(fmt.Sprintf("info %d", i), "info", 0.71+float64(i)/100))
		in = append(in, finding(fmt.Sprintf("warning %d", i), "warning", 0.71))
	}
	in = append(in, finding("nit", "nit", 0.99))
	kept, withheld := deepFilter(apart(in), 0.7, 2)
	var warnings, low []string
	for _, f := range kept {
		if f.Severity == "warning" {
			warnings = append(warnings, f.Title)
		} else {
			low = append(low, f.Title)
		}
	}
	if len(warnings) != 4 {
		t.Errorf("warnings kept = %v, want all four", warnings)
	}
	if len(low) != 2 || low[0] != "info 3" || low[1] != "info 2" {
		t.Errorf("low-priority kept = %v, want info 3 then info 2: info outranks a nit whatever its confidence", low)
	}
	if len(withheld) != 3 {
		t.Errorf("withheld %d, want the three over the cap", len(withheld))
	}
}

// A cap of zero is off.
func TestADeepFilterCapOfZeroKeepsEveryLowPriorityFinding(t *testing.T) {
	in := []Finding{finding("a", "info", 0.8), finding("b", "info", 0.8), finding("c", "info", 0.8)}
	kept, withheld := deepFilter(apart(in), 0.7, 0)
	if len(kept) != 3 || len(withheld) != 0 {
		t.Errorf("kept %d, withheld %d; want every finding kept", len(kept), len(withheld))
	}
}

// Analyzer findings carry no reviewer confidence; the floor would drop every
// one of them, so they pass the deep selection untouched.
func TestTheDeepSelectionLeavesAnalyzerFindingsAlone(t *testing.T) {
	e, _ := deepEngine(t, &deepLLM{}, appRepo(t))
	lint := Finding{Path: "app.go", Line: 4, Severity: "warning", Class: "correctness", Title: "lint", Rationale: "r", FromAnalyzer: true, Source: "golangci-lint"}
	out, withheld, stages := e.deepSelect(context.Background(), []Finding{lint}, nil)
	if len(out) != 1 || len(withheld) != 0 || len(stages) != 0 {
		t.Errorf("out %v, withheld %v, stages %v; want the analyzer finding kept and nothing run", keptTitles(out), withheld, stages)
	}
}

// The keep rule: proven is kept, plausible only at warning or above with
// confidence 0.75 or more, unsupported and anything else withheld.
func TestTheCritiqueKeepRule(t *testing.T) {
	for _, tc := range []struct {
		evidence, severity string
		confidence         float64
		keep               bool
	}{
		{"proven", "info", 0.70, true},
		{"plausible", "warning", 0.75, true},
		{"plausible", "warning", 0.74, false},
		{"plausible", "info", 0.95, false},
		{"plausible", "critical", 0.80, true},
		{"unsupported", "critical", 1.0, false},
		{"maybe", "critical", 1.0, false},
	} {
		got := deepCritiqueKeeps(tc.evidence, finding("f", tc.severity, tc.confidence))
		if got != tc.keep {
			t.Errorf("%s %s at %.2f: keep = %v, want %v", tc.evidence, tc.severity, tc.confidence, got, tc.keep)
		}
	}
}

func critiqueEngine(t *testing.T, reviewer *deepLLM) *Engine {
	t.Helper()
	e, _ := deepEngine(t, reviewer, appRepo(t))
	return e
}

// The critique withholds what it grades unsupported, with its reason, and
// keeps what it grades proven.
func TestTheCritiqueWithholdsUnsupportedFindings(t *testing.T) {
	fs := []Finding{finding("real", "error", 0.9), finding("wrong", "error", 0.9)}
	e := critiqueEngine(t, &deepLLM{grades: gradeAll(t, "proven", "unsupported")})
	kept, withheld, failed := e.deepCritique(context.Background(), fs, nil)
	if failed != nil {
		t.Fatalf("stage failed: %+v", failed)
	}
	if got := keptTitles(kept); len(got) != 1 || got[0] != "real" {
		t.Errorf("kept = %v, want only the proven finding", got)
	}
	if len(withheld) != 1 || withheld[0].Expert != deepCritiqueExpert || !strings.Contains(withheld[0].Reason, "unsupported") {
		t.Errorf("withheld = %+v, want the unsupported finding with its grade", withheld)
	}
}

// A critique that fails has judged nothing: every finding is kept and the
// stage is reported as not run, so the review does not read as filtered.
func TestACritiqueThatFailsKeepsEveryFindingAndReportsTheStage(t *testing.T) {
	fs := []Finding{finding("a", "error", 0.9), finding("b", "error", 0.9)}
	e := critiqueEngine(t, &deepLLM{critiqueErr: errors.New("upstream 503")})
	kept, withheld, failed := e.deepCritique(context.Background(), fs, nil)
	if len(kept) != 2 || len(withheld) != 0 || failed == nil || failed.Stage != "critique" {
		t.Errorf("kept %d, withheld %d, stage %+v; want both kept and the critique reported", len(kept), len(withheld), failed)
	}
}

// A reply with no grades is a critique that did not do its job, not a grade of
// every finding as unsupported.
func TestACritiqueWithNoGradesKeepsEveryFinding(t *testing.T) {
	fs := []Finding{finding("a", "error", 0.9)}
	for name, reply := range map[string]*llms.Response{
		"an empty list": gradeAll(t),
		"prose":         nil,
	} {
		e := critiqueEngine(t, &deepLLM{grades: reply})
		kept, _, failed := e.deepCritique(context.Background(), fs, nil)
		if len(kept) != 1 || failed == nil || failed.Reason != "no grades" {
			t.Errorf("%s: kept %d, stage %+v; want the finding kept and no grades reported", name, len(kept), failed)
		}
	}
}

// Once the critique grades anything, a finding it left ungraded is withheld.
func TestAFindingTheCritiqueLeftUngradedIsWithheld(t *testing.T) {
	fs := []Finding{finding("graded", "error", 0.9), finding("ungraded", "error", 0.9)}
	e := critiqueEngine(t, &deepLLM{grades: gradeAll(t, "proven")})
	kept, withheld, _ := e.deepCritique(context.Background(), fs, nil)
	if got := keptTitles(kept); len(got) != 1 || got[0] != "graded" {
		t.Errorf("kept = %v, want only the graded finding", got)
	}
	if len(withheld) != 1 || !strings.Contains(withheld[0].Reason, "no grade") {
		t.Errorf("withheld = %+v, want the ungraded finding with that reason", withheld)
	}
}

// The critique is shown the diff around each finding's cited line, so it
// grades what the code shows and not only the finding's own quotation.
func TestTheCritiqueSeesTheDiffAroundEachFinding(t *testing.T) {
	fs := confident(twoFindings(), 0.9)
	reviewer := &deepLLM{
		general:  []llms.Response{submit(t, fs[:1])},
		security: []llms.Response{submit(t, nil)},
		grades:   gradeAll(t, "proven"),
	}
	e, _ := deepEngine(t, reviewer, appRepo(t))
	if _, err := e.Review(context.Background(), vcs.Ref{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reviewer.critiqueRequest, `http.Get("http://x")`) {
		t.Errorf("critique request = %q, want the diff line the finding cites", reviewer.critiqueRequest)
	}
}

// A finding the deep selection withheld nobody disagreed with, so the review
// does not blame its absence on a domain expert.
func TestAFindingWithheldByTheDeepSelectionIsNotBlamedOnADisagreeingExpert(t *testing.T) {
	for _, expert := range []string{deepFilterExpert, deepCritiqueExpert} {
		r := &Report{Overruled: []Overruled{{Finding: finding("f", "error", 0.5), Expert: expert, Reason: "x"}}}
		if got := r.WithheldCause(); got != "by a second check" {
			t.Errorf("%s: cause = %q, want by a second check", expert, got)
		}
	}
}

func at(path string, line int, class, title, rationale string) Finding {
	return Finding{Path: path, Line: line, Class: class, Title: title, Rationale: rationale, Severity: "warning"}
}

// Each branch of Mira's duplicate rule, and the case each one must leave
// apart: one defect filed twice is one finding, two defects are two.
func TestTheDeepDuplicateRule(t *testing.T) {
	long := "the deferred close runs on a nil response body when the request fails and panics the handler"
	other := "the cache key omits the tenant so one tenant reads another tenant's cached rows on a hit"
	for _, tc := range []struct {
		name string
		a, b Finding
		dup  bool
	}{
		{"same line and class, unrelated titles",
			at("b.tsx", 553, "correctness", "Async forEach leaves calendar deletion unawaited", "x"),
			at("b.tsx", 553, "correctness", "requestReschedule returns before calendar events are deleted", "y"), true},
		{"same line, two classes, unrelated titles",
			at("a.go", 9, "resource", "File handle leaks on the error path", "x"),
			at("a.go", 9, "security", "Path joins untrusted input", "y"), false},
		{"same class two lines apart, similar text",
			at("a.go", 10, "correctness", "Nil body closed", long),
			at("a.go", 12, "correctness", "Close on failed request", long), true},
		{"same class two lines apart, different text",
			at("a.go", 10, "correctness", "Nil body closed", long),
			at("a.go", 12, "correctness", "Cache key misses tenant", other), false},
		{"overlapping span, titles sharing words",
			Finding{Path: "a.go", Line: 20, EndLine: 30, Class: "correctness", Title: "Async forEach defers calendar deletion past function return"},
			Finding{Path: "a.go", Line: 25, Class: "concurrency", Title: "Async forEach drops calendar deletions and escapes try/catch"}, true},
		{"one file, far apart, near-identical titles",
			at("a.go", 10, "correctness", "Ignored error from http.Get", "x"),
			at("a.go", 90, "contract", "Ignored error from http.Get call", "y"), true},
		{"two files, near-identical title and rationale",
			at("a.go", 10, "correctness", "Ignored error from http.Get", long),
			at("b.go", 10, "correctness", "Ignored error from http.Get", long), true},
		{"two files, same title, different rationale",
			at("a.go", 10, "correctness", "Ignored error from http.Get", long),
			at("b.go", 10, "correctness", "Ignored error from http.Get", other), false},
	} {
		if got := deepDuplicate(tc.a, tc.b); got != tc.dup {
			t.Errorf("%s: duplicate = %v, want %v", tc.name, got, tc.dup)
		}
	}
}

// Of two findings that are one defect, the higher ranked is posted and the
// other is listed as repeating it.
func TestTheDeepFilterKeepsTheHigherRankedOfTwoDuplicates(t *testing.T) {
	lower := finding("Async forEach leaves calendar deletion unawaited", "warning", 0.95)
	higher := finding("requestReschedule returns before calendar events are deleted", "error", 0.80)
	kept, withheld := deepFilter([]Finding{lower, higher}, 0.7, 5)
	if got := keptTitles(kept); len(got) != 1 || got[0] != higher.Title {
		t.Errorf("kept = %v, want only the error", got)
	}
	if len(withheld) != 1 || !strings.Contains(withheld[0].Reason, "repeats") || !strings.Contains(withheld[0].Reason, "requestReschedule") {
		t.Errorf("withheld = %+v, want the warning, naming the finding it repeats", withheld)
	}

	// At one severity, the more confident of the two is the one posted.
	doubtful := finding("Async forEach leaves calendar deletion unawaited", "warning", 0.75)
	sure := finding("requestReschedule returns before calendar events are deleted", "warning", 0.95)
	kept, _ = deepFilter([]Finding{doubtful, sure}, 0.7, 5)
	if got := keptTitles(kept); len(got) != 1 || got[0] != sure.Title {
		t.Errorf("kept = %v, want the more confident warning", got)
	}
}
