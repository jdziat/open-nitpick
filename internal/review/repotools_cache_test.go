package review

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jdziat/open-nitpick/internal/vcs"
	llms "github.com/nocturnium/llm-go-sdk/v6"
)

type lookupProvider struct {
	vcs.Provider
	calls   atomic.Int64
	read    func(context.Context, string) error
	content []byte
}

func (p *lookupProvider) FileContent(ctx context.Context, _ vcs.Ref, path string) ([]byte, error) {
	p.calls.Add(1)
	if p.read != nil {
		if err := p.read(ctx, path); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if path == "missing.go" {
		return nil, vcs.ErrNotFound
	}
	if p.content != nil {
		return p.content, nil
	}
	return []byte("one\ntwo\nthree\n"), nil
}

func TestLookupTurnOverlapsReadsAndPreservesCallOrder(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	started := make(chan string, 2)
	release := make(chan struct{})
	provider := &lookupProvider{read: func(ctx context.Context, path string) error {
		started <- path
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	tools := newRepoTools("", provider, vcs.Ref{})
	got := make(chan []string, 1)
	go func() {
		got <- lookupTurn(ctx, tools, []llms.ToolCall{
			call("a", "read_file", map[string]any{"path": "a.go", "start_line": 2, "end_line": 2}),
			call("b", "read_file", map[string]any{"path": "b.go", "start_line": 1, "end_line": 1}),
			call("done", "submit_verdict", map[string]any{}),
			call("never", "read_file", map[string]any{"path": "never.go"}),
		}, 2)
	}()
	for range 2 {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("the second read did not start while the first was waiting")
		}
	}
	close(release)
	select {
	case answers := <-got:
		if len(answers) != 2 || answers[0] != "    2 two\n" || answers[1] != "    1 one\n" {
			t.Fatalf("answers were not returned in call order: %q", answers)
		}
	case <-ctx.Done():
		t.Fatal("lookup workers did not join")
	}
	if got := provider.calls.Load(); got != 2 {
		t.Fatalf("reads = %d: a lookup after submission or past the budget ran", got)
	}
}

func TestLookupTurnHonorsTheRemainingReadBudget(t *testing.T) {
	provider := &lookupProvider{}
	tools := newRepoTools("", provider, vcs.Ref{})
	calls := []llms.ToolCall{
		call("a", "read_file", map[string]any{"path": "a.go"}),
		call("b", "read_file", map[string]any{"path": "b.go"}),
	}
	if got := lookupTurn(t.Context(), tools, calls, 0); len(got) != 0 || provider.calls.Load() != 0 {
		t.Fatalf("exhausted budget ran reads: %q, calls=%d", got, provider.calls.Load())
	}
	if got := lookupTurn(t.Context(), tools, calls, 1); len(got) != 1 || provider.calls.Load() != 1 {
		t.Fatalf("one remaining lookup: %q, calls=%d", got, provider.calls.Load())
	}
}

func TestLookupTurnStopsBeforeASubmission(t *testing.T) {
	provider := &lookupProvider{}
	tools := newRepoTools("", provider, vcs.Ref{})
	answers := lookupTurn(t.Context(), tools, []llms.ToolCall{
		call("before", "read_file", map[string]any{"path": "before.go"}),
		call("done", "submit_verdict", map[string]any{}),
		call("after", "read_file", map[string]any{"path": "after.go"}),
	}, verifyMaxLookups)
	if len(answers) != 1 || provider.calls.Load() != 1 {
		t.Fatalf("prefetched across a submission: %q, reads=%d", answers, provider.calls.Load())
	}
}

func TestRepositoryLookupsShareCapturedBytesAcrossWorkers(t *testing.T) {
	provider := &lookupProvider{}
	tools := newRepoTools("", provider, vcs.Ref{})
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			if got := tools.read(t.Context(), "app.go", 2, 2); got != "    2 two\n" {
				t.Errorf("read = %q", got)
			}
		})
	}
	wg.Wait()
	if got := provider.calls.Load(); got != 1 {
		t.Fatalf("provider reads = %d, want one captured source shared by all workers", got)
	}
	fresh := newRepoTools("", provider, vcs.Ref{})
	fresh.read(t.Context(), "app.go", 1, 1)
	if got := provider.calls.Load(); got != 2 {
		t.Fatalf("a new review reused the previous snapshot: reads = %d", got)
	}
}

