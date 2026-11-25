package linters

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nitpick-oss/nitpick/internal/config"
	"github.com/nitpick-oss/nitpick/internal/diff"
	"github.com/nitpick-oss/nitpick/internal/review"
)

func BenchmarkFrozenAnalyzerDetection(b *testing.B) {
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
			data, err := os.ReadFile(workload.Diff)
			if err != nil {
				b.Fatal(err)
			}
			files, err := diff.Parse(data)
			if err != nil || len(files) == 0 {
				b.Fatalf("frozen diff: %v", err)
			}
			raw := data
			data, err = os.ReadFile(workload.Sources)
			if err != nil {
				b.Fatal(err)
			}
			var sources map[string][]byte
			if err := json.Unmarshal(data, &sources); err != nil {
				b.Fatal(err)
			}
			root := b.TempDir()
			for name, content := range sources {
				if !filepath.IsLocal(name) {
					b.Fatalf("source path escapes snapshot: %q", name)
				}
				target := filepath.Join(root, name)
				if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
					b.Fatal(err)
				}
				if err := os.WriteFile(target, content, 0o600); err != nil {
					b.Fatal(err)
				}
			}
			cfg := config.Defaults()
			if mode := os.Getenv("NITPICK_PERFORMANCE_RUN_ANALYZERS"); mode != "" {
				cfg.Linters.Enabled = []string{"golangci-lint"}
				cfg.Linters.AutoDetect = new(bool)
				if mode == "review" {
					b.Run("scripted-review", func(b *testing.B) {
						benchmarkReviewWithAnalyzer(b, workload.Name, root, cfg, raw, sources)
					})
					return
				}
				b.Run("actual-run", func(b *testing.B) {
					benchmarkAnalyzerRun(b, workload.Name, root, cfg, files)
				})
				return
			}
			set := New(root, cfg, nil)
			paths, rejected := safePaths(reviewablePaths(cfg, files))
			if len(paths) == 0 || len(rejected) > 0 || len(set.runners) == 0 {
				b.Fatal("frozen workload did not select analyzer detection")
			}
			type result struct{ Name, Error string }
			detect := func(ctx context.Context, parallel bool) []result {
				results := make([]result, len(set.runners))
				var workers sync.WaitGroup
				for i, runner := range set.runners {
					load := func() {
						results[i].Name = runner.Name()
						if err := runner.Detect(ctx, root, paths); err != nil {
							results[i].Error = strings.ReplaceAll(err.Error(), root, "<snapshot>")
						}
					}
					if parallel {
						workers.Go(load)
					} else {
						load()
					}
				}
				workers.Wait()
				return results
			}
			expected, err := json.Marshal(detect(b.Context(), false))
			if err != nil {
				b.Fatal(err)
			}
			for _, parallel := range []bool{false, true} {
				b.Run(fmt.Sprintf("parallel=%t", parallel), func(b *testing.B) {
					samples := make([]time.Duration, 0, b.N)
					b.ReportAllocs()
					b.ResetTimer()
					for range b.N {
						started := time.Now()
						results := detect(b.Context(), parallel)
						samples = append(samples, time.Since(started))
						b.StopTimer()
						actual, err := json.Marshal(results)
						if err != nil || string(actual) != string(expected) {
							b.Fatalf("detection roster changed: %s, want %s, err=%v", actual, expected, err)
						}
						b.StartTimer()
					}
					b.StopTimer()
					if directory := os.Getenv("NITPICK_PERFORMANCE_SAMPLES"); directory != "" {
						if err := os.MkdirAll(directory, 0o700); err != nil {
							b.Fatal(err)
						}
						value := struct {
							Name                   string
							Parallel               bool
							OrderedDetectionSHA256 string
							Durations              []time.Duration
						}{workload.Name, parallel, fmt.Sprintf("%x", sha256.Sum256(expected)), samples}
						data, err := json.MarshalIndent(value, "", "  ")
						if err != nil {
							b.Fatal(err)
						}
						target := filepath.Join(directory, fmt.Sprintf("%s-parallel-%t.json", workload.Name, parallel))
						if err := os.WriteFile(target, data, 0o600); err != nil {
							b.Fatal(err)
						}
					}
					b.Logf("ordered-detection-sha256=%x, runners=%d", sha256.Sum256(expected), len(set.runners))
				})
			}
		})
	}
}

func benchmarkAnalyzerRun(b *testing.B, name, root string, cfg *config.Config, files diff.Files) {
	b.Helper()
	type sample struct {
		Duration               time.Duration
		PayloadSHA256          string
		Ran, Failed, NoTargets int
	}
	var samples []sample
	var expected string
	b.ResetTimer()
	for range b.N {
		set := New(root, cfg, nil)
		started := time.Now()
		findings, err := set.Run(b.Context(), files)
		elapsed := time.Since(started)
		b.StopTimer()
		current := sample{Duration: elapsed}
		for _, status := range set.Statuses() {
			switch status.Outcome {
			case review.LinterRan:
				current.Ran++
			case review.LinterFailed:
				current.Failed++
			case review.LinterSkipped:
				if status.NoTargets {
					current.NoTargets++
				}
			}
		}
		if err != nil || current.Failed != 0 || current.Ran+current.NoTargets == 0 {
			b.Fatalf("analyzer did not run or prove no targets: %v, statuses=%+v", err, set.Statuses())
		}
		payload, err := json.Marshal([]any{findings, set.Statuses(), set.Discarded(), set.Uncovered()})
		if err != nil {
			b.Fatal(err)
		}
		payload = []byte(strings.ReplaceAll(string(payload), root, "<snapshot>"))
		current.PayloadSHA256 = fmt.Sprintf("%x", sha256.Sum256(payload))
		if expected == "" {
			expected = current.PayloadSHA256
		}
		if current.PayloadSHA256 != expected {
			b.Fatal("analyzer results or disclosure changed between runs")
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
		if err := os.WriteFile(filepath.Join(directory, name+"-analyzer-run.json"), data, 0o600); err != nil {
			b.Fatal(err)
		}
	}
}
