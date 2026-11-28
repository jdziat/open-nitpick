package notary

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nitpick-oss/nitpick/internal/wire"
)

// testdata/v2.10.0 is a data directory that the v2.10.0 notary command wrote through
// its own HTTP API, and the responses that binary gave for each record. The server
// that replaced it has to read what it wrote and answer the same way, or an
// existing deployment loses its records on upgrade.
//
// The fixture is copied before use, so a test that opens it cannot change it.
func oldData(t *testing.T) string {
	t.Helper()
	dst := t.TempDir()
	src := filepath.Join("testdata", "v2.10.0", "data")
	err := filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o750)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

// The new server answers each GET with the bytes the old one did.
func TestARecordWrittenByTheLastReleaseIsReadBackByteForByte(t *testing.T) {
	store, err := wire.NewFileStore(oldData(t))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(New(testDB(t), store, []Token{NewToken("ci", RoleAuthoritative, "fixture-secret")}, false))
	defer srv.Close()

	matches, err := filepath.Glob(filepath.Join("testdata", "v2.10.0", "responses", "*.json"))
	if err != nil || len(matches) != 4 {
		t.Fatalf("fixtures = %v, %v; want the four responses the old server gave", matches, err)
	}
	for _, path := range matches {
		hash := strings.TrimSuffix(filepath.Base(path), ".json")
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		req, _ := http.NewRequest("GET", srv.URL+"/v1/records/"+hash, nil)
		req.Header.Set("Authorization", "Bearer fixture-secret")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Errorf("%s = %d", hash[60:], resp.StatusCode)
			continue
		}
		// Compared as JSON values and then as bytes: a difference in field order or
		// spacing would still be a record the old client reads, but byte equality
		// is what "unchanged" claims, so it is asserted too.
		var a, b any
		if json.Unmarshal(got, &a) != nil || json.Unmarshal(want, &b) != nil {
			t.Fatalf("%s: not JSON", hash[60:])
		}
		if string(got) != string(want) {
			t.Errorf("%s: the new server's answer differs from the old server's\nnew: %s\nold: %s", hash[60:], got, want)
		}
	}
}

// What the old server did not have, the new one does not invent.
func TestARecordTheLastReleaseNeverHadIsStillNotFound(t *testing.T) {
	store, err := wire.NewFileStore(oldData(t))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(New(testDB(t), store, []Token{NewToken("ci", RoleAuthoritative, "fixture-secret")}, false))
	defer srv.Close()
	req, _ := http.NewRequest("GET", srv.URL+"/v1/records/"+strings.Repeat("0", 63)+"5", nil)
	req.Header.Set("Authorization", "Bearer fixture-secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("a record the old server answered 404 for = %d", resp.StatusCode)
	}
}

// The directory is not only readable: it keeps working. A clear moves a record
// into history beside the old one, and a write after it replaces the record.
func TestAnOldDataDirectoryKeepsWorkingForWritesAndClears(t *testing.T) {
	dir := oldData(t)
	store, err := wire.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	hash := strings.Repeat("0", 63) + "4"
	before, ok, err := store.Get(ctx, hash)
	if err != nil || !ok {
		t.Fatalf("the record the old server cleared and rewrote is gone: %v %v", ok, err)
	}
	if before.Complete != true || before.Answers["eeeeeeeeeeeeeeee#0"] != `{"content":"second life"}` {
		t.Errorf("the rewritten record reads as %+v", before)
	}
	if err := store.Clear(ctx, hash, "after-upgrade"); err != nil {
		t.Fatal(err)
	}
	// The history the old server kept is still there, and the new clear is added
	// after it, not over it.
	oldHistory := hash + ".1.json"
	if _, err := os.Stat(filepath.Join(dir, "history", oldHistory)); err != nil {
		t.Errorf("the old server's history file %s is gone: %v", oldHistory, err)
	}
	// History is filed as <hash>.<n>.json with n counting clears. The fixture
	// record was cleared once before this test, so a clear now is its second.
	if _, err := os.Stat(filepath.Join(dir, "history", hash+".2.json")); err != nil {
		entries, _ := os.ReadDir(filepath.Join(dir, "history"))
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("a clear after the upgrade was not filed as %s.2.json in the old layout; history holds %v", hash, names)
	}
	// The unusable-answer mark the old engine wrote survives a read and a write.
	three, ok, err := store.Get(ctx, strings.Repeat("0", 63)+"3")
	if err != nil || !ok {
		t.Fatal(err)
	}
	if !strings.HasPrefix(three.Answers["dddddddddddddddd#1"], "!") || three.Effort != "medium" {
		t.Errorf("the mark or the effort did not survive: %+v", three)
	}
	// And the new listing sees the old records.
	page, _, err := store.List(ctx, wire.ListQuery{})
	if err != nil || len(page) < 3 {
		t.Errorf("the listing of an old directory = %d records, %v", len(page), err)
	}
}

// The new view shows a record the old server wrote, including the answer it
// marked unusable and the reasoning level.
func TestTheHubShowsARecordWrittenByTheLastRelease(t *testing.T) {
	store, err := wire.NewFileStore(oldData(t))
	if err != nil {
		t.Fatal(err)
	}
	h := newHub(t, store)
	h.signIn("kay", "keepers")
	code, v := getView(t, h, "/v1/records/view/"+strings.Repeat("0", 63)+"3", nil)
	if code != 200 {
		t.Fatalf("the view of an old record = %d", code)
	}
	if v.Effort != "medium" || v.Model != "z-ai/glm-5.3-flash" || v.Tool != "v2.10.0" {
		t.Errorf("the view lacks what the old record held: %+v", v)
	}
	var sawVote, sawUnusable bool
	for _, a := range v.Answers {
		sawVote = sawVote || strings.Contains(a.Text, "rejected vote")
		sawUnusable = sawUnusable || a.Unusable
	}
	if !sawVote || !sawUnusable {
		t.Errorf("the answer the old server marked unusable is not shown as one: %+v", v.Answers)
	}
}
