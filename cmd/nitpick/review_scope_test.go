package main

import (
	"context"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/review"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

func critiqueOf(cfg *config.Config) string {
	switch c := cfg.Review.Critique; {
	case c == nil:
		return "unset"
	case *c:
		return "on"
	}
	return "off"
}

// review grades its findings unless the operator chose, and an operator's own
// choice, including false, is never overwritten.
func TestReviewScopeCritiquesFindingsUnlessTheOperatorChose(t *testing.T) {
	yes, no := true, false
	cases := []struct {
		name string
		set  *bool
		want string
	}{
		{"unset takes the measured default", nil, "on"},
		{"true is kept", &yes, "on"},
		{"false stays false", &no, "off"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := config.Defaults()
			cfg.Review.Critique = c.set
			applyReviewScope(cfg)
			if got := critiqueOf(cfg); got != c.want {
				t.Errorf("review.critique = %s, want %s", got, c.want)
			}
		})
	}
}

// review does not vote unless the operator asks: voting tripled the cost of
// generation and was not separated from one run. An operator's own count is
// kept, and the critique is on either way.
func TestReviewScopeDoesNotVoteUnlessTheOperatorAsks(t *testing.T) {
	cases := []struct {
		name string
		set  int
		want int
	}{
		{"unset stays one run", 0, 0},
		{"an operator's count is kept", 3, 3},
		{"one stays one", 1, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := config.Defaults()
			cfg.Review.Votes = c.set
			applyReviewScope(cfg)
			if cfg.Review.Votes != c.want {
				t.Errorf("review.votes = %d, want %d", cfg.Review.Votes, c.want)
			}
			if got := critiqueOf(cfg); got != "on" {
				t.Errorf("review.critique = %s, want on", got)
			}
		})
	}
}

// The default is review's alone. deep-review has its own critique, and
// fast-review and improve were not measured with this one.
func TestTheOtherReviewCommandsDoNotInheritTheCritiqueDefault(t *testing.T) {
	scopes := map[string]func(*config.Config){
		"deep-review": applyDeepReviewScope,
		"fast-review": applyFastReviewScope,
		"improve":     applyImproveScope,
	}
	for name, scope := range scopes {
		cfg := config.Defaults()
		scope(cfg)
		if got := critiqueOf(cfg); got != "unset" {
			t.Errorf("%s set review.critique to %s", name, got)
		}
		if cfg.Review.Votes != 0 {
			t.Errorf("%s set review.votes to %d", name, cfg.Review.Votes)
		}
		if cfg.Review.WholePullRequest != nil {
			t.Errorf("%s set review.whole_pull_request", name)
		}
	}
}

// review reads a pull request whole unless the operator turned it off, and the
// limits it reads under are the measured ones.
func TestReviewScopeReadsWholeUnlessTheOperatorTurnedItOff(t *testing.T) {
	yes, no := true, false
	cases := []struct {
		name      string
		set       *bool
		wantFiles int
		wantToks  int
	}{
		{"unset reads whole", nil, wholeReviewFiles, wholeReviewTokens},
		{"true reads whole", &yes, wholeReviewFiles, wholeReviewTokens},
		{"false keeps the configured batches", &no, config.Defaults().Review.MaxFilesPerRequest, config.Defaults().Review.TokenBudgetPerRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := config.Defaults()
			cfg.Review.WholePullRequest = c.set
			applyReviewScope(cfg)
			if cfg.Review.MaxFilesPerRequest != c.wantFiles || cfg.Review.TokenBudgetPerRequest != c.wantToks {
				t.Errorf("limits = %d files, %d tokens; want %d and %d",
					cfg.Review.MaxFilesPerRequest, cfg.Review.TokenBudgetPerRequest, c.wantFiles, c.wantToks)
			}
		})
	}
}

