package review

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	llms "github.com/nocturnium/llm-go-sdk/v6"

	"github.com/nitpick-oss/nitpick/internal/llm"
	"github.com/nitpick-oss/nitpick/internal/vcs"
)

type boundedTestModel struct {
	toolLLM
	n       int
	respond func(int, []llms.Message, llms.CallOptions) (*llms.Response, error)
}

func (m *boundedTestModel) GenerateContent(_ context.Context, msgs []llms.Message, options ...llms.CallOption) (*llms.Response, error) {
	var settings llms.CallOptions
	for _, option := range options {
		option(&settings)
	}
	m.n++
	return m.respond(m.n, msgs, settings)
}

type boundedTestProvider struct {
	stubProvider
	reads atomic.Int64
	read  func(context.Context, vcs.Ref, string) ([]byte, error)
}

func (p *boundedTestProvider) FileContent(ctx context.Context, ref vcs.Ref, path string) ([]byte, error) {
	p.reads.Add(1)
	return p.read(ctx, ref, path)
}

func boundedTestEngine(t *testing.T, model *boundedTestModel, provider vcs.Provider) (*Engine, *llm.Client, json.RawMessage, *repoTools) {
	t.Helper()
	engine := newEngine(t, &scriptedLLM{}, provider, nil)
	schema, err := findingsSchema(offeredClasses(engine.Config.Review.Slop))
	if err != nil {
		t.Fatal(err)
	}
	return engine, llm.NewClientForTest(model, engine.Config.Models.Default), schema, newRepoTools("", provider, vcs.Ref{Head: "reviewed-commit"})
}

func boundedCall(id, name, args string) llms.ToolCall {
	return llms.ToolCall{ID: id, Type: llms.ToolTypeFunction, Function: &llms.FunctionCall{Name: name, Arguments: args}}
}

func TestBoundedGenerationCleanSubmissionNeedsOneRequest(t *testing.T) {
	model := &boundedTestModel{respond: func(turn int, _ []llms.Message, options llms.CallOptions) (*llms.Response, error) {
		if turn != 1 || options.ToolChoice == nil || options.ToolChoice.Mode != llms.ToolChoiceAuto {
			t.Fatalf("direct submission turn=%d choice=%+v", turn, options.ToolChoice)
		}
		return &llms.Response{ToolCalls: []llms.ToolCall{boundedCall("submit", "submit_findings", `{"findings":[]}`)}}, nil
	}}
	engine, client, schema, tools := boundedTestEngine(t, model, &stubProvider{})
	result, lookups, err := engine.boundedGeneration(t.Context(), client, []llms.Message{{Role: llms.RoleUser, Content: "review"}}, schema, tools)
	if err != nil || len(result.Findings) != 0 || len(lookups) != 0 || model.n != 1 {
		t.Fatalf("clean submission result=%+v lookups=%+v calls=%d err=%v", result, lookups, model.n, err)
	}
}

