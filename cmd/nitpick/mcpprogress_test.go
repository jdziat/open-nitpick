package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPReviewReportsStagesAndElapsedTime(t *testing.T) {
	notifications := make(chan *mcp.ProgressNotificationParams, 100)
	heartbeatSeen := make(chan struct{})
	var heartbeatOnce sync.Once
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "wait"}, func(ctx context.Context, req *mcp.CallToolRequest, _ ExplainIn) (*mcp.CallToolResult, ExplainOut, error) {
		ctx, log, finish := startMCPReview(ctx, req, slog.New(slog.NewTextHandler(io.Discard, nil)), 5*time.Second, time.Millisecond)
		defer finish()
		log.With("model", "test").WithGroup("review").Info("reviewing batch")
		select {
		case <-ctx.Done():
			return nil, ExplainOut{}, ctx.Err()
		case <-heartbeatSeen:
			return textResult("done"), ExplainOut{Text: "done"}, nil
		}
	})
	st, ct := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ss.Close() }()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, &mcp.ClientOptions{
		ProgressNotificationHandler: func(_ context.Context, req *mcp.ProgressNotificationClientRequest) {
			notifications <- req.Params
			if strings.Contains(req.Params.Message, "waiting for this stage to finish") {
				heartbeatOnce.Do(func() { close(heartbeatSeen) })
			}
		},
	})
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cs.Close() }()
	params := &mcp.CallToolParams{Name: "wait", Arguments: map[string]any{}}
	params.SetProgressToken("review-1")
	res, err := cs.CallTool(ctx, params)
	if err != nil || res.IsError {
		t.Fatalf("call: %v, %v", res, err)
	}
	var previous float64
	var stage, heartbeat bool
drain:
	for {
		select {
		case n := <-notifications:
			if n.ProgressToken != "review-1" || n.Progress <= previous {
				t.Fatalf("invalid progress: %+v", n)
			}
			previous = n.Progress
			stage = stage || strings.Contains(n.Message, "reviewing batch; elapsed")
			heartbeat = heartbeat || strings.Contains(n.Message, "waiting for this stage to finish")
		default:
			if !stage || !heartbeat {
				t.Fatalf("stage=%v heartbeat=%v", stage, heartbeat)
			}
			break drain
		}
	}
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "wait", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("call without progress token: %v, %v", res, err)
	}
	select {
	case n := <-notifications:
		t.Fatalf("notification without token: %+v", n)
	default:
	}
}

func TestMCPReviewDeadlineAndCallerCancellationPropagate(t *testing.T) {
	for _, callerCancel := range []bool{false, true} {
		t.Run(map[bool]string{false: "deadline", true: "caller"}[callerCancel], func(t *testing.T) {
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx, _, finish := startMCPReview(parent, nil, slog.Default(), 10*time.Millisecond, time.Millisecond)
			defer finish()
			if callerCancel {
				cancel()
			}
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
				t.Fatal("review context did not stop")
			}
			want := context.DeadlineExceeded
			if callerCancel {
				want = context.Canceled
			}
			if !errors.Is(ctx.Err(), want) {
				t.Fatalf("error = %v, want %v", ctx.Err(), want)
			}
		})
	}
}

func TestMCPReviewRejectsInvalidDeadlines(t *testing.T) {
	session := mcpSession(t, t.TempDir())
	for _, seconds := range []int{-1, 86401} {
		res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "review", Arguments: map[string]any{"timeout_seconds": seconds}})
		if err != nil {
			t.Fatal(err)
		}
		if !res.IsError || !strings.Contains(textOf(res), "timeout_seconds") {
			t.Fatalf("invalid deadline: %+v", res)
		}
	}
}

func TestMCPReviewStopsAnOutstandingModelRequest(t *testing.T) {
	for _, callerCancel := range []bool{false, true} {
		t.Run(map[bool]string{false: "deadline", true: "caller"}[callerCancel], func(t *testing.T) {
			t.Setenv("OPENAI_API_KEY", "test-key")
			t.Setenv("NITPICK_TRUST_CONFIG_ENDPOINTS", "true")
			started := make(chan struct{}, 10)
			stopped := make(chan struct{}, 10)
			release := make(chan struct{})
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				started <- struct{}{}
				select {
				case <-r.Context().Done():
					stopped <- struct{}{}
				case <-release:
				}
			}))
			defer provider.Close()
			defer close(release)
			repo := t.TempDir()
			git(t, repo, "init")
			git(t, repo, "config", "user.email", "test@example.com")
			git(t, repo, "config", "user.name", "Test")
			policy := fmt.Sprintf("models:\n  default:\n    provider: openai\n    model: test\n    base_url: %s/v1\n    allow_private_endpoint: true\n", provider.URL)
			for name, content := range map[string]string{".nitpick.yaml": policy, "main.go": "package main\nfunc main() {}\n"} {
				if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			git(t, repo, "add", ".")
			git(t, repo, "commit", "-m", "test: baseline")
			if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package main\nfunc main() { panic(\"failure\") }\n"), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			tools := &mcpTools{root: repo, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
			result := make(chan error, 1)
			go func() {
				_, _, err := tools.review(ctx, nil, ReviewIn{NoLinters: true, TimeoutSeconds: 1})
				result <- err
			}()
			select {
			case <-started:
			case err := <-result:
				t.Fatalf("review stopped before inference: %v", err)
			case <-ctx.Done():
				t.Fatal("model request never started")
			}
			if callerCancel {
				cancel()
			}
			select {
			case err := <-result:
				want := context.DeadlineExceeded
				if callerCancel {
					want = context.Canceled
				}
				if !errors.Is(err, want) {
					t.Fatalf("error = %v, want %v", err, want)
				}
				if callerCancel && strings.Contains(err.Error(), "increase timeout_seconds") {
					t.Fatalf("cancellation suggested increasing deadline: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("review did not stop")
			}
			select {
			case <-stopped:
			case <-time.After(time.Second):
				t.Fatal("model request was not cancelled")
			}
		})
	}
}

func TestMCPProgressDoesNotBlockReviewOnSlowClients(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	var once sync.Once
	notify := func(ctx context.Context, _ float64, _ string) error {
		once.Do(func() { close(started) })
		<-ctx.Done()
		return ctx.Err()
	}
	_, log, finish := startMCPProgress(parent, slog.New(slog.NewTextHandler(io.Discard, nil)), 5*time.Second, time.Millisecond, notify)
	defer finish()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("progress writer did not start")
	}
	logged := make(chan struct{})
	go func() {
		defer close(logged)
		for i := range 1000 {
			log.Info("reviewing batch", "batch", i)
		}
	}()
	select {
	case <-logged:
	case <-time.After(time.Second):
		t.Fatal("slow client blocked review logging")
	}
	cancel()
	finished := make(chan struct{})
	go func() { finish(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("cancelled progress writer did not stop")
	}
}