func TestCancelledLookupDoesNotPoisonLaterReads(t *testing.T) {
	provider := &lookupProvider{}
	tools := newRepoTools("", provider, vcs.Ref{})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := tools.fileBytes(ctx, "app.go"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read = %v", err)
	}
	if got := tools.read(t.Context(), "app.go", 1, 1); got != "    1 one\n" {
		t.Fatalf("fresh read after cancellation = %q", got)
	}
}

func TestGrepChildTimeoutDoesNotPoisonLaterSearches(t *testing.T) {
	root := gitRepo(t, map[string]string{"app.go": "package sample\n"})
	installGitGrepFailure(t, "exec sleep 1")
	tools := newRepoTools(root, nil, vcs.Ref{})
	tools.grepTimeout = 20 * time.Millisecond
	if got := tools.grep(t.Context(), "package", "*.go"); got != "the search timed out; use a narrower pattern or a path glob" {
		t.Fatalf("first search = %q", got)
	}
	tools.grepTimeout = 5 * time.Second
	if got := tools.grep(t.Context(), "package", "*.go"); got != "app.go:1:package sample" {
		t.Fatalf("child timeout was retained: %q", got)
	}
}

func TestTransientGrepFailureDoesNotPoisonLaterSearches(t *testing.T) {
	root := gitRepo(t, map[string]string{"app.go": "package sample\n"})
	installGitGrepFailure(t, "echo temporary >&2\nexit 2")
	tools := newRepoTools(root, nil, vcs.Ref{})
	if got := tools.grep(t.Context(), "package", "*.go"); got != "the search failed: temporary" {
		t.Fatalf("first search = %q", got)
	}
	if got := tools.grep(t.Context(), "package", "*.go"); got != "app.go:1:package sample" {
		t.Fatalf("transient failure was retained: %q", got)
	}
}

