package vcs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// proposeFake answers the sequence ProposeChange walks, and records what it
// was asked to do. head is what the pull request reports; pushed lets a test
// move it between the two reads.
type proposeFake struct {
	head      string
	movedTo   string
	headRepo  string
	baseRepo  string
	refExists bool

	reads       int
	commitBody  map[string]any
	prBody      map[string]any
	created     string
	deleted     string
	updated     bool
	sameTree    bool
	truncated   bool
	treeEntries []map[string]any

	failSecondRead bool
	failCreatePR   bool
	forbidWrites   bool
	rateLimited    bool
	treeBody       map[string]any
}

func (f *proposeFake) handler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		// The pull request. The second read may report a moved head.
		case r.Method == http.MethodGet && strings.HasSuffix(p, "/pulls/7"):
			f.reads++
			if f.reads > 1 && f.failSecondRead {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			head := f.head
			if f.reads > 1 && f.movedTo != "" {
				head = f.movedTo
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"number": 7,
				"base":   map[string]any{"ref": "main", "sha": "base01", "repo": map[string]any{"full_name": f.baseRepo}},
				"head":   map[string]any{"ref": "topic", "sha": head, "repo": map[string]any{"full_name": f.headRepo}},
			})

		case r.Method == http.MethodGet && strings.Contains(p, "/git/trees/"):
			entries := f.treeEntries
			if entries == nil {
				entries = []map[string]any{
					{"path": "a.go", "mode": "100644", "type": "blob"},
					{"path": "b.go", "mode": "100644", "type": "blob"},
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"sha": "tree-base", "truncated": f.truncated, "tree": entries,
			})

		case strings.Contains(p, "/git/commits/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"sha": f.head, "tree": map[string]any{"sha": "tree-base"}})

		case f.rateLimited && r.Method == http.MethodPost:
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"message":           "API rate limit exceeded",
				"documentation_url": "https://docs.github.com/rest/rate-limit",
			})

		case f.forbidWrites && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]any{"message": "Resource not accessible by integration"})

		case r.Method == http.MethodPost && strings.HasSuffix(p, "/git/trees"):
			_ = json.NewDecoder(r.Body).Decode(&f.treeBody)
			sha := "tree-new"
			if f.sameTree {
				sha = "tree-base"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"sha": sha})

		case r.Method == http.MethodPost && strings.HasSuffix(p, "/git/commits"):
			_ = json.NewDecoder(r.Body).Decode(&f.commitBody)
			_ = json.NewEncoder(w).Encode(map[string]any{"sha": "commit99"})

		case r.Method == http.MethodPost && strings.HasSuffix(p, "/git/refs"):
			if f.refExists {
				w.WriteHeader(http.StatusUnprocessableEntity)
				_ = json.NewEncoder(w).Encode(map[string]any{"message": "Reference already exists"})
				return
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.created, _ = body["ref"].(string)
			_ = json.NewEncoder(w).Encode(map[string]any{"ref": f.created})

		case r.Method == http.MethodPatch && strings.Contains(p, "/git/refs"):
			// The capability must never move an existing ref. Reaching here
			// at all means every other guard is advisory.
			f.updated = true
			t.Error("ProposeChange updated a ref instead of creating one")

		case r.Method == http.MethodDelete && strings.Contains(p, "/git/refs"):
			f.deleted = p

		case r.Method == http.MethodPost && strings.HasSuffix(p, "/pulls"):
			if f.failCreatePR {
				w.WriteHeader(http.StatusUnprocessableEntity)
				_ = json.NewEncoder(w).Encode(map[string]any{"message": "refused"})
				return
			}
			_ = json.NewDecoder(r.Body).Decode(&f.prBody)
			_ = json.NewEncoder(w).Encode(map[string]any{"number": 99, "html_url": "https://example/99"})

		default:
			// Repositories.GetCommit for the head message, and anything else.
			_ = json.NewEncoder(w).Encode(map[string]any{})
		}
	}
}

func goodProposal() Proposal {
	return Proposal{
		Base: "head01", Branch: "nitpick/fix/7-aa11bb22", Into: "topic",
		Message: "fix: guard the nil map", Title: "fix: guard the nil map", Body: "unverified",
		Edits:      []FileEdit{{Path: "a.go", Content: []byte("package a\n")}},
		AllowPaths: []string{"a.go", "b.go"},
	}
}

func propose(t *testing.T, f *proposeFake, p Proposal) (*ProposedChange, error) {
	t.Helper()
	gh := newFakeGitHub(t, f.handler(t))
	return gh.ProposeChange(context.Background(), testRef(), p)
}

