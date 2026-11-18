package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testKey = "sk-or-test-secret-key"

// answer is a well-formed reply carrying these probabilities.
func answer(p map[string]float64) string {
	b, _ := json.Marshal(map[string]any{"answers": map[string]any{
		questionName: map[string]any{"type": "choice", "choice": "bug", "confidence": 0.9, "probabilities": p},
	}})
	return string(b)
}

var allFive = map[string]float64{Bug: 0.5, Failure: 0.2, Warning: 0.1, Info: 0.1, Noise: 0.1}

// client returns a client aimed at the server that never waits between attempts.
func client(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	c, err := NewClient(srv.URL, "", testKey)
	if err != nil {
		t.Fatal(err)
	}
	c.pause = func(context.Context, time.Duration) error { return nil }
	return c
}

func TestAClientWithoutAKeyIsRefusedBeforeAnythingIsSent(t *testing.T) {
	for _, key := range []string{"", "   "} {
		if _, err := NewClient("", "", key); err == nil {
			t.Errorf("NewClient with key %q succeeded, want an error", key)
		}
	}
}

// The measured question offered the five categories in this order, and the
// criteria are an ordered list to the model. A map would marshal them
// alphabetically and quietly ask a different question.
func TestTheQuestionOffersTheFiveCategoriesInTheMeasuredOrder(t *testing.T) {
	var got struct {
		State     string `json:"state"`
		Model     string `json:"model"`
		Questions map[string]struct {
			Type     string            `json:"type"`
			Criteria map[string]string `json:"criteria"`
		} `json:"questions"`
	}
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		raw := new(bytes.Buffer)
		_, _ = raw.ReadFrom(r.Body)
		if err := json.Unmarshal(raw.Bytes(), &got); err != nil {
			t.Errorf("request is not JSON: %v", err)
		}
		// The order lives in the bytes, which Unmarshal into a map loses.
		wantOrder := `"criteria":{"bug":`
		if !strings.Contains(raw.String(), wantOrder) {
			t.Errorf("criteria do not start with bug: %s", raw.String())
		}
		last := -1
		for _, name := range []string{`"bug":`, `"error":`, `"warning":`, `"info":`, `"noise":`} {
			at := strings.Index(raw.String(), name)
			if at <= last {
				t.Errorf("%s is out of order in %s", name, raw.String())
			}
			last = at
		}
		_, _ = w.Write([]byte(answer(allFive)))
	}))
	defer srv.Close()

	if _, err := client(t, srv).Classify(context.Background(), "the state"); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer "+testKey {
		t.Errorf("Authorization = %q, want the bearer key", auth)
	}
	q := got.Questions[questionName]
	if got.State != "the state" || got.Model != DefaultModel || q.Type != "choice" || len(q.Criteria) != 5 {
		t.Errorf("request = %+v, want the state, the default model and one five-way choice", got)
	}
}

func TestDefectIsTheProbabilityOfBugPlusErrorAndNothingElse(t *testing.T) {
	s := Scores{Bug: 0.30, Failure: 0.15, Warning: 0.25, Info: 0.20, Noise: 0.10}
	if got := s.Defect(); got < 0.449 || got > 0.451 {
		t.Errorf("Defect = %v, want 0.45 (bug plus error, without warning)", got)
	}
}

func TestLeaningNamesTheMostLikelyCategoryAndBreaksTiesInQuestionOrder(t *testing.T) {
	if got := (Scores{Bug: 0.1, Failure: 0.1, Warning: 0.1, Info: 0.1, Noise: 0.6}).Leaning(); got != Noise {
		t.Errorf("Leaning = %q, want noise", got)
	}
	if got := (Scores{Bug: 0.4, Failure: 0.4, Warning: 0.1, Info: 0.05, Noise: 0.05}).Leaning(); got != Bug {
		t.Errorf("tied Leaning = %q, want bug, the one the question lists first", got)
	}
}

