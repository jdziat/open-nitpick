// Package commits validates commit subjects without executing repository code.
package commits

import (
	"errors"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Commit holds the metadata needed to evaluate subject policy.
type Commit struct {
	SHA         string `json:"sha"`
	Subject     string `json:"subject"`
	ParentCount int    `json:"parent_count"`
}

// Policy states required conventions; observations never weaken these rules.
type Policy struct {
	// Types lists allowed lowercase Conventional Commit types.
	Types []string `json:"types" yaml:"types"`
	// MaxDescriptionRunes limits Unicode characters after the colon and space.
	MaxDescriptionRunes int `json:"max_description_runes" yaml:"max_description_runes"`
	// ExemptMerges skips subjects only when Git records multiple parents.
	ExemptMerges bool `json:"exempt_merges" yaml:"exempt_merges"`
}

// Defaults returns the repository's existing Conventional Commit policy.
func Defaults() Policy {
	return Policy{Types: []string{"feat", "fix", "docs", "style", "refactor", "perf", "test", "build", "ci", "chore", "revert", "evals", "prompt"}, MaxDescriptionRunes: 72, ExemptMerges: true}
}

var subject = regexp.MustCompile(`^([a-z]+)(\([a-z0-9._/-]+\))?!?: ([^ ].*)$`)
var typeName = regexp.MustCompile(`^[a-z]+$`)

// Check rejects ambiguous or unusable policy before any commits are evaluated.
func (p Policy) Check() error {
	if len(p.Types) == 0 || p.MaxDescriptionRunes < 1 {
		return errors.New("commit policy needs allowed types and a positive description length")
	}
	for i, name := range p.Types {
		if !typeName.MatchString(name) || slices.Contains(p.Types[:i], name) {
			return errors.New("commit types must be unique lowercase words")
		}
	}
	return nil
}

// Violation names the exact subject rule a commit failed.
type Violation struct {
	Rule    string `json:"rule"`
	Message string `json:"message"`
}

// Validate checks one subject; merge exemptions use topology rather than prose.
func Validate(c Commit, p Policy) []Violation {
	if err := p.Check(); err != nil {
		return []Violation{{"commits.policy", err.Error()}}
	}
	if c.ParentCount > 1 && p.ExemptMerges {
		return nil
	}
	if !utf8.ValidString(c.Subject) || strings.IndexFunc(c.Subject, unicode.IsControl) >= 0 {
		return []Violation{{"commits.subject-format", "subject must be one line of valid text without control characters"}}
	}
	match := subject.FindStringSubmatch(c.Subject)
	if match == nil || strings.TrimSpace(match[3]) == "" {
		return []Violation{{"commits.subject-format", "use type(scope)!: description with a nonempty description"}}
	}
	var out []Violation
	if !slices.Contains(p.Types, match[1]) {
		out = append(out, Violation{"commits.type", "type is not allowed by the selected commit policy"})
	}
	if utf8.RuneCountInString(match[3]) > p.MaxDescriptionRunes {
		out = append(out, Violation{"commits.subject-length", "description exceeds the configured Unicode character limit"})
	}
	return out
}
