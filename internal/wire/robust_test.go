package wire

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var robustHash = strings.Repeat("c", 64)

func robustRecord(answers int, complete bool) Record {
	a := map[string]string{}
	for i := range answers {
		a[fmt.Sprintf("k%d", i)] = strings.Repeat("x", 1000)
	}
	return Record{Schema: Schema, Hash: robustHash, Answers: a, Batches: answers, Complete: complete, Source: "ci"}
}

func TestACorruptRecordFileDoesNotBlockEveryLaterWriteForThatHash(t *testing.T) {
	dir := t.TempDir()
	s, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, robustHash+".json")
	garbage := []byte(`{"schema":4,"hash":"` + robustHash + `","answers":{"k":`) // truncated mid-record
	if err := os.WriteFile(path, garbage, 0o640); err != nil {
		t.Fatal(err)
	}

	// Reading it is an error, visibly: a corrupt record must not read as absent.
	if _, ok, err := s.Get(context.Background(), robustHash); err == nil || ok {
		t.Fatalf("a corrupt record read as ok=%v err=%v, want an error", ok, err)
	}
	// But it must not wedge the hash: the next write replaces it.
	want := robustRecord(3, true)
	if err := s.Put(context.Background(), want); err != nil {
		t.Fatalf("a write onto a corrupt record failed for good: %v", err)
	}
	got, ok, err := s.Get(context.Background(), robustHash)
	if err != nil || !ok || !got.Complete || len(got.Answers) != 3 {
		t.Fatalf("after the write: ok=%v err=%v %+v", ok, err, got.Complete)
	}
	// And the bytes that were there are kept, not silently destroyed.
	kept := false
	_ = filepath.Walk(filepath.Join(dir, "history"), func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			if b, _ := os.ReadFile(p); bytes.Equal(b, garbage) {
				kept = true
			}
		}
		return nil
	})
	if !kept {
		t.Error("the corrupt file was overwritten with no copy kept")
	}
}

func TestClearingACorruptRecordDoesNotFailForEver(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewFileStore(dir)
	if err := os.WriteFile(filepath.Join(dir, robustHash+".json"), []byte("{"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := s.Clear(context.Background(), robustHash, "alice"); err != nil {
		t.Fatalf("-fresh could never clear a corrupt record: %v", err)
	}
	if _, _, err := s.Get(context.Background(), robustHash); err != nil {
		t.Fatalf("after the clear Get still fails: %v", err)
	}
}

func TestTwoStoresOnOneDirectoryNeverTearARecord(t *testing.T) {
	dir := t.TempDir()
	// Two instances stand for two processes: separate locks, one directory.
	a, _ := NewFileStore(dir)
	b, _ := NewFileStore(dir)
	ctx := context.Background()
	var wg sync.WaitGroup
	torn := make(chan string, 8)
	for _, st := range []*FileStore{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 150 {
				if err := st.Put(ctx, robustRecord(1+i%40, false)); err != nil {
					torn <- "put: " + err.Error()
					return
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 400 {
			if _, _, err := a.Get(ctx, robustHash); err != nil {
				torn <- "get: " + err.Error()
				return
			}
		}
	}()
	wg.Wait()
	close(torn)
	for msg := range torn {
		t.Errorf("a record was torn or a write failed while two stores shared a directory: %s", msg)
	}
	if _, _, err := a.Get(ctx, robustHash); err != nil {
		t.Errorf("the final record is unreadable: %v", err)
	}
	left, _ := filepath.Glob(filepath.Join(dir, "*.tmp*"))
	if len(left) > 0 {
		t.Errorf("temporary files left behind: %v", left)
	}
}

func TestAStrayTemporaryFileIsNeverServedAsARecord(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewFileStore(dir)
	if err := os.WriteFile(filepath.Join(dir, robustHash+".json.tmp"), []byte(`{"schema":4,"complete":true}`), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.Get(context.Background(), robustHash); err != nil || ok {
		t.Fatalf("a leftover temporary file was served: ok=%v err=%v", ok, err)
	}
}

func TestRecordsAreNotReadableByEveryoneOnTheMachine(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewFileStore(dir)
	if err := s.Put(context.Background(), robustRecord(2, true)); err != nil {
		t.Fatal(err)
	}
	if err := s.Clear(context.Background(), robustHash, "alice"); err != nil {
		t.Fatal(err)
	}
	var files []string
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && p != dir {
			files = append(files, p)
			// A record holds excerpts of the code that was reviewed.
			if info.Mode().Perm()&0o007 != 0 {
				t.Errorf("%s is %v: other users can read or write it", p, info.Mode().Perm())
			}
		}
		return nil
	})
	// The history directory, the stub the clear left, and the cleared record.
	if len(files) < 3 {
		t.Fatalf("setup: only %d paths, the record and its history were not both written", len(files))
	}
}

func TestClearingARecordThatDoesNotExistCreatesNothing(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewFileStore(dir)
	if err := s.Clear(context.Background(), robustHash, "alice"); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	hist, _ := os.ReadDir(filepath.Join(dir, "history"))
	if len(entries) != 1 || len(hist) != 0 {
		t.Errorf("a clear of nothing left %d entries and %d history files, want only the history directory", len(entries), len(hist))
	}
}

func TestStartingAStoreClearsTheTemporariesACrashLeftButNotAnotherProcessesWrite(t *testing.T) {
	dir := t.TempDir()
	if _, err := NewFileStore(dir); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, robustHash+".json.123456.tmp")
	fresh := filepath.Join(dir, robustHash+".json.654321.tmp")
	other := filepath.Join(dir, "notes.tmp") // not a record's temporary: never touched
	for _, p := range []string{stale, fresh, other} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-time.Hour)
	for _, p := range []string{stale, other} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := NewFileStore(dir); err != nil {
		t.Fatal(err)
	}
	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }
	if exists(stale) {
		t.Error("a temporary from a crashed write an hour ago was kept for ever")
	}
	if !exists(fresh) {
		t.Error("a temporary a minute old was removed: it may be another process's write in progress")
	}
	if !exists(other) {
		t.Error("a file that is not a record's temporary was removed")
	}
}

func TestOnlyOneProcessMayServeADirectoryAtATime(t *testing.T) {
	dir := t.TempDir()
	first, _ := NewFileStore(dir)
	second, _ := NewFileStore(dir)
	if err := first.Lock(); err != nil {
		t.Fatalf("the first claim failed: %v", err)
	}
	err := second.Lock()
	if !errors.Is(err, ErrDirectoryInUse) {
		t.Fatalf("a second claim on a held directory returned %v, want ErrDirectoryInUse", err)
	}
	if err := first.Unlock(); err != nil {
		t.Fatal(err)
	}
	if err := second.Lock(); err != nil {
		t.Errorf("the directory was not free after the first server let go: %v", err)
	}
	_ = second.Unlock()
}
