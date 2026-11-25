package linters

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	llms "github.com/nocturnium/llm-go-sdk/v6"

	"github.com/nitpick-oss/nitpick/internal/config"
	"github.com/nitpick-oss/nitpick/internal/llm"
	"github.com/nitpick-oss/nitpick/internal/review"
	"github.com/nitpick-oss/nitpick/internal/vcs"
)

type analyzerProfileProvider struct {
	vcs.Provider
	raw     []byte
	sources map[string][]byte
}

func (*analyzerProfileProvider) Name() string { return "frozen" }
func (*analyzerProfileProvider) PullRequest(context.Context, vcs.Ref) (*vcs.PullRequest, error) {
	return &vcs.PullRequest{Title: "Frozen analyzer profile", HeadSHA: "frozen-head"}, nil
}
func (p *analyzerProfileProvider) Diff(context.Context, vcs.Ref) ([]byte, error) { return p.raw, nil }
func (p *analyzerProfileProvider) FileContent(_ context.Context, _ vcs.Ref, name string) ([]byte, error) {
	text, ok := p.sources[name]
	if !ok {
		return nil, vcs.ErrNotFound
	}
	return text, nil
}
func (*analyzerProfileProvider) PublishReview(context.Context, vcs.Ref, vcs.Review) error { return nil }

type analyzerProfileModel struct {
	llms.LLM
	mu       sync.Mutex
	requests []string
}

func (*analyzerProfileModel) Provider() llms.Provider { return "openai" }
func (*analyzerProfileModel) Model() string           { return "scripted" }
func (m *analyzerProfileModel) GenerateContent(_ context.Context, msgs []llms.Message, opts ...llms.CallOption) (*llms.Response, error) {
	applied := *llms.ApplyOptions(opts...)
	applied.Trace = nil
	data, err := json.Marshal([]any{msgs, applied})
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.requests = append(m.requests, fmt.Sprintf("%x", sha256.Sum256(data)))
	m.mu.Unlock()
	response := `{"findings":[]}`
	for _, msg := range msgs {
		if msg.Role == llms.RoleSystem && strings.Contains(msg.Content, "triaging findings") {
			response = `{"findings":[],"summary":"Scripted model has no findings."}`
		}
	}
	return &llms.Response{Content: response}, nil
}

func benchmarkReviewWithAnalyzer(b *testing.B, name, root string, cfg *config.Config, raw []byte, sources map[string][]byte) {
	b.Helper()
	cfg.Models.Default = config.ModelSpec{Provider: "openai", Model: "scripted"}
	cfg.Review.Knowledge = false
	cfg.Review.Standards = false
	cfg.Review.Flow = false
	cfg.Review.Learnings.Enabled = false
	type sample struct {
		Duration             time.Duration
		PayloadSHA256        string
		ModelCalls, Findings int
	}
	var samples []sample
	var expected string
	b.ResetTimer()
	for range b.N {
		model := &analyzerProfileModel{}
		provider := &analyzerProfileProvider{raw: raw, sources: sources}
		engine := review.NewEngine(cfg, provider, review.NoPolicy("frozen performance fixture"), func(policy *config.Config) (*llm.Roles, error) {
			client := llm.NewClientForTest(model, policy.Models.Default)
			return &llm.Roles{Review: client, Triage: client}, nil
		}, nil)
		engine.Linters = func(policy *config.Config) review.LinterRunner { return New(root, policy, nil) }
		started := time.Now()
		report, err := engine.Review(b.Context(), vcs.Ref{})
		elapsed := time.Since(started)
		b.StopTimer()
		if err != nil {
			b.Fatal(err)
		}
		if !report.PipelineComplete() {
			b.Fatalf("scripted review incomplete: %+v", report.Stages)
		}
		for _, status := range report.Linters {
			if status.Outcome == review.LinterFailed {
				b.Fatalf("analyzer failed: %+v", status)
			}
		}
		report.Elapsed = 0
		sort.Strings(model.requests)
		data, err := json.Marshal([]any{report, model.requests})
		if err != nil {
			b.Fatal(err)
		}
		data = []byte(strings.ReplaceAll(string(data), root, "<snapshot>"))
		digest := fmt.Sprintf("%x", sha256.Sum256(data))
		if expected == "" {
			expected = digest
		}
		if digest != expected {
			b.Fatal("ordered report or request multiset changed")
		}
		samples = append(samples, sample{Duration: elapsed, PayloadSHA256: digest, ModelCalls: len(model.requests), Findings: len(report.Findings)})
		b.StartTimer()
	}
	b.StopTimer()
	if directory := os.Getenv("NITPICK_PERFORMANCE_SAMPLES"); directory != "" {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			b.Fatal(err)
		}
		data, err := json.MarshalIndent(samples, "", "  ")
		if err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, name+"-review-analyzer.json"), data, 0o600); err != nil {
			b.Fatal(err)
		}
	}
}
