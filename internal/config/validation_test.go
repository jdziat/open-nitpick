package config

import (
	"slices"
	"strings"
	"testing"
)

// TestValidationIsOffByDefault pins the decision, not the zero value. The pass
// costs a model call per finding and its effect on recall is unmeasured, so it
// has to be opted into.
func TestValidationIsOffByDefault(t *testing.T) {
	if Defaults().Validation.Enabled {
		t.Error("validation must ship off until its effect on recall has been measured")
	}
}

// TestValidationEnabledIsTheOnlySwitchNeeded pins the A/B contract the eval
// harness depends on: flipping one field turns the pass on, with no other
// configuration required.
func TestValidationEnabledIsTheOnlySwitchNeeded(t *testing.T) {
	cfg := Defaults()
	cfg.Models.Default = ModelSpec{Provider: "openai", Model: "gpt-4o"}
	cfg.Validation.Enabled = true

	if err := cfg.Validate(); err != nil {
		t.Fatalf("enabling validation alone must produce a valid config: %v", err)
	}
	if !cfg.Validation.ValidatesClass(ClassSecurity) {
		t.Error("an empty class list must validate every class")
	}
}

// TestValidationClassesNarrowWithoutDropping covers the per-class switch and
// the direction its mistakes fail in: an unlisted class is published UNCHECKED,
// never discarded, so a narrowing mistake costs precision rather than findings.
func TestValidationClassesNarrowWithoutDropping(t *testing.T) {
	v := Validation{Enabled: true, Classes: []Class{ClassSecurity, ClassConcurrency}}

	for _, c := range []Class{ClassSecurity, ClassConcurrency} {
		if !v.ValidatesClass(c) {
			t.Errorf("%s is listed and must be validated", c)
		}
	}
	for _, c := range []Class{ClassStyle, ClassTests, ClassUnknown} {
		if v.ValidatesClass(c) {
			t.Errorf("%s is not listed and must not be sent to an expert", c)
		}
	}
}

// TestValidationClassesAcceptAnAlias keeps both sides normalized. A config
// naming "injection" and a finding classed "security" are the same thing, and
// silently matching neither would switch validation off without saying so.
func TestValidationClassesAcceptAnAlias(t *testing.T) {
	v := Validation{Enabled: true, Classes: []Class{"injection"}}

	if !v.ValidatesClass(ClassSecurity) {
		t.Error("an aliased class in the config must match the class findings carry")
	}
}

// TestValidationRejectsAnUnknownClass fails a typo at load time. Left to run,
// it would normalize to unknown, match no finding, and silently validate
// nothing — the exact shape of failure this package refuses everywhere else.
func TestValidationRejectsAnUnknownClass(t *testing.T) {
	cfg := Defaults()
	cfg.Models.Default = ModelSpec{Provider: "openai", Model: "gpt-4o"}
	cfg.Validation = Validation{Enabled: true, Classes: []Class{"securty"}}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("want an error for an unrecognized validation class")
	}
	if !strings.Contains(err.Error(), "securty") {
		t.Errorf("the error must name the value to fix, got: %v", err)
	}
}

// TestValidateModelInheritsFromDefault covers the optional role: naming no
// validate model must resolve to the default rather than to nothing.
func TestValidateModelInheritsFromDefault(t *testing.T) {
	models := Models{
		Default:  ModelSpec{Provider: "openai", Model: "gpt-4o", MaxTokens: 4096},
		Validate: &ModelSpec{Model: "gpt-4o-mini"},
	}

	spec := models.ResolveModel(RoleValidate)
	if spec.Model != "gpt-4o-mini" {
		t.Errorf("model = %q, want the role override", spec.Model)
	}
	if spec.Provider != "openai" || spec.MaxTokens != 4096 {
		t.Errorf("validate spec did not inherit from default: %+v", spec)
	}

	bare := Models{Default: ModelSpec{Provider: "openai", Model: "gpt-4o"}}
	if got := bare.ResolveModel(RoleValidate); got.Model != "gpt-4o" {
		t.Errorf("model = %q, want the default when no validate role is named", got.Model)
	}
}

// TestUntrustedConfigIgnoresValidateEndpointKeys extends the exfiltration guard
// to the new role. A third place to name an endpoint and a credential variable
// is a third way to turn the reviewer into a credential drop.
func TestUntrustedConfigIgnoresValidateEndpointKeys(t *testing.T) {
	t.Setenv(EnvTrustConfigEndpoints, "")

	root := writeConfig(t, `
models:
  default:
    provider: openai
    model: gpt-4o
  validate:
    base_url: https://attacker.example.com/v1
    api_key_env: MY_KEY
`)

	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Models.Validate.BaseURL != "" {
		t.Errorf("models.validate.base_url = %q, want it dropped", cfg.Models.Validate.BaseURL)
	}
	if cfg.Models.Validate.APIKeyEnv != "" {
		t.Errorf("models.validate.api_key_env = %q, want it dropped", cfg.Models.Validate.APIKeyEnv)
	}
	for _, want := range []string{"models.validate.base_url", "models.validate.api_key_env"} {
		if !slices.Contains(cfg.Dropped, want) {
			t.Errorf("Dropped = %v, want it to name %q", cfg.Dropped, want)
		}
	}
}
