package wire

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

// A settings entry equal to a section marker used to be indistinguishable from
// the marker, so these two inputs hashed alike and one review's stored answers
// would have replayed for the other.
func TestInputsWithADelimiterShapedSettingHashApart(t *testing.T) {
	a := Inputs{System: "s", Settings: []string{"batches"}, Batches: []string{"x"}}
	b := Inputs{System: "s", Batches: []string{"batches", "x"}}
	if a.Hash() == b.Hash() {
		t.Fatal("different inputs share a hash")
	}
}

func TestAClearedRecordIsReplacedThroughATemporaryFileNotInPlace(t *testing.T) {
	dir := t.TempDir()
	s, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.Put(ctx, Record{Hash: h64, Complete: true, Source: "ci"}); err != nil {
		t.Fatal(err)
	}
	// A directory squatting on the temporary name makes the temp write fail. An
	// in-place write would ignore it and truncate the live record.
	if err := os.Mkdir(filepath.Join(dir, h64+".json.tmp"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := s.Clear(ctx, h64, "dev"); err == nil {
		t.Fatal("clear succeeded although the stub could not be written")
	}
	got, ok, err := s.Get(ctx, h64)
	if err != nil || !ok || !got.Complete {
		t.Fatalf("live record after a failed clear: ok=%v err=%v rec=%+v, want it intact", ok, err, got)
	}
}

func TestAClearedByNameWithQueryCharactersIsRecordedWhole(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query().Get("by")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	s, err := NewHTTPStore(srv.URL, "t")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Clear(context.Background(), h64, "dev & co=1"); err != nil {
		t.Fatal(err)
	}
	if got != "dev & co=1" {
		t.Fatalf("server read by=%q", got)
	}
}

func TestACancelledRequestIsNotReportedAsAnUnreachableNotary(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	s, err := NewHTTPStore(srv.URL, "t")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = s.Get(ctx, h64)
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrUnreachable) {
		t.Fatalf("err = %v, want context.Canceled and not ErrUnreachable", err)
	}
}
