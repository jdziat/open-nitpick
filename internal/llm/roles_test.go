package llm

import (
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
)

// A config that names no validate model still gets a validation client, built
// from `default`, and it is a separate client carrying the review model's id.
//
// This is the precondition behind issue 45: the eval harness metered composite
// runs into a map keyed by model id, so this client's meter replaced the
// reviewer's and the row's cost lost every review call. Anything that meters
// or accounts per client has to treat the two as distinct; anything that
// prices them has to treat them as one model.
func TestBuildRolesGivesValidateItsOwnClientOnTheReviewModel(t *testing.T) {
	clearModelEnv(t)
	t.Setenv(envOpenRouterAPIKey, "or_test")

	cfg := &config.Config{Models: config.Models{
		Default: config.ModelSpec{Provider: ProviderOpenRouter, Model: "qwen/qwen3.8-27b"},
		Triage:  &config.ModelSpec{Provider: ProviderOpenRouter, Model: "z-ai/glm-5.3-flash"},
	}}

	roles, err := BuildRoles(cfg)
	if err != nil {
		t.Fatalf("BuildRoles: %v", err)
	}

	if roles.Validate == nil {
		t.Fatal("Validate is nil; the fallback to default is what makes the collision possible")
	}
	if roles.Validate == roles.Review {
		t.Fatal("Validate and Review are one client; if this ever holds, keying meters " +
			"by model id would be safe, and the comment in evals.sumMeters is stale")
	}
	if roles.Validate.Spec.Model != roles.Review.Spec.Model {
		t.Fatalf("Validate model = %q, Review model = %q, want both %q",
			roles.Validate.Spec.Model, roles.Review.Spec.Model, cfg.Models.Default.Model)
	}

	// Each dedupes on the pointer, so both are visited, which is the identity
	// a per-client meter map must use.
	var seen []*Client
	roles.Each(func(c *Client) { seen = append(seen, c) })

	var sawReview, sawValidate bool
	for _, c := range seen {
		sawReview = sawReview || c == roles.Review
		sawValidate = sawValidate || c == roles.Validate
	}
	if !sawReview || !sawValidate {
		t.Errorf("Each visited review=%v validate=%v, want both", sawReview, sawValidate)
	}
}
