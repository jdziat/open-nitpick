package config

import (
	"errors"
	"fmt"
	"strings"
)

// Association is what the forge says a commenter is to the repository.
//
// The values are GitHub's author_association, lowercased. They are the only
// statement about a person that arrives WITH the event and needs no API call,
// which matters because the check has to happen before anything is spent.
type Association string

// The associations a forge reports.
const (
	AssocOwner        Association = "owner"
	AssocMember       Association = "member"
	AssocCollaborator Association = "collaborator"
	AssocContributor  Association = "contributor"
	AssocFirstTimer   Association = "first_time_contributor"
	AssocNone         Association = "none"
)

// DefaultRespondFrom is who may command the reviewer by comment.
//
// It is a closed list rather than an open one, and that is the whole point. A
// comment event runs in the BASE repository holding the repository's model
// credential, whoever wrote the comment. On a public repository an open list
// means every account on the forge can spend the maintainer's money, one
// "@open-nitpick explain this" at a time, and nothing about a per-answer size cap
// bounds a total whose multiplier is the number of strangers.
//
// CONTRIBUTOR is deliberately absent. It means "has a merged commit", which is
// a permanent grant: one accepted typo fix buys unlimited calls forever.
var DefaultRespondFrom = []Association{AssocOwner, AssocMember, AssocCollaborator}

// Respond bounds who may make the reviewer spend money by talking to it.
type Respond struct {
	// From lists the associations allowed to command the reviewer. Empty means
	// DefaultRespondFrom. A list containing "none" allows anyone, which is a
	// decision an operator can make explicitly and cannot make by accident.
	From []Association `yaml:"from"`

	// MaxPerPullRequest caps how many comments the reviewer answers on one
	// pull request, counting its own replies as the record of how many it has
	// answered. Zero, the default, is no cap.
	//
	// It bounds the case the association list does not: a collaborator, or an
	// automation acting as one, in a loop.
	MaxPerPullRequest int `yaml:"max_per_pull_request"`

	// Fix bounds who may ask for a finding to be APPLIED, which is a write to
	// the repository rather than an answer in a thread.
	Fix FixRespond `yaml:"fix"`
}

// FixRespond bounds who may make the reviewer change code.
//
// Separate from Respond because the two gates guard different things. An
// association is a reasonable answer to "may this person spend my model
// credit"; it is a weak answer to "may this person have code written into my
// repository", since the forge's COLLABORATOR covers anyone invited at all,
// read level included. The permission check in the fix path is the real
// invariant and this is the cheap half of it.
type FixRespond struct {
	// From lists the associations allowed to ask. Empty means
	// DefaultRespondFrom, the same set answers use.
	From []Association `yaml:"from"`

	// MaxPerPullRequest caps how many fix passes one pull request may trigger.
	// Zero, the default, is no cap.
	//
	// It is not the answer cap. One "fix all" is a single answer and a model
	// call per finding, so a count of answers bounds nothing here.
	MaxPerPullRequest int `yaml:"max_per_pull_request"`
}

// Allows reports whether an association may ask for a fix.
//
// "none" is refused here even when it is written down, which is the one place
// this differs from Respond. An operator may reasonably decide that anyone can
// spend their model credit on an answer. Nobody decides that anyone can write
// to their repository, so a config saying so is a mistake rather than a
// choice, and it is refused at validation as well.
func (f FixRespond) Allows(assoc string) bool {
	got := Association(strings.ToLower(strings.TrimSpace(assoc)))
	if got == "" || got == AssocNone {
		return false
	}
	for _, want := range f.EffectiveFrom() {
		if want == got {
			return true
		}
	}
	return false
}

// EffectiveFrom resolves the default.
func (f FixRespond) EffectiveFrom() []Association {
	if len(f.From) == 0 {
		return DefaultRespondFrom
	}
	return f.From
}

// validate checks the fix block.
func (f FixRespond) validate() []error {
	var errs []error

	for _, a := range f.From {
		switch Association(strings.ToLower(strings.TrimSpace(string(a)))) {
		case AssocOwner, AssocMember, AssocCollaborator, AssocContributor, AssocFirstTimer:
		case AssocNone:
			errs = append(errs, errors.New(
				"review.respond.fix.from may not contain \"none\": that would let anyone write to the repository"))
		default:
			errs = append(errs, fmt.Errorf("review.respond.fix.from: %q is not an association", a))
		}
	}
	if f.MaxPerPullRequest < 0 {
		errs = append(errs, errors.New("review.respond.fix.max_per_pull_request cannot be negative"))
	}
	return errs
}

// Allows reports whether an association may command the reviewer.
//
// An unrecognized or absent association is refused. The forge sends this field
// on every comment event, so an empty one means the payload is not the shape
// this code was written against, and defaulting to "allow" on a payload nobody
// understands is how a spending control becomes decorative.
func (r Respond) Allows(assoc string) bool {
	got := Association(strings.ToLower(strings.TrimSpace(assoc)))
	if got == "" {
		return false
	}

	for _, want := range r.EffectiveFrom() {
		if want == got {
			return true
		}
		// "none" is the forge's word for a stranger, so allowing it is the
		// operator saying anyone may.
		if want == AssocNone {
			return true
		}
	}
	return false
}

// EffectiveFrom resolves the default.
func (r Respond) EffectiveFrom() []Association {
	if len(r.From) == 0 {
		return DefaultRespondFrom
	}
	return r.From
}

// String renders the allowed set for a log line.
func (r Respond) String() string {
	parts := make([]string, 0, len(r.EffectiveFrom()))
	for _, a := range r.EffectiveFrom() {
		parts = append(parts, string(a))
	}
	return strings.Join(parts, ", ")
}

// validate checks the respond block.
func (r Respond) validate() []error {
	var errs []error

	known := map[Association]bool{
		AssocOwner: true, AssocMember: true, AssocCollaborator: true,
		AssocContributor: true, AssocFirstTimer: true, AssocNone: true,
	}
	for _, a := range r.From {
		if !known[Association(strings.ToLower(string(a)))] {
			errs = append(errs, fmt.Errorf(
				"review.respond.from %q is not a forge association: use owner, member, "+
					"collaborator, contributor, first_time_contributor, or none", a))
		}
	}
	if r.MaxPerPullRequest < 0 {
		errs = append(errs, fmt.Errorf("review.respond.max_per_pull_request cannot be negative"))
	}

	return errs
}

// SummaryStyle chooses how a review's walkthrough is produced.
type SummaryStyle string

// The two styles.
const (
	// SummaryReceipt counts what the run did and prints that. No model is
	// asked, so nothing in it can be invented. It is the default because the
	// alternative is a description of code the writer never saw.
	SummaryReceipt SummaryStyle = "receipt"

	// SummaryProse keeps the generated walkthrough. Available for a team that
	// wants prose and accepts that it is written from the findings list rather
	// than from the change; see docs/findings.md.
	SummaryProse SummaryStyle = "prose"
)

// EffectiveSummaryStyle resolves the default.
func (r Review) EffectiveSummaryStyle() SummaryStyle {
	if r.SummaryStyle == "" {
		return SummaryReceipt
	}
	return r.SummaryStyle
}

// validateSummaryStyle checks the style names one of the two.
func (r Review) validateSummaryStyle() []error {
	switch r.EffectiveSummaryStyle() {
	case SummaryReceipt, SummaryProse:
		return nil
	}
	return []error{fmt.Errorf("review.summary_style %q is not %s or %s",
		r.SummaryStyle, SummaryReceipt, SummaryProse)}
}
