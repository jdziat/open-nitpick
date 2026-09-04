package prompt

import (
	"strings"
	"testing"
)

func TestFamilyOfReadsTheModelNameNotTheVendor(t *testing.T) {
	cases := map[string]Family{
		"z-ai/glm-5.3-flash":            FamilyGLM,
		"hf:zai-org/GLM-5.3-Flash":      FamilyGLM,
		"glm-5.3":                       FamilyGLM,
		"qwen/qwen3.8-27b":              FamilyQwen,
		"hf:Qwen/Qwen3.8-27B":           FamilyQwen,
		"deepseek/deepseek-v4-pro-0813": FamilyDeepSeek,
		"anthropic/claude-sonnet-4.6":   FamilyNone,
		"openai/gpt-5.6-luna":           FamilyNone,
		"x-ai/grok-4.6":                 FamilyNone,
		"moonshotai/kimi-k3":            FamilyNone,
		"openrouter/auto":               FamilyNone,
		"":                              FamilyNone,
	}
	for model, want := range cases {
		if got := FamilyOf(model); got != want {
			t.Errorf("FamilyOf(%q) = %q, want %q", model, got, want)
		}
	}
}

func TestModelGuidanceIsALayerOnlyForFamiliesThatHaveOne(t *testing.T) {
	if ModelGuidance("anthropic/claude-sonnet-4.6") != "" {
		t.Fatal("a family without guidance gets no layer")
	}

	if ModelGuidance("z-ai/glm-5.3-flash") != "" || ModelGuidance("deepseek/deepseek-v4-pro-0813") != "" {
		t.Fatal("GLM and DeepSeek notes were measured out or never measured; see ModelGuidance")
	}

	p, err := Build(NameReview, Options{ModelText: ModelGuidance("qwen/qwen3.8-27b")})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, l := range p.Layers {
		names = append(names, l.Name)
	}
	if strings.Join(names, ",") != LayerBase+","+LayerModel {
		t.Errorf("layers = %v", names)
	}
	if !strings.Contains(p.Explain(), "----- layer: model -----") {
		t.Error("explain-config must show the model layer so it can be read without spending tokens")
	}

	// The guidance never restates the bar: it may say what not to file, never
	// what to look for.
	for _, model := range []string{"qwen/qwen3.8-27b", "hf:Qwen/Qwen3.8-27B"} {
		g := strings.ToLower(ModelGuidance(model))
		for _, banned := range []string{"look for", "check for", "report any", "watch for"} {
			if strings.Contains(g, banned) {
				t.Errorf("%s guidance says %q, which widens the search rather than correcting a habit", model, banned)
			}
		}
	}
}
