package wire

import (
	"context"
	"strings"
	"testing"
)

var h64 = strings.Repeat("cd", 32)

func TestInputHashChangesWithEveryField(t *testing.T) {
	base := Inputs{Tool: "t", Provider: "p", Model: "m", System: "s", Settings: []string{"a=1"}, Batches: []string{"x", "y"}}
	want := base.Hash()
	for name, mut := range map[string]func(*Inputs){
		"tool":     func(i *Inputs) { i.Tool = "t2" },
		"provider": func(i *Inputs) { i.Provider = "p2" },
		"model":    func(i *Inputs) { i.Model = "m2" },
		"system":   func(i *Inputs) { i.System = "s2" },
		"settings": func(i *Inputs) { i.Settings = []string{"a=2"} },
		"batches":  func(i *Inputs) { i.Batches = []string{"x", "z"} },
		"order":    func(i *Inputs) { i.Batches = []string{"y", "x"} },
	} {
		in := base
		mut(&in)
		if in.Hash() == want {
			t.Errorf("%s change kept the hash", name)
		}
	}
}

func TestInputHashSeparatesDifferentSplitsOfTheSameBytes(t *testing.T) {
	a := Inputs{Batches: []string{"ab", "c"}}
	b := Inputs{Batches: []string{"a", "bc"}}
	if a.Hash() == b.Hash() {
		t.Fatal("length prefix missing")
	}
}

func TestFileStoreKeepsACompleteRecordWhenAnIncompleteOneArrives(t *testing.T) {
	s, _ := NewFileStore(t.TempDir())
	ctx := context.Background()
	_ = s.Put(ctx, Record{Hash: h64, Complete: true, Source: "ci"})
	_ = s.Put(ctx, Record{Hash: h64, Complete: false, Source: "late"})
	got, ok, _ := s.Get(ctx, h64)
	if !ok || !got.Complete || got.Source != "ci" {
		t.Fatalf("%+v", got)
	}
}

func TestFileStoreRefusesNonHashKeys(t *testing.T) {
	s, _ := NewFileStore(t.TempDir())
	if err := s.Put(context.Background(), Record{Hash: "../x"}); err == nil {
		t.Fatal("accepted a path as a hash")
	}
}
