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

	reads      int
	commitBody map[string]any
	prBody     map[string]any
	created    string
	deleted    string
	updated    bool
	sameTree   bool
}

func (f *proposeFake) handler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		// The pull request. The second read may report a moved head.
		case r.Method == http.MethodGet && strings.HasSuffix(p, "/pulls/7"):
			f.reads++
			head := f.head
			if f.reads > 1 && f.movedTo != "" {
				head = f.movedTo
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"number": 7,
				"base":   map[string]any{"ref": "main", "sha": "base01", "repo": map[string]any{"full_name": f.baseRepo}},
				"head":   map[string]any{"ref": "topic", "sha": head, "repo": map[string]any{"full_name": f.headRepo}},
			})

		case strings.Contains(p, "/git/commits/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"sha": f.head, "tree": map[string]any{"sha": "tree-base"}})

		case r.Method == http.MethodPost && strings.HasSuffix(p, "/git/trees"):
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
