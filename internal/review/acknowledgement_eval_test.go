//go:build eval

package review

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	llms "github.com/nocturnium/llm-go-sdk/v6"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/llm"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// TestLiveTriageSeparatesAcknowledgementsFromWeakClaims makes paid model calls
// only when explicitly enabled. Its mixed controls exercise semantic decisions
// that scripted accounting tests cannot establish.
func TestLiveTriageSeparatesAcknowledgementsFromWeakClaims(t *testing.T) {
	if os.Getenv("NITPICK_EVAL_ACKNOWLEDGEMENTS") != "1" {
		t.Skip("set NITPICK_EVAL_ACKNOWLEDGEMENTS=1 to run live triage controls")
	}
	cfg := config.Defaults()
	cfg.Review.Summary = false
	cfg.Review.Slop = true
	cfg.Review.TriageNoNewClaims = true
	client, err := llm.Build(config.ModelSpec{Provider: "synthetic", Model: "hf:Qwen/Qwen3.8-27B"})
	if err != nil {
		t.Fatal(err)
	}
	client.LLM = &acknowledgementRecorder{LLM: client.LLM, t: t}
	engine := &Engine{Config: cfg, Roles: &llm.Roles{Triage: client}}
	prompt, err := engine.triagePrompt()
	if err != nil {
		t.Fatal(err)
	}
	schema, err := triageSchema(offeredClasses(cfg.Review.Slop))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("model=%s prompt_sha256=%x schema_sha256=%x", client.String(), sha256.Sum256([]byte(prompt)), sha256.Sum256(schema))
	findings := []Finding{
		{Path: "ack.go", Line: 7, Severity: "nit", Class: "correctness", Title: "No defect found in records package", Rationale: "Register returns the encoder error from save, so the documented persistence contract is met. The shown code has no branching, state, or second caller that would create a hidden rule. No test is required for the shown non-branching path."},
		{Path: "success.go", Line: 4, Severity: "nit", Class: "correctness", Title: "Shipping boundary matches contract", Rationale: "The boundary returns zero at 5000 cents as documented. All shown paths satisfy the contract."},
		{Path: "slop.go", Line: 1, Severity: "nit", Class: "slop", Title: "No slop findings in service.go", Rationale: "This is a negative check result, not a defect. It names no consequence and has no action."},
		{Path: "weak.go", Line: 3, Severity: "nit", Class: "correctness", Title: "An unseen caller might pass nil", Rationale: "An external caller might pass nil and panic. No such caller is shown, so the consequence depends on unavailable context."},
		{Path: "mixed.go", Line: 9, Severity: "nit", Class: "correctness", Title: "No defect found on the happy path; failure handling may lose data", Rationale: "The successful path is correct. If a caller passes a failing writer, its error is discarded and the record may be lost."},
		{Path: "coverage.go", Line: 5, Severity: "nit", Class: "tests", Title: "Equality boundary has no assertion", Rationale: "The shown tests check rejection after expiry but omit equality; changing the comparison to <= would pass them."},
		{Path: "analyzer.go", Line: 8, Severity: "nit", Class: "correctness", Title: "Analyzer reports no issue", Rationale: "No issue was found.", FromAnalyzer: true, Source: "golangci-lint(gosec)"},
		{Path: "warning.go", Line: 2, Severity: "warning", Class: "correctness", Title: "No issue found", Rationale: "No issue was found."},
		{Path: "patch.go", Line: 6, Severity: "nit", Class: "correctness", Title: "No issue found", Rationale: "No issue was found.", Suggestion: "return err"},
	}
	for iteration := range 3 {
		t.Run(fmt.Sprintf("order_%d", iteration+1), func(t *testing.T) {
			input := slices.Clone(findings)
			if iteration == 1 {
				slices.Reverse(input)
			}
			if iteration == 2 {
				input = append(input[3:], input[:3]...)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
			defer cancel()
			started := time.Now()
			_, kept, decisions, err := engine.triage(ctx, &vcs.PullRequest{}, input)
			record, marshalErr := json.Marshal(struct {
				Kept      []Finding   `json:"kept"`
				Decisions []Overruled `json:"decisions"`
			}{kept, decisions})
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			t.Logf("seconds=%.2f result=%s", time.Since(started).Seconds(), record)
			if err != nil {
				t.Fatal(err)
			}
			if len(kept) != 6 || len(decisions) != 3 {
				t.Errorf("kept=%d decisions=%d, want 6 substantive/protected entries and 3 acknowledgements", len(kept), len(decisions))
			}
			for _, expected := range findings[3:] {
				if !slices.ContainsFunc(kept, func(f Finding) bool { return f.Path == expected.Path && f.Rationale == expected.Rationale }) {
					t.Errorf("lost substantive or protected claim: %s", expected.Path)
				}
			}
			for _, expected := range findings[:3] {
				if !slices.ContainsFunc(decisions, func(d Overruled) bool {
					return d.Finding.Path == expected.Path && d.Finding.Rationale == expected.Rationale
				}) {
					t.Errorf("acknowledgement was not recorded as a decision: %s", expected.Path)
				}
			}
		})
	}
}

// acknowledgementRecorder retains the model decision before accounting rejects
// malformed or conflicting entries, so a live failure can be diagnosed offline.
type acknowledgementRecorder struct {
	llms.LLM
	t *testing.T
}

func (r *acknowledgementRecorder) GenerateContent(ctx context.Context, msgs []llms.Message, opts ...llms.CallOption) (*llms.Response, error) {
	var call llms.CallOptions
	for _, option := range opts {
		option(&call)
	}
	if call.ResponseFormat != nil && call.ResponseFormat.JSONSchema != nil {
		r.t.Logf("response_schema=%s", call.ResponseFormat.JSONSchema.Schema)
	}
	response, err := r.LLM.GenerateContent(ctx, msgs, opts...)
	if response != nil {
		r.t.Logf("raw_response=%q", response.Content)
	}
	return response, err
}
