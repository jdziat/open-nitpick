package bundle

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nitpick-oss/nitpick/internal/config"
	"github.com/nitpick-oss/nitpick/internal/diff"
	"github.com/nitpick-oss/nitpick/internal/vcs"
)

func BenchmarkFrozenPublicWorkloadAssembly(b *testing.B) {
	manifest := os.Getenv("NITPICK_PERFORMANCE_WORKLOADS")
	if manifest == "" {
		b.Skip("set NITPICK_PERFORMANCE_WORKLOADS to the frozen benchmarks.json")
	}
	data, err := os.ReadFile(manifest)
	if err != nil {
		b.Fatal(err)
	}
	var workloads []struct{ Name, Diff, Sources string }
	if err := json.Unmarshal(data, &workloads); err != nil {
		b.Fatal(err)
	}
	if len(workloads) == 0 {
		b.Fatal("no frozen workloads")
	}
	for _, workload := range workloads {
		b.Run(workload.Name, func(b *testing.B) {
			raw, err := os.ReadFile(workload.Diff)
			if err != nil {
				b.Fatal(err)
			}
			files, err := diff.Parse(raw)
			if err != nil {
				b.Fatal(err)
			}
			if len(files) == 0 {
				b.Fatal("workload diff has no files")
			}
			data, err := os.ReadFile(workload.Sources)
			if err != nil {
				b.Fatal(err)
			}
			contents := map[string][]byte{}
			if err := json.Unmarshal(data, &contents); err != nil {
				b.Fatal(err)
			}
			dirs := snapshotDirectories(contents)
			var fetches atomic.Int64
			fetch := func(ctx context.Context, name string) ([]byte, error) {
				fetches.Add(1)
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				content, ok := contents[name]
				if !ok {
					return nil, os.ErrNotExist
				}
				return content, nil
			}
			list := func(ctx context.Context, name string) ([]string, error) {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				name = strings.TrimPrefix(path.Clean(name), "./")
				if name == "." {
					name = ""
				}
				values, ok := dirs[name]
				if !ok {
					return nil, os.ErrNotExist
				}
				return slices.Clone(values), nil
			}
			cfg := config.Defaults()
			cfg.Review.MaxFiles = 200
			cfg.Review.TokenBudgetPerRequest = 120000
			cfg.Review.IncludeFullFiles = true
			cfg.Review.RelatedContext = true
			expected, err := AssembleWith(b.Context(), cfg, files, fetch, list)
			if err != nil || expected.Files() == 0 {
				b.Fatalf("baseline assembly: %+v %v", expected, err)
			}
			reference, err := json.Marshal(expected)
			if err != nil {
				b.Fatal(err)
			}
			digest := sha256.Sum256(reference)
			if root := os.Getenv("NITPICK_PERFORMANCE_GIT"); root != "" {
				_, head, ok := strings.Cut(filepath.Base(filepath.Dir(workload.Diff)), "-")
				if !ok || len(head) != 40 {
					b.Fatal("frozen diff path must contain base-head commit IDs")
				}
				provider := vcs.NewLocal(root, io.Discard)
				fetch = func(ctx context.Context, name string) ([]byte, error) {
					fetches.Add(1)
					return provider.FileContent(ctx, vcs.Ref{Head: head}, name)
				}
			} else if os.Getenv("NITPICK_PERFORMANCE_WORKTREE") == "1" {
				root := b.TempDir()
				for name, data := range contents {
					if !filepath.IsLocal(filepath.FromSlash(name)) {
						b.Fatalf("unsafe frozen source path %q", name)
					}
					full := filepath.Join(root, filepath.FromSlash(name))
					if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
						b.Fatal(err)
					}
					if err := os.WriteFile(full, data, 0o600); err != nil {
						b.Fatal(err)
					}
				}
				provider := vcs.NewLocal(root, io.Discard)
				fetch = func(ctx context.Context, name string) ([]byte, error) {
					fetches.Add(1)
					return provider.FileContent(ctx, vcs.Ref{Head: vcs.Worktree}, name)
				}
			}
			fetches.Store(0)
			durations := make([]time.Duration, 0, b.N)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				started := time.Now()
				plan, err := AssembleWith(b.Context(), cfg, files, fetch, list)
				elapsed := time.Since(started)
				if err != nil {
					b.Fatal(err)
				}
				encoded, err := json.Marshal(plan)
				if err != nil {
					b.Fatal(err)
				}
				if sha256.Sum256(encoded) != digest {
					b.Fatal("ordered plan changed for identical frozen input")
				}
				durations = append(durations, elapsed)
			}
			b.StopTimer()
			if directory := os.Getenv("NITPICK_PERFORMANCE_SAMPLES"); directory != "" {
				if err := os.MkdirAll(directory, 0o700); err != nil {
					b.Fatal(err)
				}
				artifact := struct {
					Name, OrderedPlanSHA256 string
					Durations               []time.Duration
				}{workload.Name, fmt.Sprintf("%x", digest), durations}
				data, err := json.MarshalIndent(artifact, "", "  ")
				if err != nil {
					b.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(directory, workload.Name+".json"), data, 0o600); err != nil {
					b.Fatal(err)
				}
			}
			slices.Sort(durations)
			if len(durations) > 0 {
				b.ReportMetric(float64(durations[(len(durations)-1)*95/100].Nanoseconds()), "assembly-p95-ns")
			}
			b.ReportMetric(float64(fetches.Load())/float64(b.N), "fetches/op")
			b.ReportMetric(float64(expected.Files()), "files/op")
			b.Logf("ordered-plan-sha256=%x", digest)
		})
	}
}

func snapshotDirectories(contents map[string][]byte) map[string][]string {
	entries := map[string]map[string]bool{}
	add := func(dir, name string) {
		if dir == "." {
			dir = ""
		}
		if entries[dir] == nil {
			entries[dir] = map[string]bool{}
		}
		entries[dir][name] = true
	}
	for name := range contents {
		dir := path.Dir(name)
		add(dir, path.Base(name))
		for dir != "." && dir != "" {
			parent := path.Dir(dir)
			add(parent, path.Base(dir)+"/")
			dir = parent
		}
	}
	out := map[string][]string{}
	for dir, names := range entries {
		for name := range names {
			out[dir] = append(out[dir], name)
		}
		slices.Sort(out[dir])
	}
	return out
}
