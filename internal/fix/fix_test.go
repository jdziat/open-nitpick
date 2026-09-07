package fix

import (
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/vcs"
)

func req() Request {
	return Request{
		Findings: []Finding{{Path: "a.go", Line: 12, Body: "Guard the nil map", Fingerprint: "aa11bb22cc33"}},
		Files:    map[string]string{"a.go": "package a\n"},
	}
}

// The same ask twice produces the same branch, which is how a repeat is
// recognised rather than piling up branches, and the name matches the pattern
// the write path will accept.
func TestBranchIsDerivedAndRepeatable(t *testing.T) {
	if a, b := Branch(7, "aa11bb22cc33"), Branch(7, "aa11bb22cc33"); a != b {
		t.Errorf("%q and %q differ for the same ask", a, b)
	}
	if got := Branch(7, "aa11bb22cc33"); got != "nitpick/fix/7-aa11bb22" {
		t.Errorf("branch = %q", got)
	}
	if got := Branch(7, ""); got != "nitpick/fix/7-all" {
		t.Errorf("a whole pass = %q", got)
	}
	if a, b := Branch(7, "aa11"), Branch(8, "aa11"); a == b {
		t.Error("two pull requests share a branch name")
	}
}

// Nothing to publish is a result, not an empty proposal.
func TestNoEditsIsNotAProposal(t *testing.T) {
	if _, ok := Proposal(req(), Result{}, &vcs.PullRequest{Number: 7}, "nitpick/fix/7-a", "body"); ok {
		t.Error("a result with no edits produced a proposal")
	}
}

// The allowlist is the files the pass was given, so the write cannot reach a
// path nobody handed it even if the model names one.
func TestAProposalAllowsOnlyTheFilesItWasGiven(t *testing.T) {
	r := req()
	r.Files["b.go"] = "package b\n"
	res := Result{Edits: map[string]string{"a.go": "package a // fixed\n"}}

	p, ok := Proposal(r, res, &vcs.PullRequest{Number: 7, HeadSHA: "head01", HeadRef: "topic"}, "nitpick/fix/7-a", "body")
	if !ok {
		t.Fatal("no proposal")
	}
	if len(p.Edits) != 1 || p.Edits[0].Path != "a.go" {
		t.Errorf("edits = %+v", p.Edits)
	}
	if len(p.AllowPaths) != 2 {
		t.Errorf("allow = %v, want every file the pass was given", p.AllowPaths)
	}
	if p.Base != "head01" || p.Into != "topic" {
		t.Errorf("base = %q, into = %q; the proposal must target the branch under review", p.Base, p.Into)
	}
}

// The body says what was not verified, in those words. It is the only place a
// reader learns that nothing here was compiled.
func TestTheBodySaysWhatWasNotVerified(t *testing.T) {
	res := Result{
		Edits:   map[string]string{"a.go": "package a // fixed\n"},
		Skipped: map[string]string{"b.go": "the finding names a caller I was not given"},
	}

	body := Body(req(), res, "head01deadbeef", "jdziat", 7)

	for _, want := range []string{
		"Not compiled, not run, not tested",
		"checks on this pull request are the only verification",
		"head01deadbeef",
		"reverted here",
		"`a.go`",
		"the finding names a caller I was not given",
		"aa11bb22cc33",
		"@jdziat",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the body is missing %q:\n%s", want, body)
		}
	}

	// It must not claim the finding is fixed.
	for _, banned := range []string{"fixes the", "resolves the", "verified"} {
		if strings.Contains(strings.ToLower(body), banned) {
			t.Errorf("the body claims more than it knows, containing %q:\n%s", banned, body)
		}
	}
}
