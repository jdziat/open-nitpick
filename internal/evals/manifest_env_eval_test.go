//go:build eval

package evals

import (
	"os"
	"strings"
)

// envOn reports whether an eval switch is set to anything but off.
func envOn(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "", "0", "false", "off":
		return false
	}
	return true
}

// manifestTypeFromEnv names the review type a live run is measuring, from the
// same switches the harness reads, so the registration cannot disagree with
// what was dispatched.
func manifestTypeFromEnv() string {
	switch {
	case securityPersonaActive():
		return TypeSecurity
	case envOn(EnvSlop):
		return TypeSlop
	}
	return TypeReview
}

// manifestStagesFromEnv lists the optional stages the harness enables. The
// harness has no switch for verification, so only validation can be listed.
func manifestStagesFromEnv() []string {
	if envOn(EnvValidation) {
		return []string{StageValidation}
	}
	return nil
}

// manifestCorpusFromEnv names the fixture selection, or "all" when unrestricted.
func manifestCorpusFromEnv() string {
	if value := strings.TrimSpace(os.Getenv(EnvFixtures)); value != "" {
		return value
	}
	return "all"
}
