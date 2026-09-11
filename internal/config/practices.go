package config

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/jdziat/open-nitpick/internal/commits"
	"github.com/jdziat/open-nitpick/internal/vcs"
	"gopkg.in/yaml.v3"
)

// Practices configures explicit engineering checks separately from inferred conventions.
type Practices struct {
	// Profile selects engineering checks for reviews; repository-wide scans require -profile engineering.
	Profile string `yaml:"profile" json:"profile"`
	// Commits defines accepted subject types, length and merge exemptions.
	Commits commits.Policy `yaml:"commits" json:"commits"`
	// Required names checks that must finish; finding severity is controlled separately by FailOn.
	Required []string `yaml:"required" json:"required"`
	// RequiredConventions enforces named probe rules even when repository adherence falls below the inference threshold.
	RequiredConventions []string `yaml:"required_conventions" json:"required_conventions"`
	// SlopRules makes the named deterministic tells blocking independently of their density.
	SlopRules []string `yaml:"slop_rules" json:"slop_rules"`
	// Budget limits estimated source tokens for a tree model assessment; zero leaves source selection unlimited.
	Budget int `yaml:"budget" json:"budget"`
	// Boundaries requires the configured Go import restrictions to be evaluated.
	Boundaries []PracticeBoundary `yaml:"boundaries" json:"boundaries"`
	// Exceptions accepts bounded findings over unchanged evidence without waiving missing coverage.
	Exceptions []PracticeException `yaml:"exceptions" json:"exceptions"`
	// FailOn sets blocking severity thresholds by check ID; none disables findings gates without waiving required completion.
	FailOn map[string]Severity `yaml:"fail_on" json:"fail_on"`
	// Ignore adds path exclusions to review.ignore; an empty list adds no exclusions.
	Ignore []string `yaml:"ignore" json:"ignore"`
}

// PracticeBoundary prohibits dependency edges from a named import-path pattern.
type PracticeBoundary struct {
	// From matches full Go import paths using path.Match syntax; * does not cross /.
	// All selected Go sources contribute direct imports, across build constraints.
	From string `yaml:"from" json:"from"`
	// Forbid matches direct dependency import paths, with no implicit subtree match.
	Forbid []string `yaml:"forbid" json:"forbid"`
	// Reason records the accepted architectural constraint behind the restriction.
	Reason string `yaml:"reason" json:"reason"`
}

// PracticeException accepts one finding over unchanged evidence, with a reason.
type PracticeException struct {
	// Rule names the exact finding rule accepted by this exception.
	Rule string `yaml:"rule" json:"rule"`
	// Target identifies the exact file, commit or title represented by the finding.
	Target string `yaml:"target" json:"target"`
	// Fingerprint binds the exception to the rule version, location and source evidence.
	Fingerprint string `yaml:"fingerprint" json:"fingerprint"`
	// Reason explains why this particular finding is accepted.
	Reason string `yaml:"reason" json:"reason"`
	// Expires disables the exception at 00:00 UTC on this YYYY-MM-DD date; empty means no expiry.
	Expires string `yaml:"expires" json:"expires"`
}

// DefaultPractices leaves selection opt-in while requiring complete engineering evidence.
func DefaultPractices() Practices {
	return Practices{Commits: commits.Defaults(), Required: []string{"conventions", "linters", "commits", "slop-tells", "slop", "design"}}
}

