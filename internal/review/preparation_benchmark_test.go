package review

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	llms "github.com/nocturnium/llm-go-sdk/v6"

	"github.com/nitpick-oss/nitpick/internal/bundle"
	"github.com/nitpick-oss/nitpick/internal/config"
	"github.com/nitpick-oss/nitpick/internal/diff"
	"github.com/nitpick-oss/nitpick/internal/llm"
	"github.com/nitpick-oss/nitpick/internal/memo"
	"github.com/nitpick-oss/nitpick/internal/vcs"
)

type sourceProfileProvider struct {
	stubProvider
	sources  map[string][]byte
	dirs     map[string][]string
	memoize  bool
	delay    time.Duration
	files    memo.Cache[string, []byte]
	listings memo.Cache[string, []string]
	mu       sync.Mutex
	reads    map[string]int
	lists    map[string]int
}

type preparationModel struct {
	scriptedLLM
	requestMu sync.Mutex
	requests  []string
}

func (m *preparationModel) GenerateContent(ctx context.Context, messages []llms.Message, options ...llms.CallOption) (*llms.Response, error) {
	data, err := json.Marshal(struct {
		Messages []llms.Message
		Options  *llms.CallOptions
	}{messages, llms.ApplyOptions(options...)})
	if err != nil {
		return nil, err
	}
	m.requestMu.Lock()
	m.requests = append(m.requests, string(data))
	m.requestMu.Unlock()
	return m.scriptedLLM.GenerateContent(ctx, messages, options...)
}

