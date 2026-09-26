package security

import (
	"slices"
	"strings"
	"testing"
)

func TestFrozenRequiredListsAlwaysAndWhenApplicable(t *testing.T) {
	got := FrozenRequired()
	for _, id := range RequiredAlways {
		if !slices.Contains(got, id) {
			t.Fatalf("FrozenRequired missing always-required %q", id)
		}
	}
	for _, id := range RequiredWhenApplicable {
		if !slices.Contains(got, id) {
			t.Fatalf("FrozenRequired missing when-applicable %q", id)
		}
	}
	if len(got) != len(RequiredAlways)+len(RequiredWhenApplicable) {
		t.Fatalf("FrozenRequired len=%d, want %d", len(got), len(RequiredAlways)+len(RequiredWhenApplicable))
	}
}

func TestValidateAnalyzersExtendsRejectsAllowlistOmittingGitleaks(t *testing.T) {
	// Mutation: accepting a shrink would greenwash theater (plan test 3).
	allowlist := []string{"osv-scanner", "zizmor", "checkov", "brakeman", "golangci-lint"}
	err := ValidateAnalyzersExtends(allowlist)
	if err == nil {
		t.Fatal("allowlist omitting gitleaks must error")
	}
	if !strings.Contains(err.Error(), "gitleaks") {
		t.Fatalf("error %v should name gitleaks", err)
	}
}

func TestValidateAnalyzersExtendsAllowsEmptyAndPureExtras(t *testing.T) {
	if err := ValidateAnalyzersExtends(nil); err != nil {
		t.Fatalf("empty extras: %v", err)
	}
	if err := ValidateAnalyzersExtends([]string{"semgrep"}); err != nil {
		t.Fatalf("pure additive extras: %v", err)
	}
	full := append(FrozenRequired(), "semgrep")
	if err := ValidateAnalyzersExtends(full); err != nil {
		t.Fatalf("full frozen plus extra: %v", err)
	}
}
