//go:build eval

package knowledge

import (
	"context"
	"os"
	"testing"
)

// Does retrieval put the right entry first for a diff that has one?
//
// The question the whole feature turns on, asked against the committed index
// and the real embedding model. Judge-free: each query names the entry a
// person would want, and the test reports where it landed.
func TestLiveRetrievalRanksTheRightEntry(t *testing.T) {
	if os.Getenv("EMBED_PROVIDER") == "" {
		t.Skip("set EMBED_PROVIDER and EMBED_MODEL")
	}
	entries, err := Corpus()
	if err != nil {
		t.Fatalf("Corpus: %v", err)
	}
	raw, err := os.ReadFile("index.json")
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	ix, err := LoadIndex(raw, entries)
	if err != nil {
		t.Fatalf("LoadIndex: %v", err)
	}

	e := newLiveEmbedder(t)
	cases := []struct {
		want  string
		query string
	}{
		{"go-defer-in-loop", "for _, p := range paths {\n\tf, err := os.Open(p)\n\tdefer f.Close()\n}"},
		{"go-time-after-leak", "for {\n\tselect {\n\tcase m := <-ch:\n\t\thandle(m)\n\tcase <-time.After(time.Hour):\n\t\treturn\n\t}\n}"},
		{"sql-rows-err-unchecked", "rows, _ := db.Query(q)\nfor rows.Next() {\n\trows.Scan(&x)\n\tout = append(out, x)\n}\nreturn out, nil"},
		{"python-mutable-default", "def add_item(item, basket=[]):\n    basket.append(item)\n    return basket"},
		{"sh-set-e-pipeline", "set -e\ncurl -fsSL \"$url\" | tar -xz -C /usr/local"},
		{"js-array-sort-mutates", "const top = scores.sort().slice(0, 3);"},
	}

	hitsAt1, hitsAt3 := 0, 0
	for _, tc := range cases {
		vecs, err := e.Embed(context.Background(), []string{tc.query})
		if err != nil {
			t.Fatalf("embed %s: %v", tc.want, err)
		}
		got, err := ix.Nearest(vecs[0], entries, 3)
		if err != nil {
			t.Fatalf("Nearest: %v", err)
		}
		rank := -1
		for i, h := range got {
			if h.Entry.ID == tc.want {
				rank = i + 1
			}
		}
		switch {
		case rank == 1:
			hitsAt1++
			hitsAt3++
		case rank > 0:
			hitsAt3++
		}
		t.Logf("%-26s rank=%d  top=%s (%.3f)", tc.want, rank, got[0].Entry.ID, got[0].Score)
	}
	t.Logf("hit@1 = %d/%d, hit@3 = %d/%d", hitsAt1, len(cases), hitsAt3, len(cases))

	// A test that only logs is a test that cannot fail, and this one is the
	// evidence behind the claim that retrieval finds the right entry. The bar
	// is every query, because each names the one entry a person would want and
	// the corpus holds fourteen.
	if hitsAt3 != len(cases) {
		t.Errorf("the right entry was outside the top 3 for %d of %d queries",
			len(cases)-hitsAt3, len(cases))
	}
	if hitsAt1 < len(cases)-1 {
		t.Errorf("the right entry ranked first for only %d of %d queries",
			hitsAt1, len(cases))
	}
}
