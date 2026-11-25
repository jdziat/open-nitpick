package review

import (
	"strings"
	"testing"

	"github.com/nitpick-oss/nitpick/internal/config"
	"github.com/nitpick-oss/nitpick/internal/prompt"
)

// agentPrompts builds both agents under a prompts setting and returns each one's
// text by agent name.
func agentPrompts(t *testing.T, prompts string) map[string]string {
	t.Helper()
	e := newEngine(t, &scriptedLLM{}, &stubProvider{}, func(c *config.Config) {
		c.Review.Deep.Prompts = prompts
	})
	system, err := e.reviewPromptFor(e.Roles.Review)
	if err != nil {
		t.Fatal(err)
	}
	agents, err := e.deepAgents(e.Roles.Review, system)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, a := range agents {
		out[a.name] = a.system
	}
	return out
}

// An empty setting, or "nitpick", is this tool's own prompt, and the security
// agent extends it. The default is Mira's, so this has to be asked for.
func TestDeepAgentsKeepThisToolsPromptsWhenAskedFor(t *testing.T) {
	for _, setting := range []string{"", config.DeepPromptsNitpick} {
		checkOwnPrompts(t, agentPrompts(t, setting))
	}
}

// Left unset, a deep review reviews under Mira's adapted prompts, because that
// is what scored level with Mira on the benchmark and this tool's own did not.
func TestDeepAgentsReviewUnderMirasPromptsByDefault(t *testing.T) {
	e := newEngine(t, &scriptedLLM{}, &stubProvider{}, nil)
	system, err := e.reviewPromptFor(e.Roles.Review)
	if err != nil {
		t.Fatal(err)
	}
	agents, err := e.deepAgents(e.Roles.Review, system)
	if err != nil {
		t.Fatal(err)
	}
	var general string
	for _, a := range agents {
		if a.name == config.DeepAgentGeneral {
			general = a.system
		}
	}
	if !strings.Contains(general, "## Cross-Boundary Tracing") {
		t.Error("the default general agent does not review under Mira's prompt")
	}
}

func checkOwnPrompts(t *testing.T, got map[string]string) {
	t.Helper()
	general, security := got[config.DeepAgentGeneral], got[config.DeepAgentSecurity]
	for name, text := range map[string]string{"general": general, "security": security} {
		if !strings.Contains(text, "## The bar") {
			t.Errorf("%s agent lost this tool's own prompt by default", name)
		}
		if strings.Contains(text, "Cross-Boundary Tracing") || strings.Contains(text, "What to look for") {
			t.Errorf("%s agent got Mira's text without asking for it", name)
		}
	}
	focus, _ := prompt.DeepSecurity()
	if !strings.Contains(security, strings.TrimSpace(focus)) {
		t.Error("the security agent no longer extends the review prompt with its focus section")
	}
}

// Each agent gets its own prompt under mira, and each keeps the layers after the
// base: the voice, and the tool note that tells the agent how to finish.
func TestMiraPromptsGiveEachAgentItsOwnBaseAndKeepTheLayersAndTheToolNote(t *testing.T) {
	got := agentPrompts(t, config.DeepPromptsMira)
	general, security := got[config.DeepAgentGeneral], got[config.DeepAgentSecurity]

	if !strings.Contains(general, "## Cross-Boundary Tracing") || strings.Contains(general, "## What to look for") {
		t.Error("the general agent does not review under Mira's general prompt")
	}
	if !strings.Contains(security, "## What to look for") || strings.Contains(security, "## Cross-Boundary Tracing") {
		t.Error("the security agent does not review under Mira's security prompt")
	}
	for name, text := range map[string]string{"general": general, "security": security} {
		if strings.Contains(text, "## The bar") {
			t.Errorf("%s agent still carries this tool's own base prompt under mira", name)
		}
		if !strings.Contains(text, prompt.Voice[:40]) {
			t.Errorf("%s agent lost the voice layer", name)
		}
		if !strings.Contains(text, "## Looking things up") || !strings.Contains(text, "submit_findings") {
			t.Errorf("%s agent lost the tool note that tells it how to file", name)
		}
	}
}
