package evals

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// SampleArtifact holds stage evidence and the exact fixture used for attribution.
type SampleArtifact struct {
	Variant     string    `json:"variant"`
	FixtureHash string    `json:"fixture_hash"`
	Failure     string    `json:"failure,omitempty"`
	Result      RunResult `json:"result"`
}

// CaptureSample writes local evidence for successful, silent and failed reviews.
func CaptureSample(dir, variant string, fixture Fixture, result RunResult) error {
	if dir == "" {
		return nil
	}
	artifact := SampleArtifact{Variant: variant, FixtureHash: fixtureFingerprint(fixture), Result: result}
	if result.Err != nil {
		artifact.Failure = result.Err.Error()
	}
	data, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		return fmt.Errorf("encode sample capture: %w", err)
	}
	parent := filepath.Join(dir, captureSlug(result.Model), captureSlug(variant))
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("create sample capture: %w", err)
	}
	path := filepath.Join(parent, captureSlug(fixture.Name)+"-"+strconv.Itoa(result.Run)+".json")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create sample capture %s: %w", path, err)
	}
	_, writeErr := f.Write(append(data, '\n'))
	closeErr := f.Close()
	if writeErr != nil {
		return fmt.Errorf("write sample capture: %w", writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close sample capture: %w", closeErr)
	}
	return nil
}

func captureSlug(s string) string {
	if s == "" {
		return "diff-only"
	}
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, s)
}