// Validate rejects misspelled check names instead of silently dropping coverage.
func (p Practices) Validate() error {
	var problems []error
	for _, name := range append(slices.Clone(p.SlopRules), p.RequiredConventions...) {
		if strings.TrimSpace(name) == "" {
			problems = append(problems, errors.New("practice rule identifiers cannot be blank"))
		}
	}
	for i, pattern := range p.Ignore {
		if !validGlob(pattern) {
			problems = append(problems, fmt.Errorf("practices.ignore[%d]: invalid glob %q", i, pattern))
		}
	}
	if p.Profile != "" && p.Profile != "engineering" {
		problems = append(problems, fmt.Errorf("unknown practices.profile %q", p.Profile))
	}
	if err := p.Commits.Check(); err != nil {
		problems = append(problems, fmt.Errorf("practices.commits: %w", err))
	}
	known := []string{"conventions", "linters", "commits", "commit-title", "slop-tells", "slop", "design", "design-boundaries", "security"}
	for id, threshold := range p.FailOn {
		if !slices.Contains(known, id) || !threshold.Valid() {
			problems = append(problems, fmt.Errorf("invalid practices.fail_on threshold for %q", id))
		}
	}
	for i, id := range p.Required {
		if !slices.Contains(known, id) || slices.Contains(p.Required[:i], id) {
			problems = append(problems, fmt.Errorf("invalid or repeated practices.required check %q", id))
		}
	}
	if p.Budget < 0 {
		problems = append(problems, errors.New("practices.budget must be nonnegative"))
	}
	for _, boundary := range p.Boundaries {
		if boundary.From == "" || len(boundary.Forbid) == 0 || strings.TrimSpace(boundary.Reason) == "" {
			problems = append(problems, errors.New("practice boundaries need from, forbid and reason"))
		}
		for _, pattern := range append(slices.Clone(boundary.Forbid), boundary.From) {
			if _, err := path.Match(pattern, ""); err != nil || pattern == "" {
				problems = append(problems, fmt.Errorf("invalid boundary pattern %q", pattern))
			}
		}
	}
	for _, exception := range p.Exceptions {
		decoded, err := hex.DecodeString(exception.Fingerprint)
		if exception.Rule == "" || exception.Target == "" || strings.TrimSpace(exception.Reason) == "" || err != nil || len(decoded) != sha256.Size {
			problems = append(problems, errors.New("practice exceptions need rule, exact target, SHA256 fingerprint and reason"))
		}
		if exception.Expires != "" {
			if _, err := time.Parse(time.DateOnly, exception.Expires); err != nil {
				problems = append(problems, errors.New("practice exception expiry must be YYYY-MM-DD"))
			}
		}
	}
	return errors.Join(problems...)
}

// PracticePolicy records the blocks deterministic checks need without loading models.
type PracticePolicy struct {
	Practices      Practices `yaml:"practices" json:"practices"`
	Standards      Standards `yaml:"standards" json:"standards"`
	Review         Review    `yaml:"review" json:"review"`
	Linters        Linters   `yaml:"linters" json:"linters"`
	Source         string    `yaml:"-" json:"-"`
	Digest         string    `yaml:"-" json:"-"`
	BaseRevision   string    `yaml:"-" json:"-"`
	IgnoredUnknown []string  `yaml:"-" json:"ignored_unknown,omitempty"`
}

// ReadPracticePolicy reads accepted policy at base or an external operator file.
// It never requires model configuration merely to validate commit subjects.
func ReadPracticePolicy(ctx context.Context, root, path, base string) (PracticePolicy, error) {
	defaults := Defaults()
	result := PracticePolicy{Practices: DefaultPractices(), Review: defaults.Review, Linters: defaults.Linters, Source: "defaults"}
	root, err := filepath.Abs(root)
	if err != nil {
		return result, err
	}
	if path == "" {
		path = filepath.Join(root, FileName)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return result, err
	}
	rel, inside := (&Config{Source: path}).RepoRelative(root)
	var raw []byte
	if inside {
		if base == "" {
			base = "HEAD"
		}
		local := vcs.NewLocal(root, nil)
		sha, resolveErr := local.CommitSHA(ctx, base)
		if resolveErr != nil {
			return result, fmt.Errorf("resolve accepted practices policy: %w", resolveErr)
		}
		raw, err = local.PolicyFile(ctx, sha, rel)
		result.BaseRevision = sha
		result.Source = rel + "@" + sha
		if errors.Is(err, vcs.ErrNotFound) {
			result.Source = "defaults (no " + rel + " at " + sha + ")"
			err = nil
		}
	} else {
		raw, err = os.ReadFile(path)
		result.Source = "operator:" + path
	}
	if err != nil {
		return result, fmt.Errorf("read practices policy: %w", err)
	}
	userPath, userRaw, err := userDocument(nil)
	if err != nil {
		return result, err
	}
	if err := decodePracticeBlocks(userRaw, &result); err != nil {
		return result, fmt.Errorf("user practices policy: %w", err)
	}
	if len(userRaw) > 0 {
		result.Source += "; user:" + userPath
	}
	if err := decodePracticeBlocks(raw, &result); err != nil {
		return result, err
	}
	if len(result.IgnoredUnknown) > 0 {
		result.Source += "; ignored unknown keys: " + strings.Join(result.IgnoredUnknown, ", ")
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return result, err
	}
	digest := sha256.Sum256(encoded)
	result.Digest = hex.EncodeToString(digest[:])
	return result, nil
}

