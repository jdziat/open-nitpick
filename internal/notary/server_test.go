package notary

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nitpick-oss/nitpick/internal/wire"
)

var hash = strings.Repeat("ab", 32)

func setup(t *testing.T, allowClear bool) (string, func(token string) *wire.HTTPStore) {
	t.Helper()
	fs, err := wire.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(New(fs, []Token{
		NewToken("ci", RoleAuthoritative, "ci-secret"),
		NewToken("dev", RoleProvisional, "dev-secret"),
		NewToken("viewer", RoleRead, "view-secret"),
	}, allowClear))
	t.Cleanup(srv.Close)
	return srv.URL, func(tok string) *wire.HTTPStore {
		s, _ := wire.NewHTTPStore(srv.URL, tok)
		return s
	}
}

func rec(complete bool) wire.Record {
	return wire.Record{Schema: wire.Schema, Hash: hash, Answers: map[string]string{"k": "{}"}, Batches: 1, Complete: complete}
}

func TestAuthoritativeWritesAndAnyTokenReads(t *testing.T) {
	_, as := setup(t, false)
	ctx := context.Background()
	if err := as("ci-secret").Put(ctx, rec(true)); err != nil {
		t.Fatal(err)
	}
	for _, tok := range []string{"ci-secret", "dev-secret", "view-secret"} {
		r, ok, err := as(tok).Get(ctx, hash)
		if err != nil || !ok || !r.Complete {
			t.Errorf("%s: %v %v %v", tok, r, ok, err)
		}
	}
}

func TestUnknownTokenAndReadTokenCannotWrite(t *testing.T) {
	_, as := setup(t, false)
	ctx := context.Background()
	if err := as("nope").Put(ctx, rec(true)); !errors.Is(err, wire.ErrForbidden) {
		t.Errorf("unknown token: %v", err)
	}
	if err := as("view-secret").Put(ctx, rec(true)); !errors.Is(err, wire.ErrForbidden) {
		t.Errorf("read token: %v", err)
	}
}

func TestProvisionalWritesOnlyWhereNoCompleteRecordExists(t *testing.T) {
	_, as := setup(t, false)
	ctx := context.Background()
	if err := as("dev-secret").Put(ctx, rec(true)); err != nil {
		t.Fatalf("provisional write to an empty slot: %v", err)
	}
	got, _, _ := as("ci-secret").Get(ctx, hash)
	if got.Source != "provisional:dev" {
		t.Fatalf("source %q", got.Source)
	}
	// CI may replace a provisional record.
	if err := as("ci-secret").Put(ctx, rec(true)); err != nil {
		t.Fatal(err)
	}
	// A provisional token may not replace a complete one.
	if err := as("dev-secret").Put(ctx, rec(true)); err == nil {
		t.Fatal("provisional token replaced a complete record")
	}
	got, _, _ = as("ci-secret").Get(ctx, hash)
	if got.Source != "ci" {
		t.Fatalf("authoritative record was replaced: %q", got.Source)
	}
}

func TestClearRulesFollowTheOrgSetting(t *testing.T) {
	for _, allow := range []bool{false, true} {
		_, as := setup(t, allow)
		ctx := context.Background()
		_ = as("ci-secret").Put(ctx, rec(true))
		err := as("dev-secret").Clear(ctx, hash, "alice")
		if allow && err != nil {
			t.Errorf("allowed author clear failed: %v", err)
		}
		if !allow && !errors.Is(err, wire.ErrForbidden) {
			t.Errorf("author clear should be refused: %v", err)
		}
		if err := as("view-secret").Clear(ctx, hash, "x"); !errors.Is(err, wire.ErrForbidden) {
			t.Errorf("read token cleared: %v", err)
		}
		if err := as("ci-secret").Clear(ctx, hash, "ci"); err != nil {
			t.Errorf("authoritative clear: %v", err)
		}
	}
}

func TestClearedRecordIsGoneButTheCountSurvives(t *testing.T) {
	_, as := setup(t, false)
	ctx := context.Background()
	_ = as("ci-secret").Put(ctx, rec(true))
	_ = as("ci-secret").Clear(ctx, hash, "alice")
	if stub, _, _ := as("ci-secret").Get(ctx, hash); stub.Complete || len(stub.Answers) != 0 || stub.Clears != 1 {
		t.Fatalf("a cleared record must serve no answers and keep its clear count: %+v", stub)
	}
	_ = as("ci-secret").Put(ctx, rec(true))
	got, ok, _ := as("ci-secret").Get(ctx, hash)
	if !ok || got.Clears != 1 || len(got.ClearedBy) != 1 || got.ClearedBy[0] != "alice" {
		t.Fatalf("%+v", got)
	}
}

