package main

import (
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
)

// TestImproveScopeWidensWhatAReviewGenerates pins what the two forms of the
// command agree on. Both call this, so a change here moves both.
func TestImproveScopeWidensWhatAReviewGenerates(t *testing.T) {
	cfg := config.Defaults()
	cfg.Persona.Nitpick = config.NitpickMinimal
	cfg.Review.Slop = false
	cfg.Review.MinSeverity = config.SeverityWarning

	applyImproveScope(cfg)

	if cfg.Persona.Nitpick != config.NitpickPedantic {
		t.Errorf("nitpick level = %q, want %q", cfg.Persona.Nitpick, config.NitpickPedantic)
	}
	if !cfg.Review.Slop {
		t.Error("the slop class is off, so the pass cannot report what it is for")
	}
	if cfg.Review.MinSeverity != config.SeverityNit {
		t.Errorf("min_severity = %q, want %q: a repository publishing at warning would get silence "+
			"back from a command it typed on purpose", cfg.Review.MinSeverity, config.SeverityNit)
	}
}

// TestImproveScopeLeavesTheRestAlone is the other direction. The pass widens
// what is generated; it is not a licence to change how the review runs.
func TestImproveScopeLeavesTheRestAlone(t *testing.T) {
	cfg := config.Defaults()
	before := *cfg
	applyImproveScope(cfg)

	if cfg.Review.FailOn != before.Review.FailOn {
		t.Errorf("fail_on moved to %q; the gate is the operator's", cfg.Review.FailOn)
	}
	if cfg.Review.MaxFiles != before.Review.MaxFiles {
		t.Errorf("max_files moved to %d", cfg.Review.MaxFiles)
	}
	if cfg.Review.Mention != before.Review.Mention {
		t.Errorf("mention moved to %q", cfg.Review.Mention)
	}
}

// TestImproveRejectsALevelItCannotGenerate covers the flag, since a level
// outside the set would otherwise reach the engine as an unknown value and
// filter every class away.
func TestImproveRejectsALevelItCannotGenerate(t *testing.T) {
	err := runImproveCLI(t.Context(), []string{"-level", "bogus"})
	if err == nil {
		t.Fatal("an unknown -level was accepted")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Errorf("the error does not name the value: %v", err)
	}
}