// The commit is parented on the revision the edits were read at, the ref is
// created rather than moved, and the pull request targets the branch under
// review as a draft.
func TestProposeChangeParentsOnTheRevisionItRead(t *testing.T) {
	f := &proposeFake{head: "head01", headRepo: "o/r", baseRepo: "o/r"}

	got, err := propose(t, f, goodProposal())
	if err != nil {
		t.Fatalf("ProposeChange: %v", err)
	}
	if got == nil || got.Number != 99 {
		t.Fatalf("change = %+v", got)
	}

	// go-github sends parents as a list of SHA strings, not objects.
	parents, _ := f.commitBody["parents"].([]any)
	if len(parents) != 1 {
		t.Fatalf("parents = %v", f.commitBody["parents"])
	}
	if sha, _ := parents[0].(string); sha != "head01" {
		t.Errorf("parent = %v, want the revision the edits were read at", parents[0])
	}
	if f.created != "refs/heads/nitpick/fix/7-aa11bb22" {
		t.Errorf("created ref = %q", f.created)
	}
	if f.updated {
		t.Error("a ref was updated")
	}
	if base, _ := f.prBody["base"].(string); base != "topic" {
		t.Errorf("pull request base = %q, want the branch under review", base)
	}
	if draft, _ := f.prBody["draft"].(bool); !draft {
		t.Error("unverified model output was proposed as a ready pull request")
	}
}

// A fork is refused inside the capability, not only by the workflow. This
// token cannot push to the fork, so the write would land a branch in the base
// repository carrying fork-authored code under this tool's name.
func TestProposeChangeRefusesAForkPullRequest(t *testing.T) {
	f := &proposeFake{head: "head01", headRepo: "someone/r", baseRepo: "o/r"}

	_, err := propose(t, f, goodProposal())
	if err == nil {
		t.Fatal("a fork pull request was accepted")
	}
	if !errors.Is(err, ErrForbidden) {
		t.Errorf("err = %v, want ErrForbidden", err)
	}
	if f.created != "" {
		t.Errorf("a branch was created anyway: %s", f.created)
	}
}

// Whole files are written, so a proposal whose base is stale would re-assert
// content over a newer tree and revert whatever landed in between.
func TestProposeChangeRefusesAStaleBase(t *testing.T) {
	f := &proposeFake{head: "head02", headRepo: "o/r", baseRepo: "o/r"}

	_, err := propose(t, f, goodProposal()) // proposal reads head01
	if !errors.Is(err, ErrHeadMoved) {
		t.Fatalf("err = %v, want ErrHeadMoved", err)
	}
	if f.created != "" {
		t.Errorf("a branch was created against a stale base: %s", f.created)
	}
}

// A push that lands while the proposal is being built is caught after the ref
// is created, and the branch is removed rather than left pointing at a commit
// that reverts someone.
func TestProposeChangeUnwindsWhenTheHeadMovesUnderIt(t *testing.T) {
	f := &proposeFake{head: "head01", movedTo: "head02", headRepo: "o/r", baseRepo: "o/r"}

	_, err := propose(t, f, goodProposal())
	if !errors.Is(err, ErrHeadMoved) {
		t.Fatalf("err = %v, want ErrHeadMoved", err)
	}
	if f.deleted == "" {
		t.Error("the branch was left behind")
	}
	if f.prBody != nil {
		t.Error("a pull request was opened against a moved head")
	}
}

// Paths the pull request does not change are refused. Without this the
// capability is an arbitrary write driven by text a contributor authored.
func TestProposeChangeRefusesPathsOutsideTheChange(t *testing.T) {
	for name, edit := range map[string]FileEdit{
		"not in the change": {Path: "elsewhere.go", Content: []byte("x")},
		"a workflow":        {Path: ".github/workflows/ci.yml", Content: []byte("x")},
		"the config":        {Path: ".nitpick.yaml", Content: []byte("x")},
		"leaves the repo":   {Path: "../../etc/passwd", Content: []byte("x")},
		"absolute":          {Path: "/etc/passwd", Content: []byte("x")},
		"a backslash":       {Path: `a\..\b.go`, Content: []byte("x")},
		"deletes a file":    {Path: "a.go", Content: nil},
	} {
		t.Run(name, func(t *testing.T) {
			f := &proposeFake{head: "head01", headRepo: "o/r", baseRepo: "o/r"}
			p := goodProposal()
			p.Edits = []FileEdit{edit}

			if _, err := propose(t, f, p); err == nil {
				t.Fatal("accepted")
			}
			if f.created != "" {
				t.Errorf("a branch was created anyway: %s", f.created)
			}
		})
	}
}

