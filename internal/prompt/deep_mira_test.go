package prompt

import (
	"strings"
	"testing"
)

func miraBases(t *testing.T) map[string]string {
	t.Helper()
	general, err := DeepMiraGeneral()
	if err != nil {
		t.Fatal(err)
	}
	security, err := DeepMiraSecurity()
	if err != nil {
		t.Fatal(err)
	}
	return map[string]string{"general": general, "security": security}
}

// The prompts are rendered as repository-owned templates, so a stray "{{" in
// text adapted from another tool's Jinja would fail every deep review that asks
// for them.
func TestMiraPromptsRenderAsBasePromptsAndKeepTheLayersAfterThem(t *testing.T) {
	for name, base := range miraBases(t) {
		p, err := Build(NameReview, Options{Override: base})
		if err != nil {
			t.Fatalf("%s: Build: %v", name, err)
		}
		text := p.String()
		if !strings.HasPrefix(text, strings.TrimRight(base, "\n")) {
			t.Errorf("%s: the rendered prompt does not start with its own base text", name)
		}
		if !strings.Contains(text, Voice[:40]) {
			t.Errorf("%s: the voice layer is missing after the base, so findings would be written in no house style", name)
		}
	}
}

// Words from Mira's own output schema and context sections would tell the model
// to fill fields this tool does not have, or to call a tool it was not given.
func TestMiraPromptsCarryNoWordsFromTheSchemaTheyWereAdaptedFrom(t *testing.T) {
	forbidden := []string{"agent_prompt", "existing_code", "submit_review", "key_issues", "MIRA_CACHE_BOUNDARY",
		"blocker", "nitpick", "Mira", "{%", "{{", "Valid File Paths"}
	for name, base := range miraBases(t) {
		for _, word := range forbidden {
			if strings.Contains(base, word) {
				t.Errorf("%s prompt still contains %q", name, word)
			}
		}
	}
}

// What each prompt is for: the general one is the review, the security one is a
// separate reviewer and must not inherit the general agent's job.
func TestMiraGeneralAndSecurityPromptsKeepTheirOwnSections(t *testing.T) {
	b := miraBases(t)
	for _, want := range []string{"## Cross-Boundary Tracing", "## Refactor Regressions", "## Contract Changes", "## Do NOT Speculate"} {
		if !strings.Contains(b["general"], want) {
			t.Errorf("general prompt is missing %q", want)
		}
		if strings.Contains(b["security"], want) {
			t.Errorf("security prompt contains the general agent's %q", want)
		}
	}
	for _, want := range []string{"## What to look for", "## High-signal patterns to grep mentally"} {
		if !strings.Contains(b["security"], want) {
			t.Errorf("security prompt is missing %q", want)
		}
	}
}

// Both prompts must speak this tool's severity scale, or every finding they
// produce lands in the unknown bucket. The general prompt lists the scale as a
// guide; the security prompt assigns it in its rules.
func TestMiraPromptsUseThisToolsSeverityScale(t *testing.T) {
	b := miraBases(t)
	for _, level := range []string{"critical", "error", "warning", "info", "nit"} {
		if !strings.Contains(b["general"], "- **"+level+"**:") {
			t.Errorf("the general prompt's severity guide has no %q entry", level)
		}
	}
	for _, level := range []string{"`error`", "`critical`", "`warning`", "`info`"} {
		if !strings.Contains(b["security"], level) {
			t.Errorf("the security prompt never assigns %s", level)
		}
	}
}
