package main

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"log/slog"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/converse"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

type fakeDismissalForge struct {
	author  string
	files   map[string]string // file content by path, read at the base only
	teams   map[string]bool   // membership by "org/slug/login"
	teamErr error
	baseErr error
	readAt  []string
}

func (f *fakeDismissalForge) PullRequest(context.Context, vcs.Ref) (*vcs.PullRequest, error) {
	return &vcs.PullRequest{Author: f.author}, nil
}

func (f *fakeDismissalForge) BaseRevision(context.Context, vcs.Ref) (string, error) {
	return "base-sha", f.baseErr
}

func (f *fakeDismissalForge) FileContent(_ context.Context, ref vcs.Ref, path string) ([]byte, error) {
	f.readAt = append(f.readAt, ref.Head)
	if c, ok := f.files[path]; ok {
		return []byte(c), nil
	}
	return nil, vcs.ErrNotFound
}

func (f *fakeDismissalForge) IsTeamMember(_ context.Context, org, slug, login string) (bool, error) {
	return f.teams[org+"/"+slug+"/"+login], f.teamErr
}

func TestMayDismissFollowsCodeownersAndTheAuthorDefault(t *testing.T) {
	owners := map[string]string{".github/CODEOWNERS": "*.go @org/core @alice\n/docs/ @bob\n"}
	off := false
	cases := []struct {
		name   string
		forge  *fakeDismissalForge
		cfg    config.DismissRespond
		login  string
		path   string
		want   bool
		reason string
	}{
		{"a stranger to an owned path is refused",
			&fakeDismissalForge{author: "carol", files: owners}, config.DismissRespond{}, "dave", "a.go", false, "owned by"},
		{"a named owner may close it",
			&fakeDismissalForge{author: "carol", files: owners}, config.DismissRespond{}, "Alice", "a.go", true, ""},
		{"a team member may close it",
			&fakeDismissalForge{author: "carol", files: owners, teams: map[string]bool{"org/core/dave": true}}, config.DismissRespond{}, "dave", "a.go", true, ""},
		{"the author may close findings on their own change by default",
			&fakeDismissalForge{author: "carol", files: owners}, config.DismissRespond{}, "carol", "a.go", true, ""},
		{"the author is refused when allow_author is off",
			&fakeDismissalForge{author: "carol", files: owners}, config.DismissRespond{AllowAuthor: &off}, "carol", "a.go", false, "on their own change"},
		{"an author who is also an owner is refused when allow_author is off",
			&fakeDismissalForge{author: "alice", files: owners}, config.DismissRespond{AllowAuthor: &off}, "alice", "a.go", false, "on their own change"},
		{"allow_author off holds under association authority too",
			&fakeDismissalForge{author: "carol", files: owners}, config.DismissRespond{Authority: config.AuthorityAssociation, AllowAuthor: &off}, "carol", "a.go", false, "on their own change"},
		{"association authority with allow_author off still admits other collaborators",
			&fakeDismissalForge{author: "carol", files: owners}, config.DismissRespond{Authority: config.AuthorityAssociation, AllowAuthor: &off}, "dave", "a.go", true, ""},
		{"a path no rule covers falls back to the association gate",
			&fakeDismissalForge{author: "carol", files: owners}, config.DismissRespond{}, "dave", "notes.txt", true, ""},
		{"no CODEOWNERS falls back to the association gate",
			&fakeDismissalForge{author: "carol"}, config.DismissRespond{}, "dave", "a.go", true, ""},
		{"association authority admits a stranger to an owned path",
			&fakeDismissalForge{author: "carol", files: owners}, config.DismissRespond{Authority: config.AuthorityAssociation}, "dave", "a.go", true, ""},
		{"a failed team lookup does not refuse an owner named directly",
			&fakeDismissalForge{author: "carol", files: owners, teamErr: errors.New("403")}, config.DismissRespond{}, "alice", "a.go", true, ""},
		{"a failed team lookup refuses",
			&fakeDismissalForge{author: "carol", files: owners, teamErr: errors.New("403")}, config.DismissRespond{}, "dave", "a.go", false, "could not be checked"},
		{"an unresolvable base refuses",
			&fakeDismissalForge{author: "carol", files: owners, baseErr: errors.New("gone")}, config.DismissRespond{}, "dave", "a.go", false, "could not be read"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, reason, _ := mayDismiss(context.Background(), c.forge, c.cfg, vcs.Ref{Owner: "o", Repo: "r", Number: 1}, c.login, c.path)
			if got != c.want {
				t.Fatalf("allowed = %v (%s), want %v", got, reason, c.want)
			}
			if c.reason != "" && !strings.Contains(reason, c.reason) {
				t.Errorf("reason = %q, want it to mention %q", reason, c.reason)
			}
		})
	}
}

// The change under review can edit CODEOWNERS to name its own author. The file
// has to be read at the base, so that edit is not in force for this decision.
func TestMayDismissReadsCodeownersAtTheBaseRevisionOnly(t *testing.T) {
	f := &fakeDismissalForge{author: "carol", files: map[string]string{".github/CODEOWNERS": "* @alice\n"}}
	_, _, _ = mayDismiss(context.Background(), f, config.DismissRespond{}, vcs.Ref{Owner: "o", Repo: "r", Number: 1}, "dave", "a.go")
	if len(f.readAt) == 0 {
		t.Fatal("CODEOWNERS was never read, so the test proves nothing")
	}
	for _, at := range f.readAt {
		if at != "base-sha" {
			t.Errorf("CODEOWNERS read at %q, want the base revision", at)
		}
	}
}

