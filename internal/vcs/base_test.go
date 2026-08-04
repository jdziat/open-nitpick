package vcs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// gitIn runs a git command in dir, failing the test on error.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=t@example.com",
	)

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestLocalBaseRevisionMatchesTheDiffRange is the property that makes the base
// revision usable at all: a file read there has to be the file the diff
// subtracted. Diff uses a three-dot range, so the base is the fork point — the
// tip of the base branch has moved on and describes a state this change was
// never measured against.
func TestLocalBaseRevisionMatchesTheDiffRange(t *testing.T) {
	dir := newRepo(t)

	gitIn(t, dir, "checkout", "-qb", "feature")
	write(t, dir, "a.go", "package a\n\nfunc F() int {\n\treturn 2\n}\n")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-qm", "feature change")

	gitIn(t, dir, "checkout", "-q", "main")
	write(t, dir, "other.go", "package a\n\nvar Unrelated = true\n")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-qm", "unrelated main change")

	got, err := NewLocal(dir, nil).BaseRevision(context.Background(), Ref{Base: "main", Head: "feature"})
	if err != nil {
		t.Fatalf("BaseRevision: %v", err)
	}

	if want := gitIn(t, dir, "merge-base", "main", "feature"); got != want {
		t.Errorf("BaseRevision = %s, want the merge base %s", got, want)
	}
	if tip := gitIn(t, dir, "rev-parse", "main"); got == tip {
		t.Error("BaseRevision returned the base branch tip, which the three-dot diff never compared against")
	}
}

// TestLocalBaseRevisionOfTheWorkingTreeSkipsUncommittedEdits is the whole point
// of resolving policy at a base: reading through it must not return the version
// the author is currently writing.
func TestLocalBaseRevisionOfTheWorkingTreeSkipsUncommittedEdits(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "a.go", "package a\n// edited\n")

	local := NewLocal(dir, nil)
	ctx := context.Background()

	base, err := BaseRevision(ctx, local, Ref{})
	if err != nil {
		t.Fatalf("BaseRevision: %v", err)
	}
	if want := gitIn(t, dir, "rev-parse", "HEAD"); base != want {
		t.Errorf("BaseRevision = %s, want HEAD %s", base, want)
	}

	got, err := local.FileContent(ctx, Ref{}.At(base), "a.go")
	if err != nil {
		t.Fatalf("FileContent: %v", err)
	}
	if strings.Contains(string(got), "// edited") {
		t.Errorf("reading at the base returned the uncommitted version:\n%s", got)
	}
}

func TestLocalBaseRevisionOfAnExplicitHead(t *testing.T) {
	dir := newRepo(t)
	// `git diff <head>` compares the working tree against head, so head is what
	// the change was measured against.
	write(t, dir, "a.go", "package a\n// edited\n")

	got, err := NewLocal(dir, nil).BaseRevision(context.Background(), Ref{Head: "HEAD"})
	if err != nil {
		t.Fatalf("BaseRevision: %v", err)
	}
	if want := gitIn(t, dir, "rev-parse", "HEAD"); got != want {
		t.Errorf("BaseRevision = %s, want %s", got, want)
	}
}

// noBaseProvider is a Provider with no way to name a base revision, which is
// what a wrapper or a test double that never forwarded the capability looks
// like from the outside.
type noBaseProvider struct{}

func (noBaseProvider) Name() string { return "nobase" }

func (noBaseProvider) PullRequest(context.Context, Ref) (*PullRequest, error) {
	return &PullRequest{}, nil
}
func (noBaseProvider) Diff(context.Context, Ref) ([]byte, error) { return nil, nil }
func (noBaseProvider) FileContent(context.Context, Ref, string) ([]byte, error) {
	return nil, ErrNotFound
}
func (noBaseProvider) PublishReview(context.Context, Ref, Review) error { return nil }

// emptyBaseProvider names a blank base revision, the way a half-implemented
// resolver would.
type emptyBaseProvider struct{ noBaseProvider }

func (emptyBaseProvider) BaseRevision(context.Context, Ref) (string, error) { return "  ", nil }

func TestBaseRevisionReportsProvidersThatCannotAnswer(t *testing.T) {
	_, err := BaseRevision(context.Background(), noBaseProvider{}, Ref{})
	if !errors.Is(err, ErrNoBaseRevision) {
		t.Fatalf("err = %v, want ErrNoBaseRevision", err)
	}
	// The caller has to be able to name the provider it could not ask.
	if !strings.Contains(err.Error(), "nobase") {
		t.Errorf("error should name the provider, got: %v", err)
	}
}

// TestBaseRevisionRefusesAnEmptyRevision: Ref.At("") is the working tree for
// the local provider, so passing an empty revision through would read exactly
// the changed version the caller is trying to avoid.
func TestBaseRevisionRefusesAnEmptyRevision(t *testing.T) {
	if _, err := BaseRevision(context.Background(), emptyBaseProvider{}, Ref{}); !errors.Is(err, ErrNoBaseRevision) {
		t.Fatalf("err = %v, want ErrNoBaseRevision for a blank revision", err)
	}
}

func TestRefAtMovesTheHeadOnly(t *testing.T) {
	ref := Ref{Owner: "o", Repo: "r", Number: 7, Base: "main", Head: "feature"}

	at := ref.At("3f2a1b")
	if at.Head != "3f2a1b" {
		t.Errorf("Head = %q, want the requested revision", at.Head)
	}
	// FileContent reads Head; the rest still has to identify the repository.
	if at.Owner != "o" || at.Repo != "r" || at.Number != 7 || at.Base != "main" {
		t.Errorf("At dropped identifying fields: %+v", at)
	}
	if ref.Head != "feature" {
		t.Error("At mutated the receiver")
	}
}

func TestGitHubBaseRevisionPrefersTheBaseSHA(t *testing.T) {
	gh := newFakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"number": 7,
			"base":   map[string]any{"ref": "main", "sha": "basesha1"},
			"head":   map[string]any{"ref": "feature", "sha": "headsha1"},
		})
	})

	got, err := BaseRevision(context.Background(), gh, testRef())
	if err != nil {
		t.Fatalf("BaseRevision: %v", err)
	}
	// The branch name resolves to whatever has landed since; the SHA is the
	// state this pull request was actually measured against.
	if got != "basesha1" {
		t.Errorf("BaseRevision = %q, want the base SHA", got)
	}
}

func TestGitHubBaseRevisionFallsBackToTheBranchName(t *testing.T) {
	gh := newFakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"number": 7,
			"base":   map[string]any{"ref": "main"},
			"head":   map[string]any{"ref": "feature", "sha": "headsha1"},
		})
	})

	got, err := BaseRevision(context.Background(), gh, testRef())
	if err != nil {
		t.Fatalf("BaseRevision: %v", err)
	}
	if got != "main" {
		t.Errorf("BaseRevision = %q, want the base branch when no SHA is reported", got)
	}
}
