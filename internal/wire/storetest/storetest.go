// Package storetest holds the behaviour every wire.Store and wire.Lister has to
// have, so the file store and the database store are held to one standard.
package storetest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nitpick-oss/nitpick/internal/wire"
)

// NewStore returns a new, empty store. Several calls within one test return stores
// over the same data, standing for several servers.
type NewStore func(t *testing.T) wire.Store

// Hash is the nth record hash.
func Hash(n int) string { return fmt.Sprintf("%064x", n) }

func rec(n int, mod func(*wire.Record)) wire.Record {
	r := wire.Record{
		Schema: wire.Schema, Hash: Hash(n), Tool: "v2.10.0", Provider: "openrouter", Model: "m1",
		Created: time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC).Add(time.Duration(n) * time.Minute),
		Answers: map[string]string{"r1#0": `{"content":"a"}`}, Batches: 1, Complete: true, Source: "ci",
	}
	if mod != nil {
		mod(&r)
	}
	return r
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// Options says which behaviours a store is expected to have.
type Options struct {
	// MultiServer is set for a store that more than one server may write at once.
	// The file store is not one: its answer to a second server was to refuse to
	// start it, so two of them on one directory lose updates.
	MultiServer bool
}

// Run runs every behaviour against stores from open.
func Run(t *testing.T, open NewStore, opt Options) {
	t.Helper()
	ctx := context.Background()
	t.Run("a missing record is none and not an error", func(t *testing.T) {
		got, ok, err := open(t).Get(ctx, Hash(1))
		if err != nil || ok || got.Hash != "" {
			t.Errorf("Get = %+v, %v, %v, want none", got, ok, err)
		}
	})

	t.Run("a record is read back as written", func(t *testing.T) {
		s := open(t)
		want := rec(1, func(r *wire.Record) {
			r.Effort = "low"
			r.Answers = map[string]string{"a#0": "x", "b#1": strings.Repeat("é", 1000), "c#2": "!unusable"}
			r.Batches = 3
		})
		must(t, s.Put(ctx, want))
		got, ok, err := s.Get(ctx, want.Hash)
		if err != nil || !ok {
			t.Fatalf("Get = %v, %v", ok, err)
		}
		if got.Hash != want.Hash || got.Effort != "low" || got.Batches != 3 || !got.Created.Equal(want.Created) || len(got.Answers) != 3 || got.Answers["b#1"] != want.Answers["b#1"] {
			t.Errorf("read %+v, wrote %+v", got, want)
		}
	})

	t.Run("a hash that is not a record hash is refused", func(t *testing.T) {
		s := open(t)
		for _, bad := range []string{"", "abc", strings.Repeat("g", 64), strings.Repeat("A", 64), "../" + strings.Repeat("a", 61)} {
			if _, _, err := s.Get(ctx, bad); err == nil {
				t.Errorf("Get(%q) was accepted", bad)
			}
			r := rec(1, nil)
			r.Hash = bad
			if err := s.Put(ctx, r); err == nil {
				t.Errorf("Put with hash %q was accepted", bad)
			}
		}
	})

	t.Run("a complete record is never replaced by an incomplete one", func(t *testing.T) {
		s := open(t)
		must(t, s.Put(ctx, rec(1, nil)))
		must(t, s.Put(ctx, rec(1, func(r *wire.Record) {
			r.Complete = false
			r.Answers = map[string]string{"z#0": "partial"}
			r.Batches = 2
		})))
		got, _, _ := s.Get(ctx, Hash(1))
		if !got.Complete || got.Answers["r1#0"] == "" || got.Answers["z#0"] != "" {
			t.Errorf("the complete record was replaced: %+v", got)
		}
	})

	t.Run("an incomplete record is replaced by a complete one and by a later incomplete one", func(t *testing.T) {
		s := open(t)
		must(t, s.Put(ctx, rec(1, func(r *wire.Record) { r.Complete = false; r.Batches = 2 })))
		must(t, s.Put(ctx, rec(1, func(r *wire.Record) {
			r.Complete = false
			r.Batches = 2
			r.Answers = map[string]string{"a#0": "1", "b#0": "2"}
		})))
		if got, _, _ := s.Get(ctx, Hash(1)); len(got.Answers) != 2 {
			t.Errorf("a later incomplete record did not replace the first: %+v", got)
		}
		must(t, s.Put(ctx, rec(1, nil)))
		if got, _, _ := s.Get(ctx, Hash(1)); !got.Complete {
			t.Errorf("a complete record did not replace an incomplete one: %+v", got)
		}
	})

	t.Run("a clear is counted, leaves a stub, and carries into the next record", func(t *testing.T) {
		s := open(t)
		must(t, s.Put(ctx, rec(1, nil)))
		must(t, s.Clear(ctx, Hash(1), "alice"))
		stub, ok, err := s.Get(ctx, Hash(1))
		if err != nil || !ok || stub.Complete || len(stub.Answers) != 0 || stub.Clears != 1 || len(stub.ClearedBy) != 1 || stub.ClearedBy[0] != "alice" {
			t.Fatalf("after a clear: %+v, %v, %v", stub, ok, err)
		}
		must(t, s.Put(ctx, rec(1, nil)))
		again, _, _ := s.Get(ctx, Hash(1))
		if !again.Complete || again.Clears != 1 || len(again.ClearedBy) != 1 {
			t.Errorf("the clear did not carry into the next record: %+v", again)
		}
		must(t, s.Clear(ctx, Hash(1), "bob"))
		if got, _, _ := s.Get(ctx, Hash(1)); got.Clears != 2 || strings.Join(got.ClearedBy, ",") != "alice,bob" {
			t.Errorf("second clear: %+v", got)
		}
	})

	t.Run("clearing what is not there is not an error", func(t *testing.T) {
		if err := open(t).Clear(ctx, Hash(9), "alice"); err != nil {
			t.Errorf("Clear on a missing record: %v", err)
		}
	})

	t.Run("two servers writing one hash at once leave one complete record", func(t *testing.T) {
		if !opt.MultiServer {
			t.Skip("the store is for one server")
		}
		a, b := open(t), open(t)
		var wg sync.WaitGroup
		errs := make(chan error, 40)
		for i := 0; i < 20; i++ {
			for _, s := range []wire.Store{a, b} {
				wg.Add(1)
				go func() {
					defer wg.Done()
					complete := i%5 == 0
					errs <- s.Put(ctx, rec(1, func(r *wire.Record) {
						r.Complete = complete
						r.Answers = map[string]string{fmt.Sprintf("w%d#0", i): "x"}
					}))
				}()
			}
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			must(t, err)
		}
		got, ok, err := a.Get(ctx, Hash(1))
		if err != nil || !ok || !got.Complete {
			t.Errorf("after 40 racing writes, with some complete: %+v, %v, %v", got, ok, err)
		}
	})

	t.Run("two servers clearing one hash at once count both", func(t *testing.T) {
		if !opt.MultiServer {
			t.Skip("the store is for one server")
		}
		a, b := open(t), open(t)
		must(t, a.Put(ctx, rec(1, nil)))
		var wg sync.WaitGroup
		for i := 0; i < 10; i++ {
			for _, s := range []wire.Store{a, b} {
				wg.Add(1)
				go func() { defer wg.Done(); must(t, s.Clear(ctx, Hash(1), fmt.Sprintf("u%d", i))) }()
			}
		}
		wg.Wait()
		if got, _, _ := a.Get(ctx, Hash(1)); got.Clears != 20 || len(got.ClearedBy) != 20 {
			t.Errorf("20 clears were counted as %d with %d names", got.Clears, len(got.ClearedBy))
		}
	})

	t.Run("a large record is stored whole", func(t *testing.T) {
		s := open(t)
		big := rec(1, func(r *wire.Record) { r.Answers = map[string]string{"a#0": strings.Repeat("x", 8<<20)} })
		must(t, s.Put(ctx, big))
		got, ok, err := s.Get(ctx, Hash(1))
		if err != nil || !ok || len(got.Answers["a#0"]) != 8<<20 {
			t.Errorf("an 8 MiB answer came back as %d bytes (%v, %v)", len(got.Answers["a#0"]), ok, err)
		}
	})

	lister := func(t *testing.T) (wire.Store, wire.Lister) {
		t.Helper()
		s := open(t)
		l, ok := s.(wire.Lister)
		if !ok {
			t.Skip("the store does not list")
		}
		return s, l
	}

	t.Run("a listing is newest written first and holds every record once across pages", func(t *testing.T) {
		s, l := lister(t)
		for i := 1; i <= 7; i++ {
			must(t, s.Put(ctx, rec(i, nil)))
			time.Sleep(15 * time.Millisecond)
		}
		var seen []string
		cursor := ""
		for page := 0; page < 10; page++ {
			got, next, err := l.List(ctx, wire.ListQuery{Limit: 3, Cursor: cursor})
			must(t, err)
			for _, sum := range got {
				seen = append(seen, sum.Hash)
			}
			if next == "" {
				break
			}
			cursor = next
		}
		if len(seen) != 7 {
			t.Fatalf("paged through %d of 7 records: %v", len(seen), seen)
		}
		for i, h := range seen {
			if h != Hash(7-i) {
				t.Errorf("position %d is %s, want %s (newest written first)", i, h[60:], Hash(7 - i)[60:])
			}
		}
	})

	t.Run("a listing says what a record holds without its answers", func(t *testing.T) {
		s, l := lister(t)
		must(t, s.Put(ctx, rec(1, func(r *wire.Record) {
			r.Batches = 4
			r.Complete = false
			r.Effort = "low"
			r.Answers = map[string]string{"a#0": "1", "b#0": "2"}
		})))
		must(t, s.Clear(ctx, Hash(2), "x")) // a clear of nothing leaves nothing to list
		got, _, err := l.List(ctx, wire.ListQuery{})
		must(t, err)
		if len(got) != 1 {
			t.Fatalf("listed %d, want the one record", len(got))
		}
		x := got[0]
		if x.Hash != Hash(1) || x.Complete || x.Batches != 4 || x.Answers != 2 || x.Effort != "low" || x.Source != "ci" || x.Model != "m1" || x.Tool != "v2.10.0" || x.Provider != "openrouter" || !x.Created.Equal(rec(1, nil).Created) {
			t.Errorf("summary = %+v", x)
		}
	})

	t.Run("a cleared record lists as an empty stub with no creation time", func(t *testing.T) {
		s, l := lister(t)
		must(t, s.Put(ctx, rec(1, nil)))
		must(t, s.Clear(ctx, Hash(1), "alice"))
		got, _, err := l.List(ctx, wire.ListQuery{})
		must(t, err)
		if len(got) != 1 {
			t.Fatalf("listed %d, want the stub", len(got))
		}
		if x := got[0]; x.Hash != Hash(1) || x.Complete || x.Answers != 0 || x.Clears != 1 || !x.Created.IsZero() {
			t.Errorf("stub summary = %+v, want an incomplete record with no answers, one clear and a zero creation time", x)
		}
	})

	t.Run("a listing filters on model, version and creation time", func(t *testing.T) {
		s, l := lister(t)
		must(t, s.Put(ctx, rec(1, func(r *wire.Record) { r.Model = "alpha"; r.Tool = "v1" })))
		must(t, s.Put(ctx, rec(2, func(r *wire.Record) { r.Model = "beta"; r.Tool = "v1" })))
		must(t, s.Put(ctx, rec(3, func(r *wire.Record) { r.Model = "beta"; r.Tool = "v2" })))
		count := func(q wire.ListQuery) int { got, _, err := l.List(ctx, q); must(t, err); return len(got) }
		if got := count(wire.ListQuery{Model: "beta"}); got != 2 {
			t.Errorf("model beta: %d, want 2", got)
		}
		if got := count(wire.ListQuery{Model: "beta", Tool: "v2"}); got != 1 {
			t.Errorf("model beta and version v2: %d, want 1", got)
		}
		if got := count(wire.ListQuery{Model: "bet"}); got != 0 {
			t.Errorf("a model prefix matched %d: the filter is exact", got)
		}
		t2 := rec(2, nil).Created
		if got := count(wire.ListQuery{Since: t2}); got != 2 {
			t.Errorf("since the second record: %d, want 2 (the bound is inclusive)", got)
		}
		if got := count(wire.ListQuery{Until: t2}); got != 1 {
			t.Errorf("until the second record: %d, want 1 (the bound is exclusive)", got)
		}
	})

	t.Run("a listing refuses a cursor it did not give", func(t *testing.T) {
		_, l := lister(t)
		for _, bad := range []string{"x", "12-abc", "-" + Hash(1), "1-" + strings.Repeat("Z", 64)} {
			if _, _, err := l.List(ctx, wire.ListQuery{Cursor: bad}); !errors.Is(err, wire.ErrBadQuery) {
				t.Errorf("cursor %q: err = %v, want ErrBadQuery", bad, err)
			}
		}
	})

	t.Run("a listing is capped at 200 and defaults to 50", func(t *testing.T) {
		s, l := lister(t)
		for i := 1; i <= 210; i++ {
			must(t, s.Put(ctx, rec(i, nil)))
		}
		if got, next, _ := l.List(ctx, wire.ListQuery{}); len(got) != 50 || next == "" {
			t.Errorf("default page = %d next %q, want 50 and a next", len(got), next)
		}
		if got, next, _ := l.List(ctx, wire.ListQuery{Limit: 1000}); len(got) != 200 || next == "" {
			t.Errorf("a limit of 1000 gave %d, want 200", len(got))
		}
	})

	t.Run("an empty store lists as no records", func(t *testing.T) {
		_, l := lister(t)
		got, next, err := l.List(ctx, wire.ListQuery{})
		if err != nil || len(got) != 0 || next != "" {
			t.Errorf("List = %v, %q, %v", got, next, err)
		}
	})
}
