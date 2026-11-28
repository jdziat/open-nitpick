package state

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nitpick-oss/nitpick/internal/database"
	"github.com/nitpick-oss/nitpick/internal/database/databasetest"
)

var t0 = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

// at is a store whose clock reads t0 plus d, over the database db, so a test can move
// time and have another server, over the same database, see a different now.
func at(db *database.DB, d time.Duration) *Store {
	return New(db).WithClock(func() time.Time { return t0.Add(d) })
}

func TestAnEntryWrittenByOneServerIsReadByAnother(t *testing.T) {
	databasetest.Each(t, Models(), func(t *databasetest.Sub) {
		db := t.DB
		ctx := context.Background()
		a, b := at(db, 0), at(db, time.Second)
		if err := a.Put(ctx, "k", Entry{Value: []byte("v"), Expires: t0.Add(time.Minute)}); err != nil {
			t.Fatal(err)
		}
		got, ok, err := b.Get(ctx, "k")
		if err != nil || !ok || string(got.Value) != "v" || !got.Expires.Equal(t0.Add(time.Minute)) {
			t.Errorf("Get = %+v, %v, %v", got, ok, err)
		}
		if _, ok, _ := b.Get(ctx, "absent"); ok {
			t.Error("a key nobody wrote was found")
		}
		// A write replaces.
		_ = a.Put(ctx, "k", Entry{Value: []byte("w"), Expires: t0.Add(time.Hour)})
		if got, _, _ := b.Get(ctx, "k"); string(got.Value) != "w" {
			t.Errorf("a second write did not replace the first: %q", got.Value)
		}
		// Any bytes are kept, including NUL and invalid UTF-8.
		raw := []byte{0, 1, 0xff, 0xfe, 'a', 0}
		_ = a.Put(ctx, "bin", Entry{Value: raw, Expires: t0.Add(time.Hour)})
		if got, _, _ := b.Get(ctx, "bin"); !bytes.Equal(got.Value, raw) {
			t.Errorf("binary value came back as %v", got.Value)
		}
	})
}

func TestAnExpiredEntryIsAbsentToEveryOperationBeforeAndAfterASweep(t *testing.T) {
	databasetest.Each(t, Models(), func(t *databasetest.Sub) {
		db := t.DB
		ctx := context.Background()
		now := at(db, 0)
		later := at(db, 2*time.Minute)
		_ = now.Put(ctx, "k", Entry{Value: []byte("v"), Expires: t0.Add(time.Minute)})
		if _, ok, _ := now.Get(ctx, "k"); !ok {
			t.Fatal("setup: the entry is not live before its expiry")
		}
		if _, ok, _ := later.Get(ctx, "k"); ok {
			t.Error("Get returned an entry past its expiry")
		}
		if _, ok, _ := later.Take(ctx, "k"); ok {
			t.Error("Take returned an entry past its expiry")
		}
		_ = now.Put(ctx, "k", Entry{Value: []byte("v"), Expires: t0.Add(time.Minute)})
		saw := &Entry{}
		_ = later.Update(ctx, "k", func(cur *Entry) (*Entry, error) { saw = cur; return nil, nil })
		if saw != nil {
			t.Errorf("Update showed an expired entry: %+v", saw)
		}
		_ = now.Put(ctx, "k", Entry{Value: []byte("v"), Expires: t0.Add(time.Minute)})
		_ = now.Put(ctx, "live", Entry{Value: []byte("v"), Expires: t0.Add(time.Hour)})
		// Nothing has touched them since, so they are still rows and only the clock
		// says one has expired.
		if n, _ := later.Count(ctx, "k"); n != 0 {
			t.Errorf("Count included an expired entry: %d", n)
		}
		if n, _ := later.Count(ctx, "live"); n != 1 {
			t.Errorf("Count missed a live entry: %d", n)
		}
		if err := later.Sweep(ctx); err != nil {
			t.Fatal(err)
		}
		var left int64
		db.Model(&row{}).Count(&left)
		if left != 1 {
			t.Errorf("a sweep left %d rows, want only the one that has not expired", left)
		}
	})
}

