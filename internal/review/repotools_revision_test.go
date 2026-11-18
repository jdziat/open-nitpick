package review

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/vcs"
)

func commitLookupTree(tb testing.TB, root string) string {
	tb.Helper()
	for _, args := range [][]string{
		{"add", "-A"},
		{"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "commit", "-qm", "snapshot"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			tb.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		tb.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func historicalLookupTree(t *testing.T) (string, vcs.Ref) {
	t.Helper()
	root := gitRepo(t, map[string]string{
		"example.go": "package p\nvar Reviewed = true\n",
		"removed.go": "package p\nvar Removed = true\n",
	})
	head := commitLookupTree(t, root)
	for name, content := range map[string]string{
		"example.go": "package p\nvar Newer = true\n",
		"added.go":   "package p\nvar Added = true\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(filepath.Join(root, "removed.go")); err != nil {
		t.Fatal(err)
	}
	commitLookupTree(t, root)
	for name, content := range map[string]string{
		"example.go":   "package p\nvar Dirty = true\n",
		"untracked.go": "package p\nvar Untracked = true\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root, vcs.Ref{Head: head}
}

func TestRepositoryReadsUseTheReviewedCommitDespiteCheckoutChanges(t *testing.T) {
	root, ref := historicalLookupTree(t)
	tools := newRepoTools(root, nil, ref)
	for name, want := range map[string]string{"example.go": "2 var Reviewed = true", "removed.go": "2 var Removed = true"} {
		if got := tools.read(t.Context(), name, 1, 5); !strings.Contains(got, want) {
			t.Errorf("read %s: %q, want evidence from %s: %q", name, got, ref.Head, want)
		}
	}
	if got := tools.read(t.Context(), "added.go", 1, 5); !strings.HasPrefix(got, "no such file:") {
		t.Errorf("a file added after the reviewed commit became evidence: %q", got)
	}
}

func TestRepositorySearchUsesOnlyTheReviewedCommitAndNormalPaths(t *testing.T) {
	root, ref := historicalLookupTree(t)
	tools := newRepoTools(root, &vcs.Local{Dir: root}, ref)
	want := "example.go:2:var Reviewed = true\nremoved.go:2:var Removed = true"
	if got := tools.grep(t.Context(), "var ", ""); got != want {
		t.Errorf("search = %q, want %q: another checkout must not supply evidence", got, want)
	}
	if got := tools.grep(t.Context(), "Dirty|Newer|Added|Untracked", ""); got != "(no matches)" {
		t.Errorf("later or uncommitted code leaked into a negative search: %q", got)
	}
}

func TestRepositoryWorktreeLookupsStillIncludeUncommittedEvidence(t *testing.T) {
	root, _ := historicalLookupTree(t)
	tools := newRepoTools(root, nil, vcs.Ref{})
	if got := tools.read(t.Context(), "example.go", 1, 5); !strings.Contains(got, "Dirty") {
		t.Fatalf("worktree read = %q, want uncommitted code", got)
	}
	if got := tools.read(t.Context(), "untracked.go", 1, 5); !strings.Contains(got, "Untracked") {
		t.Fatalf("worktree read of a new file = %q, want its source", got)
	}
	if got := tools.grep(t.Context(), "Dirty", ""); !strings.Contains(got, "example.go:2:var Dirty = true") {
		t.Fatalf("worktree search = %q, want the edited tracked file", got)
	}
}

func TestRepositoryLookupsNeverSubstituteCheckoutForMissingRevision(t *testing.T) {
	root, _ := historicalLookupTree(t)
	tools := newRepoTools(root, nil, vcs.Ref{Head: strings.Repeat("0", 40)})
	if got := tools.read(t.Context(), "example.go", 1, 5); !strings.HasPrefix(got, "no such file:") {
		t.Errorf("read with an unavailable revision returned checkout evidence: %q", got)
	}
	if got := tools.grep(t.Context(), "Dirty", ""); !strings.HasPrefix(got, "the search failed:") {
		t.Errorf("search with an unavailable revision returned checkout evidence: %q", got)
	}
}
