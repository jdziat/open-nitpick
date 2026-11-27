package insights

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

func review(repo string, at time.Time, fs ...Finding) Review {
	return Review{Repo: repo, Commit: "abc1234", At: at, Result: "findings", Complete: true, Findings: fs}
}

func finding(path, sev string) Finding {
	return Finding{Path: path, Line: 10, Severity: sev, Class: "bug", Title: "t " + path}
}

func TestStoreKeepsWhatItWasGivenAcrossARestart(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(context.Background(), review("acme/api", t0, finding("a.go", "error"))); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = again.Close() }()
	got := again.Reviews(Filter{})
	if len(got) != 1 || got[0].Repo != "acme/api" || len(got[0].Findings) != 1 {
		t.Fatalf("after a restart the store holds %+v", got)
	}
}

func TestStoreSkipsATornFinalLineAndKeepsEarlierReviews(t *testing.T) {
	dir := t.TempDir()
	s, _ := OpenStore(dir)
	_, _ = s.Add(context.Background(), review("acme/api", t0))
	_ = s.Close()
	f, err := os.OpenFile(filepath.Join(dir, "reviews.jsonl"), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"repo":"acme/api","at":"2026-1`)
	_ = f.Close()
	again, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("a torn last line must not stop the server starting: %v", err)
	}
	defer func() { _ = again.Close() }()
	if n := len(again.Reviews(Filter{})); n != 1 {
		t.Fatalf("kept %d reviews, want the 1 that was whole", n)
	}
	if again.Skipped() != 1 {
		t.Fatalf("skipped = %d, a dropped line must be counted so it is not silent", again.Skipped())
	}
}

func TestStoreIsIdempotentForTheSameReview(t *testing.T) {
	dir := t.TempDir()
	s, _ := OpenStore(dir)
	defer func() { _ = s.Close() }()
	r := review("acme/api", t0, finding("a.go", "error"))
	a, _ := s.Add(context.Background(), r)
	b, _ := s.Add(context.Background(), r)
	if a.ID != b.ID || len(s.Reviews(Filter{})) != 1 {
		t.Fatalf("resending one review made %d records", len(s.Reviews(Filter{})))
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "reviews.jsonl")); strings.Count(string(b), "\n") != 1 {
		t.Fatalf("a retried review must not grow the file: %q", b)
	}
}

func TestStoreRefusesWhatCannotBeARepoOrAPath(t *testing.T) {
	s, _ := OpenStore(t.TempDir())
	defer func() { _ = s.Close() }()
	for name, r := range map[string]Review{
		"no repo":        review("", t0),
		"repo with ctrl": review("a\nb", t0),
		"huge repo":      review(strings.Repeat("x", 300), t0),
		"future":         review("acme/api", time.Now().Add(48*time.Hour)),
		"bad severity":   review("acme/api", t0, finding("a.go", "catastrophic")),
		"path escapes":   review("acme/api", t0, finding("../../etc/passwd", "error")),
	} {
		if _, err := s.Add(context.Background(), r); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if n := len(s.Reviews(Filter{})); n != 0 {
		t.Fatalf("a refused review was kept: %d", n)
	}
}

func TestStoreScrubsSecretsFromFindingText(t *testing.T) {
	s, _ := OpenStore(t.TempDir())
	defer func() { _ = s.Close() }()
	f := finding("a.go", "error")
	f.Rationale = "the key ghp_abcdefghijklmnopqrstuvwxyz0123456789 is committed"
	got, err := s.Add(context.Background(), review("acme/api", t0, f))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got.Findings[0].Rationale, "ghp_") {
		t.Fatalf("a token reached the store: %q", got.Findings[0].Rationale)
	}
}

