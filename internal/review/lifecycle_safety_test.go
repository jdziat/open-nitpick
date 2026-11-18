package review

import (
	"context"
	"errors"
	"testing"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

func TestRenamedAndMovedPathsDoNotCloseUnreadThreads(t *testing.T) {
	for _, newPath := range []string{"renamed.go", "moved/old.go"} {
		provider := &resolvingProvider{}
		engine := newEngine(t, &scriptedLLM{}, provider, nil)
		prior := &vcs.PriorReview{Head: "previous", Comments: []vcs.PriorComment{{ID: 1, Path: "old.go", Fingerprint: "old-finding"}}}
		resolved := engine.superseded(t.Context(), vcs.Ref{}, prior, &Incremental{Since: "previous", Reviewed: []string{newPath}}, nil, nil, nil)
		if len(resolved) != 0 || len(provider.resolved) != 0 {
			t.Fatalf("new path %s closed an unread old thread: %+v", newPath, resolved)
		}
	}
}

func TestRecurringAndWithheldFindingsKeepTheirExistingThreads(t *testing.T) {
	finding := Finding{Path: "a.go", Line: 2, Title: "Resource is not closed"}
	for _, withheld := range []bool{false, true} {
		provider := &resolvingProvider{}
		engine := newEngine(t, &scriptedLLM{}, provider, nil)
		prior := &vcs.PriorReview{Head: "previous", Comments: []vcs.PriorComment{{ID: 1, Path: finding.Path, Fingerprint: Fingerprint(finding)}}}
		findings, held := []Finding{finding}, []Finding(nil)
		if withheld {
			held, findings = findings, nil
		}
		if resolved := engine.superseded(t.Context(), vcs.Ref{}, prior, &Incremental{Since: "previous", Reviewed: []string{finding.Path}}, findings, held, nil); len(resolved) != 0 || len(provider.resolved) != 0 {
			t.Fatalf("recurrence closed a thread: %+v", resolved)
		}
	}
}

type partialResolutionProvider struct {
	resolvingProvider
	confirmed []int64
}

func (p *partialResolutionProvider) ResolveThreads(context.Context, vcs.Ref, []int64, string) ([]int64, error) {
	return p.confirmed, errors.New("forge denied remaining thread mutations")
}

func TestDeniedAndPartialThreadMutationsCannotPermitApproval(t *testing.T) {
	for _, confirmed := range [][]int64{nil, {1}} {
		provider := &partialResolutionProvider{confirmed: confirmed}
		engine := newEngine(t, &scriptedLLM{}, provider, func(c *config.Config) { c.Review.Approve.Enabled = true })
		prior := &vcs.PriorReview{Head: "previous", Comments: []vcs.PriorComment{{ID: 1, Path: "a.go", Fingerprint: "a"}, {ID: 2, Path: "b.go", Fingerprint: "b"}}}
		inc := &Incremental{Since: "previous", Reviewed: []string{"a.go", "b.go"}}
		resolved := engine.superseded(t.Context(), vcs.Ref{}, prior, inc, nil, nil, nil)
		if len(resolved) != len(confirmed) {
			t.Fatalf("unconfirmed roots were marked resolved: %+v", resolved)
		}
		report := &Report{PriorComments: 2, Incremental: inc, Superseded: resolved}
		if reviewEvent(report, engine.Config) != vcs.EventComment {
			t.Fatal("unconfirmed threads allowed approval")
		}
	}
}

func TestOutdatedThreadsRequireReviewedPathsBeforeResolution(t *testing.T) {
	provider := &resolvingProvider{}
	engine := newEngine(t, &scriptedLLM{fallback: mustJSON(t, Result{})}, provider, nil)
	prior := &vcs.PriorReview{Head: "previous", Comments: []vcs.PriorComment{
		{ID: 1, Path: "reviewed.go", Line: 0, Fingerprint: "reviewed"},
		{ID: 2, Path: "unread.go", Line: 0, Fingerprint: "unread"},
		{ID: 3, Path: "skipped.go", Line: 0, Fingerprint: "skipped"},
	}}
	inc := &Incremental{Since: "previous", Reviewed: []string{"reviewed.go", "skipped.go"}}
	got := engine.superseded(t.Context(), vcs.Ref{}, prior, inc, nil, nil, []bundle.Skip{{Path: "skipped.go"}})
	if len(got) != 1 || got[0].ID != 1 || len(provider.resolved) != 1 || provider.resolved[0] != 1 {
		t.Fatalf("unreviewed outdated thread was closed: resolved=%+v calls=%v", got, provider.resolved)
	}
}

func TestCleanApprovalCannotResolveUnreadOutdatedThreads(t *testing.T) {
	provider := &resolvingProvider{}
	engine := newEngine(t, &scriptedLLM{fallback: mustJSON(t, Result{})}, provider, func(c *config.Config) {
		c.Review.Approve.Enabled = true
	})
	prior := &vcs.PriorReview{Head: "previous", Comments: []vcs.PriorComment{
		{ID: 1, Path: "reviewed.go", Line: 0, Fingerprint: "reviewed"},
		{ID: 2, Path: "unread.go", Line: 0, Fingerprint: "unread"},
	}}
	report := &Report{PriorComments: 2, Incremental: &Incremental{Since: "previous", Reviewed: []string{"reviewed.go"}}}
	report.Superseded = engine.resolveClearedForApprove(t.Context(), vcs.Ref{}, prior, report, nil, nil)
	if len(report.Superseded) != 1 || report.Superseded[0].ID != 1 {
		t.Fatalf("clean approval resolved an unread thread: %+v", report.Superseded)
	}
	if got := reviewEvent(report, engine.Config); got != vcs.EventComment {
		t.Fatalf("unread standing thread allowed %s", got)
	}
}