// An empty allowlist refuses everything: it is a containment boundary, so its
// absence fails closed rather than open.
func TestAnEmptyAllowlistRefusesEveryEdit(t *testing.T) {
	f := &proposeFake{head: "head01", headRepo: "o/r", baseRepo: "o/r"}
	p := goodProposal()
	p.AllowPaths = nil

	if _, err := propose(t, f, p); !errors.Is(err, ErrOutsideChange) {
		t.Fatalf("err = %v, want ErrOutsideChange", err)
	}
}

// A branch name that did not come from this package's own derivation is
// refused, so nothing model-authored reaches a ref.
func TestProposeChangeRefusesABranchNameItDidNotDerive(t *testing.T) {
	for _, branch := range []string{"main", "topic", "nitpick/fix", "../evil", "nitpick/fix/../x", "NITPICK/fix/7-a"} {
		f := &proposeFake{head: "head01", headRepo: "o/r", baseRepo: "o/r"}
		p := goodProposal()
		p.Branch = branch

		if _, err := propose(t, f, p); err == nil {
			t.Errorf("branch %q was accepted", branch)
		}
	}
}

// Nothing changed is not a pull request. Caught before the branch exists,
// rather than as a refused empty comparison after one was created.
func TestProposeChangeThatChangesNothingCreatesNothing(t *testing.T) {
	f := &proposeFake{head: "head01", headRepo: "o/r", baseRepo: "o/r", sameTree: true}

	got, err := propose(t, f, goodProposal())
	if err != nil {
		t.Fatalf("ProposeChange: %v", err)
	}
	if got != nil {
		t.Errorf("change = %+v, want nothing", got)
	}
	if f.created != "" {
		t.Errorf("a branch was created for no change: %s", f.created)
	}
}

// The same ask twice produces the same branch name, so an existing ref is the
// repeat rather than a collision to work around.
func TestProposeChangeReportsAnExistingBranch(t *testing.T) {
	f := &proposeFake{head: "head01", headRepo: "o/r", baseRepo: "o/r", refExists: true}

	if _, err := propose(t, f, goodProposal()); !errors.Is(err, ErrRefExists) {
		t.Fatalf("err = %v, want ErrRefExists", err)
	}
}

// A fork the forge does not name is refused. GitHub reports a null head
// repository for a fork that has since been deleted, and its head commit is
// still reachable through the base repository's network, so treating unknown
// as same is the fork case wearing a blank name.
func TestProposeChangeRefusesAnUnnamedHeadRepository(t *testing.T) {
	// Both cases, and the second is the one only this guard catches: with a
	// base repository present the inequality below it would refuse an empty
	// head anyway, so a test that stopped there would pass with the guard
	// removed.
	for name, f := range map[string]*proposeFake{
		"head unnamed":     {head: "head01", headRepo: "", baseRepo: "o/r"},
		"neither reported": {head: "head01", headRepo: "", baseRepo: ""},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := propose(t, f, goodProposal())
			if !errors.Is(err, ErrForbidden) {
				t.Fatalf("err = %v, want ErrForbidden", err)
			}
			if f.created != "" {
				t.Errorf("a branch was created anyway: %s", f.created)
			}
		})
	}
}

// The mode comes from the tree. Writing every edit as a regular file clears
// the executable bit on a script, and the pull request looks clean until the
// file is run.
func TestProposeChangeKeepsTheModeItFound(t *testing.T) {
	f := &proposeFake{head: "head01", headRepo: "o/r", baseRepo: "o/r",
		treeEntries: []map[string]any{{"path": "a.go", "mode": "100755", "type": "blob"}}}

	if _, err := propose(t, f, goodProposal()); err != nil {
		t.Fatalf("ProposeChange: %v", err)
	}

	tree, _ := f.treeBody["tree"].([]any)
	if len(tree) != 1 {
		t.Fatalf("tree = %v", f.treeBody["tree"])
	}
	if mode, _ := tree[0].(map[string]any)["mode"].(string); mode != "100755" {
		t.Errorf("mode = %q, want the executable bit kept", mode)
	}
}

// A symlink's content is its target and a submodule's is a revision. Neither
// is a file a code-edit model should rewrite.
func TestProposeChangeRefusesAModeItDoesNotWrite(t *testing.T) {
	for name, mode := range map[string]string{"a symlink": "120000", "a submodule": "160000"} {
		t.Run(name, func(t *testing.T) {
			f := &proposeFake{head: "head01", headRepo: "o/r", baseRepo: "o/r",
				treeEntries: []map[string]any{{"path": "a.go", "mode": mode, "type": "blob"}}}

			if _, err := propose(t, f, goodProposal()); err == nil {
				t.Fatal("accepted")
			}
			if f.created != "" {
				t.Errorf("a branch was created anyway: %s", f.created)
			}
		})
	}
}

