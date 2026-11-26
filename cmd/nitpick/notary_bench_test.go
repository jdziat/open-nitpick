package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The notary benchmark. It measures what a notary changes about a review's
// cost and determinism, against a local model whose requests it counts at the
// server. It does not measure review quality: the model is a fixture, so
// nothing here says anything about what a real model would find.
//
//	NITPICK_NOTARY_BENCH=1 NITPICK_NOTARY_BENCH_OUT=/tmp/notary-bench.json \
//	  go test ./cmd/nitpick -run TestNotaryBenchmarkComparesReplayAgainstNoNotary -v -timeout 30m

const benchReps = 5

// The request body is JSON, so a newline is the two characters \n. A path
// pattern of \S+ swallowed "f00.go\nChange:" and every finding was discarded as
// anchored to a file that is not in the change.
var benchFile = regexp.MustCompile(`### File: ([^\s\\]+)`)

// benchModel is an OpenAI-compatible endpoint that answers one finding per
// file and counts what it is asked.
type benchModel struct {
	srv     *httptest.Server
	review  atomic.Int64
	triage  atomic.Int64
	latency atomic.Int64 // nanoseconds per request
	// failReviewFrom makes every review request after this many fail, the way
	// a dropped connection would. Negative never fails.
	failReviewFrom atomic.Int64
	// drift puts a per-request counter in the finding, so two runs of the same
	// review differ: the property of a real model that a replay removes.
	drift atomic.Bool
	seq   atomic.Int64
}

func newBenchModel(t *testing.T) *benchModel {
	t.Helper()
	m := &benchModel{}
	m.failReviewFrom.Store(-1)
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		text := string(body)
		if d := time.Duration(m.latency.Load()); d > 0 {
			time.Sleep(d)
		}
		var content string
		if strings.Contains(text, "triaging findings") {
			m.triage.Add(1)
			content = `{"summary":"Adds a greeting.","verdicts":[]}`
		} else {
			n := m.review.Add(1)
			if from := m.failReviewFrom.Load(); from >= 0 && n > from {
				http.Error(w, "upstream reset", http.StatusBadRequest)
				return
			}
			path := "hello.go"
			if match := benchFile.FindStringSubmatch(text); match != nil {
				path = match[1]
			}
			title := "Ignored error from http.Get"
			if m.drift.Load() {
				title = fmt.Sprintf("%s (take %d)", title, m.seq.Add(1))
			}
			finding, _ := json.Marshal(map[string]any{
				"path": path, "line": 4, "severity": "error", "category": "correctness",
				"title": title, "rationale": "resp may be nil, so the deferred Close panics.", "confidence": 0.9,
			})
			content = `{"findings":[` + string(finding) + `]}`
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "x", "object": "chat.completion", "model": "fake",
			"choices": []map[string]any{{"index": 0, "finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": content}}},
			"usage":   map[string]int{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
		})
	}))
	t.Cleanup(m.srv.Close)
	return m
}

func (m *benchModel) requests() int64 { return m.review.Load() + m.triage.Load() }

// reset zeroes the request counters between measurements. The drift sequence
// is left alone: resetting it made every run say "take 1", and the model the
// determinism arm exists to measure stopped drifting.
func (m *benchModel) reset() {
	m.review.Store(0)
	m.triage.Store(0)
}

// benchRepo is a git repository whose working tree changes files files, one
// batch each (max_files_per_request is 1).
func benchRepo(t *testing.T, m *benchModel, files int) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-q", "-b", "main")
	write(".nitpick.yaml", fmt.Sprintf(`models:
  default:
    provider: llamacpp
    model: fake
    base_url: %s
review:
  incremental: false
  critique: false
  max_files_per_request: 1
`, m.srv.URL))
	for i := range files {
		write(fmt.Sprintf("f%02d.go", i), "package hello\n\nfunc Hi() {}\n")
	}
	run("add", ".")
	run("commit", "-q", "-m", "base")
	for i := range files {
		write(fmt.Sprintf("f%02d.go", i), "package hello\n\nfunc Hi() {\n\tresp, _ := http.Get(\"http://x\")\n\tdefer resp.Body.Close()\n}\n")
	}
	return dir
}

