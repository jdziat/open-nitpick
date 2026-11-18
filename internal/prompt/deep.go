package prompt

import "fmt"

// DeepSecurity returns the focus section the deep-review security agent adds
// to the review prompt. It extends the review prompt rather than replacing it,
// so the agent files findings in the same shape as the general agent.
func DeepSecurity() (string, error) { return raw("deep_security") }

// DeepMiraGeneral returns the base prompt of the general deep-review agent
// under review.deep.prompts: mira. It replaces the review prompt's base text and
// keeps the layers after it, so the voice, persona and run instructions still
// apply. Adapted from Mira's general prompt (Apache License 2.0, see NOTICE).
func DeepMiraGeneral() (string, error) { return raw("deep_mira_general") }

// DeepMiraSecurity returns the base prompt of the security agent under
// review.deep.prompts: mira. Unlike DeepSecurity it is not an extension of the
// general prompt: Mira's security reviewer is a separate prompt. Adapted from
// Mira's security prompt (Apache License 2.0, see NOTICE).
func DeepMiraSecurity() (string, error) { return raw("deep_mira_security") }

// DeepCritique returns the system prompt of the deep-review critique, which
// grades findings and does not write them.
func DeepCritique() (string, error) { return raw("deep_critique") }

// raw reads a template that is used as written, without Build's layers.
func raw(name string) (string, error) {
	data, err := templates.ReadFile("templates/" + name + ".md")
	if err != nil {
		return "", fmt.Errorf("prompt %q: %w", name, err)
	}
	return string(data), nil
}