// A path the revision does not hold would read as "this file is new", which
// is the one case a proposal must not accept.
func TestProposeChangeRefusesAPathTheRevisionDoesNotHold(t *testing.T) {
	f := &proposeFake{head: "head01", headRepo: "o/r", baseRepo: "o/r",
		treeEntries: []map[string]any{{"path": "b.go", "mode": "100644", "type": "blob"}}}

	if _, err := propose(t, f, goodProposal()); err == nil {
		t.Fatal("a path absent from the tree was accepted")
	}
}

// A tree too large to read whole is refused rather than half-used.
func TestProposeChangeRefusesATruncatedTree(t *testing.T) {
	f := &proposeFake{head: "head01", headRepo: "o/r", baseRepo: "o/r", truncated: true}

	if _, err := propose(t, f, goodProposal()); err == nil {
		t.Fatal("a truncated tree was used")
	}
}

// A re-read that fails is treated as moved. Opening the pull request with no
// check at all on a transient error is the failure the guard exists to
// prevent, arriving through a rate limit rather than a push.
func TestProposeChangeTreatsAFailedRereadAsMoved(t *testing.T) {
	f := &proposeFake{head: "head01", headRepo: "o/r", baseRepo: "o/r", failSecondRead: true}

	_, err := propose(t, f, goodProposal())
	if !errors.Is(err, ErrHeadMoved) {
		t.Fatalf("err = %v, want ErrHeadMoved", err)
	}
	if f.prBody != nil {
		t.Error("a pull request was opened without the re-read succeeding")
	}
	if f.deleted == "" {
		t.Error("the branch was left behind")
	}
}

// A pull request that could not be opened takes its branch with it. Left
// behind, the ref has nothing to link to, and the next identical ask reports
// it as an earlier proposal that does not exist.
func TestProposeChangeUnwindsWhenThePullRequestIsRefused(t *testing.T) {
	f := &proposeFake{head: "head01", headRepo: "o/r", baseRepo: "o/r", failCreatePR: true}

	if _, err := propose(t, f, goodProposal()); err == nil {
		t.Fatal("a refused pull request reported success")
	}
	if f.deleted == "" {
		t.Error("the branch was left behind with no pull request")
	}
}

// A forge that refuses the write is reported as a missing permission rather
// than as a bare status, because the two lead somewhere different: one is a
// setting to change, the other is a request to repeat.
//
// The branch must not exist afterwards either. A 403 on the first write means
// nothing was created, and a test that only checked the error would pass just
// as well if the code created the ref and then failed.
func TestProposeChangeNamesARefusedCredential(t *testing.T) {
	f := &proposeFake{head: "head01", headRepo: "o/r", baseRepo: "o/r", forbidWrites: true}
	change, err := propose(t, f, goodProposal())
	if !errors.Is(err, ErrNoWriteAccess) {
		t.Fatalf("error = %v, want ErrNoWriteAccess", err)
	}
	if change != nil {
		t.Errorf("change = %+v, want nil", change)
	}
	if f.created != "" {
		t.Errorf("created ref %q after a refused write", f.created)
	}
	// The forge's own answer has to survive, or the run log cannot tell a
	// missing permission from the other things that arrive as 403.
	if !strings.Contains(err.Error(), "not accessible by integration") {
		t.Errorf("error = %v, want the forge response kept", err)
	}
}

// A rate limit is a 403 and is not a permission problem. Reporting it as one
// sends the reader to the App settings to wait out a number that resets on its
// own.
//
// This passes because go-github decodes a rate-limited 403 into its own type
// rather than an ErrorResponse, not because writeErr tests for it. The test is
// here to fail if that ever changes.
func TestProposeChangeDoesNotCallARateLimitAPermission(t *testing.T) {
	f := &proposeFake{head: "head01", headRepo: "o/r", baseRepo: "o/r", rateLimited: true}
	_, err := propose(t, f, goodProposal())
	if err == nil {
		t.Fatal("a rate-limited write succeeded")
	}
	if errors.Is(err, ErrNoWriteAccess) {
		t.Errorf("a rate limit was reported as ErrNoWriteAccess: %v", err)
	}
}

// A write that fails for any other reason keeps its own error. Mapping every
// failure to a permission problem would send the reader to the App settings
// for an outage.
func TestProposeChangeDoesNotCallEveryFailureAPermission(t *testing.T) {
	f := &proposeFake{head: "head01", headRepo: "o/r", baseRepo: "o/r", failCreatePR: true}
	if _, err := propose(t, f, goodProposal()); errors.Is(err, ErrNoWriteAccess) {
		t.Fatalf("a 422 was reported as ErrNoWriteAccess: %v", err)
	}
}