func TestBoundedGenerationReadsPinnedSourceBeforeSubmitting(t *testing.T) {
	provider := &boundedTestProvider{read: func(_ context.Context, ref vcs.Ref, path string) ([]byte, error) {
		if ref.Head != "reviewed-commit" || path != "app.go" {
			t.Errorf("read %s at %s", path, ref.Head)
		}
		return []byte("return value / 1000"), nil
	}}
	model := &boundedTestModel{respond: func(turn int, msgs []llms.Message, options llms.CallOptions) (*llms.Response, error) {
		if turn == 1 {
			return &llms.Response{ToolCalls: []llms.ToolCall{boundedCall("read", "read_file", `{"path":"app.go","start_line":1,"end_line":1}`)}}, nil
		}
		if turn != 2 || len(options.Tools) != 1 || options.Tools[0].Function.Name != "submit_findings" || options.ToolChoice == nil || options.ToolChoice.Tool != "submit_findings" {
			t.Fatalf("continuation turn=%d tools=%+v choice=%+v", turn, options.Tools, options.ToolChoice)
		}
		var evidence string
		for _, msg := range msgs {
			if msg.Role == llms.RoleTool {
				evidence += msg.Content
			}
		}
		if !strings.Contains(evidence, "return value / 1000") || strings.Contains(evidence, "unreviewed") {
			t.Fatalf("source evidence = %q", evidence)
		}
		return &llms.Response{ToolCalls: []llms.ToolCall{boundedCall("submit", "submit_findings", `{"findings":[{"path":"app.go","line":1,"severity":"warning","category":"correctness","class":"correctness","title":"Wrong conversion","rationale":"The caller passes seconds.","confidence":0.9}]}`)}}, nil
	}}
	engine, client, schema, tools := boundedTestEngine(t, model, provider)
	tools.root = t.TempDir()
	if err := os.WriteFile(filepath.Join(tools.root, "app.go"), []byte("unreviewed checkout"), 0o600); err != nil {
		t.Fatal(err)
	}
	backing := []llms.Message{{Role: llms.RoleSystem, Content: "system"}, {Role: llms.RoleUser, Content: "diff"}, {Content: "sentinel"}}
	var snapshots []Snapshot
	engine.OnSnapshot = func(snapshot Snapshot) { snapshots = append(snapshots, snapshot) }
	result, lookups, err := engine.boundedGeneration(t.Context(), client, backing[:2], schema, tools)
	if err != nil || len(result.Findings) != 1 || len(lookups) != 1 || provider.reads.Load() != 1 || model.n != 2 {
		t.Fatalf("read submission result=%+v lookups=%+v calls=%d reads=%d err=%v", result, lookups, model.n, provider.reads.Load(), err)
	}
	if backing[2].Content != "sentinel" || len(snapshots) != 1 || snapshots[0].Stage != "generation-lookups" {
		t.Fatalf("mutated input or missing lookup snapshot: input=%+v snapshots=%+v", backing, snapshots)
	}
	if lookups[0].Digest != boundedLookupDigest("    1 return value / 1000\n") {
		t.Fatalf("lookup identity = %+v", lookups[0])
	}
}

func TestBoundedGenerationCapsEvidenceAndRetainsFullDigests(t *testing.T) {
	answer := strings.Repeat("é", 10_000)
	provider := &boundedTestProvider{read: func(context.Context, vcs.Ref, string) ([]byte, error) { return []byte(answer), nil }}
	model := &boundedTestModel{respond: func(turn int, msgs []llms.Message, _ llms.CallOptions) (*llms.Response, error) {
		if turn == 1 {
			var calls []llms.ToolCall
			for i := range 4 {
				calls = append(calls, boundedCall(fmt.Sprint(i), "read_file", fmt.Sprintf(`{"path":"file%d.go","start_line":1,"end_line":1}`, i)))
			}
			return &llms.Response{ToolCalls: calls}, nil
		}
		bytes, messages := 0, 0
		for _, msg := range msgs {
			if msg.Role == llms.RoleTool {
				bytes += len(msg.Content)
				messages++
				if !utf8.ValidString(msg.Content) {
					t.Fatal("output limit split a UTF-8 sequence")
				}
			}
		}
		if bytes > 8192 || bytes < 8190 || messages != 4 {
			t.Fatalf("returned %d bytes in %d tool messages", bytes, messages)
		}
		return &llms.Response{ToolCalls: []llms.ToolCall{boundedCall("submit", "submit_findings", `{"findings":[]}`)}}, nil
	}}
	engine, client, schema, tools := boundedTestEngine(t, model, provider)
	_, lookups, err := engine.boundedGeneration(t.Context(), client, nil, schema, tools)
	if err != nil || provider.reads.Load() != 4 || len(lookups) != 4 || model.n != 2 {
		t.Fatalf("bounded round lookups=%+v reads=%d calls=%d err=%v", lookups, provider.reads.Load(), model.n, err)
	}
	full := "    1 " + answer + "\n"
	for _, lookup := range lookups {
		if lookup.Digest != boundedLookupDigest(full) || lookup.Digest == lookupDigest(full) || lookup.Digest == boundedLookupDigest(full+"changed tail") {
			t.Fatalf("lookup digest lost undisclosed content: %+v", lookup)
		}
	}
}

