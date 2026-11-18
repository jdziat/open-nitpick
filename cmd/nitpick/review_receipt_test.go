package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/llm"
	"github.com/jdziat/open-nitpick/internal/review"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

type receiptFailureProvider struct{ vcs.Provider }

func (p receiptFailureProvider) PullRequest(context.Context, vcs.Ref) (*vcs.PullRequest, error) {
	return nil, errors.New("fixture fatal after billed attempts")
}

func TestFatalReviewStillWritesItsAttemptLedgerAsJSON(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "")
	t.Setenv("GITHUB_REF", "")
	t.Setenv("PR_NUMBER", "")
	t.Setenv("OPENAI_API_KEY", "fixture")
	t.Setenv("NITPICK_TRUST_CONFIG_ENDPOINTS", "1")
	root := initRepo(t)
	actionsFile := filepath.Join(t.TempDir(), "outputs")
	t.Setenv("GITHUB_OUTPUT", actionsFile)
	write(t, root, ".nitpick.yaml", "models:\n  default: {provider: openai, model: gpt-4o, api_key_env: OPENAI_API_KEY}\n")
	var runErr error
	out := captureStdout(t, func() {
		runErr = reviewWithScope(t.Context(), "review", []string{"-repo", root, "-format", "json"}, nil, func(e *review.Engine) {
			e.Provider = receiptFailureProvider{Provider: e.Provider}
			e.ModelCalls = func() []llm.CallRecord {
				return []llm.CallRecord{{Role: "review", Model: "billed-attempt", Failed: true}}
			}
		})
	})
	if runErr == nil || !strings.Contains(runErr.Error(), "fixture fatal") {
		t.Fatalf("wrong fatal error: %v", runErr)
	}
	var receipt map[string]any
	if err := json.Unmarshal([]byte(out), &receipt); err != nil {
		t.Fatalf("fatal run lost its JSON: %v %q", err, out)
	}
	if receipt["result"] != "error" || receipt["complete"] != false || !strings.Contains(out, "billed-attempt") {
		t.Fatalf("fatal receipt lost attempted calls: %s", out)
	}
	outputs, err := os.ReadFile(actionsFile)
	if err != nil || !strings.Contains(string(outputs), "complete=false") {
		t.Fatalf("fatal action output reported complete: %s %v", outputs, err)
	}
}
