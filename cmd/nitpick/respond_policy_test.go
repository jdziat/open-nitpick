package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// A comment on a change that edits .nitpick.yaml is answered under the version
// already accepted, not the one the change wrote.
//
// respond had no BasePolicy at all, so the config on disk decided who may spend
// the repository's credit, which model answered, who may write, and what the
// improve pass reviewed under. For an inline thread comment, the event
// `@open-nitpick fix` is typed on, actions/checkout resolves the pull request's
// merge ref, so the file on disk is the change's own.
func TestRespondAnswersUnderThePolicyTheChangeDidNotWrite(t *testing.T) {
	// The operator's environment, which is what a withheld configuration falls
	// back to. In CI the Action sets these from its own inputs.
	t.Setenv("LLM_PROVIDER", "openai")
	t.Setenv("LLM_MODEL", "the-operators-choice")
	t.Setenv("OPENAI_API_KEY", "test-key")

	repo := t.TempDir()
	hostile := "models:\n  default:\n    provider: openai\n    model: chosen-by-the-change\n" +
		"review:\n  respond:\n    from: [none]\n"
	if err := os.WriteFile(filepath.Join(repo, config.FileName), []byte(hostile), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := loadConfig(repo, "")
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if got := cfg.Models.ResolveModel(config.RoleReview).Model; got != "chosen-by-the-change" {
		t.Fatalf("the fixture does not say what this test is about: model = %q", got)
	}

	// A diff that touches the config file, which is what makes the change one
	// that supplies its own policy.
	edits := &fixedDiff{diff: "diff --git a/" + config.FileName + " b/" + config.FileName +
		"\n--- a/" + config.FileName + "\n+++ b/" + config.FileName +
		"\n@@ -1,1 +1,1 @@\n-old\n+new\n"}

	policy, raw, err := respondPolicy(context.Background(), edits, repo, cfg,
		vcs.Ref{Owner: "o", Repo: "r", Number: 1}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("respondPolicy: %v", err)
	}
	if len(raw) == 0 {
		t.Error("the diff was not returned, so the caller fetches it a second time")
	}

	// The checkout has no history, so no accepted version can be read and the
	// resolver falls back to built-in defaults. That is still a policy the
	// change did not write, which is the property.
	if got := policy.Models.ResolveModel(config.RoleReview).Model; got != "the-operators-choice" {
		t.Errorf("model = %q, want the operator's own: the change chose the model that answers it", got)
	}

	// And the gate it wrote for itself does not apply. `from: [none]` is the
	// forge's word for a stranger, so naming it is the config saying anyone
	// may spend this repository's credit.
	const stranger = "FIRST_TIME_CONTRIBUTOR"
	if !cfg.Review.Respond.Allows(stranger) {
		t.Fatal("the fixture does not say what this test is about: its own gate refuses a stranger")
	}
	if policy.Review.Respond.Allows(stranger) {
		t.Error("the change opened the gate on who may spend this repository's credit")
	}
}

// A change that leaves the config alone is answered under the config on disk,
// and costs one diff read and nothing else.
//
// The substitution exists for the change that edits the file. Applying it to
// every comment would answer ordinary questions under built-in defaults.
func TestRespondLeavesAnOrdinaryChangeAlone(t *testing.T) {
	repo := t.TempDir()
	body := "models:\n  default:\n    provider: openai\n    model: the-operators-choice\n"
	if err := os.WriteFile(filepath.Join(repo, config.FileName), []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := loadConfig(repo, "")
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}

	ordinary := &fixedDiff{diff: "diff --git a/app.go b/app.go\n--- a/app.go\n+++ b/app.go\n@@ -1,1 +1,1 @@\n-a\n+b\n"}

	policy, _, err := respondPolicy(context.Background(), ordinary, repo, cfg,
		vcs.Ref{Owner: "o", Repo: "r", Number: 1}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("respondPolicy: %v", err)
	}
	if got := policy.Models.ResolveModel(config.RoleReview).Model; got != "the-operators-choice" {
		t.Errorf("model = %q, want the operator's own: an ordinary change was answered under defaults", got)
	}
	if ordinary.calls != 1 {
		t.Errorf("provider calls = %d, want 1: only the diff", ordinary.calls)
	}
}

// A diff that cannot be read refuses the answer.
//
// Continuing would answer under the file on disk, which is the thing this
// exists to stop.
func TestRespondRefusesWhenItCannotReadTheChange(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, config.FileName),
		[]byte("models:\n  default: {provider: openai, model: m}\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := loadConfig(repo, "")
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}

	if _, _, err := respondPolicy(context.Background(), &fixedDiff{err: io.ErrUnexpectedEOF},
		repo, cfg, vcs.Ref{}, slog.New(slog.DiscardHandler)); err == nil {
		t.Fatal("answered without being able to read the change")
	} else if !strings.Contains(err.Error(), "resolve the policy") {
		t.Errorf("error = %v, want it to name what it could not do", err)
	}
}

// fixedDiff is a provider that answers one diff and counts what it was asked.
//
// It is not a BaseResolver, so no accepted version can be read and the
// resolver falls back to built-in defaults. That is still a policy the change
// did not write, which is the property under test; internal/config covers the
// base-revision half.
type fixedDiff struct {
	vcs.Provider
	diff  string
	err   error
	calls int
}

func (f *fixedDiff) Name() string { return "fixed" }

func (f *fixedDiff) Diff(context.Context, vcs.Ref) ([]byte, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return []byte(f.diff), nil
}
