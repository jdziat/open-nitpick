package config

import (
	"strings"
	"testing"
)

func TestPersonaLoadsFromYAML(t *testing.T) {
	root := writeConfig(t, `
models:
  default: {provider: openai, model: gpt-4o}
persona:
  nitpick: pedantic
  politeness: warm
  emoji: false
`)

	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Persona.Nitpick != NitpickPedantic || cfg.Persona.Politeness != PolitenessWarm {
		t.Errorf("persona = %+v", cfg.Persona)
	}
	if cfg.Persona.EmojiEnabled() {
		t.Error("emoji: false should disable emoji")
	}
	// Unset axes must inherit rather than blank out.
	if cfg.Persona.Verbosity != VerbosityNormal {
		t.Errorf("verbosity = %q, want the default to be inherited", cfg.Persona.Verbosity)
	}
}

func TestPersonaRejectsUnknownValues(t *testing.T) {
	for _, body := range []string{"nitpick: aggressive", "politeness: sarcastic", "verbosity: epic"} {
		t.Run(body, func(t *testing.T) {
			root := writeConfig(t, "models:\n  default: {provider: openai, model: gpt-4o}\npersona:\n  "+body+"\n")

			_, err := Load(root)
			if err == nil {
				t.Fatalf("want an error for %q", body)
			}
			if !strings.Contains(err.Error(), "persona.") {
				t.Errorf("error should name the persona field, got: %v", err)
			}
		})
	}
}

func TestBooleanPersonaFieldsCanBeTurnedOff(t *testing.T) {
	// Pointer-typed booleans exist so that `false` is distinguishable from
	// unset; a plain bool would make emoji:false indistinguishable from
	// omitting the key, and the setting would silently do nothing.
	root := writeConfig(t, `
models:
  default: {provider: openai, model: gpt-4o}
persona:
  emoji: false
  praise: true
`)

	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Persona.EmojiEnabled() {
		t.Error("emoji: false was ignored")
	}
	if !cfg.Persona.PraiseEnabled() {
		t.Error("praise: true was ignored")
	}
}
