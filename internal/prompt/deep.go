package prompt

import "fmt"

// DeepSecurity returns the focus section the deep-review security agent adds
// to the review prompt. It extends the review prompt rather than replacing it,
// so the agent files findings in the same shape as the general agent.
func DeepSecurity() (string, error) { return raw("deep_security") }

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
