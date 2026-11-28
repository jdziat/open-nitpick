package insights

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nitpick-oss/nitpick/internal/database"
	"github.com/nitpick-oss/nitpick/internal/database/databasetest"
)

// openStore is a store on a fresh database on the engine this run is for.
func openStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(databasetest.One(t, Models()))
}

// viewAt loads the window a query asks about, failing the test when it cannot.
func viewAt(t *testing.T, s *Store, q Query) *View {
	t.Helper()
	v, err := s.View(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// all is every review in the store, as a View over a window wide enough to hold them.
func all(t *testing.T, s *Store) []Review {
	t.Helper()
	return viewAt(t, s, Query{Now: time.Now().UTC().Add(maxSkew), Days: 3650}).Reviews(Filter{})
}

var t0 = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

func review(repo string, at time.Time, fs ...Finding) Review {
	return Review{Repo: repo, Commit: "abc1234", At: at, Result: "findings", Complete: true, Findings: fs}
}

func finding(path, sev string) Finding {
	return Finding{Path: path, Line: 10, Severity: sev, Class: "bug", Title: "t " + path}
}

func TestAReviewAddedByOneServerIsReadByAnother(t *testing.T) {
	databasetest.Each(t, Models(), func(t *testing.T, db *database.DB) {
		a, b := NewStore(db), NewStore(db)
		kept, err := a.Add(context.Background(), review("acme/api", t0, finding("a.go", "error")))
		if err != nil {
			t.Fatal(err)
		}
		got, ok, err := b.Review(context.Background(), kept.ID)
		if err != nil || !ok || got.Repo != "acme/api" || len(got.Findings) != 1 || got.Findings[0].Path != "a.go" {
			t.Fatalf("the second server read %+v, %v, %v", got, ok, err)
		}
		if n := len(all(t, b)); n != 1 {
			t.Errorf("the second server's view holds %d reviews, want 1", n)
		}
		if _, ok, err := b.Review(context.Background(), "no-such-id"); err != nil || ok {
			t.Errorf("a missing review: %v, %v", ok, err)
		}
	})
}

func TestTheSameReviewSentToTwoServersAtOnceIsKeptOnce(t *testing.T) {
	databasetest.Each(t, Models(), func(t *testing.T, db *database.DB) {
		r := review("acme/api", t0, finding("a.go", "error"))
		var wg sync.WaitGroup
		ids := make(chan string, 16)
		for i := range 16 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				got, err := NewStore(db).Add(context.Background(), r)
				if err != nil {
					t.Errorf("writer %d: %v", i, err)
					return
				}
				ids <- got.ID
			}()
		}
		wg.Wait()
		close(ids)
		seen := map[string]bool{}
		for id := range ids {
			seen[id] = true
		}
		if len(seen) != 1 {
			t.Errorf("sixteen sends of one review returned %d ids", len(seen))
		}
		if n := len(all(t, NewStore(db))); n != 1 {
			t.Errorf("sixteen sends of one review made %d records, a CI retry must not double a repo's findings", n)
		}
	})
}

func TestManyDistinctReviewsWrittenAtOnceAreAllKept(t *testing.T) {
	databasetest.Each(t, Models(), func(t *testing.T, db *database.DB) {
		var wg sync.WaitGroup
		for i := range 40 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				r := review("acme/api", t0.Add(time.Duration(i)*time.Minute), finding(fmt.Sprintf("f%d.go", i), "error"))
				if _, err := NewStore(db).Add(context.Background(), r); err != nil {
					t.Errorf("review %d: %v", i, err)
				}
			}()
		}
		wg.Wait()
		if n := len(all(t, NewStore(db))); n != 40 {
			t.Errorf("40 reviews written at once left %d", n)
		}
	})
}

func TestAViewHoldsOnlyTheWindowsAQueryCanAskAboutNewestFirst(t *testing.T) {
	s := openStore(t)
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	days := func(n int) time.Time { return now.AddDate(0, 0, -n) }
	for _, r := range []Review{
		review("acme/api", days(1), finding("a.go", "error")),              // in the window
		review("acme/web", days(5), finding("b.go", "error")),              // in the window, another repo
		review("acme/api", days(10), finding("c.go", "error")),             // in the prior window
		review("acme/api", days(40), finding("d.go", "error")),             // before both
		review("acme/api", now.Add(time.Minute), finding("e.go", "error")), // after now
	} {
		if _, err := s.Add(context.Background(), r); err != nil {
			t.Fatal(err)
		}
	}
	got := viewAt(t, s, Query{Now: now, Days: 7}).Reviews(Filter{})
	var paths []string
	for _, r := range got {
		paths = append(paths, r.Findings[0].Path)
	}
	if strings.Join(paths, ",") != "a.go,b.go,c.go" {
		t.Errorf("a 7 day view holds %v, want the window and the one before it, newest first, and nothing after now", paths)
	}
}

