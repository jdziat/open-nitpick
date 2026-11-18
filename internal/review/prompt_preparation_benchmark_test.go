package review

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/llm"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

func BenchmarkFrozenTaskPromptPreparation(b *testing.B) {
	manifest := os.Getenv("NITPICK_PERFORMANCE_WORKLOADS")
	if manifest == "" {
		b.Skip("set NITPICK_PERFORMANCE_WORKLOADS to frozen benchmarks.json")
	}
	data, err := os.ReadFile(manifest)
	if err != nil {
		b.Fatal(err)
	}
	var workloads []struct{ Name, Diff, Sources string }
	if err := json.Unmarshal(data, &workloads); err != nil || len(workloads) == 0 {
		b.Fatalf("frozen workloads: %v", err)
	}
	for _, workload := range workloads {
		b.Run(workload.Name, func(b *testing.B) {
			raw, err := os.ReadFile(workload.Diff)
			if err != nil {
				b.Fatal(err)
			}
			files, err := diff.Parse(raw)
			if err != nil {
				b.Fatal(err)
			}
			raw, err = os.ReadFile(workload.Sources)
			if err != nil {
				b.Fatal(err)
			}
			var sources map[string][]byte
			if err := json.Unmarshal(raw, &sources); err != nil {
				b.Fatal(err)
			}
			cfg := config.Defaults()
			cfg.Models.Default = config.ModelSpec{Provider: "openai", Model: "scripted"}
			cfg.Review.Knowledge = false
			cfg.Review.Standards = false
			cfg.Review.MaxFilesPerRequest = 1
			plan, err := bundle.AssembleReserving(b.Context(), cfg, files, func(_ context.Context, name string) ([]byte, error) {
				data, ok := sources[name]
				if !ok {
					return nil, vcs.ErrNotFound
				}
				return data, nil
			}, nil, bundle.Reserve{})
			if err != nil || len(plan.Batches) == 0 {
				b.Fatalf("assemble prompt fixtures: %v", err)
			}
			for i := range plan.Batches {
				plan.Batches[i].DesignTask = fmt.Sprintf("render-%d", i)
			}
			model := &preparationModel{}
			client := llm.NewClientForTest(model, cfg.Models.Default)
			e := NewEngine(cfg, &stubProvider{}, NoPolicy("frozen prompt preparation"), nil, nil)
			e.Roles = &llm.Roles{Review: client, Triage: client}
			type sample struct {
				Duration          time.Duration
				PayloadSHA256     string
				Batches, Findings int
			}
			var samples []sample
			var expected string
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				start := time.Now()
				offset := len(model.requests)
				var findings []Finding
				for _, batch := range plan.Batches {
					result := Result{Findings: []Finding{{Path: batch.Entries[0].File.Path, Line: 1, Severity: "warning", Class: "correctness", Title: "Context probe", Rationale: "Probe retains the context delivered to validation."}}}
					response, _ := json.Marshal(result)
					model.byPrompt = map[string]string{"Frozen system prompt": string(response)}
					out, err := e.analyzeBatchWith(b.Context(), client, "Frozen system prompt", "Frozen PR title and body", batch, false)
					if err != nil {
						b.Fatal(err)
					}
					findings = append(findings, out...)
				}
				elapsed := time.Since(start)
				b.StopTimer()
				if len(findings) != len(plan.Batches) {
					b.Fatal("context probe did not return one finding per task-style batch")
				}
				for _, f := range findings {
					if f.TaskContext == nil || f.TaskContext.Text == "" {
						b.Fatal("task context missing")
					}
				}
				data, err := promptPreparationSnapshot(findings, model.requests[offset:])
				if err != nil {
					b.Fatal(err)
				}
				digest := fmt.Sprintf("%x", sha256.Sum256(data))
				if expected == "" {
					expected = digest
				}
				if digest != expected {
					b.Fatal("prompt or validation context changed")
				}
				samples = append(samples, sample{Duration: elapsed, PayloadSHA256: digest, Batches: len(plan.Batches), Findings: len(findings)})
				b.StartTimer()
			}
			b.StopTimer()
			if dir := os.Getenv("NITPICK_PERFORMANCE_SAMPLES"); dir != "" {
				if err := os.MkdirAll(dir, 0o700); err != nil {
					b.Fatal(err)
				}
				data, err := json.MarshalIndent(samples, "", "  ")
				if err != nil {
					b.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, workload.Name+"-task-prompts.json"), data, 0o600); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func promptPreparationSnapshot(findings []Finding, requests []string) ([]byte, error) {
	// Finding's model schema omits engine-owned evidence and attribution.
	owned := make([]map[string]any, len(findings))
	for i, f := range findings {
		owned[i] = map[string]any{
			"TaskContext":        f.TaskContext,
			"SummaryOnly":        f.SummaryOnly,
			"SeverityTranslated": f.SeverityTranslated,
			"RawSeverity":        f.RawSeverity,
			"FixValidated":       f.FixValidated,
			"Source":             f.Source,
			"Triager":            f.Triager,
			"Evidence":           f.Evidence,
			"Unresolved":         f.Unresolved,
			"UnresolvedBy":       f.UnresolvedBy,
			"FromAnalyzer":       f.FromAnalyzer,
		}
	}
	return json.Marshal([]any{findings, owned, requests})
}

func TestPromptPreparationProofDetectsChangedValidationContext(t *testing.T) {
	f := Finding{Path: "one.txt", TaskContext: &TaskContext{ID: "task", Text: "original", Lines: map[string]int{"one.txt": 1}}}
	before, err := promptPreparationSnapshot([]Finding{f}, []string{"request"})
	if err != nil {
		t.Fatal(err)
	}
	f.TaskContext.Text = "changed"
	after, err := promptPreparationSnapshot([]Finding{f}, []string{"request"})
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(before) == sha256.Sum256(after) {
		t.Fatal("changed validation context escaped the replay proof")
	}
}