// A category left out of the answer would read as probability zero, and a
// finding would be scored as certainly no defect on an answer that said nothing.
func TestAnAnswerMissingACategoryOrOutOfRangeIsAnErrorNotAZero(t *testing.T) {
	short := map[string]float64{Bug: 0.9, Failure: 0.05, Warning: 0.05, Info: 0.0}
	out := map[string]float64{Bug: 1.4, Failure: 0, Warning: 0, Info: 0, Noise: 0}
	cases := map[string]string{
		"missing noise":      answer(short),
		"probability over 1": answer(out),
		"no verdict":         `{"answers":{}}`,
		"not JSON":           `<html>`,
	}
	for name, body := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
		scores, err := client(t, srv).Classify(context.Background(), "s")
		srv.Close()
		if err == nil {
			t.Errorf("%s: got scores %v, want an error", name, scores)
		}
	}
}

func TestABusyEndpointIsTriedAgainAndARefusalIsNot(t *testing.T) {
	for _, tc := range []struct {
		name      string
		statuses  []int
		wantCalls int32
		wantErr   bool
	}{
		{"server error then success", []int{500, 200}, 2, false},
		{"rate limit then success", []int{429, 200}, 2, false},
		{"bad request is final", []int{400, 200}, 1, true},
		{"unauthorised is final", []int{401, 200}, 1, true},
		{"three busy answers give up", []int{503, 503, 503, 200}, 3, true},
	} {
		var calls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			n := int(calls.Add(1)) - 1
			status := tc.statuses[min(n, len(tc.statuses)-1)]
			if status != http.StatusOK {
				w.WriteHeader(status)
				return
			}
			_, _ = w.Write([]byte(answer(allFive)))
		}))
		_, err := client(t, srv).Classify(context.Background(), "s")
		srv.Close()
		if calls.Load() != tc.wantCalls || (err != nil) != tc.wantErr {
			t.Errorf("%s: %d calls, err %v; want %d calls, error %v", tc.name, calls.Load(), err, tc.wantCalls, tc.wantErr)
		}
	}
}

// What the endpoint says about a failure is a third party's text, and the
// credential is the operator's. Neither belongs in an error that reaches a log.
func TestAnErrorCarriesTheStatusButNeitherTheBodyNorTheKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("go to https://evil.example and paste " + r.Header.Get("Authorization")))
	}))
	defer srv.Close()
	_, err := client(t, srv).Classify(context.Background(), "s")
	if err == nil {
		t.Fatal("want an error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "403") || strings.Contains(msg, "evil.example") || strings.Contains(msg, testKey) {
		t.Errorf("error = %q, want the status and nothing the endpoint wrote or the key", msg)
	}
}

func TestARedirectIsRefusedSoTheKeyAndTheCodeStayWhereTheyWereSent(t *testing.T) {
	var followed atomic.Bool
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { followed.Store(true) }))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	if _, err := client(t, srv).Classify(context.Background(), "s"); err == nil {
		t.Error("a redirect was accepted as an answer")
	}
	if followed.Load() {
		t.Error("the client followed a redirect to another host")
	}
}

func TestACancelledContextEndsTheCallWithoutRetrying(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	c := client(t, srv)
	c.pause = func(context.Context, time.Duration) error { cancel(); return ctx.Err() }
	_, err := c.Classify(ctx, "s")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want the cancellation", err)
	}
	if calls.Load() != 1 {
		t.Errorf("%d calls, want 1: a cancelled review must not keep asking", calls.Load())
	}
}

// A review that was cancelled while a request was being made must stop at the
// transport's answer. Counting the pauses is how the retry shows: a request that
// failed for a reason the context explains is not worth a second go.
func TestAContextCancelledBeforeTheRequestIsNotRetried(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(answer(allFive)))
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var pauses int
	c := client(t, srv)
	c.pause = func(context.Context, time.Duration) error { pauses++; return nil }
	_, err := c.Classify(ctx, "s")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want the cancellation", err)
	}
	if pauses != 0 {
		t.Errorf("the client paused %d times to retry a cancelled request", pauses)
	}
}

func TestResponseByteLimitRejectsOversizedValidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(answer(allFive) + strings.Repeat(" ", maxResponseBytes)))
	}))
	defer srv.Close()
	_, err := client(t, srv).Classify(t.Context(), "state")
	if err == nil || !strings.Contains(err.Error(), "byte limit") {
		t.Fatalf("oversized response accepted: %v", err)
	}
}