func TestStoreCapsFindingsPerReviewAndSaysSo(t *testing.T) {
	s, _ := OpenStore(t.TempDir())
	defer func() { _ = s.Close() }()
	var fs []Finding
	for i := 0; i < MaxFindings+5; i++ {
		fs = append(fs, finding("a.go", "nit"))
	}
	got, err := s.Add(context.Background(), review("acme/api", t0, fs...))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Findings) != MaxFindings || got.Truncated != 5 {
		t.Fatalf("kept %d, truncated %d", len(got.Findings), got.Truncated)
	}
}

func TestStoreCountsADuplicatedLineInTheFileOnce(t *testing.T) {
	dir := t.TempDir()
	s, _ := OpenStore(dir)
	_, _ = s.Add(context.Background(), review("acme/api", t0, finding("a.go", "error")))
	_ = s.Close()
	p := filepath.Join(dir, "reviews.jsonl")
	b, _ := os.ReadFile(p)
	if err := os.WriteFile(p, append(b, b...), 0o600); err != nil {
		t.Fatal(err)
	}
	again, _ := OpenStore(dir)
	defer func() { _ = again.Close() }()
	if n := len(again.Reviews(Filter{})); n != 1 {
		t.Fatalf("a duplicated line made %d reviews", n)
	}
}

func TestARepairedTornTailLetsTheNextReviewSurviveARestart(t *testing.T) {
	dir := t.TempDir()
	s, _ := OpenStore(dir)
	_, _ = s.Add(context.Background(), review("acme/api", t0, finding("a.go", "error")))
	_ = s.Close()
	f, _ := os.OpenFile(filepath.Join(dir, "reviews.jsonl"), os.O_APPEND|os.O_WRONLY, 0o600)
	_, _ = f.WriteString(`{"repo":"torn`) // a crash mid-write: no newline
	_ = f.Close()
	s, _ = OpenStore(dir)
	if _, err := s.Add(context.Background(), review("acme/web", t0.Add(time.Hour), finding("b.go", "nit"))); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	again, _ := OpenStore(dir)
	defer func() { _ = again.Close() }()
	if n := len(again.Reviews(Filter{})); n != 2 {
		t.Fatalf("%d reviews survived, want the one before the tear and the one after: the new line was glued to the torn one", n)
	}
}

func TestAFailedWriteLeavesNoPartialLineForTheNextReviewToJoin(t *testing.T) {
	dir := t.TempDir()
	s, _ := OpenStore(dir)
	real := s.write
	s.write = func(b []byte) (int, error) {
		n, _ := real(b[:len(b)/2])
		return n, errors.New("no space left on device")
	}
	if _, err := s.Add(context.Background(), review("acme/api", t0, finding("a.go", "error"))); err == nil {
		t.Fatal("a failed write was reported as kept")
	}
	if n := len(s.Reviews(Filter{})); n != 0 {
		t.Fatalf("a review whose write failed is in the index: %d", n)
	}
	s.write = real
	if _, err := s.Add(context.Background(), review("acme/web", t0, finding("b.go", "nit"))); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	again, _ := OpenStore(dir)
	defer func() { _ = again.Close() }()
	if n, skipped := len(again.Reviews(Filter{})), again.Skipped(); n != 1 || skipped != 0 {
		t.Fatalf("%d reviews, %d skipped lines: the half line must have been removed", n, skipped)
	}
}

func TestAReviewDatedMinutesAheadIsRefusedButSmallClockSkewIsNot(t *testing.T) {
	s, _ := OpenStore(t.TempDir())
	defer func() { _ = s.Close() }()
	if _, err := s.Add(context.Background(), review("r", time.Now().Add(time.Minute))); err != nil {
		t.Fatalf("one minute of skew was refused: %v", err)
	}
	if _, err := s.Add(context.Background(), review("r", time.Now().Add(30*time.Minute))); err == nil {
		t.Fatal("a review dated 30 minutes ahead is invisible to every query and must be refused, not accepted")
	}
}

