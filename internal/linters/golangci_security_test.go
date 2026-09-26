package linters

import (
	"strings"
	"testing"
)

// TestGolangciLintForceGosecStateNamesGosec lets the security roster prove the
// overlay was requested: State must contain gosec:enabled when ForceGosec is
// set, and must not claim it on an ordinary review run.
func TestGolangciLintForceGosecStateNamesGosec(t *testing.T) {
	forced := (&golangciLint{ForceGosec: true}).State()
	if !strings.Contains(forced, "gosec:enabled") {
		t.Errorf("ForceGosec State = %q, want substring gosec:enabled", forced)
	}
	if !strings.Contains(forced, "security analyzer config") {
		t.Errorf("ForceGosec with no operator config State = %q, want security analyzer config", forced)
	}

	plain := (&golangciLint{}).State()
	if strings.Contains(plain, "gosec:enabled") {
		t.Errorf("ordinary State = %q, must not claim gosec without ForceGosec", plain)
	}
}

// TestGolangciSecurityDefaultsEmbedGosec guards the embed itself: ForceGosec
// without an operator config materializes this file, and a file that omits
// gosec would leave --enable=gosec as the only enablement path.
func TestGolangciSecurityDefaultsEmbedGosec(t *testing.T) {
	body := string(golangciSecurityDefaults)
	if !enableListNamesGosec(body) {
		t.Fatal("golangci-security.yml must list gosec under linters.enable")
	}
	if !strings.Contains(body, "generated: disable") {
		t.Fatal("golangci-security.yml must disable generated-file exclusions")
	}
}

// enableListNamesGosec reports whether the YAML enable list contains gosec as
// an item, ignoring comments so a prose mention of gosec cannot greenwash.
func enableListNamesGosec(body string) bool {
	inEnable := false
	for _, raw := range strings.Split(body, "\n") {
		line := raw
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "enable:":
			inEnable = true
		case inEnable && trimmed == "- gosec":
			return true
		case inEnable && strings.HasPrefix(trimmed, "- "):
			continue
		case inEnable && trimmed != "":
			inEnable = false
		}
	}
	return false
}
