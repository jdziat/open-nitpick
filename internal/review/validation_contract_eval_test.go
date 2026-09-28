//go:build eval

package review

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/llm"
)

// TestLiveValidationReturnsCompleteVerdicts replays the two version-5 claims
// whose severity verdicts omitted a level, using the full fixture source.
func TestLiveValidationReturnsCompleteVerdicts(t *testing.T) {
	if os.Getenv("NITPICK_EVAL_VALIDATION_CONTRACT") != "1" {
		t.Skip("set NITPICK_EVAL_VALIDATION_CONTRACT=1 for live validation calls")
	}
	data, err := os.ReadFile("../../notes/evidence/design-v5-trials.json")
	if err != nil {
		t.Fatal(err)
	}
	var reports struct {
		Runs []struct {
			Mechanism, Variant string
			Iteration          int
			Checks             []struct {
				Findings []struct {
					Rule, Title, Rationale, Severity string
					Target                           struct {
						ID   string
						Line int
					}
				}
			}
		}
	}
	if err := json.Unmarshal(data, &reports); err != nil {
		t.Fatal(err)
	}
	client, err := llm.Build(config.ModelSpec{Provider: "synthetic", Model: "hf:zai-org/GLM-5.3-Flash"})
	if err != nil {
		t.Fatal(err)
	}
	recorder := &acknowledgementRecorder{LLM: client.LLM, t: t}
	client.LLM = recorder
	validator := &Validator{Client: client}
	for _, test := range []struct {
		mechanism, variant, title string
		iteration                 int
		files                     []string
	}{
		{"lifecycle", "bad", "Put panics on nil map before Configure", 2, []string{"cache.go", "service.go"}},
		{"contracts", "good", "No test pins version-1 readability contract", 3, []string{"store.go"}},
	} {
		t.Run(test.mechanism, func(t *testing.T) {
			var finding Finding
			matches := 0
			for _, run := range reports.Runs {
				if run.Mechanism != test.mechanism || run.Variant != test.variant || run.Iteration != test.iteration {
					continue
				}
				for _, check := range run.Checks {
					for _, f := range check.Findings {
						if f.Title == test.title && f.Target.ID == test.files[0] {
							finding = Finding{Path: f.Target.ID, Line: f.Target.Line, Class: strings.TrimPrefix(f.Rule, "model."), Title: f.Title, Rationale: f.Rationale, Severity: f.Severity}
							matches++
						}
					}
				}
			}
			if matches != 1 {
				t.Fatalf("matched %d retained claims, want 1", matches)
			}
			var source strings.Builder
			for _, name := range test.files {
				body, err := os.ReadFile(filepath.Join("../practices/testdata", test.mechanism, test.variant, name+".txt"))
				if err != nil {
					t.Fatal(err)
				}
				fmt.Fprintf(&source, "File: %s\n", name)
				for line, text := range strings.Split(string(body), "\n") {
					fmt.Fprintf(&source, "%6d %s\n", line+1, text)
				}
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
			defer cancel()
			before := recorder.calls.Load()
			started := time.Now()
			outcome := validator.check(ctx, finding, source.String())
			result, err := json.Marshal(map[string]any{"finding": finding, "refuted": outcome.refuted, "revised": outcome.revised, "reason": outcome.reason, "unresolved": outcome.unresolved, "failure": outcome.failure})
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("model=%s source_sha256=%x seconds=%.2f result=%s", client.String(), sha256.Sum256([]byte(source.String())), time.Since(started).Seconds(), result)
			if recorder.calls.Load() == before {
				t.Fatal("validation did not call the model")
			}
			if outcome.failure != "" {
				t.Fatalf("incomplete expert response: %s", outcome.failure)
			}
		})
	}
}
