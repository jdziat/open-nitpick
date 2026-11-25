package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofrs/flock"
	"github.com/nitpick-oss/nitpick/internal/config"
	llms "github.com/nocturnium/llm-go-sdk/v6"
)

func TestReplayCacheSeparatesEndpointsAndCredentials(t *testing.T) {
	t.Setenv(EnvResponseCache, t.TempDir())
	t.Setenv("NITPICK_REPLAY_TEST_KEY", "first-account")
	var calls atomic.Int32
	server := func(answer string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": answer}}},
				"usage":   map[string]int{"prompt_tokens": 2, "completion_tokens": 1},
			})
		}))
	}
	a, b := server("A"), server("B")
	defer a.Close()
	defer b.Close()
	spec := config.ModelSpec{Provider: "openai", Model: "same-model", BaseURL: a.URL, APIKeyEnv: "NITPICK_REPLAY_TEST_KEY", AllowPrivateEndpoint: true}
	for _, tc := range []struct{ url, key, want string }{{a.URL, "first-account", "A"}, {b.URL, "first-account", "B"}, {b.URL, "second-account", "B"}, {a.URL, "first-account", "A"}} {
		spec.BaseURL = tc.url
		t.Setenv("NITPICK_REPLAY_TEST_KEY", tc.key)
		client, err := Build(spec)
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.LLM.GenerateContent(t.Context(), []llms.Message{{Role: llms.RoleUser, Content: "same prompt"}})
		if err != nil || response.Content != tc.want {
			t.Fatalf("endpoint %s got %+v, %v", tc.want, response, err)
		}
	}
	if calls.Load() != 3 {
		t.Fatalf("distinct endpoints/accounts made %d calls, want 3", calls.Load())
	}
}

