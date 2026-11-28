package auth

import (
	"net/http"
	"net/url"
	"testing"
	"time"
)

// stateOf reads the state parameter the provider put in the callback URL, which is
// the key of the flow the sign-in left behind.
func stateOf(t *testing.T, callback string) string {
	t.Helper()
	u, err := url.Parse(callback)
	if err != nil || u.Query().Get("state") == "" {
		t.Fatalf("no state in %q (%v)", callback, err)
	}
	return u.Query().Get("state")
}

func TestAFloodOfSignInStartsFromOneAddressDoesNotEvictAFlowInProgress(t *testing.T) {
	s := newOIDCSite(t, newFakeIDP(t))
	s.o.flows.max = 40                  // small, so an unlimited flood would fill it and evict
	real := stateOf(t, s.begin("/ui/")) // a real sign-in, in progress
	refused := 0
	for i := 0; i < 300; i++ {
		resp, err := s.c.Get(s.ts.URL + "/auth/oidc/login")
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			refused++
			if resp.Header.Get("Retry-After") == "" {
				t.Fatal("a refusal must say when to try again")
			}
		}
	}
	held := s.o.flows.len(bg)
	if _, alive := s.o.flows.take(bg, real); !alive {
		t.Fatalf("300 starts from one address evicted the sign-in in progress (table holds %d, %d starts were refused)", held, refused)
	}
	if refused == 0 || held > startMax+1 {
		t.Fatalf("held %d flows, refused %d: one address may start at most %d a minute", held, refused, startMax)
	}
}

func TestStartingSignInsIsLimitedPerAddressAndTheWindowExpires(t *testing.T) {
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	f := testFlows(t, func() time.Time { return now }, 1000)
	for i := 0; i < startMax; i++ {
		if !f.admit(bg, "203.0.113.7") {
			t.Fatalf("start %d of %d was refused", i+1, startMax)
		}
	}
	if f.admit(bg, "203.0.113.7") {
		t.Fatal("start over the limit was admitted")
	}
	if !f.admit(bg, "203.0.113.8") {
		t.Fatal("another address was refused for the first one's starts")
	}
	now = now.Add(startWindow + time.Second)
	if !f.admit(bg, "203.0.113.7") {
		t.Fatal("the address is still refused after the window passed")
	}
}

func TestTheStartLimitTableIsBoundedSoManyAddressesCannotFillTheDatabase(t *testing.T) {
	old := maxStartAddrs
	maxStartAddrs = 40
	t.Cleanup(func() { maxStartAddrs = old })
	f := testFlows(t, nil, 1000)
	admitted := 0
	for i := 0; i < 120; i++ {
		if f.admit(bg, "198.51.100."+string(rune('a'+i%26))+string(rune('a'+i/26))) {
			admitted++
		}
	}
	n, err := f.st.Count(bg, "start:")
	if err != nil || n > maxStartAddrs {
		t.Fatalf("the table holds %d addresses (%v), over its bound of %d", n, err, maxStartAddrs)
	}
	if admitted != maxStartAddrs {
		t.Errorf("%d of 120 new addresses were admitted, want %d: a flood from many addresses is refused once the table is full", admitted, maxStartAddrs)
	}
	// An address already in the table is not turned away by the table being full.
	if !f.admit(bg, "198.51.100.aa") {
		t.Error("an address that was already tracked was refused because the table is full")
	}
}

func TestAFlowTakenOnOneServerIsGoneOnAnother(t *testing.T) {
	db := testStore(t, nil)
	a, b := newFlows(100), newFlows(100)
	a.use(db, nil)
	b.use(db, nil)
	key, err := a.put(bg, &flow{secret: "nonce", verifier: "v", next: "/ui/", binding: [32]byte{1, 2, 3}})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := b.take(bg, key)
	if !ok || got.secret != "nonce" || got.verifier != "v" || got.next != "/ui/" || got.binding != ([32]byte{1, 2, 3}) {
		t.Fatalf("the second server took %+v, %v", got, ok)
	}
	if _, ok := a.take(bg, key); ok {
		t.Error("a sign-in answer was good twice, once on each server")
	}
	if n, _ := db.Count(bg, "flow:"); n != 0 {
		t.Errorf("a taken flow left %d rows", n)
	}
}

func TestADatabaseDumpHoldsNoFlowKeyThatCompletesASignIn(t *testing.T) {
	f := testFlows(t, nil, 100)
	key, err := f.put(bg, &flow{secret: "nonce-value"})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := f.st.Count(bg, "flow:")
	if err != nil || rows != 1 {
		t.Fatalf("rows = %d, %v", rows, err)
	}
	if _, ok, _ := f.st.Get(bg, "flow:"+key); ok {
		t.Error("the flow is stored under the key the browser carries, so a dump would complete a sign-in")
	}
}
