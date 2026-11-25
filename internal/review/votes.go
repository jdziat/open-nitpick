package review

import (
	"context"
	"errors"
	"sync"

	"github.com/jdziat/open-nitpick/internal/bundle"
)

// reviewVoted reviews one batch review.votes times at once and keeps the
// findings a majority of the runs report. A model that is asked the same
// question several times does not give the same findings, and the ones that
// come back every time are the likelier defects.
//
// A run that fails counts as a run that reported nothing, so it cannot help a
// finding reach the majority. When too few runs succeed for any finding to
// reach it, the batch fails instead of returning an empty list: an empty list
// would read as a clean batch.
func (e *Engine) reviewVoted(ctx context.Context, r reviewers, prContext string, b bundle.Batch) ([]Finding, []Escalation, error) {
	runs := e.Config.Review.Votes
	if runs < 2 {
		return e.reviewWith(ctx, r, prContext, b)
	}

	type outcome struct {
		findings    []Finding
		escalations []Escalation
		err         error
	}
	outcomes := make([]outcome, runs)
	var wg sync.WaitGroup
	for i := range outcomes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			outcomes[i].findings, outcomes[i].escalations, outcomes[i].err = e.reviewWith(ctx, r, prContext, b)
		}()
	}
	wg.Wait()

	need := runs/2 + 1
	var lists [][]Finding
	var escalations []Escalation
	var errs []error
	for _, o := range outcomes {
		escalations = append(escalations, o.escalations...)
		if o.err != nil {
			errs = append(errs, o.err)
			continue
		}
		lists = append(lists, o.findings)
	}
	if len(lists) < need {
		return nil, escalations, errors.Join(errs...)
	}
	if len(errs) > 0 {
		e.log().Warn("some vote runs failed; counting the rest", "files", b.Paths(),
			"runs", runs, "failed", len(errs), "error", errors.Join(errs...))
	}
	voted := voteFindings(lists, need)
	e.log().Info("votes counted", "files", b.Paths(), "runs", runs, "answered", len(lists),
		"reported", countFindings(lists), "kept", len(voted))
	return voted, escalations, nil
}

func countFindings(lists [][]Finding) int {
	n := 0
	for _, l := range lists {
		n += len(l)
	}
	return n
}

// voteFindings keeps one finding for each defect that at least need of the
// lists report, judged by deepDuplicate, and returns the most confident report
// of it. A list that reports one defect twice counts once.
func voteFindings(lists [][]Finding, need int) []Finding {
	type group struct {
		best  Finding
		lists map[int]bool
	}
	var groups []*group
	for i, list := range lists {
		for _, f := range list {
			var home *group
			for _, g := range groups {
				if deepDuplicate(g.best, f) {
					home = g
					break
				}
			}
			if home == nil {
				groups = append(groups, &group{best: f, lists: map[int]bool{i: true}})
				continue
			}
			home.lists[i] = true
			if confidenceOf(f) > confidenceOf(home.best) {
				home.best = f
			}
		}
	}
	var kept []Finding
	for _, g := range groups {
		if len(g.lists) >= need {
			kept = append(kept, g.best)
		}
	}
	return kept
}
