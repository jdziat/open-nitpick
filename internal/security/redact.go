package security

import (
	"github.com/nitpick-oss/nitpick/internal/redact"
	"github.com/nitpick-oss/nitpick/internal/review"
)

// RedactSecrets replaces credential-shaped and high-entropy spans with
// REDACTED. The scrubber is internal/redact, shared with the server.
func RedactSecrets(text string) string { return redact.Secrets(text) }

// RedactFinding redacts every free-text field a finding serializes: Title,
// Category, Rationale, Suggestion, Unresolved and Citation. Citation quotes
// shown code verbatim, so it is where a secret on the cited line would surface.
func RedactFinding(f *review.Finding) {
	if f == nil {
		return
	}
	f.Title = RedactSecrets(f.Title)
	f.Category = RedactSecrets(f.Category)
	f.Citation = RedactSecrets(f.Citation)
	f.Rationale = RedactSecrets(f.Rationale)
	f.Suggestion = RedactSecrets(f.Suggestion)
	f.Unresolved = RedactSecrets(f.Unresolved)
}
