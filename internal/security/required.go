// Package security holds the frozen required scanner set, roster completeness,
// and secret redaction for the nitpick security command.
package security

import (
	"fmt"
	"slices"
)

// RequiredAlways are scanners the security command must always attempt.
var RequiredAlways = []string{"osv-scanner", "gitleaks"}

// RequiredWhenApplicable are scanners required only when catalog matchers say
// they apply to the scanned tree.
var RequiredWhenApplicable = []string{"zizmor", "checkov", "brakeman", "golangci-lint"}

// FrozenRequired returns RequiredAlways followed by RequiredWhenApplicable.
// security.analyzers may extend this set but must not shrink it.
func FrozenRequired() []string {
	out := make([]string, 0, len(RequiredAlways)+len(RequiredWhenApplicable))
	out = append(out, RequiredAlways...)
	out = append(out, RequiredWhenApplicable...)
	return out
}

// ValidateAnalyzersExtends checks security.analyzers. An empty list means the
// frozen set alone. A non-empty list may only add ids; if it names any frozen
// required id (allowlist shape) it must name every frozen required id, so an
// allowlist cannot drop gitleaks or osv-scanner.
func ValidateAnalyzersExtends(extras []string) error {
	if len(extras) == 0 {
		return nil
	}
	frozen := FrozenRequired()
	named := 0
	var missing []string
	for _, id := range frozen {
		if slices.Contains(extras, id) {
			named++
			continue
		}
		missing = append(missing, id)
	}
	if named == 0 {
		return nil
	}
	if named < len(frozen) {
		return fmt.Errorf("security.analyzers cannot remove frozen ids; missing %v", missing)
	}
	return nil
}
