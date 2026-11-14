package prompt

import "fmt"

// Verify returns the system prompt for the repository verification pass.
//
// Read on its own rather than through Build for Critique's old reason: Build
// layers the reviewer's voice over a base prompt, and a checker told how to
// word a finding starts writing findings.
func Verify() (string, error) {
	raw, err := templates.ReadFile("templates/verify.md")
	if err != nil {
		return "", fmt.Errorf("prompt %q: %w", "verify", err)
	}
	return string(raw), nil
}