func TestBoundedGenerationRejectsInvalidOrAbsentOperations(t *testing.T) {
	read := boundedCall("read", "read_file", `{"path":"app.go"}`)
	submit := boundedCall("submit", "submit_findings", `{"findings":[]}`)
	var excess []llms.ToolCall
	for i := range 5 {
		excess = append(excess, boundedCall(fmt.Sprint(i), "read_file", fmt.Sprintf(`{"path":"file%d.go"}`, i)))
	}
	cases := []struct {
		name  string
		calls []llms.ToolCall
	}{
		{"absent submission", nil},
		{"unknown tool", []llms.ToolCall{boundedCall("bad", "shell", `{}`)}},
		{"missing function", []llms.ToolCall{{ID: "missing", Type: llms.ToolTypeFunction}}},
		{"wrong operation type", []llms.ToolCall{{ID: "wrong", Type: "custom", Function: &llms.FunctionCall{Name: "read_file", Arguments: `{"path":"app.go"}`}}}},
		{"unavailable search", []llms.ToolCall{boundedCall("grep", "grep_repo", `{"pattern":"x"}`)}},
		{"malformed arguments", []llms.ToolCall{boundedCall("read", "read_file", `{`)}},
		{"missing path", []llms.ToolCall{boundedCall("read", "read_file", `{}`)}},
		{"wrong argument type", []llms.ToolCall{boundedCall("read", "read_file", `{"path":"app.go","start_line":"first"}`)}},
		{"null submission", []llms.ToolCall{boundedCall("submit", "submit_findings", `null`)}},
		{"absent findings", []llms.ToolCall{boundedCall("submit", "submit_findings", `{}`)}},
		{"null findings", []llms.ToolCall{boundedCall("submit", "submit_findings", `{"findings":null}`)}},
		{"malformed finding", []llms.ToolCall{boundedCall("submit", "submit_findings", `{"findings":[{}]}`)}},
		{"mixed submission", []llms.ToolCall{read, submit}},
		{"repeated call identity", []llms.ToolCall{read, read}},
		{"excess operations", excess},
		{"missing call identity", []llms.ToolCall{boundedCall("", "read_file", `{"path":"app.go"}`)}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			model := &boundedTestModel{respond: func(int, []llms.Message, llms.CallOptions) (*llms.Response, error) {
				return &llms.Response{Content: `{"findings":[]}`, ToolCalls: test.calls}, nil
			}}
			provider := &boundedTestProvider{read: func(context.Context, vcs.Ref, string) ([]byte, error) {
				t.Error("invalid operations reached repository")
				return nil, nil
			}}
			engine, client, schema, tools := boundedTestEngine(t, model, provider)
			if _, _, err := engine.boundedGeneration(t.Context(), client, nil, schema, tools); err == nil || model.n != 1 || provider.reads.Load() != 0 {
				t.Fatalf("invalid request calls=%d reads=%d err=%v", model.n, provider.reads.Load(), err)
			}
		})
	}
}

func TestBoundedGenerationRefusesAdditionalLookupRound(t *testing.T) {
	provider := &boundedTestProvider{read: func(context.Context, vcs.Ref, string) ([]byte, error) { return []byte("source"), nil }}
	for _, second := range []*llms.Response{nil, {Content: `{"findings":[]}`}, {ToolCalls: []llms.ToolCall{boundedCall("again", "read_file", `{"path":"other.go"}`)}}, {ToolCalls: []llms.ToolCall{boundedCall("submit", "submit_findings", `{}`)}}} {
		model := &boundedTestModel{respond: func(turn int, _ []llms.Message, _ llms.CallOptions) (*llms.Response, error) {
			if turn == 1 {
				return &llms.Response{ToolCalls: []llms.ToolCall{boundedCall("read", "read_file", `{"path":"app.go"}`)}}, nil
			}
			return second, nil
		}}
		engine, client, schema, tools := boundedTestEngine(t, model, provider)
		before := provider.reads.Load()
		if _, lookups, err := engine.boundedGeneration(t.Context(), client, nil, schema, tools); err == nil || model.n != 2 || len(lookups) != 1 || provider.reads.Load()-before != 1 {
			t.Fatalf("unfinished round calls=%d lookups=%+v err=%v", model.n, lookups, err)
		}
	}
}

func TestBoundedGenerationSharesOneLookupDeadline(t *testing.T) {
	var mu sync.Mutex
	var deadlines []time.Time
	provider := &boundedTestProvider{read: func(ctx context.Context, _ vcs.Ref, _ string) ([]byte, error) {
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Error("lookup has no deadline")
		}
		mu.Lock()
		deadlines = append(deadlines, deadline)
		mu.Unlock()
		return []byte("source"), nil
	}}
	model := &boundedTestModel{respond: func(turn int, _ []llms.Message, _ llms.CallOptions) (*llms.Response, error) {
		if turn == 1 {
			return &llms.Response{ToolCalls: []llms.ToolCall{boundedCall("a", "read_file", `{"path":"a.go"}`), boundedCall("b", "read_file", `{"path":"b.go"}`)}}, nil
		}
		return &llms.Response{ToolCalls: []llms.ToolCall{boundedCall("submit", "submit_findings", `{"findings":[]}`)}}, nil
	}}
	engine, client, schema, tools := boundedTestEngine(t, model, provider)
	started := time.Now()
	if _, _, err := engine.boundedGeneration(t.Context(), client, nil, schema, tools); err != nil {
		t.Fatal(err)
	}
	if len(deadlines) != 2 || !deadlines[0].Equal(deadlines[1]) || deadlines[0].Before(started.Add(4*time.Second)) || deadlines[0].After(time.Now().Add(5*time.Second)) {
		t.Fatalf("lookup deadlines = %v", deadlines)
	}
}

