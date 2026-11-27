package auth

import (
	"net/http"
	"testing"
	"time"
)

func TestAFloodOfSignInStartsFromOneAddressDoesNotEvictAFlowInProgress(t *testing.T) {
	s := newOIDCSite(t, newFakeIDP(t))
	s.o.flows.max = 40 // small, so an unlimited flood would fill it and evict
	s.begin("/ui/")    // a real sign-in, in progress
	s.o.flows.mu.Lock()
	var real string
	for k := range s.o.flows.items {
		real = k
	}
	s.o.flows.mu.Unlock()
	if real == "" {
		t.Fatal("the sign-in left no flow")
	}
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
	s.o.flows.mu.Lock()
	_, alive := s.o.flows.items[real]
	held := len(s.o.flows.items)
	s.o.flows.mu.Unlock()
	if !alive {
		t.Fatalf("300 starts from one address evicted the sign-in in progress (table holds %d, %d starts were refused)", held, refused)
	}
	if refused == 0 || held > startMax+1 {
		t.Fatalf("held %d flows, refused %d: one address may start at most %d a minute", held, refused, startMax)
	}
}

func TestStartingSignInsIsLimitedPerAddressAndTheWindowExpires(t *testing.T) {
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	f := newFlows(1000)
	f.now = func() time.Time { return now }
	for i := 0; i < startMax; i++ {
		if !f.admit("203.0.113.7") {
			t.Fatalf("start %d of %d was refused", i+1, startMax)
		}
	}
	if f.admit("203.0.113.7") {
		t.Fatal("start over the limit was admitted")
	}
	if !f.admit("203.0.113.8") {
		t.Fatal("another address was refused for the first one's starts")
	}
	now = now.Add(startWindow + time.Second)
	if !f.admit("203.0.113.7") {
		t.Fatal("the address is still refused after the window passed")
	}
}

func TestTheStartLimitTableIsBoundedSoManyAddressesCannotFillMemory(t *testing.T) {
	f := newFlows(1000)
	for i := 0; i < maxStartAddrs+500; i++ {
		f.admit(string(rune(0x4e00+i%20000)) + string(rune(i)))
	}
	f.mu.Lock()
	n := len(f.starts)
	f.mu.Unlock()
	if n > maxStartAddrs {
		t.Fatalf("the table holds %d addresses, over its bound of %d", n, maxStartAddrs)
	}
}