type benchRun struct {
	out      map[string]any
	wall     time.Duration
	requests int64
	err      error
}

func (r benchRun) mode() string {
	n, _ := r.out["notary"].(map[string]any)
	s, _ := n["Mode"].(string)
	return s
}

func (r benchRun) notaryInt(key string) int {
	n, _ := r.out["notary"].(map[string]any)
	v, _ := n[key].(float64)
	return int(v)
}

// findings is a hash of the published findings, so two runs are compared on
// what they said rather than on how long they took.
func (r benchRun) findings() string {
	raw, _ := json.Marshal(r.out["findings"])
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:8])
}

func (r benchRun) count() int {
	f, _ := r.out["findings"].([]any)
	return len(f)
}

func measure(t *testing.T, m *benchModel, repo string, args ...string) benchRun {
	t.Helper()
	m.reset()
	started := time.Now()
	out, err := reviewJSON(t, repo, args...)
	return benchRun{out: out, wall: time.Since(started), requests: m.requests(), err: err}
}

// wantFindings fails a run that found nothing. Every comparison below is of
// what two reviews said, and two empty reviews agree about anything.
func wantFindings(t *testing.T, r benchRun, batches int, what string) {
	t.Helper()
	if r.count() != batches {
		t.Fatalf("%s: %d findings, want %d (one per file): the comparison would be of empty sets", what, r.count(), batches)
	}
}

type cell struct {
	Batches   int     `json:"batches"`
	LatencyMS int     `json:"model_latency_ms"`
	Arm       string  `json:"arm"`
	N         int     `json:"n"`
	Requests  []int64 `json:"requests"`
	WallMS    []int64 `json:"wall_ms"`
	Findings  []int   `json:"findings"`
	Distinct  int     `json:"distinct_finding_sets"`
	Modes     string  `json:"modes"`
	Record    []int64 `json:"record_bytes,omitempty"`
}

