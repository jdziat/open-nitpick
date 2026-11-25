package main

import (
	"context"
	"testing"

	"github.com/nitpick-oss/nitpick/internal/config"
	"github.com/nitpick-oss/nitpick/internal/vcs"
)

// Deep review reads each pull request whole, at review.deep's limits, on the
// configuration it was given and on a policy resolved for one pull request,
// without changing either.
func TestDeepReviewScopeSendsEachPullRequestWhole(t *testing.T) {
	cfg := config.Defaults()
	cfg.Review.Deep.MaxFilesPerRequest = 40
	cfg.Review.Deep.TokenBudgetPerRequest = 90000
	applyDeepReviewScope(cfg)
	if cfg.Review.MaxFilesPerRequest != 40 || cfg.Review.TokenBudgetPerRequest != 90000 {
		t.Errorf("scoped batch limits = %d files, %d tokens; want review.deep's 40 and 90000",
			cfg.Review.MaxFilesPerRequest, cfg.Review.TokenBudgetPerRequest)
	}

	base := config.Defaults()
	base.Models.Default = config.ModelSpec{Provider: "ollama", Model: "local"}
	scoped := deepReviewScoped{inner: fixedPolicy{cfg: base}}
	got, _, err := scoped.ResolvePolicy(context.Background(), vcs.Ref{}, nil, []string{config.FileName})
	if err != nil {
		t.Fatal(err)
	}
	if got.Review.MaxFilesPerRequest != base.Review.Deep.MaxFilesPerRequest || got.Review.TokenBudgetPerRequest != base.Review.Deep.TokenBudgetPerRequest {
		t.Errorf("resolved policy batch limits = %d, %d; want review.deep's", got.Review.MaxFilesPerRequest, got.Review.TokenBudgetPerRequest)
	}
	if base.Review.MaxFilesPerRequest != config.Defaults().Review.MaxFilesPerRequest {
		t.Error("scoping mutated the resolved policy")
	}
}

// Under the prose style the walkthrough comes from triage, which deep review
// does not run, so a deep review left at prose would open with no walkthrough.
// It prints the receipt instead, on the given configuration and on a policy
// resolved for one pull request.
func TestDeepReviewPrintsTheReceiptUnderTheProseStyle(t *testing.T) {
	cfg := config.Defaults()
	cfg.Review.SummaryStyle = config.SummaryProse
	applyDeepReviewScope(cfg)
	if got := cfg.Review.EffectiveSummaryStyle(); got != config.SummaryReceipt {
		t.Errorf("scoped summary style = %q; want %q", got, config.SummaryReceipt)
	}

	base := config.Defaults()
	base.Models.Default = config.ModelSpec{Provider: "ollama", Model: "local"}
	base.Review.SummaryStyle = config.SummaryProse
	scoped := deepReviewScoped{inner: fixedPolicy{cfg: base}}
	got, _, err := scoped.ResolvePolicy(context.Background(), vcs.Ref{}, nil, []string{config.FileName})
	if err != nil {
		t.Fatal(err)
	}
	if style := got.Review.EffectiveSummaryStyle(); style != config.SummaryReceipt {
		t.Errorf("resolved policy summary style = %q; want %q", style, config.SummaryReceipt)
	}
	if base.Review.SummaryStyle != config.SummaryProse {
		t.Error("scoping changed the resolved policy's own summary style")
	}
}