type replyingForge struct {
	fakeDismissalForge
	replies []string
}

func (f *replyingForge) ReplyToReviewComment(_ context.Context, _ vcs.Ref, _ int64, body string) error {
	f.replies = append(f.replies, body)
	return nil
}

func TestARefusedReplyIsSilentAndARefusedResolveIsAnswered(t *testing.T) {
	owned := map[string]string{".github/CODEOWNERS": "*.go @alice\n"}
	policy := config.Defaults()
	ev := &converse.Event{Author: "dave", Path: "a.go", RootID: 9, CommentID: 10}
	log := slog.New(slog.DiscardHandler)
	ref := vcs.Ref{Owner: "o", Repo: "r", Number: 1}

	f := &replyingForge{fakeDismissalForge: fakeDismissalForge{author: "carol", files: owned}}
	if ok, err := allowClose(context.Background(), f, policy, ref, ev, converse.KindReply, log); ok || err != nil {
		t.Fatalf("reply from a non-owner: ok=%v err=%v, want refused", ok, err)
	}
	if len(f.replies) != 0 {
		t.Errorf("a refused reply was answered: %q", f.replies)
	}

	if ok, err := allowClose(context.Background(), f, policy, ref, ev, converse.KindResolve, log); ok || err != nil {
		t.Fatalf("resolve from a non-owner: ok=%v err=%v, want refused", ok, err)
	}
	if len(f.replies) != 1 || !strings.Contains(f.replies[0], "a.go is owned by @alice") {
		t.Errorf("replies = %q, want one refusal naming the owner", f.replies)
	}

	owner := &converse.Event{Author: "alice", Path: "a.go", RootID: 9}
	if ok, _ := allowClose(context.Background(), f, policy, ref, owner, converse.KindResolve, log); !ok {
		t.Error("the owner was refused")
	}
}

// Both close paths in runRespond must pass through allowClose before they act:
// the reply before runReply weighs it, the resolve before ResolveThreads.
// Asserted on the source, because runRespond reaches them only through a live
// forge and a model.
func TestBothClosePathsAreGatedBeforeTheyAct(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "respond.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var calls []string
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "runRespond" {
			return true
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				switch fun := call.Fun.(type) {
				case *ast.Ident:
					calls = append(calls, fun.Name)
				case *ast.SelectorExpr:
					calls = append(calls, fun.Sel.Name)
				}
			}
			return true
		})
		return false
	})
	gates, resolveDelegated := 0, false
	for _, c := range calls {
		switch c {
		case "allowClose":
			gates++
		case "resolveOnRequest":
			resolveDelegated = true
		case "runReply", "ResolveThreads":
			if gates == 0 {
				t.Errorf("%s is reached in runRespond before any allowClose call", c)
			}
		}
	}
	if gates < 1 {
		t.Error("the reply path in runRespond does not call allowClose")
	}
	if !resolveDelegated {
		t.Error("runRespond does not resolve through resolveOnRequest, the gated path")
	}
}

type resolvingForge struct {
	replyingForge
	resolved [][]int64
}

func (f *resolvingForge) ThreadComments(context.Context, vcs.Ref, int64) ([]vcs.ThreadComment, error) {
	return []vcs.ThreadComment{{ID: 9, Own: true, Body: "finding\n<!-- open-nitpick fp:aa11 class:correctness -->"}}, nil
}

func (f *resolvingForge) ThreadResolved(context.Context, vcs.Ref, int64) (bool, error) {
	return false, nil
}

func (f *resolvingForge) ResolveThreads(_ context.Context, _ vcs.Ref, ids []int64, _ string) ([]int64, error) {
	f.resolved = append(f.resolved, ids)
	return ids, nil
}

func (f *resolvingForge) React(context.Context, vcs.Ref, int64, bool, string) error { return nil }

// A refusal has to stop the resolve, not only be logged beside it.
func TestARefusedResolveNeverResolvesTheThread(t *testing.T) {
	owned := map[string]string{".github/CODEOWNERS": "*.go @alice\n"}
	log := slog.New(slog.DiscardHandler)
	ref := vcs.Ref{Owner: "o", Repo: "r", Number: 1}

	f := &resolvingForge{replyingForge: replyingForge{fakeDismissalForge: fakeDismissalForge{author: "carol", files: owned}}}
	stranger := &converse.Event{Author: "dave", Path: "a.go", RootID: 9, CommentID: 10, Inline: true}
	if err := resolveOnRequest(context.Background(), f, config.Defaults(), ref, stranger, log); err != nil {
		t.Fatalf("resolveOnRequest: %v", err)
	}
	if len(f.resolved) != 0 {
		t.Errorf("a non-owner's resolve closed threads %v", f.resolved)
	}

	owner := &converse.Event{Author: "alice", Path: "a.go", RootID: 9, CommentID: 11, Inline: true}
	if err := resolveOnRequest(context.Background(), f, config.Defaults(), ref, owner, log); err != nil {
		t.Fatalf("resolveOnRequest: %v", err)
	}
	if len(f.resolved) != 1 {
		t.Errorf("the owner's resolve closed %v, want the one thread", f.resolved)
	}
}