func (p *sourceProfileProvider) wait(ctx context.Context) error {
	if p.delay == 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(p.delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *sourceProfileProvider) FileContent(ctx context.Context, _ vcs.Ref, name string) ([]byte, error) {
	load := func() ([]byte, error) {
		p.mu.Lock()
		p.reads[name]++
		p.mu.Unlock()
		if err := p.wait(ctx); err != nil {
			return nil, err
		}
		data, ok := p.sources[name]
		if !ok {
			return nil, vcs.ErrNotFound
		}
		return data, ctx.Err()
	}
	if p.memoize {
		return p.files.Get(ctx, name, load)
	}
	return load()
}

func (p *sourceProfileProvider) ListDir(ctx context.Context, _ vcs.Ref, dir string) ([]string, error) {
	dir = strings.Trim(path.Clean("/"+dir), "/")
	load := func() ([]string, error) {
		p.mu.Lock()
		p.lists[dir]++
		p.mu.Unlock()
		if err := p.wait(ctx); err != nil {
			return nil, err
		}
		files, ok := p.dirs[dir]
		if !ok {
			return nil, vcs.ErrNotFound
		}
		return slices.Clone(files), ctx.Err()
	}
	if p.memoize {
		return p.listings.Get(ctx, dir, load)
	}
	return load()
}

func profileSourceDirectories(sources map[string][]byte) map[string][]string {
	dirs := map[string][]string{}
	for file := range sources {
		for dir, base := path.Dir(file), path.Base(file); ; {
			if dir == "." {
				dir = ""
			}
			dirs[dir] = append(dirs[dir], base)
			if dir == "" {
				break
			}
			dir, base = path.Dir(dir), path.Base(dir)+"/"
		}
	}
	for dir, names := range dirs {
		sort.Strings(names)
		dirs[dir] = slices.Compact(names)
	}
	return dirs
}

func BenchmarkFrozenReviewPreparation(b *testing.B) {
	var delay time.Duration
	if value := os.Getenv("NITPICK_PERFORMANCE_SOURCE_DELAY"); value != "" {
		var err error
		delay, err = time.ParseDuration(value)
		if err != nil || delay < 0 {
			b.Fatalf("source delay: %v", err)
		}
	}
	manifest := os.Getenv("NITPICK_PERFORMANCE_WORKLOADS")
	if manifest == "" {
		b.Skip("set NITPICK_PERFORMANCE_WORKLOADS to frozen benchmarks.json")
	}
	data, err := os.ReadFile(manifest)
	if err != nil {
		b.Fatal(err)
	}
	var workloads []struct{ Name, Diff, Sources string }
	if err := json.Unmarshal(data, &workloads); err != nil || len(workloads) == 0 {
		b.Fatalf("decode frozen workloads: %v", err)
	}
	for _, workload := range workloads {
		b.Run(workload.Name, func(b *testing.B) {
			raw, err := os.ReadFile(workload.Diff)
			if err != nil {
				b.Fatal(err)
			}
			data, err := os.ReadFile(workload.Sources)
			if err != nil {
				b.Fatal(err)
			}
			var sources map[string][]byte
			if err := json.Unmarshal(data, &sources); err != nil {
				b.Fatal(err)
			}
			dirs := profileSourceDirectories(sources)
			for _, budget := range []bool{false, true} {
				var expected []byte
				for _, memoize := range []bool{false, true} {
					b.Run(fmt.Sprintf("budget=%t/memo=%t", budget, memoize), func(b *testing.B) {
						type sample struct {
							Duration                                                                  time.Duration
							FileReads, DistinctFiles, DirectoryReads, DistinctDirectories, ModelCalls int
							PlanAndRequestsSHA256                                                     string
						}
						samples := make([]sample, 0, b.N)
						b.ReportAllocs()
						b.ResetTimer()
						for range b.N {
							cfg := config.Defaults()
							cfg.Models.Default = config.ModelSpec{Provider: "openai", Model: "scripted"}
							cfg.Review.Flow = true
							if budget {
								cfg.Review.Budget = config.Budget{MaxSpend: 0.03, Prices: config.BudgetPrices{Input: 1, Output: 1}, CompletionRatio: 1, Overhead: 1, MinFiles: 1}
							}
							provider := &sourceProfileProvider{stubProvider: stubProvider{diff: string(raw)}, sources: sources, dirs: dirs, memoize: memoize, delay: delay, reads: map[string]int{}, lists: map[string]int{}}
							model := &preparationModel{scriptedLLM: scriptedLLM{fallback: `{"findings":[],"summary":"","verdicts":[]}`}}
							client := llm.NewClientForTest(model, cfg.Models.Default)
							engine := NewEngine(cfg, provider, NoPolicy("frozen offline performance fixture"), func(*config.Config) (*llm.Roles, error) {
								return &llm.Roles{Review: client, Triage: client}, nil
							}, nil)
							started := time.Now()
							report, err := engine.Review(b.Context(), vcs.Ref{})
							elapsed := time.Since(started)
							b.StopTimer()
							if err != nil || report == nil || !report.Complete() || report.Plan == nil {
								b.Fatalf("scripted review did not complete: %v", err)
							}
							plan := *report.Plan
							model.requestMu.Lock()
							prompts := slices.Clone(model.requests)
							model.requestMu.Unlock()
							// Review workers can submit independent batches in either order.
							sort.Strings(prompts)
							identity, err := json.Marshal(struct {
								Plan    bundle.Plan
								Prompts []string
								Budget  *Fit
							}{plan, prompts, report.Budget})
							if err != nil {
								b.Fatal(err)
							}
							if expected == nil {
								expected = identity
							} else if string(identity) != string(expected) {
								b.Fatal("capturing source changed a prepared plan, model input, or budget")
							}
							current := sample{Duration: elapsed, DistinctFiles: len(provider.reads), DistinctDirectories: len(provider.lists), ModelCalls: model.callCount(), PlanAndRequestsSHA256: fmt.Sprintf("%x", sha256.Sum256(identity))}
							for _, count := range provider.reads {
								current.FileReads += count
							}
							for _, count := range provider.lists {
								current.DirectoryReads += count
							}
							samples = append(samples, current)
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
							name := fmt.Sprintf("%s-budget-%t-memo-%t.json", workload.Name, budget, memoize)
							if err := os.WriteFile(filepath.Join(directory, name), data, 0o600); err != nil {
								b.Fatal(err)
							}
						}
					})
				}
			}
		})
	}
}

func BenchmarkFrozenEngineeringPreparation(b *testing.B) {
	manifest := os.Getenv("NITPICK_PERFORMANCE_WORKLOADS")
	if manifest == "" {
		b.Skip("set NITPICK_PERFORMANCE_WORKLOADS to frozen benchmarks.json")
	}
	data, err := os.ReadFile(manifest)
	if err != nil {
		b.Fatal(err)
	}
	var workloads []struct{ Name, Diff, Sources string }
	if err := json.Unmarshal(data, &workloads); err != nil || len(workloads) == 0 {
		b.Fatalf("decode frozen workloads: %v", err)
	}
	for _, workload := range workloads {
		b.Run(workload.Name, func(b *testing.B) {
			raw, err := os.ReadFile(workload.Diff)
			if err != nil {
				b.Fatal(err)
			}
			files, err := diff.Parse(raw)
			if err != nil || len(files) == 0 {
				b.Fatalf("frozen diff: %v", err)
			}
			data, err := os.ReadFile(workload.Sources)
			if err != nil {
				b.Fatal(err)
			}
			var sources map[string][]byte
			if err := json.Unmarshal(data, &sources); err != nil {
				b.Fatal(err)
			}
			dirs := profileSourceDirectories(sources)
			type sample struct {
				Duration                        time.Duration
				PlanSHA256                      string
				Tasks, Batches, Skipped, Errors int
			}
			samples := make([]sample, 0, b.N)
			var expected string
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				cfg := config.Defaults()
				cfg.Practices.Profile = "engineering"
				cfg.Models.Default = config.ModelSpec{Provider: "openai", Model: "scripted"}
				provider := &sourceProfileProvider{stubProvider: stubProvider{diff: string(raw)}, sources: sources, dirs: dirs, reads: map[string]int{}, lists: map[string]int{}}
				engine := NewEngine(cfg, provider, nil, nil, nil)
				pr, err := provider.PullRequest(b.Context(), vcs.Ref{})
				if err != nil {
					b.Fatal(err)
				}
				reserve := bundle.Reserve{Tokens: engine.framingTokens(pr)}
				started := time.Now()
				execution := engine.assembleDesign(b.Context(), vcs.Ref{}, files, reserve)
				elapsed := time.Since(started)
				b.StopTimer()
				if execution.Plan == nil || len(execution.Design.Tasks) == 0 {
					b.Fatal("engineering planning did not run")
				}
				data, err := json.Marshal(execution)
				if err != nil {
					b.Fatal(err)
				}
				digest := fmt.Sprintf("%x", sha256.Sum256(data))
				if expected == "" {
					expected = digest
				} else if digest != expected {
					b.Fatal("engineering plan changed between iterations")
				}
				samples = append(samples, sample{Duration: elapsed, PlanSHA256: digest, Tasks: len(execution.Design.Tasks), Batches: len(execution.Plan.Batches), Skipped: len(execution.Plan.Skipped), Errors: len(execution.Design.Errors)})
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
				if err := os.WriteFile(filepath.Join(directory, workload.Name+"-engineering.json"), data, 0o600); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
