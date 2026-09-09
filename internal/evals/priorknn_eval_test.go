//go:build eval

package evals

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/llm"
)

// Can past labelled findings tell a new finding's noise from its signal?
//
// The mechanism history-based correlation would use, tested without building
// it: index one corpus's findings with their labels, classify another corpus's
// findings by nearest neighbour, and report accuracy against always-guessing.
//
// Trained and tested on different corpora, collected four days apart, so a
// score here is not the corpus recognising itself.
func TestPastFindingsClassifyNewOnes(t *testing.T) {
	if os.Getenv("NITPICK_EVAL_EMBED_PROVIDER") == "" {
		t.Skip("set NITPICK_EVAL_EMBED_PROVIDER and NITPICK_EVAL_EMBED_MODEL")
	}
	train := loadLabelled(t, "testdata/findings-dumps/*.jsonl")
	test := loadLabelled(t, ".eval-runs/*.jsonl")
	if len(train) < 50 || len(test) < 50 {
		t.Skipf("train=%d test=%d", len(train), len(test))
	}
	t.Logf("train n=%d (callers corpus)  test n=%d (knowledge corpus)", len(train), len(test))

	e, err := llm.BuildEmbedder(context.Background(), config.ModelSpec{
		Provider: os.Getenv("NITPICK_EVAL_EMBED_PROVIDER"),
		Model:    os.Getenv("NITPICK_EVAL_EMBED_MODEL"),
	})
	if err != nil {
		t.Fatal(err)
	}
	trainV := embedAll(t, e, titles(train))
	testV := embedAll(t, e, titles(test))

	for _, k := range []int{1, 3, 5} {
		correct := 0
		for i, r := range test {
			votes := 0
			for _, n := range nearestK(testV[i], trainV, k) {
				if train[n].Matched {
					votes++
				}
			}
			if (votes*2 > k) == r.Matched {
				correct++
			}
		}
		acc := float64(correct) / float64(len(test))
		t.Logf("k=%d: %.3f accuracy", k, acc)
	}

	real := 0
	for _, r := range test {
		if r.Matched {
			real++
		}
	}
	base := math.Max(float64(real), float64(len(test)-real)) / float64(len(test))
	t.Logf("baseline (always the majority class): %.3f", base)
}

func loadLabelled(t *testing.T, glob string) []dumpRow {
	t.Helper()
	files, _ := filepath.Glob(glob)
	var out []dumpRow
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
			out = append(out, r)
		}
	}
	return out
}

func titles(rows []dumpRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Title
	}
	return out
}

func embedAll(t *testing.T, e *llm.Embedder, texts []string) [][]float32 {
	t.Helper()
	var out [][]float32
	for i := 0; i < len(texts); i += 32 {
		j := min(i+32, len(texts))
		v, err := e.Embed(context.Background(), texts[i:j])
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, v...)
	}
	return out
}

func nearestK(q []float32, pool [][]float32, k int) []int {
	type scored struct {
		i int
		s float64
	}
	all := make([]scored, len(pool))
	for i, v := range pool {
		all[i] = scored{i, cos(q, v)}
	}
	for i := 0; i < k && i < len(all); i++ {
		best := i
		for j := i + 1; j < len(all); j++ {
			if all[j].s > all[best].s {
				best = j
			}
		}
		all[i], all[best] = all[best], all[i]
	}
	out := make([]int, 0, k)
	for i := 0; i < k && i < len(all); i++ {
		out = append(out, all[i].i)
	}
	return out
}

func cos(a, b []float32) float64 {
	var d, na, nb float64
	for i := range a {
		x, y := float64(a[i]), float64(b[i])
		d += x * y
		na += x * x
		nb += y * y
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return d / (math.Sqrt(na) * math.Sqrt(nb))
}
