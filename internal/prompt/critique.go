package prompt

import "fmt"

// Critique returns the system prompt for the evidence-grading pass.
//
// It is read on its own rather than through Build, because Build layers the
// reviewer's voice and persona over a base prompt, and a grader that is told how
// to word a finding starts writing findings.
func Critique() (string, error) {
	raw, err := templates.ReadFile("templates/critique.md")
	if err != nil {
		return "", fmt.Errorf("prompt %q: %w", "critique", err)
	}
	return string(raw), nil
}