func installGitGrepFailure(t *testing.T, first string) {
	t.Helper()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	shim := t.TempDir()
	script := `#!/bin/sh
if [ "$2" = "grep" ] && [ ! -e "$NITPICK_TEST_MARKER" ]; then
  : > "$NITPICK_TEST_MARKER"
  ` + first + `
fi
exec "$NITPICK_TEST_REAL_GIT" "$@"
`
	if err := os.WriteFile(filepath.Join(shim, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NITPICK_TEST_REAL_GIT", realGit)
	t.Setenv("NITPICK_TEST_MARKER", filepath.Join(shim, "first"))
	t.Setenv("PATH", shim+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestOversizedSourceIsReturnedWithoutRetainingPayloads(t *testing.T) {
	provider := &lookupProvider{content: []byte(strings.Repeat("x", toolReadBytes+1))}
	tools := newRepoTools("", provider, vcs.Ref{})
	want := fmt.Sprintf("app.go is %d bytes, over the %d byte limit for reading; grep it instead", toolReadBytes+1, toolReadBytes)
	for range 2 {
		if got := tools.read(t.Context(), "app.go", 1, 1); got != want {
			t.Fatalf("oversized read = %q", got)
		}
	}
	probe := errors.New("cache miss")
	_, err := tools.files.Get(t.Context(), "app.go", func() (capturedFile, error) {
		return capturedFile{}, probe
	})
	if !errors.Is(err, probe) || tools.cacheBytes.Load() != 0 {
		t.Fatalf("oversized payload retained: charged=%d, err=%v", tools.cacheBytes.Load(), err)
	}
	if got := provider.calls.Load(); got != 2 {
		t.Fatalf("oversized source fetched %d times", got)
	}
}

func TestOversizedSourcePreservesJevEvidencePrefix(t *testing.T) {
	data := []byte(strings.Repeat("éa\n", toolReadBytes/4+1))
	provider := &lookupProvider{content: data}
	tools := newRepoTools("", provider, vcs.Ref{})
	var expected strings.Builder
	for i := 1; expected.Len() < jevMaxFile+10; i++ {
		fmt.Fprintf(&expected, "%5d  éa\n", i)
	}
	want := truncate(expected.String(), jevMaxFile)
	if got := truncate(wholeFile(t.Context(), tools, "app.go"), jevMaxFile); got != want {
		t.Fatalf("large-file Jev prefix changed: length=%d, want=%d", len(got), len(want))
	}
	if tools.cacheBytes.Load() != 0 {
		t.Fatalf("Jev retained oversized source: %d charged bytes", tools.cacheBytes.Load())
	}
}

func TestFullCacheReturnsResultsWithoutRetainingPayloads(t *testing.T) {
	provider := &lookupProvider{}
	tools := newRepoTools("", provider, vcs.Ref{})
	tools.cacheBytes.Store(toolCacheBytes)
	for range 2 {
		if got := tools.read(t.Context(), "app.go", 1, 1); got != "    1 one\n" {
			t.Fatalf("full cache changed the result: %q", got)
		}
	}
	if provider.calls.Load() != 2 || tools.cacheBytes.Load() != toolCacheBytes {
		t.Fatalf("full cache retained more source: reads=%d, charged=%d", provider.calls.Load(), tools.cacheBytes.Load())
	}
}
func TestTransientProviderReadCanRecoverWithinOneSnapshot(t *testing.T) {
	temporary := errors.New("temporary upstream 503")
	var attempts atomic.Int64
	provider := &lookupProvider{read: func(context.Context, string) error {
		if attempts.Add(1) == 1 {
			return temporary
		}
		return nil
	}}
	tools := newRepoTools("", provider, vcs.Ref{})
	if _, err := tools.fileBytes(t.Context(), "app.go"); !errors.Is(err, temporary) {
		t.Fatalf("first provider error = %v", err)
	}
	for range 2 {
		if got := tools.read(t.Context(), "app.go", 1, 1); got != "    1 one\n" {
			t.Fatalf("transient failure was retained: %q", got)
		}
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("provider attempts = %d, want failure then one shared success", got)
	}
}

func TestMissingSourceIsRetainedWithinOneSnapshot(t *testing.T) {
	provider := &lookupProvider{}
	tools := newRepoTools("", provider, vcs.Ref{})
	for range 2 {
		if _, err := tools.fileBytes(t.Context(), "missing.go"); !errors.Is(err, vcs.ErrNotFound) {
			t.Fatalf("missing source = %v", err)
		}
	}
	if got := provider.calls.Load(); got != 1 {
		t.Fatalf("stable absence was fetched %d times", got)
	}
}

type pinnedLookupProvider struct {
	stubProvider
	head  int
	reads atomic.Int64
}

func (p *pinnedLookupProvider) PullRequest(context.Context, vcs.Ref) (*vcs.PullRequest, error) {
	p.head++
	return &vcs.PullRequest{Title: "Cache scope", HeadSHA: fmt.Sprintf("%040d", p.head)}, nil
}

func (p *pinnedLookupProvider) FileContent(_ context.Context, ref vcs.Ref, path string) ([]byte, error) {
	p.reads.Add(1)
	if path != "app.go" {
		return nil, vcs.ErrNotFound
	}
	return fmt.Appendf(nil, "package app // head %s\n", ref.Head), nil
}

func TestReviewLookupCacheIsScopedToEachPinnedHead(t *testing.T) {
	provider := &pinnedLookupProvider{stubProvider: stubProvider{diff: engineDiff}}
	model := &scriptedLLM{fallback: `{"findings":[]}`}
	engine := newEngine(t, model, provider, nil)
	var previous *repoTools
	for head := 1; head <= 2; head++ {
		if _, err := engine.Review(t.Context(), vcs.Ref{Number: 1}); err != nil {
			t.Fatal(err)
		}
		tools := engine.repositoryTools(vcs.Ref{})
		if tools == previous {
			t.Fatal("review reused the previous head's lookup cache")
		}
		before := provider.reads.Load()
		want := fmt.Sprintf("    1 package app // head %040d\n", head)
		for range 2 {
			if got := tools.read(t.Context(), "app.go", 1, 1); got != want {
				t.Fatalf("head %d: read = %q, want %q", head, got, want)
			}
		}
		if got := provider.reads.Load() - before; got != 1 {
			t.Fatalf("head %d: reads=%d, want one shared read", head, got)
		}
		previous = tools
	}
}