func TestReplayCacheCoordinatesIndependentProcesses(t *testing.T) {
	const env = "NITPICK_REPLAY_PROCESS_TEST"
	if root := os.Getenv(env); root != "" {
		inner := &processCountingLLM{root: root}
		r := newReplayLLM(inner, filepath.Join(root, "cache"))
		got, err := ask(t, r, "one purchase")
		if err != nil || got.Content != "answer" {
			t.Fatalf("subprocess response %+v, %v", got, err)
		}
		return
	}
	root := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var workers []*exec.Cmd
	for range 4 {
		cmd := exec.CommandContext(t.Context(), executable, "-test.run=^TestReplayCacheCoordinatesIndependentProcesses$")
		cmd.Env = append(os.Environ(), env+"="+root)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		workers = append(workers, cmd)
	}
	for _, cmd := range workers {
		if err := cmd.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := filepath.Glob(filepath.Join(root, "purchase-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("four processes bought %d responses, want 1", len(entries))
	}
}

type processCountingLLM struct{ root string }

func (*processCountingLLM) Provider() llms.Provider { return "test" }
func (*processCountingLLM) Model() string           { return "m" }
func (*processCountingLLM) Stream(context.Context, []llms.Message, ...llms.CallOption) (<-chan llms.StreamChunk, error) {
	return nil, errors.New("unused")
}
func (p *processCountingLLM) GenerateContent(context.Context, []llms.Message, ...llms.CallOption) (*llms.Response, error) {
	f, err := os.CreateTemp(p.root, "purchase-*")
	if err != nil {
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	return &llms.Response{Content: "answer", FinishReason: llms.FinishReasonStop}, nil
}

func TestReplayCacheWaitRespectsCancellation(t *testing.T) {
	inner := &countingLLM{resp: llms.Response{Content: "answer", FinishReason: llms.FinishReasonStop}}
	r := newReplayLLM(inner, t.TempDir())
	key, _ := r.key([]llms.Message{{Role: llms.RoleUser, Content: "x"}}, nil)
	path := r.path(key)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	lock := flock.New(path + ".lock")
	if err := lock.Lock(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()
	ctx, cancel := context.WithCancel(t.Context())
	// Cancel only once the call is already waiting on the held lock, so the
	// lock wait honours it; a context cancelled up front returns earlier.
	time.AfterFunc(100*time.Millisecond, cancel)
	started := time.Now()
	_, err := r.GenerateContent(ctx, []llms.Message{{Role: llms.RoleUser, Content: "x"}})
	if time.Since(started) < 50*time.Millisecond {
		t.Fatalf("returned in %v: the call never waited on the lock", time.Since(started))
	}
	if !errors.Is(err, context.Canceled) || inner.calls.Load() != 0 {
		t.Fatalf("cancelled cache wait: err %v, purchases %d", err, inner.calls.Load())
	}
}

func TestReplayCacheDoesNotLosePartialResponseOnError(t *testing.T) {
	inner := &partialErrorLLM{}
	r := newReplayLLM(inner, t.TempDir())
	got, err := ask(t, r, "x")
	if err == nil || got == nil || got.Content != "partial" {
		t.Fatalf("partial response lost: %+v, %v", got, err)
	}
}

type partialErrorLLM struct{ countingLLM }

func (*partialErrorLLM) GenerateContent(context.Context, []llms.Message, ...llms.CallOption) (*llms.Response, error) {
	return &llms.Response{Content: "partial"}, errors.New("interrupted")
}

func TestReplayCacheReturnsIndependentResponsesToWorkers(t *testing.T) {
	inner := &countingLLM{resp: llms.Response{Content: "answer", FinishReason: llms.FinishReasonStop}}
	r := newReplayLLM(inner, t.TempDir())
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := ask(t, r, "x")
			if err != nil {
				t.Error(err)
				return
			}
			got.Content = "changed"
		}()
	}
	wg.Wait()
	got, err := ask(t, r, "x")
	if err != nil || got.Content != "answer" || inner.calls.Load() != 1 {
		t.Fatalf("mutation escaped cached response: %+v, %v, calls %d", got, err, inner.calls.Load())
	}
}

func TestReplayNamespaceDoesNotDependOnTimeoutOrExecutable(t *testing.T) {
	cfg := llms.Config{BaseURL: "https://example.invalid", APIKey: "private"}
	a, err := replayNamespace(config.ModelSpec{Provider: "openai"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Timeout = time.Minute
	cfg.Model = "another-model"
	b, err := replayNamespace(config.ModelSpec{Provider: "openai"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatal("non-model settings invalidated previously paid responses")
	}
}

func TestReplayCacheRetryApprovalCannotSpendTwice(t *testing.T) {
	inner := &countingLLM{err: errors.New("no JSON object found in response")}
	if !ShouldEscalate(inner.err) {
		t.Fatal("fixture must ordinarily trigger escalation")
	}
	r := newReplayLLM(inner, t.TempDir())
	_, err := ask(t, r, "x")
	if !errors.Is(err, ErrRecordedAttempt) || ShouldEscalate(err) {
		t.Fatalf("uncertain purchase permits escalation: %v", err)
	}
	key, _ := r.key([]llms.Message{{Role: llms.RoleUser, Content: "x"}}, nil)
	t.Setenv(EnvRetryRequest, key+":operator-approval-1")
	_, _ = ask(t, r, "x")
	_, err = ask(t, r, "x")
	if !errors.Is(err, ErrRecordedAttempt) || inner.calls.Load() != 2 {
		t.Fatalf("retry approval reused: calls %d, err %v", inner.calls.Load(), err)
	}
}

func TestReplayCacheKeepsIndependentSamplesSeparateAndResumable(t *testing.T) {
	inner := &countingLLM{resp: llms.Response{Content: "answer", FinishReason: llms.FinishReasonStop}}
	r := newReplayLLM(inner, t.TempDir())
	for _, sample := range []string{"fixture/run1", "fixture/run2", "fixture/run1"} {
		_, err := r.GenerateContent(WithResponseSample(t.Context(), sample), []llms.Message{{Role: llms.RoleUser, Content: "x"}}, llms.WithTemperature(0.7))
		if err != nil {
			t.Fatal(err)
		}
	}
	if inner.calls.Load() != 2 {
		t.Fatalf("two independent samples made %d purchases", inner.calls.Load())
	}
}

func TestReplayCacheBlocksAnotherProcessAfterAcceptedRequestCrashes(t *testing.T) {
	const childEnv = "NITPICK_REPLAY_CRASH_SERVER"
	if endpoint := os.Getenv(childEnv); endpoint != "" {
		client, err := Build(config.ModelSpec{Provider: "openai", Model: "crash-test", BaseURL: endpoint, APIKeyEnv: "NITPICK_REPLAY_CRASH_KEY", AllowPrivateEndpoint: true})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = client.LLM.GenerateContent(t.Context(), []llms.Message{{Role: llms.RoleUser, Content: "same crash request"}})
		return
	}
	t.Setenv(EnvResponseCache, t.TempDir())
	t.Setenv("NITPICK_REPLAY_CRASH_KEY", "fixture-secret")
	accepted := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(accepted)
		}
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.CommandContext(t.Context(), executable, "-test.run=^TestReplayCacheBlocksAnotherProcessAfterAcceptedRequestCrashes$")
	child.Env = append(os.Environ(), childEnv+"="+srv.URL)
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-accepted:
	case <-time.After(10 * time.Second):
		_ = child.Process.Kill()
		_ = child.Wait()
		t.Fatal("child did not reach provider")
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	client, err := Build(config.ModelSpec{Provider: "openai", Model: "crash-test", BaseURL: srv.URL, APIKeyEnv: "NITPICK_REPLAY_CRASH_KEY", AllowPrivateEndpoint: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.LLM.GenerateContent(t.Context(), []llms.Message{{Role: llms.RoleUser, Content: "same crash request"}})
	if !errors.Is(err, ErrRecordedAttempt) || calls.Load() != 1 {
		t.Fatalf("uncertain purchase repeated: calls %d, err %v", calls.Load(), err)
	}
}
