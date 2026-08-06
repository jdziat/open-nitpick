package config

import (
	"strings"
	"testing"
)

// TestLinterMaxSeverityDefaultsToNoReduction is the half of the knob that is
// easiest to get wrong in the same direction as the bug it replaces.
//
// internal/linters used to fold a recognized analyzer "CRITICAL" onto error, so
// review.fail_on: critical gated on nothing an analyzer could produce. Moving
// that decision into configuration only helps if the DEFAULT stops making it:
// a ceiling below critical here would be the identical policy for every
// repository, spelled as a key instead of a switch case.
func TestLinterMaxSeverityDefaultsToNoReduction(t *testing.T) {
	l := Defaults().Linters

	for _, s := range []Severity{SeverityNit, SeverityInfo, SeverityWarning, SeverityError, SeverityCritical} {
		if got := l.CapSeverity(s); got != s {
			t.Errorf("the default ceiling reduced %q to %q; an analyzer that named a level we "+
				"have must reach it unless an operator said otherwise", s, got)
		}
	}
}

func TestLinterCapSeverityReducesOnlyAbove(t *testing.T) {
	l := Linters{MaxSeverity: SeverityWarning}

	cases := map[Severity]Severity{
		SeverityCritical: SeverityWarning,
		SeverityError:    SeverityWarning,
		SeverityWarning:  SeverityWarning,
		// A ceiling is not a floor. Capping at warning must not promote a nit.
		SeverityInfo: SeverityInfo,
		SeverityNit:  SeverityNit,
	}

	for in, want := range cases {
		if got := l.CapSeverity(in); got != want {
			t.Errorf("Linters{MaxSeverity: warning}.CapSeverity(%q) = %q, want %q", in, got, want)
		}
	}

	// Case is the operator's business, not the caller's.
	padded := Linters{MaxSeverity: "  Warning "}
	if got := padded.CapSeverity(SeverityCritical); got != SeverityWarning {
		t.Errorf("CapSeverity with a padded, capitalized ceiling = %q, want warning", got)
	}
}

// TestAnUnusableLinterCeilingCapsNothing pins the direction the fallback leans.
//
// Severity("").Rank() is the unknown floor, which is info's — so a ceiling
// comparison written without this guard would silently reduce EVERY analyzer
// finding to info for any Config assembled in code rather than loaded from
// disk. Silent severity loss is the failure this whole change exists to end, so
// an unusable ceiling caps nothing and Validate refuses to let one reach a run.
func TestAnUnusableLinterCeilingCapsNothing(t *testing.T) {
	for _, ceiling := range []Severity{"", "spicy", SeverityNone} {
		l := Linters{MaxSeverity: ceiling}
		if got := l.CapSeverity(SeverityCritical); got != SeverityCritical {
			t.Errorf("a ceiling of %q reduced critical to %q instead of capping nothing", ceiling, got)
		}
	}
}

func TestValidateRejectsAnUnusableLinterCeiling(t *testing.T) {
	cases := map[string]struct {
		ceiling Severity
		want    string
	}{
		"unknown word": {ceiling: "spicy", want: "is not a severity"},
		// none ranks above critical so that fail_on: none never fires. As a
		// ceiling it would read as "cap at more than critical", which is not
		// what anyone writing it means.
		"the gate sentinel": {ceiling: SeverityNone, want: "linters.mode: off"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := Defaults()
			cfg.Models.Default = ModelSpec{Provider: "openai", Model: "gpt-4o"}
			cfg.Linters.MaxSeverity = tc.ceiling

			err := cfg.Validate()
			if err == nil {
				t.Fatalf("linters.max_severity %q should be rejected before any token is spent", tc.ceiling)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error should say %q so the operator knows what to edit, got: %v", tc.want, err)
			}
		})
	}
}

func TestLinterCeilingLoadsFromYAML(t *testing.T) {
	cfg, err := loadBytes([]byte(`
models:
  default:
    provider: openai
    model: gpt-4o
linters:
  max_severity: WARNING
`), "test")
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if cfg.Linters.MaxSeverity != SeverityWarning {
		t.Errorf("max_severity = %q, want warning", cfg.Linters.MaxSeverity)
	}
	if cfg.Linters.Mode != LinterAuto {
		t.Errorf("mode = %q; naming one linters key must not clear the others", cfg.Linters.Mode)
	}
}