// A policy resolved for one pull request is built from the repository's own
// configuration, so the review scope has to be applied to it too, without
// touching the policy it was resolved from.
func TestReviewScopeSurvivesAPolicyResolvedForOnePullRequest(t *testing.T) {
	base := config.Defaults()
	base.Models.Default = config.ModelSpec{Provider: "ollama", Model: "local"}
	scoped := reviewScoped{inner: fixedPolicy{cfg: base}}
	got, _, err := scoped.ResolvePolicy(context.Background(), vcs.Ref{}, nil, []string{config.FileName})
	if err != nil {
		t.Fatal(err)
	}
	if got.Review.MaxFilesPerRequest != 60 || critiqueOf(got) != "on" {
		t.Errorf("resolved policy lost the review scope: %d files, critique %s",
			got.Review.MaxFilesPerRequest, critiqueOf(got))
	}
	if base.Review.MaxFilesPerRequest != config.Defaults().Review.MaxFilesPerRequest || base.Review.Critique != nil {
		t.Error("scoping changed the policy it was resolved from")
	}
	if none, _, err := (reviewScoped{}).ResolvePolicy(context.Background(), vcs.Ref{}, nil, nil); none != nil || err != nil {
		t.Errorf("no inner resolver gave %v, %v; want nothing", none, err)
	}
}

// The measured limits are written out here, not read from the constants the
// code uses: a test that compares the constant with itself passes whatever it
// is changed to.
func TestReviewReadsWholeAtTheLimitsItWasMeasuredAt(t *testing.T) {
	cfg := config.Defaults()
	applyReviewScope(cfg)
	if cfg.Review.MaxFilesPerRequest != 60 || cfg.Review.TokenBudgetPerRequest != 120000 {
		t.Errorf("review reads under %d files and %d tokens, want the 60 and 120000 that were measured",
			cfg.Review.MaxFilesPerRequest, cfg.Review.TokenBudgetPerRequest)
	}
}

// The command, not only its scope function, keeps the scope on a resolved
// policy: configureReview is what runReview hands the engine.
func TestTheReviewCommandWrapsItsPolicyResolver(t *testing.T) {
	base := config.Defaults()
	base.Models.Default = config.ModelSpec{Provider: "ollama", Model: "local"}
	engine := &review.Engine{Policy: fixedPolicy{cfg: base}}
	configureReview(engine)
	got, _, err := engine.Policy.ResolvePolicy(context.Background(), vcs.Ref{}, nil, []string{config.FileName})
	if err != nil {
		t.Fatal(err)
	}
	if got.Review.MaxFilesPerRequest != 60 || critiqueOf(got) != "on" {
		t.Errorf("the engine's resolver returned %d files and critique %s, want the review scope", got.Review.MaxFilesPerRequest, critiqueOf(got))
	}
}

// runReview is what the command line calls. Its scope and engine setup are the
// review ones, which no test of those functions alone can show.
func TestRunReviewPassesTheReviewScopeAndEngineSetup(t *testing.T) {
	var name string
	var scope func(*config.Config)
	var configure func(*review.Engine)
	saved := scopedReview
	t.Cleanup(func() { scopedReview = saved })
	scopedReview = func(_ context.Context, n string, _ []string, s func(*config.Config), c func(*review.Engine)) error {
		name, scope, configure = n, s, c
		return nil
	}
	if err := runReview(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if name != "review" || scope == nil || configure == nil {
		t.Fatalf("runReview passed %q, scope nil=%v, configure nil=%v; want review with both", name, scope == nil, configure == nil)
	}
	cfg := config.Defaults()
	scope(cfg)
	if cfg.Review.MaxFilesPerRequest != 60 || critiqueOf(cfg) != "on" {
		t.Errorf("runReview's scope gave %d files, critique %s", cfg.Review.MaxFilesPerRequest, critiqueOf(cfg))
	}
	base := config.Defaults()
	base.Models.Default = config.ModelSpec{Provider: "ollama", Model: "local"}
	engine := &review.Engine{Policy: fixedPolicy{cfg: base}}
	configure(engine)
	got, _, err := engine.Policy.ResolvePolicy(context.Background(), vcs.Ref{}, nil, []string{config.FileName})
	if err != nil || got.Review.MaxFilesPerRequest != 60 {
		t.Errorf("runReview's engine setup left a resolved policy with %v files (err %v)", got, err)
	}
}
