package evals

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// The pre-registered evaluation of retrieved knowledge, issue #84.
//
// The files are testdata rather than a Go literal for the reason the corpus
// entries are files: a pre-registration is a claim someone reviews and dates,
// and changing one should be a diff a reader can see rather than an edit
// inside a test.

// PreRegistration is what was committed to before the run.
type PreRegistration struct {
	Version  int    `yaml:"version"`
	Frozen   string `yaml:"frozen"`
	Status   string `yaml:"status"`
	Question string `yaml:"question"`
	Corpora  []struct {
		Repo      string `yaml:"repo"`
		Language  string `yaml:"language"`
		Exercises string `yaml:"exercises"`
	} `yaml:"corpora"`
	Arms struct {
		PreFix   int `yaml:"pre_fix"`
		Repaired int `yaml:"repaired"`
		Clean    int `yaml:"clean"`
	} `yaml:"arms"`
	Repeats      int      `yaml:"repeats"`
	Snapshots    string   `yaml:"snapshots"`
	HoldoutRepos []string `yaml:"holdout_repos"`
	BudgetUSD    float64  `yaml:"budget_usd"`
	Thresholds   []struct {
		Name        string   `yaml:"name"`
		Metric      string   `yaml:"metric"`
		Denominator int      `yaml:"denominator"`
		AtLeast     *float64 `yaml:"at_least"`
		AtMost      *float64 `yaml:"at_most"`
		Why         string   `yaml:"why"`
	} `yaml:"thresholds"`
	PublishNegative bool `yaml:"publish_negative"`
	PromotesDefault bool `yaml:"promotes_default"`
}

// Snapshot is one adjudicated commit in the frozen selection.
type Snapshot struct {
	ID            string `yaml:"id"`
	Repo          string `yaml:"repo"`
	Commit        string `yaml:"commit"`
	Parent        string `yaml:"parent"`
	Arm           string `yaml:"arm"`
	Language      string `yaml:"language"`
	Class         string `yaml:"class"`
	Family        string `yaml:"family"`
	Defect        string `yaml:"defect"`
	AdjudicatedBy string `yaml:"adjudicated_by"`
}

// Selection is the frozen corpus.
type Selection struct {
	Version   int        `yaml:"version"`
	Frozen    bool       `yaml:"frozen"`
	Snapshots []Snapshot `yaml:"snapshots"`
}

// Arms counts the selection by arm.
func (s Selection) Arms() map[string]int {
	out := map[string]int{}
	for _, snap := range s.Snapshots {
		out[snap.Arm]++
	}
	return out
}

const preRegDir = "testdata/knowledge-eval"

// LoadPreRegistration reads what was committed to before the run.
func LoadPreRegistration() (*PreRegistration, error) {
	var p PreRegistration
	if err := readYAML(filepath.Join(preRegDir, "preregistration.yaml"), &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// LoadSelection reads the frozen corpus.
func LoadSelection() (*Selection, error) {
	var s Selection
	if err := readYAML(filepath.Join(preRegDir, "selection.yaml"), &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func readYAML(path string, into any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("knowledge evaluation: read %s: %w", path, err)
	}
	if err := yaml.Unmarshal(raw, into); err != nil {
		return fmt.Errorf("knowledge evaluation: parse %s: %w", path, err)
	}
	return nil
}
