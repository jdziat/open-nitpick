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
