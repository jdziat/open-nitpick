package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/flock"

	"github.com/nitpick-oss/nitpick/internal/database/databasetest"
	"github.com/nitpick-oss/nitpick/internal/insights"
	"github.com/nitpick-oss/nitpick/internal/notary"
	"github.com/nitpick-oss/nitpick/internal/server"
	"github.com/nitpick-oss/nitpick/internal/wire"
)

func hashOf(c byte) string { return strings.Repeat(string(c), 64) }

// legacyDir is a data directory as the file-backed server left it: four notary
// records of which one was cleared, and so has history, two things in it that are
// not records, and five lines of reviews of which one repeats, one is damaged and
// one names a severity that does not exist.
func legacyDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	fs, err := wire.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, r := range []wire.Record{
		{Schema: 4, Hash: hashOf('a'), Model: "m", Created: time.Now(), Answers: map[string]string{"k": "{}"}, Batches: 1, Complete: true},
		{Schema: 4, Hash: hashOf('b'), Model: "m", Created: time.Now(), Answers: map[string]string{"k": "{}"}, Batches: 1, Complete: true},
		{Schema: 4, Hash: hashOf('c'), Model: "m", Created: time.Now(), Answers: map[string]string{"k": "{}"}, Batches: 2},
		{Schema: 4, Hash: hashOf('d'), Model: "m", Created: time.Now(), Answers: map[string]string{"k": "{}"}, Batches: 1, Complete: true},
	} {
		if err := fs.Put(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	if err := fs.Clear(ctx, hashOf('d'), "alice"); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("notes.json", `{}`)
	write(hashOf('e')+".json", "not json at all")

	at := time.Now().Add(-time.Hour)
	good := func(repo string) string {
		b, _ := json.Marshal(insights.Review{ID: "old-" + repo, Repo: repo, At: at, Result: "clean", Complete: true,
			Findings: []insights.Finding{{Path: "a.go", Line: 3, Severity: "error", Title: "x"}}})
		return string(b)
	}
	bad, _ := json.Marshal(insights.Review{ID: "old-bad", Repo: "acme/api", At: at, Result: "clean",
		Findings: []insights.Finding{{Path: "a.go", Severity: "catastrophic", Title: "x"}}})
	if err := os.MkdirAll(filepath.Join(dir, "insights"), 0o750); err != nil {
		t.Fatal(err)
	}
	reviews := strings.Join([]string{good("acme/api"), good("acme/web"), good("acme/api"), "{not a review", string(bad)}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "insights", "reviews.jsonl"), []byte(reviews), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func importer(t *testing.T) func() importResult {
	t.Helper()
	d := databasetest.One(t, append(append(notary.Models(), insights.Models()...), server.Models()...))
	dir := legacyDir(t)
	return func() importResult {
		t.Helper()
		res, err := importDir(context.Background(), d, dir)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
}

// What moved is counted, and every file is accounted for: copied, already there, or
// skipped with its name.
func TestImportingADataDirectoryCountsWhatMovedWhatWasThereAndWhatWasSkipped(t *testing.T) {
	run := importer(t)
	first := run()
	if n := first.Notary; n.Records != 4 || n.History != 1 || n.Present != 0 || n.Skipped != 2 {
		t.Errorf("notary on the first run = %+v, want 4 records, 1 history, 0 present, 2 skipped", n)
	}
	if n := first.Insights; n.Moved != 2 || n.Present != 1 || n.Skipped != 2 {
		t.Errorf("insights on the first run = %+v, want 2 moved, 1 present (the repeat), 2 skipped", n)
	}
	second := run()
	if n := second.Notary; n.Records != 0 || n.History != 0 || n.Present != 5 || n.Skipped != 2 {
		t.Errorf("notary on the second run = %+v, want everything present and nothing copied", n)
	}
	if n := second.Insights; n.Moved != 0 || n.Present != 3 || n.Skipped != 2 {
		t.Errorf("insights on the second run = %+v, want nothing copied", n)
	}
}

// The records that arrive are the ones that were there: a complete one stays
// complete, a cleared one keeps its count and its names, and a review is readable.
func TestImportedRecordsAndReviewsAreTheOnesThatWereInTheDirectory(t *testing.T) {
	db := databasetest.One(t, append(append(notary.Models(), insights.Models()...), server.Models()...))
	dir := legacyDir(t)
	if _, err := importDir(context.Background(), db, dir); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	store := notary.NewDBStore(db)
	if r, ok, err := store.Get(ctx, hashOf('a')); err != nil || !ok || !r.Complete || r.Model != "m" {
		t.Errorf("complete record = %+v %v %v", r, ok, err)
	}
	if r, ok, _ := store.Get(ctx, hashOf('c')); !ok || r.Complete || r.Batches != 2 {
		t.Errorf("incomplete record = %+v %v", r, ok)
	}
	if r, ok, _ := store.Get(ctx, hashOf('d')); !ok || r.Clears != 1 || len(r.ClearedBy) != 1 || r.ClearedBy[0] != "alice" {
		t.Errorf("cleared record = %+v %v, want its count and who cleared it", r, ok)
	}
	v, err := insights.NewStore(db).View(ctx, insights.Query{Now: time.Now().UTC(), Days: 7})
	if err != nil {
		t.Fatal(err)
	}
	if n := v.Len(); n != 2 {
		t.Errorf("the dashboard shows %d reviews, want the 2 distinct ones", n)
	}
}

// A record the server took after the move began is newer than the file. Running the
// import must not put the file's copy over it.
func TestImportNeverOverwritesARecordTheDatabaseAlreadyHas(t *testing.T) {
	db := databasetest.One(t, append(append(notary.Models(), insights.Models()...), server.Models()...))
	ctx := context.Background()
	store := notary.NewDBStore(db)
	if err := store.Put(ctx, wire.Record{Schema: 4, Hash: hashOf('a'), Model: "newer", Created: time.Now(), Answers: map[string]string{"k": "{}"}, Batches: 1, Complete: true}); err != nil {
		t.Fatal(err)
	}
	res, err := importDir(ctx, db, legacyDir(t))
	if err != nil {
		t.Fatal(err)
	}
	if res.Notary.Records != 3 || res.Notary.Present != 1 {
		t.Errorf("counts = %+v, want 3 copied and the existing one left", res.Notary)
	}
	if r, _, _ := store.Get(ctx, hashOf('a')); r.Model != "newer" {
		t.Errorf("the record was overwritten by the file: model %q", r.Model)
	}
}

// A server still writing to the directory would add to files already read, and the
// copy would be missing what it wrote.
func TestImportRefusesADirectoryAServerStillHolds(t *testing.T) {
	db := databasetest.One(t, append(append(notary.Models(), insights.Models()...), server.Models()...))
	dir := legacyDir(t)
	l := flock.New(filepath.Join(dir, ".lock"))
	if ok, err := l.TryLock(); err != nil || !ok {
		t.Fatalf("lock: %v %v", ok, err)
	}
	defer func() { _ = l.Unlock() }()
	if _, err := importDir(context.Background(), db, dir); err == nil || !strings.Contains(err.Error(), "in use") {
		t.Fatalf("err = %v, want the refusal to name the directory as in use", err)
	}
	var n int64
	if err := db.Table("notary_records").Count(&n).Error; err != nil || n != 0 {
		t.Errorf("a refused import still copied %d records (%v)", n, err)
	}
}

func TestImportNeedsADataDirectoryAndADatabase(t *testing.T) {
	var out, errb strings.Builder
	if code := runImport(context.Background(), nil, &out, &errb); code != 2 || !strings.Contains(errb.String(), "-data-dir") {
		t.Errorf("no directory: code %d %q", code, errb.String())
	}
	errb.Reset()
	if code := runImport(context.Background(), []string{"-data-dir", t.TempDir(), "-database-url", ""}, &out, &errb); code != 1 || !strings.Contains(errb.String(), "database URL") {
		t.Errorf("no database: code %d %q", code, errb.String())
	}
}
