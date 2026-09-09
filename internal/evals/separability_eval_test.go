//go:build eval

package evals

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/knowledge"
	"github.com/jdziat/open-nitpick/internal/llm"
)

type dumpRow struct {
	Fixture string `json:"fixture"`
	Title   string `json:"title"`
	Class   string `json:"class"`
	Matched bool   `json:"matched"`
	Silent  bool   `json:"silent"`
}

// Does similarity to the knowledge corpus separate a real finding from noise?
//
// The premise of using the shipped corpus as a noise filter. Measured over the
// findings the knowledge-corpus runs already labelled, so it costs one embed
// call per finding and no reviews.
func TestKnowledgeSimilaritySeparatesNoise(t *testing.T) {
	if os.Getenv("NITPICK_EVAL_EMBED_PROVIDER") == "" {
		t.Skip("set NITPICK_EVAL_EMBED_PROVIDER and NITPICK_EVAL_EMBED_MODEL")
	}
	glob := os.Getenv("SEP_DUMPS")
	if glob == "" {
		glob = ".eval-runs/*.jsonl"
	}
	files, _ := filepath.Glob(glob)
	var rows []dumpRow
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var r dumpRow
			if json.Unmarshal([]byte(line), &r) != nil || r.Silent || r.Title == "" {
				continue
			}
			rows = append(rows, r)
		}
	}
	t.Logf("dumps: %s (%d files)", glob, len(files))
	if len(rows) < 50 {
		t.Skipf("only %d labelled findings", len(rows))
	}

	entries, err := knowledge.Corpus()
	if err != nil {
		t.Fatal(err)
	}
	ix, err := knowledge.LoadIndex(mustShippedIndex(t), entries)
	if err != nil {
		t.Fatal(err)
	}
	e, err := llm.BuildEmbedder(context.Background(), config.ModelSpec{
		Provider: os.Getenv("NITPICK_EVAL_EMBED_PROVIDER"),
		Model:    os.Getenv("NITPICK_EVAL_EMBED_MODEL"),
	})
	if err != nil {
		t.Fatal(err)
	}

	texts := make([]string, len(rows))
	for i, r := range rows {
		texts[i] = r.Title
	}
	// In batches: one request per finding would be 130 round trips.
	var vecs [][]float32
	for i := 0; i < len(texts); i += 32 {
		j := min(i+32, len(texts))
		v, err := e.Embed(context.Background(), texts[i:j])
		if err != nil {
			t.Fatal(err)
		}
		vecs = append(vecs, v...)
	}

	var real, noise []float64
	for i, r := range rows {
		hits, err := ix.Nearest(vecs[i], entries, 1)
		if err != nil {
			// Fatal, not skipped. A dimension mismatch drops every row, and
			// the run then reports n=0, a NaN mean and a threshold computed
			// over nothing, which reads as a measurement. Two embedders ship
			// here at 768 and 1536 dimensions, so the mismatch is one command
			// away.
			t.Fatalf("Nearest: %v", err)
		}
		if len(hits) == 0 {
			continue
		}
		if r.Matched {
			real = append(real, hits[0].Score)
		} else {
			noise = append(noise, hits[0].Score)
		}
	}
	t.Logf("real n=%d  mean=%.3f  median=%.3f", len(real), sepMean(real), sepMedian(real))
	t.Logf("noise n=%d  mean=%.3f  median=%.3f", len(noise), sepMean(noise), sepMedian(noise))

	// The best a single threshold can do, which is the ceiling on any rule of
	// the form "drop findings below x".
	best, at := 0.0, 0.0
	for _, cut := range sepThresholds(append(append([]float64{}, real...), noise...)) {
		tp := sepCount(real, func(v float64) bool { return v >= cut })
		tn := sepCount(noise, func(v float64) bool { return v < cut })
		acc := float64(tp+tn) / float64(len(real)+len(noise))
		if acc > best {
			best, at = acc, cut
		}
	}
	t.Logf("best single threshold: %.3f accuracy at cut %.3f (baseline %.3f = always-real)",
		best, at, float64(len(real))/float64(len(real)+len(noise)))
}

func sepMean(v []float64) float64 {
	s := 0.0
	for _, x := range v {
		s += x
	}
	return s / float64(len(v))
}

func sepMedian(v []float64) float64 {
	c := append([]float64{}, v...)
	sort.Float64s(c)
	return c[len(c)/2]
}

func sepCount(v []float64, f func(float64) bool) int {
	n := 0
	for _, x := range v {
		if f(x) {
			n++
		}
	}
	return n
}

func sepThresholds(v []float64) []float64 {
	c := append([]float64{}, v...)
	sort.Float64s(c)
	out := []float64{}
	for i := range c {
		if i == 0 || math.Abs(c[i]-c[i-1]) > 1e-9 {
			out = append(out, c[i])
		}
	}
	return out
}
