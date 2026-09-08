package config

import (
	"fmt"
	"sort"
	"strings"
)

// Severity ranks a finding's importance. It is ordered, so gating policy can be
// expressed as a threshold rather than a set.
type Severity string

// Severity levels, ordered from least to most severe. SeverityNone is only
// meaningful as a threshold and is never carried by a finding.
const (
	SeverityNone     Severity = "none"
	SeverityNit      Severity = "nit"
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityError    Severity = "error"
	SeverityCritical Severity = "critical"
)

// severityRank orders severities. Higher is more severe. SeverityNone sits
// above every real severity so that using it as a threshold matches nothing,
// which is what makes fail_on: none a never-fail policy.
var severityRank = map[Severity]int{
	SeverityNit:      1,
	SeverityInfo:     2,
	SeverityWarning:  3,
	SeverityError:    4,
	SeverityCritical: 5,
	SeverityNone:     99,
}

// FindingSeverities are the levels a finding can carry, least severe first.
//
// SeverityNone is a threshold rather than a level, so the two keys that gate on
// a level a finding carries reject it: validate refuses `review.min_severity:
// none`, which would discard every finding, and `linters.max_severity: none`,
// which asks for a ceiling below the floor. The generated reference lists the
// values a key accepts from its type's constants, and Severity declares six
// where those two take five, so they answer with this list instead. It is
// derived from severityRank and sorted by it, so a level added there is offered
// here without anyone remembering to.
func FindingSeverities() []string {
	out := make([]string, 0, len(severityRank))
	for s := range severityRank {
		if s != SeverityNone {
			out = append(out, string(s))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return severityRank[Severity(out[i])] < severityRank[Severity(out[j])]
	})
	return out
}

// MinSeverityValues reports what review.min_severity accepts, for the generated
// reference. See FindingSeverities.
func (r Review) MinSeverityValues() []string { return FindingSeverities() }

// MaxSeverityValues reports what linters.max_severity accepts, for the
// generated reference. See FindingSeverities.
func (l Linters) MaxSeverityValues() []string { return FindingSeverities() }

// Rank returns the severity's ordinal. Unknown severities rank as SeverityInfo
// so that an unexpected value from a model degrades to a non-gating finding
// rather than silently disappearing or failing the build.
func (s Severity) Rank() int {
	if r, ok := severityRank[s.normalized()]; ok {
		return r
	}
	return severityRank[SeverityInfo]
}

// AtLeast reports whether s is at least as severe as threshold.
//
// SeverityNone is a threshold, never a finding's severity. Its rank sits above
// every real level so that `fail_on: none` matches nothing, but that means a
// finding claiming severity "none" would otherwise outrank critical and trip
// every gate. Models do return unexpected severities, so the receiver is
// checked explicitly rather than trusted to be a real level.
func (s Severity) AtLeast(threshold Severity) bool {
	if s.normalized() == SeverityNone {
		return false
	}
	return s.Rank() >= threshold.Rank()
}

// IsFinding reports whether s is a severity a finding may carry.
func (s Severity) IsFinding() bool {
	n := s.normalized()
	return n != SeverityNone && n.Valid()
}

// Normalize maps a model-supplied severity onto a real level.
//
// Unknown values become SeverityInfo so an unexpected vocabulary produces a
// visible, non-gating finding rather than silently vanishing or blocking a
// merge. The bool reports whether the value was recognized, so the caller can
// log the surprise instead of hiding it.
func (s Severity) Normalize() (Severity, bool) {
	n := s.normalized()
	if n.Valid() && n != SeverityNone {
		return n, true
	}
	return SeverityInfo, false
}

// Valid reports whether s is a recognized severity.
func (s Severity) Valid() bool {
	_, ok := severityRank[s.normalized()]
	return ok
}

func (s Severity) normalized() Severity {
	return Severity(strings.ToLower(strings.TrimSpace(string(s))))
}

// String returns the normalized severity name.
func (s Severity) String() string { return string(s.normalized()) }

// UnmarshalYAML accepts severities case-insensitively and rejects unknown ones
// at load time, so a typo in fail_on fails before any tokens are spent.
func (s *Severity) UnmarshalYAML(unmarshal func(any) error) error {
	var raw string
	if err := unmarshal(&raw); err != nil {
		return err
	}

	candidate := Severity(raw).normalized()
	if !candidate.Valid() {
		return fmt.Errorf("unknown severity %q (want one of: nit, info, warning, error, critical, none)", raw)
	}

	*s = candidate
	return nil
}
