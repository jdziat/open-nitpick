package evals

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nitpick-oss/nitpick/internal/llm"
)

type qualityVariant struct {
	suffix  string
	context [2]bool
}

func qualityVariants(selection string) ([]qualityVariant, error) {
	all := []qualityVariant{{"", [2]bool{false, false}}, {" +ctx", [2]bool{true, true}}, {" +default", [2]bool{true, false}}}
	if strings.TrimSpace(selection) == "" {
		return all, nil
	}
	var selected []qualityVariant
	seen := map[string]bool{}
	for _, name := range strings.Split(selection, ",") {
		name = strings.TrimSpace(name)
		if seen[name] {
			return nil, fmt.Errorf("duplicate quality variant %q", name)
		}
		seen[name] = true
		found := false
		for _, variant := range all {
			label := strings.TrimSpace(variant.suffix)
			if label == "" {
				label = "diff-only"
			}
			if name == label {
				selected = append(selected, variant)
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("unknown quality variant %q; use diff-only, +ctx, or +default", name)
		}
	}
	return selected, nil
}

func requireQualityPurchaseSetup() error {
	for _, name := range []string{EnvModels, llm.EnvResponseCache} {
		if strings.TrimSpace(os.Getenv(name)) == "" {
			return fmt.Errorf("quality benchmark requires explicit %s before provider access", name)
		}
	}
	if os.Getenv(llm.EnvReplayOnly) == "" && strings.TrimSpace(os.Getenv(llm.EnvPurchaseBudget)) == "" {
		return fmt.Errorf("live quality benchmark requires %s to cap purchases", llm.EnvPurchaseBudget)
	}
	return nil
}

// Review types a manifest may register. The type chooses the scorer and the
// stage ladder, so an unknown one is refused rather than scored as a review.
const (
	TypeReview     = "review"
	TypeDeepReview = "deep-review"
	TypeSlop       = "slop"
	TypeSecurity   = "security"
)

// Source-hash rules. Rule 1 hashed every .md under internal and cmd, so a
// documentation edit invalidated a registration; rule 2 hashes only the prompt
// templates, which are Markdown but change behaviour. Version 1 manifests keep
// the rule they were written under and are never recomputed.
const (
	HashRuleAllMarkdown     = 1
	HashRuleTemplatesOnly   = 2
	manifestVersionCurrent  = 2
	manifestVersionOriginal = 1
	templatesDir            = "internal/prompt/templates"
)

// ExperimentManifest fixes the scheduled denominator and inputs before dispatch.
type ExperimentManifest struct {
	Version int `json:"version"`
	// Type and Corpus name what was reviewed. Version 1 manifests predate them
	// and read back as a review with no named corpus.
	Type     string `json:"type,omitempty"`
	Corpus   string `json:"corpus,omitempty"`
	HashRule int    `json:"hash_rule,omitempty"`
	// Stages lists the optional pipeline stages enabled for the run, so a
	// missing snapshot for a disabled stage is not read as lost evidence.
	Stages         []string           `json:"stages,omitempty"`
	SourceHash     string             `json:"source_hash"`
	Models         []Model            `json:"models"`
	Runs           int                `json:"runs"`
	SampleOffset   int                `json:"sample_offset"`
	FixtureHashes  map[string]string  `json:"fixture_hashes"`
	Variants       map[string][2]bool `json:"variants"`
	Reasoning      string             `json:"reasoning"`
	RequestTimeout time.Duration      `json:"request_timeout_ns"`
}

func validManifestType(typ string) bool {
	switch typ {
	case TypeReview, TypeDeepReview, TypeSlop, TypeSecurity:
		return true
	}
	return false
}

// ReadExperimentManifest loads a registration of either version and returns it
// in the current shape. A version 1 file reads back as a review with hash rule 1.
func ReadExperimentManifest(path string) (ExperimentManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ExperimentManifest{}, err
	}
	var manifest ExperimentManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return ExperimentManifest{}, fmt.Errorf("parse experiment manifest: %w", err)
	}
	switch manifest.Version {
	case manifestVersionOriginal:
		manifest.Type = TypeReview
		manifest.HashRule = HashRuleAllMarkdown
	case manifestVersionCurrent:
		if !validManifestType(manifest.Type) {
			return ExperimentManifest{}, fmt.Errorf("experiment manifest has unknown type %q", manifest.Type)
		}
		if manifest.HashRule != HashRuleTemplatesOnly {
			return ExperimentManifest{}, fmt.Errorf("experiment manifest has unknown hash rule %d", manifest.HashRule)
		}
	default:
		return ExperimentManifest{}, fmt.Errorf("unsupported experiment manifest version %d", manifest.Version)
	}
	return manifest, nil
}

// WriteExperimentManifest refuses to overwrite the inputs of an earlier run.
// typ must be one of the Type constants; corpus names the fixture set.
func WriteExperimentManifest(path string, opts Options, variants map[string][2]bool, offset int, typ, corpus string, stages []string) error {
	if !validManifestType(typ) {
		return fmt.Errorf("experiment manifest type %q is not one of review, deep-review, slop, security", typ)
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	for {
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(root)
		if parent == root {
			return fmt.Errorf("cannot find evaluation source root")
		}
		root = parent
	}
	sourceHash, err := sourceHashUnderRuleTwo(root)
	if err != nil {
		return err
	}
	manifest := ExperimentManifest{
		Version: manifestVersionCurrent, Type: typ, Corpus: corpus, HashRule: HashRuleTemplatesOnly,
		Stages: stages, SourceHash: sourceHash, Models: opts.Models,
		Runs: opts.Runs, SampleOffset: offset, Variants: variants,
		FixtureHashes: map[string]string{}, Reasoning: os.Getenv(EnvReasoning), RequestTimeout: opts.Timeout,
	}
	for _, fixture := range opts.Fixtures {
		manifest.FixtureHashes[fixture.Name] = fixtureFingerprint(fixture)
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create experiment manifest: %w", err)
	}
	_, writeErr := file.Write(append(data, '\n'))
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

// hashedUnderRuleTwo reports whether a file under internal or cmd is part of
// the version 2 source hash: Go source, plus the prompt templates.
func hashedUnderRuleTwo(root, path string) bool {
	if strings.HasSuffix(path, ".go") {
		return true
	}
	if !strings.HasSuffix(path, ".md") {
		return false
	}
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return strings.HasPrefix(filepath.ToSlash(relative), templatesDir+"/")
}

// sourceHashUnderRuleTwo hashes the files that decide what a review does: Go
// source under internal and cmd, the prompt templates, go.mod and go.sum.
func sourceHashUnderRuleTwo(root string) (string, error) {
	var paths []string
	for _, dir := range []string{"internal", "cmd"} {
		if err := filepath.WalkDir(filepath.Join(root, dir), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() && (entry.Name() == "testdata" || strings.HasPrefix(entry.Name(), ".")) {
				return filepath.SkipDir
			}
			if !entry.IsDir() && hashedUnderRuleTwo(root, path) {
				paths = append(paths, path)
			}
			return nil
		}); err != nil {
			return "", err
		}
	}
	paths = append(paths, filepath.Join(root, "go.mod"), filepath.Join(root, "go.sum"))
	sort.Strings(paths)
	hash := sha256.New()
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return "", err
		}
		_, _ = hash.Write([]byte(relative + "\x00"))
		_, _ = hash.Write(data)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