func TestAReviewSentWithoutATimeIsKeptOnceWhenSentAgain(t *testing.T) {
	s, _ := OpenStore(t.TempDir())
	defer func() { _ = s.Close() }()
	r := Review{Repo: "acme/api", Commit: "abc", Result: "clean", Complete: true}
	a, _ := s.Add(context.Background(), r)
	time.Sleep(2 * time.Millisecond)
	b, _ := s.Add(context.Background(), r)
	if a.ID != b.ID || len(s.Reviews(Filter{})) != 1 {
		t.Fatalf("a retry with no at was kept twice: %d reviews", len(s.Reviews(Filter{})))
	}
}

func TestASecondStoreOnTheSameDirectoryIsRefused(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(dir); !errors.Is(err, ErrInUse) {
		t.Fatalf("err = %v, want ErrInUse: two writers would interleave their lines", err)
	}
	_ = s.Close()
	again, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("the directory was not released on Close: %v", err)
	}
	_ = again.Close()
}

func TestATailThatParsesButHasNoNewlineIsEndedBeforeTheNextReview(t *testing.T) {
	dir := t.TempDir()
	s, _ := OpenStore(dir)
	first, _ := s.Add(context.Background(), review("acme/api", t0, finding("a.go", "error")))
	_ = s.Close()
	p := filepath.Join(dir, "reviews.jsonl")
	b, _ := os.ReadFile(p)
	if err := os.WriteFile(p, bytes.TrimRight(b, "\n"), 0o600); err != nil { // a whole object, its newline lost
		t.Fatal(err)
	}
	s, _ = OpenStore(dir)
	if _, err := s.Add(context.Background(), review("acme/web", t0.Add(time.Hour), finding("b.go", "nit"))); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	again, _ := OpenStore(dir)
	defer func() { _ = again.Close() }()
	if n := len(again.Reviews(Filter{})); n != 2 || again.Skipped() != 0 {
		t.Fatalf("%d reviews and %d skipped lines: the new line was glued to %s", n, again.Skipped(), first.ID[:8])
	}
}

func TestALineTooLongToReadIsSkippedAndCountedNotFatal(t *testing.T) {
	dir := t.TempDir()
	s, _ := OpenStore(dir)
	_, _ = s.Add(context.Background(), review("acme/api", t0, finding("a.go", "error")))
	_ = s.Close()
	f, _ := os.OpenFile(filepath.Join(dir, "reviews.jsonl"), os.O_APPEND|os.O_WRONLY, 0o600)
	_, _ = f.Write(bytes.Repeat([]byte("x"), 20<<20)) // longer than any review can be
	_, _ = f.WriteString("\n")
	_ = f.Close()
	s, _ = OpenStore(dir)
	_, _ = s.Add(context.Background(), review("acme/web", t0.Add(time.Hour), finding("b.go", "nit")))
	_ = s.Close()
	again, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("one over-long line stopped the server starting: %v", err)
	}
	defer func() { _ = again.Close() }()
	if n, skipped := len(again.Reviews(Filter{})), again.Skipped(); n != 2 || skipped != 1 {
		t.Fatalf("%d reviews and %d skipped: the two whole reviews must load and the long line be counted", n, skipped)
	}
}

func TestALineOverTheLimitIsDroppedWithoutBeingKept(t *testing.T) {
	good, _ := json.Marshal(Review{ID: "abc", Repo: "r", At: t0})
	input := string(good) + "\n" + strings.Repeat("y", 5000) + "\n" + string(good[:len(good)-1]) + "7}\n"
	s := &Store{byID: map[string]int{}}
	if err := s.loadFrom(strings.NewReader(input), 1000); err != nil {
		t.Fatal(err)
	}
	if s.peakLine > 4*1000 {
		t.Fatalf("held %d bytes for one line with a limit of 1000: an over-long line must be dropped as it is read, not kept", s.peakLine)
	}
	if s.skipped != 2 || len(s.reviews) != 1 {
		t.Fatalf("skipped %d, kept %d: the 5000 byte line is over the 1000 limit and is counted, the bad JSON too, the first review is kept", s.skipped, len(s.reviews))
	}
}
