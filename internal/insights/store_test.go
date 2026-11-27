package insights

import (
	"context"
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
