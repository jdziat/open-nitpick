package config

import (
	"strings"
	"testing"
)

func TestRouteMatchAndValidation(t *testing.T) {
	m := RouteMatch{Languages: []string{"go", "Python"}, Kinds: []string{KindSecurity}, MinFiles: 2}
	if !m.Matches([]string{"python"}, []string{"security", "logic"}, 2) {
		t.Error("a batch meeting every set field matches")
	}
	if m.Matches([]string{"python"}, []string{"security"}, 1) {
		t.Error("min_files is enforced")
	}
	if m.Matches([]string{"rust"}, []string{"security"}, 2) {
		t.Error("languages is enforced")
	}
	if m.Matches([]string{"go"}, nil, 2) {
		t.Error("kinds is enforced when set")
	}
	if !(RouteMatch{}).Matches(nil, nil, 1) {
		t.Error("an empty match holds for every batch")
	}

	cfg := Defaults()
	cfg.Models.Default = ModelSpec{Provider: "openrouter", Model: "a"}
	cfg.Models.Routes = []Route{{Match: RouteMatch{Kinds: []string{"bogus"}}, Review: &ModelSpec{Model: "b"}}}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "is not a kind") || !strings.Contains(err.Error(), "needs models.router") {
		t.Errorf("unknown kind and missing router must both be reported, got %v", err)
	}

	cfg.Models.Router = &ModelSpec{Model: "r"}
	cfg.Models.Routes = []Route{{Match: RouteMatch{Kinds: []string{KindSecurity}}, Review: &ModelSpec{Model: "b", Providers: []string{"x"}}}}
	if err := cfg.Validate(); err != nil {
		t.Errorf("a valid route: %v", err)
	}
	if got := cfg.Models.ResolveRoute(cfg.Models.Routes[0]); got.Provider != "openrouter" || got.Model != "b" || len(got.Providers) != 1 {
		t.Errorf("route spec overlays default: %+v", got)
	}
	if !cfg.Models.NeedsRouter() {
		t.Error("a route naming kinds needs the router")
	}
}

func TestAPinFollowsItsModel(t *testing.T) {
	base := ModelSpec{Provider: "openrouter", Model: "google/gemma-4-31b-it", Providers: []string{"deepinfra/turbo"}}
	if got := base.overlay(ModelSpec{Model: "qwen/qwen3.8-27b"}); got.Providers != nil {
		t.Errorf("a different model does not inherit the pin: %v", got.Providers)
	}
	if got := base.overlay(ModelSpec{Temperature: floatPtr(0)}); len(got.Providers) != 1 {
		t.Errorf("the same model keeps the pin: %v", got.Providers)
	}
	if got := base.overlay(ModelSpec{Providers: []string{}}); got.Providers != nil && len(got.Providers) != 0 {
		t.Errorf("an explicit empty list clears the pin: %v", got.Providers)
	}
	if got := base.overlay(ModelSpec{Model: "x", Providers: []string{"together"}}); len(got.Providers) != 1 || got.Providers[0] != "together" {
		t.Errorf("an override's own pin wins: %v", got.Providers)
	}
}

func floatPtr(f float64) *float64 { return &f }