func TestUnreachableServerReportsTheSentinel(t *testing.T) {
	s, _ := wire.NewHTTPStore("http://127.0.0.1:1", "t")
	if _, _, err := s.Get(context.Background(), hash); !errors.Is(err, wire.ErrUnreachable) {
		t.Fatalf("%v", err)
	}
}

func TestBadHashIsRefusedNotServed(t *testing.T) {
	_, as := setup(t, false)
	for _, bad := range []string{"zz", strings.Repeat("AB", 32), strings.Repeat("a", 63)} {
		if _, _, err := as("ci-secret").Get(context.Background(), bad); err == nil {
			t.Errorf("hash %q accepted", bad)
		}
	}
	if _, ok, _ := as("ci-secret").Get(context.Background(), "../../etc/passwd"); ok {
		t.Error("path-like hash served a record")
	}
}

func TestAnIncompleteRecordDoesNotBlockAProvisionalWrite(t *testing.T) {
	_, as := setup(t, false)
	ctx := context.Background()
	_ = as("ci-secret").Put(ctx, rec(false))
	if err := as("dev-secret").Put(ctx, rec(false)); err != nil {
		t.Fatalf("provisional write blocked by a half-finished record: %v", err)
	}
}

func TestARecordWithAFieldTheServerDoesNotKnowIsRefusedNotTruncated(t *testing.T) {
	_, as := setup(t, false)
	_ = as
	fs, _ := wire.NewFileStore(t.TempDir())
	srv := httptest.NewServer(New(fs, []Token{NewToken("ci", RoleAuthoritative, "s")}, false))
	defer srv.Close()
	body := strings.NewReader(`{"schema":2,"hash":"` + hash + `","answers":{},"future_field":"x"}`)
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/v1/records/"+hash, body)
	req.Header.Set("Authorization", "Bearer s")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", resp.StatusCode)
	}
	if _, ok, _ := fs.Get(context.Background(), hash); ok {
		t.Fatal("a record with an unknown field was stored")
	}
}

func TestTheEffortFieldSurvivesAWriteThroughTheServer(t *testing.T) {
	_, as := setup(t, false)
	ctx := context.Background()
	r := rec(true)
	r.Effort = "high"
	if err := as("ci-secret").Put(ctx, r); err != nil {
		t.Fatal(err)
	}
	got, _, _ := as("ci-secret").Get(ctx, hash)
	if got.Effort != "high" {
		t.Fatalf("effort lost in transit: %q", got.Effort)
	}
}

// The URL names where a record lands and the body names what it claims to be.
// A server that trusted either alone would let a write under one hash store
// content that a later replay under another hash would read as its own.
func TestARecordWhoseHashDiffersFromTheURLIsRefusedAndStoredNowhere(t *testing.T) {
	base, as := setup(t, false)
	other := strings.Repeat("cd", 32)
	body := `{"schema":` + fmt.Sprint(wire.Schema) + `,"hash":"` + other + `","answers":{"k":"{}"},"batches":1,"complete":true}`
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut, base+"/v1/records/"+hash, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer ci-secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
	for _, h := range []string{hash, other} {
		if _, ok, err := as("ci-secret").Get(context.Background(), h); err != nil || ok {
			t.Errorf("record under %s: found=%v err=%v, want nothing stored", h[:4], ok, err)
		}
	}
}

// Silence needs proving: an empty Get must mean no record exists, never that
// the caller was not allowed to look.
func TestAnUnknownTokenCannotReadARecordThatExists(t *testing.T) {
	_, as := setup(t, false)
	ctx := context.Background()
	if err := as("ci-secret").Put(ctx, rec(true)); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := as("nope").Get(ctx, hash); ok || !errors.Is(err, wire.ErrForbidden) {
		t.Errorf("unknown token read: found=%v err=%v, want a refusal", ok, err)
	}
	if _, ok, err := as("view-secret").Get(ctx, hash); !ok || err != nil {
		t.Errorf("read token: found=%v err=%v, want the record", ok, err)
	}
}