func median(v []int64) int64 {
	s := append([]int64(nil), v...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return s[len(s)/2]
}

func span(v []int64) string {
	s := append([]int64(nil), v...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return fmt.Sprintf("%d-%d", s[0], s[len(s)-1])
}

func TestNotaryBenchmarkComparesReplayAgainstNoNotary(t *testing.T) {
	if os.Getenv("NITPICK_NOTARY_BENCH") == "" {
		t.Skip("set NITPICK_NOTARY_BENCH=1 to run the notary benchmark")
	}
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	oldStderr := os.Stderr
	os.Stderr = devnull
	t.Cleanup(func() { os.Stderr = oldStderr; _ = devnull.Close() })
	t.Setenv(EnvNotaryToken, "ci-secret")

	var cells []cell
	add := func(c *cell, r benchRun) {
		c.Requests = append(c.Requests, r.requests)
		c.WallMS = append(c.WallMS, r.wall.Milliseconds())
		c.Findings = append(c.Findings, r.count())
	}

	for _, batches := range []int{1, 4, 16} {
		for _, latencyMS := range []int{0, 200} {
			model := newBenchModel(t)
			model.latency.Store(int64(time.Duration(latencyMS) * time.Millisecond))
			repo := benchRepo(t, model, batches)
			mk := func(arm string) *cell {
				return &cell{Batches: batches, LatencyMS: latencyMS, Arm: arm}
			}
			none, first, replay, fresh := mk("no notary"), mk("notary, first review"), mk("notary, replay"), mk("notary, -fresh")
			var noneSets, firstSets, replaySets, freshSets []string
			// Interleaved: every replicate runs every arm in turn, so a drift in
			// the machine moves all of them together.
			for range benchReps {
				url, store := notaryServer(t)

				r := measure(t, model, repo)
				if r.err != nil {
					t.Fatalf("no notary: %v", r.err)
				}
				wantFindings(t, r, batches, "no notary")
				add(none, r)
				noneSets = append(noneSets, r.findings())

				r = measure(t, model, repo, "-notary", url)
				if r.err != nil {
					t.Fatalf("notary first: %v", r.err)
				}
				if r.mode() != "live" || r.requests == 0 {
					t.Fatalf("the first review did not ask the model (mode=%q requests=%d): nothing was measured", r.mode(), r.requests)
				}
				wantFindings(t, r, batches, "notary, first review")
				add(first, r)
				firstSets = append(firstSets, r.findings())
				liveSet := r.findings()
				hash, _ := r.out["notary"].(map[string]any)["Hash"].(string)
				rec, ok, err := store.Get(context.Background(), hash)
				if err != nil || !ok {
					t.Fatalf("the record the first review wrote is not in the store: ok=%v err=%v", ok, err)
				}
				raw, _ := json.Marshal(rec)
				first.Record = append(first.Record, int64(len(raw)))

				r = measure(t, model, repo, "-notary", url)
				if r.err != nil {
					t.Fatalf("notary replay: %v", r.err)
				}
				// Zero is only a result when the notary was asked and answered:
				// an empty run and a replay of nothing print the same.
				if r.mode() != "replayed" || r.notaryInt("Replayed") == 0 {
					t.Fatalf("the replay did not replay (mode=%q replayed=%d)", r.mode(), r.notaryInt("Replayed"))
				}
				if r.requests != 0 {
					t.Errorf("the replay made %d model requests at the server, want 0", r.requests)
				}
				if r.findings() != liveSet {
					t.Errorf("the replay said something different from the review it replayed")
				}
				wantFindings(t, r, batches, "notary, replay")
				add(replay, r)
				replaySets = append(replaySets, r.findings())

				r = measure(t, model, repo, "-notary", url, "-fresh", "-clear-by", "bench")
				if r.err != nil {
					t.Fatalf("notary fresh: %v", r.err)
				}
				wantFindings(t, r, batches, "notary, -fresh")
				add(fresh, r)
				freshSets = append(freshSets, r.findings())
			}
			for c, sets := range map[*cell][]string{none: noneSets, first: firstSets, replay: replaySets, fresh: freshSets} {
				c.N = len(sets)
				seen := map[string]bool{}
				for _, s := range sets {
					seen[s] = true
				}
				c.Distinct = len(seen)
			}
			cells = append(cells, *none, *first, *replay, *fresh)
		}
	}

	// Determinism: a model that answers differently each time. Without a
	// notary two reviews of one change disagree; a replay repeats the first.
	drift := newBenchModel(t)
	drift.drift.Store(true)
	driftRepo := benchRepo(t, drift, 4)
	var plain, replayed []string
	for range benchReps {
		r := measure(t, drift, driftRepo)
		if r.err != nil {
			t.Fatal(r.err)
		}
		wantFindings(t, r, 4, "drift, no notary")
		plain = append(plain, r.findings())
	}
	url, _ := notaryServer(t)
	for i := range benchReps {
		r := measure(t, drift, driftRepo, "-notary", url)
		if r.err != nil {
			t.Fatal(r.err)
		}
		if i > 0 && (r.mode() != "replayed" || r.requests != 0) {
			t.Fatalf("review %d of a recorded change was not a replay (mode=%q requests=%d)", i+1, r.mode(), r.requests)
		}
		wantFindings(t, r, 4, "drift, notary")
		replayed = append(replayed, r.findings())
	}
	distinct := func(v []string) int {
		seen := map[string]bool{}
		for _, s := range v {
			seen[s] = true
		}
		return len(seen)
	}
	// A determinism result is only one if the model drifted: a model that
	// never differed would make "the replays agree" true of any implementation.
	if distinct(plain) < 2 {
		t.Fatalf("the drifting model gave %d distinct finding sets over %d reviews: nothing was measured", distinct(plain), benchReps)
	}
	if distinct(replayed) != 1 {
		t.Errorf("reviews of one recorded change disagreed: %d distinct finding sets", distinct(replayed))
	}

	// Resume: a review that loses part of its answers, then runs again.
	type resumeRow struct {
		Batches            int   `json:"batches"`
		AnsweredBeforeLoss int   `json:"answered_before_the_loss"`
		FirstRunRequests   int64 `json:"interrupted_run_requests"`
		SecondNoNotary     int64 `json:"second_run_requests_no_notary"`
		SecondNotary       int64 `json:"second_run_requests_notary"`
		SecondNotaryMode   string
		SecondNotaryDone   bool
	}
	var resumes []resumeRow
	for _, batches := range []int{4, 16} {
		answered := batches / 2
		for _, useNotary := range []bool{false, true} {
			model := newBenchModel(t)
			repo := benchRepo(t, model, batches)
			var args []string
			if useNotary {
				url, _ := notaryServer(t)
				args = []string{"-notary", url}
			}
			model.failReviewFrom.Store(int64(answered))
			first := measure(t, model, repo, args...)
			_ = first.err // the interrupted review is expected to end in error or incomplete
			model.failReviewFrom.Store(-1)
			second := measure(t, model, repo, args...)
			if second.err != nil {
				t.Fatalf("the second run did not finish: %v", second.err)
			}
			wantFindings(t, second, batches, "resume, second run")
			idx := -1
			for i := range resumes {
				if resumes[i].Batches == batches {
					idx = i
				}
			}
			if idx < 0 {
				resumes = append(resumes, resumeRow{Batches: batches, AnsweredBeforeLoss: answered})
				idx = len(resumes) - 1
			}
			row := &resumes[idx]
			if useNotary {
				row.SecondNotary, row.SecondNotaryMode = second.requests, second.mode()
				row.SecondNotaryDone, _ = second.out["complete"].(bool)
				row.FirstRunRequests = first.requests
			} else {
				row.SecondNoNotary = second.requests
			}
		}
	}

	// Print.
	var b strings.Builder
	fmt.Fprintf(&b, "\nNOTARY BENCHMARK (synthetic model; %d replicates per cell)\n\n", benchReps)
	fmt.Fprintf(&b, "%-8s %-10s %-22s %-12s %-14s %-14s %-9s\n", "BATCHES", "LATENCY", "ARM", "REQUESTS", "WALL ms (med)", "WALL ms (rng)", "DISTINCT")
	for _, c := range cells {
		fmt.Fprintf(&b, "%-8d %-10s %-22s %-12s %-14d %-14s %d/%d\n", c.Batches, fmt.Sprintf("%dms", c.LatencyMS), c.Arm,
			fmt.Sprintf("%d (%s)", median(c.Requests), span(c.Requests)), median(c.WallMS), span(c.WallMS), c.Distinct, c.N)
	}
	fmt.Fprintf(&b, "\nRECORD SIZE (one record per review, as the notary stores it)\n")
	for _, c := range cells {
		if len(c.Record) > 0 && c.LatencyMS == 0 {
			fmt.Fprintf(&b, "  %2d batches: %d bytes (%d across replicates)\n", c.Batches, median(c.Record), len(c.Record))
		}
	}
	fmt.Fprintf(&b, "\nDETERMINISM (4 batches, a model that answers differently each time)\n")
	fmt.Fprintf(&b, "  no notary: %d reviews, %d distinct finding sets\n", benchReps, distinct(plain))
	fmt.Fprintf(&b, "  notary:    %d reviews, %d distinct finding sets (1 live, %d replays)\n", benchReps, distinct(replayed), benchReps-1)
	fmt.Fprintf(&b, "\nRESUME (model loses every review answer after the first K, then the review is run again)\n")
	for _, r := range resumes {
		fmt.Fprintf(&b, "  %2d batches, %2d answered before the loss: second run asks the model %2d times without a notary, %2d with (mode %s, complete=%v)\n",
			r.Batches, r.AnsweredBeforeLoss, r.SecondNoNotary, r.SecondNotary, r.SecondNotaryMode, r.SecondNotaryDone)
	}
	t.Log(b.String())

	if path := os.Getenv("NITPICK_NOTARY_BENCH_OUT"); path != "" {
		raw, _ := json.MarshalIndent(map[string]any{
			"replicates": benchReps, "cells": cells,
			"determinism": map[string]any{"no_notary_distinct": distinct(plain), "notary_distinct": distinct(replayed), "reviews": benchReps},
			"resume":      resumes,
		}, "", "  ")
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
