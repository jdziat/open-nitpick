//go:build eval

package evals

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/jdziat/open-nitpick/internal/review"
)

func TestAuditRetainedQualityStages(t *testing.T) {
	dir := os.Getenv("NITPICK_EVAL_AUDIT_CAPTURE")
	if dir == "" {
		t.Skip("set NITPICK_EVAL_AUDIT_CAPTURE to an existing local capture directory")
	}
	fixtures := map[string]Fixture{}
	for _, f := range EveryFixture() {
		fixtures[f.Name] = f
	}
	type row struct {
		Path    string             `json:"capture"`
		Failure string             `json:"failure,omitempty"`
		Stages  []StageAttribution `json:"stages"`
	}
	var rows []row
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var artifact SampleArtifact
		if err := json.Unmarshal(data, &artifact); err != nil {
			return err
		}
		fixture, ok := fixtures[artifact.Result.Fixture]
		if !ok || fixtureFingerprint(fixture) != artifact.FixtureHash {
			t.Errorf("capture %s references missing or changed fixture", path)
			return nil
		}
		rows = append(rows, row{Path: path, Failure: artifact.Failure, Stages: AttributeStages(fixture, artifact.Result.Snapshots, ExpectedStages(TypeReview, enabledOptionalStages(artifact.Result.Snapshots)))})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Fatal("capture directory contained no samples")
	}
	data, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+".stage-audit.json", append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("audited %d retained samples without provider access", len(rows))
}

// enabledOptionalStages infers which optional stages a run enabled from the
// snapshots it produced, because a bare capture carries no manifest. The
// inference cannot see a stage that was enabled but emitted nothing; a run with
// a manifest should pass the manifest's Stages instead.
func enabledOptionalStages(snapshots []review.Snapshot) []string {
	var enabled []string
	for _, snapshot := range snapshots {
		if snapshot.Stage == StageValidation || snapshot.Stage == StageVerification {
			enabled = append(enabled, snapshot.Stage)
		}
	}
	return enabled
}
