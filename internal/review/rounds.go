package review

import (
	"context"
	"errors"
	"fmt"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// ErrRoundLimit is returned, with a report, by a run that found the series of
// reviews already at review.max_rounds. No model was called.
//
// An error rather than a flag on a clean-looking report: every caller of
// Engine.Review that does not know about rounds then fails loudly instead of
// reading an empty Findings list as "nothing wrong".
var ErrRoundLimit = errors.New("the review round limit is reached")

// RoundsOverride is the operator's word on the series, from flags. A nil
// MaxRounds and an empty StopAt leave the resolved policy's values in force.
type RoundsOverride struct {
	MaxRounds *int
	StopAt    config.Severity
}

// RoundState says where one run sits in the series of reviews of a pull
// request. The series lives on the pull request itself: each review this tool
// published from a run that completed carries a round marker, and this tool
// counts its own.
type RoundState struct {
	// Round is this run's number, one more than the rounds already published.
	Round int

	// Max is the round limit in force, zero when unlimited.
	Max int

	// StopAt is the stop severity in force, empty when the series never
	// settles. Carried here so the caller judges settlement by the value the
	// engine resolved, not by a configuration the change may have written.
	StopAt config.Severity

	// Known is false when the series could not be counted, so Round is a guess
	// and no limit was enforced. Unknown says why.
	Known   bool
	Unknown string

	// Local is true when there is no pull request to hold a series: a run on
	// a working tree. Nothing can stand from an earlier round, so StopAt
	// judges this run alone, and Max cannot be enforced.
	Local bool

	// Limit is true when the series was already at Max and the run stopped
	// before calling a model.
	Limit bool

	// Unresolved are the threads from earlier rounds that still stand.
	Unresolved []vcs.PriorComment

	// Declined counts findings a person's accepted reply closed.
	Declined int
}

// String is the line every run ends with, so a reader of the log sees why the
// series stopped or continues.
func (s *RoundState) String() string {
	if s == nil {
		return ""
	}
	if s.Local {
		return "no pull request: rounds are not counted, and stop_at judges this run alone"
	}
	if !s.Known {
		return "round limit not enforced: " + s.Unknown
	}
	of := ""
	if s.Max > 0 {
		of = fmt.Sprintf(" of %d", s.Max)
	}
	return fmt.Sprintf("round %d%s: %d unresolved, %d declined", s.Round, of, len(s.Unresolved), s.Declined)
}

// checkRounds reads the series' state and refuses the run when the limit is
// reached.
//
// It reads the earlier reviews itself rather than borrowing the incremental
// prior, because that prior is nil whenever review.incremental is off, and a
// limit that quietly did not apply for that reason would be the same output as
// a limit nobody set. When the earlier reviews cannot be read the run goes on,
// and the state says it could not count.
func (e *Engine) checkRounds(ctx context.Context, ref vcs.Ref, prior *vcs.PriorReview, report *Report) error {
	max, stop := e.Config.Review.MaxRounds, e.Config.Review.StopAt
	if e.Rounds.MaxRounds != nil {
		max = *e.Rounds.MaxRounds
	}
	if e.Rounds.StopAt != "" {
		stop = e.Rounds.StopAt
		// "none" turns a configured stop_at off for this run.
		if stop.String() == string(config.SeverityNone) {
			stop = ""
		}
	}
	if max == 0 && stop == "" {
		return nil
	}
	state := &RoundState{Max: max, StopAt: stop}
	report.Rounds = state

	if prior == nil {
		// No pull request number means a working tree, whatever the provider
		// can do: a dry run wraps a local source in a provider that answers
		// PriorReview with an error, which is not the same as a series that
		// could not be read.
		reader, ok := e.Provider.(vcs.PriorReviewer)
		if !ok || ref.Number == 0 {
			state.Local = true
			return nil
		}
		loaded, err := reader.PriorReview(ctx, ref)
		if err != nil || loaded == nil {
			state.Unknown = "earlier reviews could not be read from this pull request"
			e.log().Warn("could not count earlier review rounds; the round limit is not enforced", "error", err)
			return nil
		}
		prior = loaded
	}

	state.Known = true
	state.Round = prior.Rounds + 1
	state.Unresolved = prior.Comments
	state.Declined = len(prior.Dismissed)

	if max > 0 && prior.Rounds >= max {
		state.Round = prior.Rounds
		state.Limit = true
		return ErrRoundLimit
	}
	return nil
}

// Settled reports whether the series of reviews is finished: nothing at or
// above stop remains, from this run or from an earlier round.
//
// A thread whose severity is missing or unreadable counts as standing at every
// level, because calling it settled would be claiming a review nobody read. A
// run that could not count its rounds is never settled for the same reason; a
// local run has no earlier rounds, so only its own findings decide.
func (r *Report) Settled(stop config.Severity) bool {
	if stop == "" || r.Rounds == nil || (!r.Rounds.Known && !r.Rounds.Local) {
		return false
	}
	if r.Practices != nil && r.Practices.ExitCode() != 0 {
		return false
	}
	for _, f := range append(append([]Finding(nil), r.Findings...), r.AlreadyReported...) {
		if f.Sev().AtLeast(stop) {
			return false
		}
	}
	closed := make(map[int64]bool, len(r.Superseded))
	for _, c := range r.Superseded {
		closed[c.ID] = true
	}
	for _, c := range r.Rounds.Unresolved {
		if closed[c.ID] {
			continue
		}
		if sev := config.Severity(c.Severity); !sev.IsFinding() || sev.AtLeast(stop) {
			return false
		}
	}
	return true
}