func TestOfManyServersTakingOneKeyAtOnceExactlyOneGetsIt(t *testing.T) {
	databasetest.Each(t, Models(), func(t *databasetest.Sub) {
		db := t.DB
		ctx := context.Background()
		if err := at(db, 0).Put(ctx, "flow", Entry{Value: []byte("once"), Expires: t0.Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		got := make(chan bool, 24)
		for range 24 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, ok, err := at(db, 0).Take(ctx, "flow")
				if err != nil {
					t.Error(err)
				}
				got <- ok
			}()
		}
		wg.Wait()
		close(got)
		won := 0
		for ok := range got {
			if ok {
				won++
			}
		}
		if won != 1 {
			t.Errorf("%d of 24 servers took an entry that is good for one use", won)
		}
	})
}

func TestUpdatesFromManyServersTakeTurnsAndNoneIsLost(t *testing.T) {
	databasetest.Each(t, Models(), func(t *databasetest.Sub) {
		db := t.DB
		ctx := context.Background()
		const writers, each = 8, 5
		var wg sync.WaitGroup
		for range writers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				s := at(db, 0)
				for range each {
					err := s.Update(ctx, "counter", func(cur *Entry) (*Entry, error) {
						n := 0
						if cur != nil {
							_, _ = fmt.Sscanf(string(cur.Value), "%d", &n)
						}
						return &Entry{Value: []byte(fmt.Sprint(n + 1)), Expires: t0.Add(time.Hour)}, nil
					})
					if err != nil {
						t.Error(err)
					}
				}
			}()
		}
		wg.Wait()
		got, _, _ := at(db, 0).Get(ctx, "counter")
		if string(got.Value) != fmt.Sprint(writers*each) {
			t.Errorf("%d servers adding %d each counted %s, want %d: an update was lost", writers, each, got.Value, writers*each)
		}
	})
}

func TestUpdateCreatesChangesKeepsAndDeletes(t *testing.T) {
	databasetest.Each(t, Models(), func(t *databasetest.Sub) {
		db := t.DB
		ctx := context.Background()
		s := at(db, 0)
		var wrote int
		set := func(v string) func(*Entry) (*Entry, error) {
			return func(cur *Entry) (*Entry, error) {
				wrote++
				return &Entry{Value: []byte(v), Expires: t0.Add(time.Hour)}, nil
			}
		}
		_ = s.Update(ctx, "k", set("a"))
		if got, ok, _ := s.Get(ctx, "k"); !ok || string(got.Value) != "a" {
			t.Fatalf("created: %+v %v", got, ok)
		}
		_ = s.Update(ctx, "k", set("b"))
		if got, _, _ := s.Get(ctx, "k"); string(got.Value) != "b" {
			t.Errorf("changed: %q", got.Value)
		}
		// Returning the entry it was given leaves it, and writes nothing.
		before := time.Now()
		_ = s.Update(ctx, "k", func(cur *Entry) (*Entry, error) { return cur, nil })
		if got, _, _ := s.Get(ctx, "k"); string(got.Value) != "b" {
			t.Errorf("kept: %q", got.Value)
		}
		_ = before
		// A function that finds nothing and returns nothing creates nothing.
		_ = s.Update(ctx, "none", func(cur *Entry) (*Entry, error) { return nil, nil })
		if _, ok, _ := s.Get(ctx, "none"); ok {
			t.Error("returning nil for an absent key created it")
		}
		_ = s.Update(ctx, "k", func(cur *Entry) (*Entry, error) { return nil, nil })
		if _, ok, _ := s.Get(ctx, "k"); ok {
			t.Error("returning nil for a present key did not delete it")
		}
		// An error from the function changes nothing.
		_ = s.Update(ctx, "e", set("x"))
		err := s.Update(ctx, "e", func(*Entry) (*Entry, error) {
			return &Entry{Value: []byte("y"), Expires: t0.Add(time.Hour)}, fmt.Errorf("no")
		})
		if got, _, _ := s.Get(ctx, "e"); err == nil || string(got.Value) != "x" {
			t.Errorf("a failed update changed the entry: err=%v value=%q", err, got.Value)
		}
	})
}

