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
	// answered, and zero, the default, is no cap at all.
	//
	// It bounds the case the association list does not: a collaborator, or an
	// automation acting as one, in a loop.
	MaxPerPullRequest int `yaml:"max_per_pull_request"`

	// Replies lets the reviewer read a reply left under one of its own
	// findings without being mentioned, and resolve the thread when the reply
	// settles it. Unset means on: a finding that cannot be answered has to be
	// dismissed by hand, and the same association list and cap that bound a
	// mention bound a reply.
	Replies *bool `yaml:"replies"`

	// Fix bounds who may ask for a finding to be APPLIED, which is a write to
	// the repository rather than an answer in a thread.
	Fix FixRespond `yaml:"fix"`

	// Dismiss bounds who may close a finding as settled, which is a statement
	// about the code rather than a request for work.
	Dismiss DismissRespond `yaml:"dismiss"`
}

// DismissAuthority names whose word closes a finding.
type DismissAuthority string

// The two authorities.
const (
	// AuthorityCodeowners lets the owners of the finding's path close it, and
	// the pull request's author unless AllowAuthor says otherwise. A path no
	// CODEOWNERS rule covers, or a repository with no CODEOWNERS, falls back
	// to review.respond.from, so turning this on never leaves a finding that
	// nobody may close. It is the default, so an install upgrading from a
	// release without this key sees replies from non-owners stop closing
	// findings in a repository that has CODEOWNERS, and a team owner needs a
	// token with read:org; AuthorityAssociation keeps the old behaviour.
	AuthorityCodeowners DismissAuthority = "codeowners"

	// AuthorityAssociation lets anyone review.respond.from admits close any
	// finding, which is what every release before this key did.
	AuthorityAssociation DismissAuthority = "association"
)

// DismissRespond bounds who may close a finding.
//
// Separate from From because From answers "may this person spend my model
// credit" and a dismissal answers a different question: a collaborator with no
// stake in a file can say its finding is fine, and the finding stays quiet on
// every later push. The statement is only worth something when the person
// making it is answerable for the code.
type DismissRespond struct {
	// Authority picks whose reply or resolve command closes a finding. Empty
	// means codeowners.
	Authority DismissAuthority `yaml:"authority"`

	// AllowAuthor lets the pull request's author close findings on their own
	// change. Unset means on: the author is who the findings are for, and a
	// reviewer that argues with them about whether they may answer it has
	// made itself an adversary. Turn it off where the author should not grade
	// their own work.
	AllowAuthor *bool `yaml:"allow_author"`
}

// EffectiveAuthority resolves the default.
func (d DismissRespond) EffectiveAuthority() DismissAuthority {
	if d.Authority == "" {
		return AuthorityCodeowners
	}
	return d.Authority
}

// AllowAuthorOn resolves allow_author, which is on when unset. Named for the
// shape the configuration reference reads a shipped default from.
func (d DismissRespond) AllowAuthorOn() bool { return d.AllowAuthor == nil || *d.AllowAuthor }

// AuthorMayDismiss reports whether the pull request's author may close
// findings on their own change.
func (d DismissRespond) AuthorMayDismiss() bool { return d.AllowAuthorOn() }

func (d DismissRespond) validate() []error {
	switch d.EffectiveAuthority() {
	case AuthorityCodeowners, AuthorityAssociation:
		return nil
	}
	return []error{fmt.Errorf("review.respond.dismiss.authority %q is not codeowners or association", d.Authority)}
}

// RepliesEnabled reports whether a reply under a finding is read.
func (r Respond) RepliesEnabled() bool { return r.Replies == nil || *r.Replies }

// FixRespond bounds who may make the reviewer change code.
//
// Separate from Respond because an association answers "may this person spend
// my credit" well and "may this person write to my repository" poorly:
// COLLABORATOR covers anyone invited at all. The permission check in the fix
// path is the real gate and this is its cheap half.
type FixRespond struct {
	// From lists the associations allowed to ask. Empty means
	// DefaultRespondFrom, the same set answers use.
	From []Association `yaml:"from"`
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
		// Case-folded, because validation folds too. Comparing raw would
		// accept `from: [OWNER]` at load and then refuse every owner at run,
		// which is a config that is wrong only when someone uses it.
		if Association(strings.ToLower(strings.TrimSpace(string(want)))) == got {
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
		if Association(strings.ToLower(strings.TrimSpace(string(want)))) == got {
			return true
		}
		// "none" is the forge's word for a stranger, so allowing it is the
		// operator saying anyone may.
		if Association(strings.ToLower(strings.TrimSpace(string(want)))) == AssocNone {
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
	errs = append(errs, r.Dismiss.validate()...)

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
