//go:build eval

package evals

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/knowledge"
	"github.com/jdziat/open-nitpick/internal/llm"
)

// Does retrieval put the entry a plant needs in front of the model?
//
// The measurement's premise. A recall gain with retrieval that never fired
// would be run-to-run variance wearing a feature's name, and the log is not
// evidence: the harness discards it.
func TestLiveKnowledgeReachesThePlants(t *testing.T) {
	if os.Getenv("NITPICK_EVAL_EMBED_PROVIDER") == "" || os.Getenv("NITPICK_EVAL_EMBED_MODEL") == "" {
		t.Skip("set NITPICK_EVAL_EMBED_PROVIDER and NITPICK_EVAL_EMBED_MODEL")
	}
	entries, err := knowledge.Corpus()
	if err != nil {
		t.Fatalf("Corpus: %v", err)
	}
	model := os.Getenv("NITPICK_EVAL_EMBED_MODEL")
	ix, err := knowledge.LoadIndex(mustShippedIndex(t), entries)
	if err != nil {
		t.Fatalf("LoadIndex: %v", err)
	}
	embedder, err := llm.BuildEmbedder(context.Background(), config.ModelSpec{
		Provider: os.Getenv("NITPICK_EVAL_EMBED_PROVIDER"),
		Model:    model,
	})
	if err != nil {
		t.Fatalf("BuildEmbedder: %v", err)
	}
	r := &knowledge.Retriever{
		Entries: entries, Index: ix, Embedder: embedder,
		Model:      embedder.Model(),
		Candidates: 20, Keep: 5,
	}

	// The entry each plant needs, by fixture.
	want := map[string]string{
		"know-go-defer-in-loop":          "go-defer-in-loop",
		"know-go-time-after-leak":        "go-time-after-leak",
		"know-go-rows-err-unchecked":     "sql-rows-err-unchecked",
		"know-py-mutable-default":        "python-mutable-default",
		"know-sh-pipeline-masks-failure": "sh-set-e-pipeline",
		"know-go-nil-map-write":          "go-nil-map-write",
	}

	got := 0
	for _, f := range KnowledgeFixtures() {
		id, ok := want[f.Name]
		if !ok {
			continue
		}
		var q strings.Builder
		var paths []string
		for p, body := range f.Head {
			paths = append(paths, p)
			q.WriteString(body)
		}
		// Every class the review pass publishes: this test asks where an entry
		// ranks for a fixture, not which pass routes it. No versions either,
		// for the same reason: a fixture is not a checkout, and an entry cut
		// for the version it declares would answer a different question.
		hits, err := r.Retrieve(context.Background(), q.String(),
			knowledge.LanguagesOf(paths), liveDefectClasses(), nil)
		if err != nil {
			t.Errorf("%s: Retrieve: %v", f.Name, err)
			continue
		}
		rank := -1
		for i, h := range hits {
			if h.Entry.ID == id {
				rank = i + 1
			}
		}
		if rank > 0 {
			got++
		}
		t.Logf("%-32s wants %-24s rank=%d of %d", f.Name, id, rank, len(hits))
	}
	if got != len(want) {
		t.Errorf("the needed entry reached %d of %d plants; retrieval is not what the on arm measured",
			got, len(want))
	}
}

// mustShippedIndex selects vectors from the configured query model's space.
func mustShippedIndex(t *testing.T) []byte {
	t.Helper()
	model := os.Getenv("NITPICK_EVAL_EMBED_PROVIDER") + "/" + os.Getenv("NITPICK_EVAL_EMBED_MODEL")
	raw, err := knowledge.SelectIndex(model)
	if err != nil {
		t.Fatalf("SelectIndex(%q): %v", model, err)
	}
	return raw
}

func TestShippedKnowledgeIndexesMatchTheConfiguredEmbeddingModel(t *testing.T) {
	entries, err := knowledge.Corpus()
	if err != nil {
		t.Fatal(err)
	}
	models, err := knowledge.ShippedModels()
	if err != nil || len(models) == 0 {
		t.Fatalf("ShippedModels: %v", err)
	}
	for _, model := range models {
		t.Run(model, func(t *testing.T) {
			provider, name, ok := strings.Cut(model, "/")
			if !ok {
				t.Fatalf("index model has no provider: %q", model)
			}
			t.Setenv("NITPICK_EVAL_EMBED_PROVIDER", provider)
			t.Setenv("NITPICK_EVAL_EMBED_MODEL", name)
			index, err := knowledge.LoadIndex(mustShippedIndex(t), entries)
			if err != nil {
				t.Fatal(err)
			}
			if err := index.CheckModel(model); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// liveDefectClasses is every class in the taxonomy, for a retrieval test that
// is not about routing.
func liveDefectClasses() map[config.Class]bool {
	out := map[config.Class]bool{}
	for _, c := range config.Classes() {
		out[c] = true
	}
	return out
}