func TestTwoServersCreatingTheSameKeyAtOnceBothSucceedAndOneValueWins(t *testing.T) {
	databasetest.Each(t, Models(), func(t *databasetest.Sub) {
		db := t.DB
		ctx := context.Background()
		var wg sync.WaitGroup
		for i := range 12 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				err := at(db, 0).Update(ctx, "new", func(cur *Entry) (*Entry, error) {
					if cur != nil {
						return cur, nil // another server created it first
					}
					// Slow, so every writer has read "absent" before any has written.
					time.Sleep(60 * time.Millisecond)
					return &Entry{Value: []byte(fmt.Sprint("by-", i)), Expires: t0.Add(time.Hour)}, nil
				})
				if err != nil {
					t.Errorf("writer %d: %v", i, err)
				}
			}()
		}
		wg.Wait()
		if got, ok, _ := at(db, 0).Get(ctx, "new"); !ok || !strings.HasPrefix(string(got.Value), "by-") {
			t.Errorf("after twelve racing creates: %+v %v", got, ok)
		}
	})
}

func TestCountAndDeleteOldestWorkOnAPrefix(t *testing.T) {
	databasetest.Each(t, Models(), func(t *databasetest.Sub) {
		db := t.DB
		ctx := context.Background()
		s := at(db, 0)
		for i := 1; i <= 3; i++ {
			_ = s.Put(ctx, fmt.Sprint("flow:", i), Entry{Value: []byte("x"), Expires: t0.Add(time.Duration(i) * time.Minute)})
		}
		_ = s.Put(ctx, "session:1", Entry{Value: []byte("x"), Expires: t0.Add(time.Second)})
		if n, _ := s.Count(ctx, "flow:"); n != 3 {
			t.Errorf("Count(flow:) = %d, want 3 and not the session", n)
		}
		if ok, err := s.DeleteOldest(ctx, "flow:"); !ok || err != nil {
			t.Fatalf("DeleteOldest = %v, %v", ok, err)
		}
		if _, ok, _ := s.Get(ctx, "flow:1"); ok {
			t.Error("the entry that expires first was not the one removed")
		}
		if _, ok, _ := s.Get(ctx, "session:1"); !ok {
			t.Error("an entry under another prefix was removed, though it expires sooner")
		}
		if ok, _ := s.DeleteOldest(ctx, "nothing:"); ok {
			t.Error("DeleteOldest reported removing something under an empty prefix")
		}
	})
}

func TestAKeyHidesWhatItWasMadeFromAndFitsTheColumn(t *testing.T) {
	long := strings.Repeat("a", 5000)
	k := Key("limit", long)
	if strings.Contains(k, "aaaa") || len(k) > 200 || !strings.HasPrefix(k, "limit:") {
		t.Errorf("Key = %q", k)
	}
	if Key("limit", "x") == Key("flow", "x") || Key("limit", "x") == Key("limit", "y") {
		t.Error("two different keys collided")
	}
	first := Key("limit", "x")
	if again := Key("limit", "x"); first != again {
		t.Errorf("a key is not stable: %q then %q", first, again)
	}
}

func TestDeleteRemovesAnEntryAndDeletingWhatIsNotThereIsNotAnError(t *testing.T) {
	databasetest.Each(t, Models(), func(t *databasetest.Sub) {
		db := t.DB
		ctx := context.Background()
		s := at(db, 0)
		_ = s.Put(ctx, "a", Entry{Value: []byte("1"), Expires: t0.Add(time.Hour)})
		_ = s.Put(ctx, "b", Entry{Value: []byte("2"), Expires: t0.Add(time.Hour)})
		if err := s.Delete(ctx, "a"); err != nil {
			t.Fatal(err)
		}
		if _, ok, _ := s.Get(ctx, "a"); ok {
			t.Error("a deleted entry is still there")
		}
		if _, ok, _ := s.Get(ctx, "b"); !ok {
			t.Error("deleting one key removed another")
		}
		if err := s.Delete(ctx, "never-written"); err != nil {
			t.Errorf("deleting what is not there: %v", err)
		}
	})
}