func TestAReadThatFailsIsAnErrorAndNotAnEmptyDashboard(t *testing.T) {
	db := databasetest.One(t, Models())
	s := NewStore(db)
	if _, err := s.Add(context.Background(), review("acme/api", t0, finding("a.go", "error"))); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.View(context.Background(), Query{Now: t0, Days: 7}); err == nil {
		t.Error("a view over a closed database returned no error, so a dashboard would read as empty")
	}
	if _, _, err := s.Review(context.Background(), "x"); err == nil {
		t.Error("a review read from a closed database returned no error")
	}
}

func TestAStoredReviewThatCannotBeDecodedFailsTheReadAndIsNotSkipped(t *testing.T) {
	db := databasetest.One(t, Models())
	s := NewStore(db)
	if _, err := s.Add(context.Background(), review("acme/api", t0, finding("a.go", "error"))); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&storedReview{}).Where("1 = 1").Update("body", []byte("{broken")).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.View(context.Background(), Query{Now: t0.Add(time.Hour), Days: 30}); err == nil || !strings.Contains(err.Error(), "cannot be decoded") {
		t.Errorf("a damaged review was passed over in silence: %v", err)
	}
}

func TestStoreIsIdempotentForTheSameReview(t *testing.T) {
	s := openStore(t)
	r := review("acme/api", t0, finding("a.go", "error"))
	a, _ := s.Add(context.Background(), r)
	b, _ := s.Add(context.Background(), r)
	if a.ID != b.ID || len(all(t, s)) != 1 {
		t.Fatalf("resending one review made %d records", len(all(t, s)))
	}
}

func TestStoreRefusesWhatCannotBeARepoOrAPath(t *testing.T) {
	s := openStore(t)
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
	if n := len(all(t, s)); n != 0 {
		t.Fatalf("a refused review was kept: %d", n)
	}
}

func TestStoreScrubsSecretsFromFindingText(t *testing.T) {
	s := openStore(t)
	f := finding("a.go", "error")
	f.Rationale = "the key ghp_abcdefghijklmnopqrstuvwxyz0123456789 is committed"
	got, err := s.Add(context.Background(), review("acme/api", t0, f))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got.Findings[0].Rationale, "ghp_") {
		t.Fatalf("a token reached the store: %q", got.Findings[0].Rationale)
	}
	stored, _, _ := s.Review(context.Background(), got.ID)
	if strings.Contains(stored.Findings[0].Rationale, "ghp_") {
		t.Fatalf("a token reached the database: %q", stored.Findings[0].Rationale)
	}
}

func TestStoreCapsFindingsPerReviewAndSaysSo(t *testing.T) {
	s := openStore(t)
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

func TestAReviewDatedMinutesAheadIsRefusedButSmallClockSkewIsNot(t *testing.T) {
	s := openStore(t)
	if _, err := s.Add(context.Background(), review("r", time.Now().Add(time.Minute))); err != nil {
		t.Fatalf("one minute of skew was refused: %v", err)
	}
	if _, err := s.Add(context.Background(), review("r", time.Now().Add(30*time.Minute))); err == nil {
		t.Fatal("a review dated 30 minutes ahead is invisible to every query and must be refused, not accepted")
	}
}

func TestAReviewSentWithoutATimeIsKeptOnceWhenSentAgain(t *testing.T) {
	s := openStore(t)
	r := Review{Repo: "acme/api", Commit: "abc", Result: "clean", Complete: true}
	a, _ := s.Add(context.Background(), r)
	time.Sleep(2 * time.Millisecond)
	b, _ := s.Add(context.Background(), r)
	if a.ID != b.ID || len(all(t, s)) != 1 {
		t.Fatalf("a retry with no at was kept twice: %d reviews", len(all(t, s)))
	}
}

func TestAResentReviewReturnsTheOneThatWasKeptNotTheOneJustSent(t *testing.T) {
	s := openStore(t)
	r := review("acme/api", t0, finding("a.go", "error"))
	r.Source = "first-sender"
	first, err := s.Add(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	r.Source = "second-sender" // the id ignores who sent it, so this is the same review
	again, err := s.Add(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != first.ID || again.Source != "first-sender" {
		t.Errorf("a resend returned source %q, want the kept review's %q", again.Source, "first-sender")
	}
}

func TestAStoredBodyThatBelongsToAnotherReviewFailsTheRead(t *testing.T) {
	db := databasetest.One(t, Models())
	s := NewStore(db)
	a, _ := s.Add(context.Background(), review("acme/api", t0, finding("a.go", "error")))
	b, _ := s.Add(context.Background(), review("acme/web", t0.Add(time.Hour), finding("b.go", "error")))
	var other storedReview
	if err := db.Where("id = ?", b.ID).First(&other).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&storedReview{}).Where("id = ?", a.ID).Update("body", other.Body).Error; err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Review(context.Background(), a.ID); err == nil {
		t.Error("a row whose body is another review's was read as that review")
	}
}