func TestBoundedGenerationLookupFailurePreventsCleanSubmission(t *testing.T) {
	for _, cancelLookup := range []bool{false, true} {
		provider := &boundedTestProvider{read: func(ctx context.Context, _ vcs.Ref, _ string) ([]byte, error) {
			if cancelLookup {
				<-ctx.Done()
				return nil, ctx.Err()
			}
			return nil, errors.New("upstream unavailable")
		}}
		model := &boundedTestModel{respond: func(int, []llms.Message, llms.CallOptions) (*llms.Response, error) {
			return &llms.Response{ToolCalls: []llms.ToolCall{boundedCall("read", "read_file", `{"path":"app.go"}`)}}, nil
		}}
		engine, client, schema, tools := boundedTestEngine(t, model, provider)
		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		_, _, err := engine.boundedGeneration(ctx, client, nil, schema, tools)
		cancel()
		if err == nil || model.n != 1 || (cancelLookup && !errors.Is(err, context.DeadlineExceeded)) {
			t.Fatalf("failed lookup became submission: cancellation=%t calls=%d err=%v", cancelLookup, model.n, err)
		}
	}
}

func TestBoundedGenerationLookupPreservesPrivatePaths(t *testing.T) {
	provider := &boundedTestProvider{read: func(context.Context, vcs.Ref, string) ([]byte, error) {
		t.Error("private lookup reached provider")
		return []byte("private credential"), nil
	}}
	model := &boundedTestModel{respond: func(turn int, msgs []llms.Message, _ llms.CallOptions) (*llms.Response, error) {
		if turn == 1 {
			return &llms.Response{ToolCalls: []llms.ToolCall{boundedCall("private", "read_file", `{"path":".env"}`)}}, nil
		}
		for _, msg := range msgs {
			if strings.Contains(msg.Content, "private credential") {
				t.Fatal("private content reached model")
			}
		}
		return &llms.Response{ToolCalls: []llms.ToolCall{boundedCall("submit", "submit_findings", `{"findings":[]}`)}}, nil
	}}
	engine, client, schema, _ := boundedTestEngine(t, model, provider)
	root := gitRepo(t, map[string]string{".gitignore": ".env\n"})
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("private credential"), 0o600); err != nil {
		t.Fatal(err)
	}
	tools := newRepoTools(root, provider, vcs.Ref{Head: vcs.Worktree})
	if _, _, err := engine.boundedGeneration(t.Context(), client, nil, schema, tools); err == nil || provider.reads.Load() != 0 || model.n != 1 {
		t.Fatalf("private lookup reads=%d calls=%d err=%v", provider.reads.Load(), model.n, err)
	}
}

func TestBoundedGenerationProviderErrorNeverTriggersFallback(t *testing.T) {
	providerError := errors.New("provider accepted request, response interrupted")
	for _, failAt := range []int{1, 2} {
		model := &boundedTestModel{respond: func(turn int, _ []llms.Message, _ llms.CallOptions) (*llms.Response, error) {
			if turn == failAt {
				return nil, providerError
			}
			return &llms.Response{ToolCalls: []llms.ToolCall{boundedCall("read", "read_file", `{"path":"app.go"}`)}}, nil
		}}
		provider := &boundedTestProvider{read: func(context.Context, vcs.Ref, string) ([]byte, error) { return []byte("source"), nil }}
		engine, client, schema, tools := boundedTestEngine(t, model, provider)
		if _, _, err := engine.boundedGeneration(t.Context(), client, nil, schema, tools); !errors.Is(err, providerError) || model.n != failAt {
			t.Fatalf("fallback after provider error: expected calls=%d actual=%d err=%v", failAt, model.n, err)
		}
	}
}
