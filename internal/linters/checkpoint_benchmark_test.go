package linters

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/llm"
	"github.com/jdziat/open-nitpick/internal/review"
	"github.com/jdziat/open-nitpick/internal/vcs"
	llms "github.com/nocturnium/llm-go-sdk/v6"
)

type checkpointBenchmarkModel struct{ calls int }

func (m *checkpointBenchmarkModel) GenerateContent(context.Context, []llms.Message, ...llms.CallOption) (*llms.Response, error) {
	m.calls++
	return &llms.Response{Content: `{"findings":[],"summary":"fixture"}`}, nil
}
func (*checkpointBenchmarkModel) Stream(context.Context, []llms.Message, ...llms.CallOption) (<-chan llms.StreamChunk, error) {
	return nil, errors.New("benchmark does not stream")
}
func (*checkpointBenchmarkModel) Provider() llms.Provider { return "scripted" }
func (*checkpointBenchmarkModel) Model() string           { return "checkpoint-fixture" }

type checkpointBenchmarkProvider struct {
	prior     *vcs.PriorReview
	published vcs.Review
}

func (*checkpointBenchmarkProvider) Name() string { return "checkpoint-fixture" }
func (*checkpointBenchmarkProvider) PullRequest(context.Context, vcs.Ref) (*vcs.PullRequest, error) {
	return &vcs.PullRequest{Number: 1, HeadSHA: "frozen-head", Title: "Python fixture"}, nil
}
func (*checkpointBenchmarkProvider) Diff(context.Context, vcs.Ref) ([]byte, error) {
	return []byte("diff --git a/app.py b/app.py\n--- a/app.py\n+++ b/app.py\n@@ -0,0 +1 @@\n+print('hello')\n"), nil
}
func (*checkpointBenchmarkProvider) FileContent(context.Context, vcs.Ref, string) ([]byte, error) {
	return []byte("print('hello')\n"), nil
}
func (p *checkpointBenchmarkProvider) PriorReview(context.Context, vcs.Ref) (*vcs.PriorReview, error) {
	return p.prior, nil
}
func (p *checkpointBenchmarkProvider) PublishReview(_ context.Context, _ vcs.Ref, result vcs.Review) error {
	p.published = result
	return nil
}

type checkpointCountingSet struct {
	*Set
	calls *int
	keys  *[]string
}

var _ review.LinterCheckpointer = checkpointCountingSet{}

func (s checkpointCountingSet) CheckpointKey(ctx context.Context, files diff.Files) string {
	key := s.Set.CheckpointKey(ctx, files)
	*s.keys = append(*s.keys, key)
	return key
}

func (s checkpointCountingSet) Run(ctx context.Context, files diff.Files) ([]review.Finding, error) {
	*s.calls++
	return s.Set.Run(ctx, files)
}

type analyzerCheckpointSample struct {
	Arm                  string
	DurationNS           int64
	AnalyzerInvocations  int
	ModelCalls           int
	ReusedAnalyzerStages int
	ReusedModelRequests  int
	MetadataSHA256       string
	IdentityChecks       []string
}

func BenchmarkFrozenAnalyzerCheckpointResume(b *testing.B) {
	directory := os.Getenv("NITPICK_PERFORMANCE_SAMPLES")
	if directory == "" {
		b.Skip("set NITPICK_PERFORMANCE_SAMPLES to retain the registered experiment")
	}
	if _, ok := checkpointExecutable("ruff"); !ok {
		b.Fatal("the registered installed native Ruff is unavailable")
	}
	repo := checkpointRepo(b)
	baselineRepo := checkpointRepo(b)
	cfg := config.Defaults()
	cfg.Models.Default = config.ModelSpec{Provider: "openai", Model: "checkpoint-fixture"}
	cfg.Review.Incremental = true
	cfg.Linters.Enabled = []string{"ruff"}
	autoDetect := false
	cfg.Linters.AutoDetect = &autoDetect
	ctx := context.Background()
	var samples []analyzerCheckpointSample
	b.ResetTimer()
	for range b.N {
		provider := &checkpointBenchmarkProvider{}
		var proof string
		var freshKeys []string
		for _, arm := range []string{"resume disabled", "resume enabled fresh", "resume enabled reused"} {
			model := &checkpointBenchmarkModel{}
			client := llm.NewClientForTest(model, cfg.Models.Default)
			engine := review.NewEngine(cfg, provider, review.NoPolicy("isolated benchmark fixture"), func(*config.Config) (*llm.Roles, error) {
				return &llm.Roles{Review: client, Triage: client}, nil
			}, nil)
			engine.Resume = arm != "resume disabled"
			calls := 0
			var keys []string
			var lastSet *Set
			engine.Linters = func(policy *config.Config) review.LinterRunner {
				root := repo
				if arm == "resume disabled" {
					root = baselineRepo
				}
				lastSet = New(root, policy, nil)
				return checkpointCountingSet{Set: lastSet, calls: &calls, keys: &keys}
			}
			start := time.Now()
			report, err := engine.Review(ctx, vcs.Ref{})
			duration := time.Since(start)
			if err != nil || report == nil || len(report.Incomplete) != 0 || len(report.Linters) != 1 || report.Linters[0].Outcome != review.LinterRan {
				b.Fatalf("%s did not complete real Ruff analysis: err=%v report=%+v", arm, err, report)
			}
			payload, err := json.Marshal([]any{report.Findings, report.Linters, report.Discarded, report.Uncovered})
			if err != nil {
				b.Fatal(err)
			}
			sum := sha256.Sum256(payload)
			hash := hex.EncodeToString(sum[:])
			if proof != "" && hash != proof {
				b.Fatal("checkpoint changed findings or analyzer coverage metadata")
			}
			proof = hash
			if arm == "resume enabled reused" && (calls != 0 || model.calls != 0 || report.ReusedAnalyzerStages != 1) {
				status, _ := lastSet.checkpointCommand(ctx, "git", "status", "--porcelain=v1", "--untracked-files=all", "--ignored=matching")
				_, configErr := json.Marshal(lastSet.cfg)
				b.Fatalf("repeat invoked work: analyzers=%d models=%d reused=%d fresh identities=%q repeated identities=%q status=%q config-encoding=%v ruff-config=%q runner-count=%d", calls, model.calls, report.ReusedAnalyzerStages, freshKeys, keys, status, configErr, lastSet.cfg.Linters.RuffConfig, len(lastSet.runners))
			}
			if arm != "resume enabled reused" && (calls != 1 || model.calls == 0) {
				b.Fatal("fresh arm did not execute its analyzer and scripted model")
			}
			samples = append(samples, analyzerCheckpointSample{Arm: arm, DurationNS: duration.Nanoseconds(), AnalyzerInvocations: calls, ModelCalls: model.calls, ReusedAnalyzerStages: report.ReusedAnalyzerStages, ReusedModelRequests: report.ReusedRequests, MetadataSHA256: hash, IdentityChecks: keys})
			if arm == "resume enabled fresh" {
				freshKeys = keys
				provider.prior = &vcs.PriorReview{Progress: provider.published.Progress}
			}
		}
	}
	b.StopTimer()
	data, err := json.MarshalIndent(samples, "", "  ")
	if err != nil {
		b.Fatal(err)
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "analyzer-checkpoint.json"), data, 0o600); err != nil {
		b.Fatal(err)
	}
}
