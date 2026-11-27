package wire

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func hashN(n int) string { return fmt.Sprintf("%064x", n) }

// put writes a record and stamps the file's modification time, which is the order
// a listing uses.
func putAt(t *testing.T, fs *FileStore, n int, mod func(*Record), at time.Time) string {
	t.Helper()
	rec := Record{Schema: Schema, Hash: hashN(n), Tool: "v1.0.0", Provider: "openrouter", Model: "m1", Created: at, Answers: map[string]string{"a": "x"}, Batches: 1, Complete: true, Source: "ci"}
	if mod != nil {
		mod(&rec)
	}
	if err := fs.Put(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(fs.dir, rec.Hash+".json"), at, at); err != nil {
		t.Fatal(err)
	}
	return rec.Hash
}

func newStore(t *testing.T) *FileStore {
	t.Helper()
	fs, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return fs
}

func list(t *testing.T, fs *FileStore, q ListQuery) ([]Summary, string) {
	t.Helper()
	page, next, err := fs.List(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	return page, next
}

func TestListingReturnsRecordsNewestFirst(t *testing.T) {
	fs := newStore(t)
	base := time.Now().Add(-time.Hour).Truncate(time.Second)
	for i := 1; i <= 3; i++ {
		putAt(t, fs, i, nil, base.Add(time.Duration(i)*time.Minute))
	}
	page, next := list(t, fs, ListQuery{})
	if len(page) != 3 || next != "" {
		t.Fatalf("page = %d records, next %q", len(page), next)
	}
	if page[0].Hash != hashN(3) || page[2].Hash != hashN(1) {
		t.Errorf("order = %s, %s, %s; want newest first", page[0].Hash[60:], page[1].Hash[60:], page[2].Hash[60:])
	}
	if page[0].Model != "m1" || page[0].Tool != "v1.0.0" || !page[0].Complete || page[0].Answers != 1 || page[0].Source != "ci" {
		t.Errorf("summary = %+v", page[0])
	}
}

// Rule 10: an empty list from an empty store is a different fact from an empty
// list because the read failed.
func TestAnEmptyStoreListsNothingAndABrokenOneFails(t *testing.T) {
	fs := newStore(t)
	page, next, err := fs.List(context.Background(), ListQuery{})
	if err != nil || len(page) != 0 || next != "" {
		t.Fatalf("an empty store = %v, %q, %v; want no records and no error", page, next, err)
	}
	// The same store, with a record: not empty, so the above is the store's.
	putAt(t, fs, 1, nil, time.Now())
	if page, _ := list(t, fs, ListQuery{}); len(page) != 1 {
		t.Fatalf("a store with one record lists %d", len(page))
	}
	// A directory that cannot be read is an error, not "nothing".
	broken := &FileStore{dir: filepath.Join(t.TempDir(), "gone")}
	if _, _, err := broken.List(context.Background(), ListQuery{}); err == nil {
		t.Error("a store whose directory cannot be read listed as empty")
	}
}

func TestListingFiltersOnModelToolAndTime(t *testing.T) {
	fs := newStore(t)
	base := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
	putAt(t, fs, 1, func(r *Record) { r.Model = "alpha"; r.Tool = "v1" }, base)
	putAt(t, fs, 2, func(r *Record) { r.Model = "beta"; r.Tool = "v1" }, base.Add(time.Hour))
	putAt(t, fs, 3, func(r *Record) { r.Model = "alpha"; r.Tool = "v2" }, base.Add(2*time.Hour))
	putAt(t, fs, 4, func(r *Record) { r.Model = "alpha"; r.Tool = "v2" }, base.Add(24*time.Hour))
	hashes := func(q ListQuery) string {
		page, _ := list(t, fs, q)
		var out []string
		for _, s := range page {
			out = append(out, s.Hash[63:])
		}
		return strings.Join(out, "")
	}
	for name, tc := range map[string]struct {
		q    ListQuery
		want string
	}{
		"no filter":           {ListQuery{}, "4321"},
		"a model":             {ListQuery{Model: "alpha"}, "431"},
		"a tool version":      {ListQuery{Tool: "v2"}, "43"},
		"both":                {ListQuery{Model: "alpha", Tool: "v2"}, "43"},
		"since":               {ListQuery{Since: base.Add(90 * time.Minute)}, "43"},
		"until (exclusive)":   {ListQuery{Until: base.Add(2 * time.Hour)}, "21"},
		"since and until":     {ListQuery{Since: base.Add(time.Hour), Until: base.Add(3 * time.Hour)}, "32"},
		"nothing matches":     {ListQuery{Model: "gamma"}, ""},
		"a prefix of a model": {ListQuery{Model: "alph"}, ""},
	} {
		if got := hashes(tc.q); got != tc.want {
			t.Errorf("%s: %q, want %q", name, got, tc.want)
		}
	}
}

// Pages join up: every record appears once, in order, however the page size
// divides the store.
func TestPaginationVisitsEveryRecordOnce(t *testing.T) {
	fs := newStore(t)
	base := time.Now().Add(-time.Hour).Truncate(time.Second)
	const total = 23
	for i := 1; i <= total; i++ {
		putAt(t, fs, i, nil, base.Add(time.Duration(i)*time.Second))
	}
	for _, size := range []int{1, 2, 5, 7, 22, 23, 24, 100} {
		var seen []string
		cursor := ""
		for pages := 0; ; pages++ {
			if pages > total+2 {
				t.Fatalf("size %d: pagination did not end", size)
			}
			page, next := list(t, fs, ListQuery{Limit: size, Cursor: cursor})
			for _, s := range page {
				seen = append(seen, s.Hash)
			}
			if next == "" {
				break
			}
			cursor = next
		}
		if len(seen) != total {
			t.Errorf("size %d: saw %d records, want %d", size, len(seen), total)
			continue
		}
		for i, h := range seen {
			if h != hashN(total-i) {
				t.Errorf("size %d: position %d is %s, want %s", size, i, h[60:], hashN(total - i)[60:])
				break
			}
		}
	}
}

// Records with the same modification time are still ordered and still paged
// without gaps or repeats.
func TestPaginationHandlesRecordsWrittenInTheSameInstant(t *testing.T) {
	fs := newStore(t)
	at := time.Now().Add(-time.Hour).Truncate(time.Second)
	for i := 1; i <= 9; i++ {
		putAt(t, fs, i, nil, at)
	}
	var seen = map[string]int{}
	cursor := ""
	for pages := 0; pages < 20; pages++ {
		page, next := list(t, fs, ListQuery{Limit: 2, Cursor: cursor})
		for _, s := range page {
			seen[s.Hash]++
		}
		if next == "" {
			break
		}
		cursor = next
	}
	if len(seen) != 9 {
		t.Errorf("saw %d distinct records, want 9", len(seen))
	}
	for h, n := range seen {
		if n != 1 {
			t.Errorf("%s appeared %d times", h[60:], n)
		}
	}
}

func TestListingBoundsItsPageSize(t *testing.T) {
	fs := newStore(t)
	base := time.Now().Add(-time.Hour)
	for i := 1; i <= maxListLimit+20; i++ {
		putAt(t, fs, i, nil, base.Add(time.Duration(i)*time.Second))
	}
	if page, _ := list(t, fs, ListQuery{}); len(page) != defaultListLimit {
		t.Errorf("the default page is %d, want %d", len(page), defaultListLimit)
	}
	if page, _ := list(t, fs, ListQuery{Limit: 100000}); len(page) != maxListLimit {
		t.Errorf("an enormous limit gave %d, want the cap of %d", len(page), maxListLimit)
	}
	if page, _ := list(t, fs, ListQuery{Limit: -5}); len(page) != defaultListLimit {
		t.Errorf("a negative limit gave %d, want the default", len(page))
	}
}

func TestACursorThatIsNotOursIsRefusedAsABadQuery(t *testing.T) {
	fs := newStore(t)
	putAt(t, fs, 1, nil, time.Now())
	for _, c := range []string{"x", "12", "12-notahash", "abc-" + hashN(1), "-" + hashN(1)} {
		_, _, err := fs.List(context.Background(), ListQuery{Cursor: c})
		if !errors.Is(err, ErrBadQuery) {
			t.Errorf("cursor %q: err = %v, want ErrBadQuery", c, err)
		}
	}
}

// Listing observes. A file that cannot be decoded is skipped and left where it is,
// and the other records are still listed.
func TestListingSkipsUnreadableRecordsAndLeavesThemInPlace(t *testing.T) {
	fs := newStore(t)
	putAt(t, fs, 1, nil, time.Now().Add(-time.Minute))
	bad := filepath.Join(fs.dir, hashN(2)+".json")
	if err := os.WriteFile(bad, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	mismatch := Record{Schema: Schema, Hash: hashN(99), Model: "m"} // a file whose name is another hash
	if err := fs.Put(context.Background(), mismatch); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(fs.dir, hashN(99)+".json"), filepath.Join(fs.dir, hashN(3)+".json")); err != nil {
		t.Fatal(err)
	}
	// Stray files that are not records.
	for _, name := range []string{"notes.txt", ".lock", "zz.json", hashN(4) + ".json.123.tmp"} {
		if err := os.WriteFile(filepath.Join(fs.dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	page, _ := list(t, fs, ListQuery{})
	if len(page) != 1 || page[0].Hash != hashN(1) {
		t.Errorf("listed %v, want only the one readable record", page)
	}
	if _, err := os.Stat(bad); err != nil {
		t.Errorf("listing moved or removed an unreadable file: %v", err)
	}
	if entries, _ := os.ReadDir(filepath.Join(fs.dir, "history")); len(entries) != 0 {
		t.Error("listing quarantined a file")
	}
}

func TestListingStopsWhenTheContextIsCancelled(t *testing.T) {
	fs := newStore(t)
	for i := 1; i <= 5; i++ {
		putAt(t, fs, i, nil, time.Now().Add(time.Duration(i)*time.Second))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := fs.List(ctx, ListQuery{}); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

// A file whose name is not a record hash is not a record, even if what is inside
// says it is one: the hash a listing returns is one the store's own Get accepts.
func TestAFileWithAnInvalidNameIsNotListedEvenIfItsBodyClaimsToBeARecord(t *testing.T) {
	fs := newStore(t)
	putAt(t, fs, 1, nil, time.Now().Add(-time.Minute))
	for _, name := range []string{"evil", "../escape", "short", strings.ToUpper(hashN(255))} {
		body := fmt.Sprintf(`{"schema":%d,"hash":%q,"model":"m","complete":true}`, Schema, name)
		path := filepath.Join(fs.dir, filepath.Base(name)+".json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	page, _ := list(t, fs, ListQuery{})
	if len(page) != 1 || page[0].Hash != hashN(1) {
		var got []string
		for _, s := range page {
			got = append(got, s.Hash)
		}
		t.Errorf("listed %v, want only the one valid record", got)
	}
	for _, s := range page {
		if _, ok, err := fs.Get(context.Background(), s.Hash); err != nil || !ok {
			t.Errorf("listed %q, which the store's own Get does not accept (%v)", s.Hash, err)
		}
	}
}