func decodePracticeBlocks(raw []byte, result *PracticePolicy) error {
	allowUnknown := ignoreUnknownKeys(nil)
	var document yaml.Node
	if err := yaml.Unmarshal(raw, &document); err != nil {
		return fmt.Errorf("parse practices policy: %w", err)
	}
	if len(document.Content) != 0 {
		mapping := document.Content[0]
		if mapping.Kind != yaml.MappingNode {
			return errors.New("practices policy must be a YAML mapping")
		}
		known := map[string]bool{}
		typ := reflect.TypeFor[Config]()
		for i := range typ.NumField() {
			name, _, _ := strings.Cut(typ.Field(i).Tag.Get("yaml"), ",")
			if name != "" && name != "-" {
				known[name] = true
			}
		}
		seen := map[string]bool{}
		for i := 0; i < len(mapping.Content); i += 2 {
			key := mapping.Content[i]
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
				return errors.New("policy block names must be strings")
			}
			name := key.Value
			if seen[name] {
				return fmt.Errorf("duplicate policy block %q", name)
			}
			seen[name] = true
			if !known[name] {
				if !allowUnknown {
					return fmt.Errorf("unknown policy block %q", name)
				}
				result.IgnoredUnknown = append(result.IgnoredUnknown, name)
			}
		}
		// Decode the original document so aliases can cross policy blocks.
		decoded := struct {
			Practices Practices            `yaml:"practices"`
			Standards Standards            `yaml:"standards"`
			Review    Review               `yaml:"review"`
			Linters   Linters              `yaml:"linters"`
			Other     map[string]yaml.Node `yaml:",inline"`
		}{Practices: result.Practices, Standards: result.Standards, Review: result.Review, Linters: result.Linters}
		decoder := yaml.NewDecoder(bytes.NewReader(raw))
		decoder.KnownFields(true)
		if err := decoder.Decode(&decoded); err != nil {
			keys, onlyUnknown := unknownFields(err)
			if !allowUnknown || !onlyUnknown {
				return fmt.Errorf("invalid practices policy: %w", err)
			}
			for _, key := range keys {
				result.IgnoredUnknown = append(result.IgnoredUnknown, key.Name)
			}
		}
		result.Practices, result.Standards = decoded.Practices, decoded.Standards
		result.Review, result.Linters = decoded.Review, decoded.Linters
	}

	problems := []error{result.Practices.Validate(), result.Standards.Validate()}
	problems = append(problems, result.Review.validate()...)
	problems = append(problems, result.Linters.validate()...)
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid practices policy: %w", err)
	}

	return nil
}

// LoadPracticeModels loads model settings through the existing accepted-policy
// resolver; deterministic practice checks do not call this loader.
func LoadPracticeModels(ctx context.Context, root, path, base string) (*Config, error) {
	if path == "" {
		path = filepath.Join(root, FileName)
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	placeholder := Defaults()
	placeholder.Source = path
	rel, inside := placeholder.RepoRelative(root)
	if !inside {
		return LoadFile(path)
	}
	local := vcs.NewLocal(root, nil)
	if base == "" {
		base = "HEAD"
	}
	sha, err := local.CommitSHA(ctx, base)
	if err != nil {
		return nil, err
	}
	return ResolvePolicy(ctx, placeholder, PolicyRequest{RepoRoot: root, Changed: []string{rel}, BaseRev: sha,
		ReadBase: func(ctx context.Context, name string) ([]byte, error) { return local.PolicyFile(ctx, sha, name) }})
}
