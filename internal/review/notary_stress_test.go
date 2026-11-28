package review

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/nitpick-oss/nitpick/internal/config"
	"github.com/nitpick-oss/nitpick/internal/database/databasetest"
	"github.com/nitpick-oss/nitpick/internal/notary"
	"github.com/nitpick-oss/nitpick/internal/server"
	"github.com/nitpick-oss/nitpick/internal/vcs"
	"github.com/nitpick-oss/nitpick/internal/wire"
)

// manyFilesDiff is a change touching n files, one hunk each, so with
// max_files_per_request 1 it packs into n batches.
func manyFilesDiff(n int) string {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "diff --git a/f%02d.go b/f%02d.go\nindex 111..222 100644\n--- a/f%02d.go\n+++ b/f%02d.go\n@@ -1,3 +1,4 @@\n package app\n \n+func Get%02d() {}\n func Old() {}\n", i, i, i, i, i)
	}
	return b.String()
}

// stressNotary is a real notary server over HTTP, backed by a file store, with
// a client store pointed at it: the path a CI job takes.
func stressNotary(t *testing.T) (*wire.HTTPStore, *wire.FileStore) {
	t.Helper()
	fs, err := wire.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(notary.New(databasetest.One(t, append(notary.Models(), server.Models()...)), fs, []notary.Token{notary.NewToken("ci", notary.RoleAuthoritative, "s")}, false))
	t.Cleanup(srv.Close)
	client, err := wire.NewHTTPStore(srv.URL, "s")
	if err != nil {
		t.Fatal(err)
	}
	return client, fs
}

func stressEngine(t *testing.T, files int, store wire.Store) (*Engine, *scriptedLLM) {
	t.Helper()
	model := &scriptedLLM{byPrompt: map[string]string{
		notaryTriageMarker: mustJSON(t, TriageResult{Summary: "s"}),
		notaryReviewMarker: mustJSON(t, Result{}),
	}}
	e := newEngine(t, model, &stubProvider{diff: manyFilesDiff(files)}, func(c *config.Config) {
		c.Review.MaxFilesPerRequest = 1
		c.Review.Concurrency = 8
	})
	e.Notary, e.NotarySource = store, "ci"
	return e, model
}

const stressFiles = 40

func TestManyBatchesThroughARealNotaryRecordEveryAnswerExactlyOnce(t *testing.T) {
	client, fs := stressNotary(t)
	e, model := stressEngine(t, stressFiles, client)
	report, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	// One request per file. Triage is skipped when no batch found anything.
	want := model.callCount()
	if want != stressFiles {
		t.Fatalf("setup: %d model calls, want one per file (%d)", want, stressFiles)
	}
	n := report.Notary
	if n.Mode != NotaryLive || n.Live != want || n.Recorded < want || !n.Complete || n.Err != "" {
		t.Fatalf("live status = %+v, want %d live answers, all recorded, complete", n, want)
	}
	rec, ok, err := fs.Get(context.Background(), n.Hash)
	if err != nil || !ok || !rec.Complete || len(rec.Answers) != want {
		t.Fatalf("stored record: ok=%v err=%v complete=%v answers=%d, want %d complete", ok, err, rec.Complete, len(rec.Answers), want)
	}

	again, replayModel := stressEngine(t, stressFiles, client)
	replay, err := again.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if replayModel.callCount() != 0 || replay.Notary.Replayed != want || replay.Notary.Live != 0 || replay.Notary.Mode != NotaryReplayed {
		t.Errorf("replay made %d model calls: %+v", replayModel.callCount(), replay.Notary)
	}
}

func TestTwoIdenticalReviewsRunningAtOnceLeaveOneCompleteRecordThatReplays(t *testing.T) {
	client, fs := stressNotary(t)
	var wg sync.WaitGroup
	reports := make([]*Report, 2)
	errs := make([]error, 2)
	start := make(chan struct{})
	for i := range 2 {
		e, _ := stressEngine(t, stressFiles, client)
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			reports[i], errs[i] = e.Review(context.Background(), vcs.Ref{})
		}()
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("review %d: %v", i, err)
		}
	}
	want := stressFiles
	hash := reports[0].Notary.Hash
	if reports[1].Notary.Hash != hash {
		t.Fatalf("two reviews of one change filed under different hashes: %s and %s", hash, reports[1].Notary.Hash)
	}
	rec, ok, _ := fs.Get(context.Background(), hash)
	if !ok || !rec.Complete || len(rec.Answers) != want {
		t.Fatalf("after the race the record is ok=%v complete=%v answers=%d, want %d complete", ok, rec.Complete, len(rec.Answers), want)
	}
	again, replayModel := stressEngine(t, stressFiles, client)
	replay, err := again.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if replayModel.callCount() != 0 || replay.Notary.Mode != NotaryReplayed {
		t.Errorf("the record the race left did not replay: %d calls, %+v", replayModel.callCount(), replay.Notary)
	}
}
