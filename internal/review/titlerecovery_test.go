package review

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/jdziat/open-nitpick/internal/vcs"
)

// TestMissingTitleDoesNotDiscardAnOtherwiseCompleteFinding pins the loss seen
// in a captured review: the model reported a critical injection with a path,
// line and rationale but no title, and Valid silently dropped it before triage.
//
// The raw snapshot must stay titleless, because it is evidence of what the
// model said; the recovered finding must reach publication with every
// substantive field untouched.
func TestMissingTitleDoesNotDiscardAnOtherwiseCompleteFinding(t *testing.T) {
	rationale := "User input reaches a shell command unescaped. An attacker can run arbitrary commands."
	raw := Finding{Path: "app.go", Line: 4, Severity: "critical", Category: "security", Class: "security",
		Rationale: rationale}

	model := &scriptedLLM{
		byPrompt: map[string]string{
			"triaging findings": mustJSON(t, TriageResult{Verdicts: verdictsFor([]Finding{raw})}),
		},
		fallback: mustJSON(t, Result{Findings: []Finding{raw}}),
	}
	provider := &stubProvider{diff: engineDiff}
	engine := newEngine(t, model, provider, nil)

	var mu sync.Mutex
	var batchSnapshot []Finding
	engine.OnSnapshot = func(s Snapshot) {
		if s.Stage == "batch-generation" {
			mu.Lock()
			batchSnapshot = append(batchSnapshot, s.Findings...)
			mu.Unlock()
		}
	}

	report, err := engine.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(batchSnapshot) != 1 || batchSnapshot[0].Title != "" {
		t.Fatalf("raw snapshot = %+v, want one finding with its title still blank", batchSnapshot)
	}
	if len(report.Findings) != 1 {
		t.Fatalf("findings = %+v, want the titleless critical finding to survive", report.Findings)
	}
	got := report.Findings[0]
	if want := "User input reaches a shell command unescaped."; got.Title != want {
		t.Errorf("title = %q, want %q", got.Title, want)
	}
	if got.Severity != "critical" || got.Rationale != rationale || got.Line != 4 || got.Path != "app.go" {
		t.Errorf("substantive fields changed: %+v", got)
	}
	if provider.published == nil || len(provider.published.Comments) != 1 ||
		!strings.Contains(provider.published.Comments[0].Body, "User input reaches a shell command unescaped.") {
		t.Errorf("recovered finding was not published: %+v", provider.published)
	}
}

func TestTitleRecoveryRefusesFindingsThatAreNotOtherwiseComplete(t *testing.T) {
	base := Finding{Path: "app.go", Line: 4, Severity: "error", Rationale: "It breaks."}
	cases := map[string]func(*Finding){
		"blank rationale":  func(f *Finding) { f.Rationale = " \n\t" },
		"blank path":       func(f *Finding) { f.Path = "" },
		"zero line":        func(f *Finding) { f.Line = 0 },
		"unknown severity": func(f *Finding) { f.Severity = "P1" },
		"severity none":    func(f *Finding) { f.Severity = "none" },
		"blank severity":   func(f *Finding) { f.Severity = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := base
			mutate(&f)
			got, ok := withRecoveredTitle(f)
			if ok || got.Title != "" || got.Valid() {
				t.Errorf("recovered %+v, want rejection", got)
			}
		})
	}
}

func TestTitleRecoveryKeepsAnExistingTitle(t *testing.T) {
	f := Finding{Path: "a.go", Line: 1, Severity: "error", Title: "Mine", Rationale: "Something else entirely."}
	got, ok := withRecoveredTitle(f)
	if ok || got.Title != "Mine" {
		t.Errorf("got %q recovered=%v, want the model's title untouched", got.Title, ok)
	}
}

func TestRecoveredTitleIsBoundedByRunesAndStable(t *testing.T) {
	f := Finding{Path: "a.go", Line: 1, Severity: "warning", Rationale: strings.Repeat("é界 ", 80)}
	first, ok := withRecoveredTitle(f)
	if !ok {
		t.Fatal("expected a recovered title")
	}
	if n := len([]rune(first.Title)); n > maxRecoveredTitle {
		t.Errorf("title has %d runes, want at most %d", n, maxRecoveredTitle)
	}
	if !strings.HasSuffix(first.Title, "…") || strings.ContainsRune(first.Title, '\uFFFD') {
		t.Errorf("title %q should end in an ellipsis and stay valid UTF-8", first.Title)
	}
	second, _ := withRecoveredTitle(f)
	if second.Title != first.Title {
		t.Errorf("recovery is not deterministic: %q vs %q", first.Title, second.Title)
	}
	again, ok := withRecoveredTitle(first)
	if ok || again.Title != first.Title {
		t.Errorf("a recovered finding must not be recovered again")
	}
}
