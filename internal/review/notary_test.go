package review

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"strings"
	"sync/atomic"
	"testing"

	llms "github.com/nocturnium/llm-go-sdk/v6"

	"github.com/nitpick-oss/nitpick/internal/bundle"
	"github.com/nitpick-oss/nitpick/internal/config"
	"github.com/nitpick-oss/nitpick/internal/llm"
	"github.com/nitpick-oss/nitpick/internal/vcs"
	"github.com/nitpick-oss/nitpick/internal/wire"
)

const (
	notaryReviewMarker = "Review the following changes"
	notaryTriageMarker = "triaging findings"
)

func notaryFinding() Finding {
	return Finding{
		Path: "app.go", Line: 4, Severity: "error", Category: "correctness",
		Title: "Ignored error from http.Get", Rationale: "resp may be nil, so the deferred Close panics.",
	}
}

func notaryModel(t *testing.T, findings ...Finding) *scriptedLLM {
	t.Helper()
	return &scriptedLLM{byPrompt: map[string]string{
		notaryTriageMarker: mustJSON(t, TriageResult{Summary: "Adds a retry path.", Verdicts: verdictsFor(findings)}),
		notaryReviewMarker: mustJSON(t, Result{Findings: findings}),
	}}
}

func notaryEngine(t *testing.T, model *scriptedLLM, store wire.Store) (*Engine, *stubProvider) {
	t.Helper()
	provider := &stubProvider{diff: engineDiff}
	e := newEngine(t, model, provider, nil)
	e.Notary = store
	e.NotarySource = "ci"
	return e, provider
}

func fileStore(t *testing.T) *wire.FileStore {
	t.Helper()
	s, err := wire.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// downStore is a notary that cannot be reached.
type downStore struct{}

func (downStore) Get(context.Context, string) (wire.Record, bool, error) {
	return wire.Record{}, false, wire.ErrUnreachable
}
func (downStore) Put(context.Context, wire.Record) error { return wire.ErrUnreachable }
func (downStore) Clear(context.Context, string, string) error {
	return wire.ErrUnreachable
}

// readOnlyStore reads from a real store and refuses every write, as a read
// token does.
type readOnlyStore struct{ wire.Store }

func (readOnlyStore) Put(context.Context, wire.Record) error { return wire.ErrForbidden }
func (readOnlyStore) Clear(context.Context, string, string) error {
	return wire.ErrForbidden
}

// failingOn answers like its model, except that any request containing marker
// fails the way a dropped connection does.
type failingOn struct {
	*scriptedLLM
	marker string
}

func (f failingOn) GenerateContent(ctx context.Context, msgs []llms.Message, opts ...llms.CallOption) (*llms.Response, error) {
	for _, m := range msgs {
		if strings.Contains(m.Content, f.marker) {
			return nil, errors.New("connection reset")
		}
	}
	return f.scriptedLLM.GenerateContent(ctx, msgs, opts...)
}

func TestTheSameInputsReplayTheRecordWithZeroModelCalls(t *testing.T) {
	store := fileStore(t)
	first := notaryModel(t, notaryFinding())
	e, _ := notaryEngine(t, first, store)
	live, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatalf("first review: %v", err)
	}
	if first.callCount() == 0 {
		t.Fatal("the first review asked no model, so the replay below proves nothing")
	}
	if live.Notary.Mode != NotaryLive || !live.Notary.Complete {
		t.Fatalf("first review notary = %+v, want a live review with a complete record", live.Notary)
	}

	second := notaryModel(t, notaryFinding())
	e2, provider := notaryEngine(t, second, store)
	replay, err := e2.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if n := second.callCount(); n != 0 {
		t.Fatalf("the replay made %d model calls, want 0; prompts: %q", n, second.prompts())
	}
	if replay.Notary.Mode != NotaryReplayed || replay.Notary.Replayed == 0 || replay.Notary.Live != 0 {
		t.Fatalf("replay notary = %+v", replay.Notary)
	}
	if len(replay.Findings) != len(live.Findings) || len(replay.Findings) != 1 {
		t.Fatalf("replay findings = %d, live = %d, want 1 each", len(replay.Findings), len(live.Findings))
	}
	if replay.Findings[0].Title != live.Findings[0].Title {
		t.Errorf("replayed title %q, live %q", replay.Findings[0].Title, live.Findings[0].Title)
	}
	if provider.published == nil {
		t.Error("a replay is an official review and publishes")
	}
}

func TestAnEmptyReplayIsDistinguishableFromANotaryThatWasNeverAsked(t *testing.T) {
	store := fileStore(t)
	e, _ := notaryEngine(t, notaryModel(t), store)
	if _, err := e.Review(context.Background(), vcs.Ref{}); err != nil {
		t.Fatal(err)
	}

	again, _ := notaryEngine(t, notaryModel(t), store)
	replay, err := again.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}

	bare, _ := notaryEngine(t, notaryModel(t), nil)
	none, err := bare.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}

	if len(replay.Findings) != 0 || len(none.Findings) != 0 {
		t.Fatalf("both reviews are meant to be clean: %d and %d findings", len(replay.Findings), len(none.Findings))
	}
	if replay.Notary.Mode != NotaryReplayed || replay.Notary.Replayed == 0 {
		t.Errorf("a clean replay must say it replayed answers: %+v", replay.Notary)
	}
	if none.Notary.Mode != NotaryNone || none.Notary.Hash != "" {
		t.Errorf("a review with no notary must say so: %+v", none.Notary)
	}
}

func TestAnInterruptedReviewAsksOnlyForWhatIsMissing(t *testing.T) {
	store := fileStore(t)
	broken := notaryModel(t, notaryFinding())
	// Review answered, triage fell over: the run is interrupted partway.
	e, _ := notaryEngine(t, broken, store)
	e.Roles.Triage = llm.NewClientForTest(failingOn{broken, notaryTriageMarker}, e.Config.Models.Default)
	first, _ := e.Review(context.Background(), vcs.Ref{})
	if first != nil && first.Notary.Complete {
		t.Fatalf("an interrupted review must not leave a complete record: %+v", first.Notary)
	}
	reviewCalls := 0
	for _, p := range broken.prompts() {
		if strings.Contains(p, notaryReviewMarker) {
			reviewCalls++
		}
	}
	if reviewCalls == 0 {
		t.Fatal("the first run never reached the review stage")
	}

	healthy := notaryModel(t, notaryFinding())
	e2, _ := notaryEngine(t, healthy, store)
	resumed, err := e2.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	for _, p := range healthy.prompts() {
		if strings.Contains(p, notaryReviewMarker) {
			t.Fatalf("the resumed run asked for an answer the record already held: %q", p)
		}
	}
	if healthy.callCount() == 0 {
		t.Fatal("the resumed run asked for nothing, so the missing triage answer came from nowhere")
	}
	if resumed.Notary.Mode != NotaryResumed || resumed.Notary.Replayed == 0 || resumed.Notary.Live == 0 {
		t.Errorf("resume notary = %+v, want some replayed and some live", resumed.Notary)
	}
	if !resumed.Notary.Complete {
		t.Errorf("the resumed run finished and must complete the record: %+v", resumed.Notary)
	}
}

func TestFreshClearsTheRecordAndAsksTheModelAgain(t *testing.T) {
	store := fileStore(t)
	e, _ := notaryEngine(t, notaryModel(t, notaryFinding()), store)
	first, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}

	again := notaryModel(t, notaryFinding())
	e2, _ := notaryEngine(t, again, store)
	e2.Fresh = true
	e2.NotarySource = "alice"
	report, err := e2.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if again.callCount() == 0 {
		t.Fatal("-fresh replayed the old record instead of reviewing again")
	}
	if report.Notary.Replayed != 0 || !report.Notary.Fresh {
		t.Errorf("fresh notary = %+v", report.Notary)
	}
	rec, ok, err := store.Get(context.Background(), first.Notary.Hash)
	if err != nil || !ok {
		t.Fatalf("record after fresh: ok=%v err=%v", ok, err)
	}
	if rec.Clears != 1 || len(rec.ClearedBy) != 1 || rec.ClearedBy[0] != "alice" {
		t.Errorf("clear history = %d %v, want one clear by alice", rec.Clears, rec.ClearedBy)
	}
	if !rec.Complete {
		t.Error("the re-review completes the record")
	}
}

func TestAReviewWithoutAUsableNotaryIsUnofficialAndNotPostedWhenRequired(t *testing.T) {
	cases := []struct {
		name  string
		store func(*testing.T) wire.Store
	}{
		{"no notary configured", func(*testing.T) wire.Store { return nil }},
		{"notary unreachable", func(*testing.T) wire.Store { return downStore{} }},
		{"token may not write", func(t *testing.T) wire.Store {
			t.Helper()
			return readOnlyStore{fileStore(t)}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, provider := notaryEngine(t, notaryModel(t, notaryFinding()), tc.store(t))
			e.RequireNotary = true
			report, err := e.Review(context.Background(), vcs.Ref{})
			if err != nil {
				t.Fatal(err)
			}
			if !report.Unofficial {
				t.Errorf("report is not labelled unofficial: %+v", report.Notary)
			}
			if provider.published != nil {
				t.Error("an unofficial review was posted")
			}
			if len(report.Findings) != 1 {
				t.Errorf("the findings are still reported locally: got %d", len(report.Findings))
			}
		})
	}
}

func TestAnUnofficialReviewStillPostsWhenTheNotaryIsNotRequired(t *testing.T) {
	e, provider := notaryEngine(t, notaryModel(t, notaryFinding()), downStore{})
	report, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Unofficial || provider.published == nil {
		t.Errorf("an optional notary must not stop a review: unofficial=%v published=%v", report.Unofficial, provider.published != nil)
	}
	if report.Notary.Mode != NotaryUnreachable {
		t.Errorf("an unreachable notary must be named: %+v", report.Notary)
	}
}

func TestAnOfficialReviewIsPostedWhenTheNotaryIsRequired(t *testing.T) {
	e, provider := notaryEngine(t, notaryModel(t, notaryFinding()), fileStore(t))
	e.RequireNotary = true
	report, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Unofficial || provider.published == nil {
		t.Errorf("a recorded review must post: %+v", report.Notary)
	}
}

func TestAChangedSettingIsADifferentReviewAndIsNotReplayed(t *testing.T) {
	store := fileStore(t)
	e, _ := notaryEngine(t, notaryModel(t, notaryFinding()), store)
	if _, err := e.Review(context.Background(), vcs.Ref{}); err != nil {
		t.Fatal(err)
	}
	other := notaryModel(t, notaryFinding())
	e2, _ := notaryEngine(t, other, store)
	e2.Config.Review.Concurrency += 3
	report, err := e2.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if other.callCount() == 0 || report.Notary.Mode == NotaryReplayed {
		t.Errorf("a changed policy replayed another policy's answers: %+v", report.Notary)
	}
}

func TestAReplayUsesTheRecordedEffortNotTheConfiguredOne(t *testing.T) {
	store := fileStore(t)
	e, _ := notaryEngine(t, notaryModel(t, notaryFinding()), store)
	e.Roles.Review.Spec.Reasoning = config.ReasoningLevel("low")
	first, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}

	later := notaryModel(t, notaryFinding())
	e2, _ := notaryEngine(t, later, store)
	e2.Roles.Review.Spec.Reasoning = config.ReasoningLevel("high")
	report, err := e2.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Notary.Hash != first.Notary.Hash {
		t.Fatalf("effort must not be hashed: %s != %s", report.Notary.Hash, first.Notary.Hash)
	}
	if later.callCount() != 0 || report.Notary.Mode != NotaryReplayed {
		t.Errorf("a different effort today asked the model again: calls=%d %+v", later.callCount(), report.Notary)
	}
	rec, _, _ := store.Get(context.Background(), first.Notary.Hash)
	if rec.Effort != "low" {
		t.Errorf("recorded effort = %q, want low", rec.Effort)
	}
}

func TestACompleteRecordWithNoAnswersIsNotServedAsAReplay(t *testing.T) {
	store := fileStore(t)
	e, _ := notaryEngine(t, notaryModel(t, notaryFinding()), store)
	first, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	rec, _, _ := store.Get(context.Background(), first.Notary.Hash)
	rec.Answers = map[string]string{}
	if err := store.Put(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	model := notaryModel(t, notaryFinding())
	e2, _ := notaryEngine(t, model, store)
	report, err := e2.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if model.callCount() == 0 || report.Notary.Mode == NotaryReplayed {
		t.Errorf("an empty record was replayed as a clean review: calls=%d %+v", model.callCount(), report.Notary)
	}
}

// refusingStore is a notary that rejects the caller's token outright.
type refusingStore struct{}

func (refusingStore) Get(context.Context, string) (wire.Record, bool, error) {
	return wire.Record{}, false, wire.ErrForbidden
}
func (refusingStore) Put(context.Context, wire.Record) error { return wire.ErrForbidden }
func (refusingStore) Clear(context.Context, string, string) error {
	return wire.ErrForbidden
}

func TestARefusedTokenMeansTheNotaryWasNotUsedNotThatNothingWasRecorded(t *testing.T) {
	e, _ := notaryEngine(t, notaryModel(t, notaryFinding()), refusingStore{})
	report, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Notary.Mode != NotaryUnreachable || report.Notary.Err == "" || report.Notary.Official() {
		t.Errorf("a notary that refused the token must read as unusable: %+v", report.Notary)
	}
}

func TestAReadOnlyTokenReplaysAndNeverCountsAsHavingRecorded(t *testing.T) {
	store := fileStore(t)
	e, _ := notaryEngine(t, notaryModel(t, notaryFinding()), store)
	if _, err := e.Review(context.Background(), vcs.Ref{}); err != nil {
		t.Fatal(err)
	}
	model := notaryModel(t, notaryFinding())
	e2, _ := notaryEngine(t, model, readOnlyStore{store})
	report, err := e2.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if model.callCount() != 0 || report.Notary.Mode != NotaryReplayed || report.Notary.Recorded != 0 {
		t.Errorf("read-only replay: calls=%d %+v", model.callCount(), report.Notary)
	}
	if !report.Notary.Official() {
		t.Error("a replay of a record CI wrote is official whoever reads it")
	}
}

// tamperedStore returns whatever record the test chose to a Get, as a notary
// holding a stale or misfiled record would.
type tamperedStore struct {
	wire.Store
	alter func(wire.Record) wire.Record
}

func (s tamperedStore) Get(ctx context.Context, hash string) (wire.Record, bool, error) {
	rec, ok, err := s.Store.Get(ctx, hash)
	if ok {
		rec = s.alter(rec)
	}
	return rec, ok, err
}

func TestARecordFiledUnderTheWrongHashOrSchemaIsNotReplayed(t *testing.T) {
	cases := map[string]func(wire.Record) wire.Record{
		"another review's hash": func(r wire.Record) wire.Record { r.Hash = strings.Repeat("0", 64); return r },
		"an older schema":       func(r wire.Record) wire.Record { r.Schema = wire.Schema - 1; return r },
	}
	for name, alter := range cases {
		t.Run(name, func(t *testing.T) {
			store := fileStore(t)
			e, _ := notaryEngine(t, notaryModel(t, notaryFinding()), store)
			if _, err := e.Review(context.Background(), vcs.Ref{}); err != nil {
				t.Fatal(err)
			}
			model := notaryModel(t, notaryFinding())
			e2, _ := notaryEngine(t, model, tamperedStore{store, alter})
			report, err := e2.Review(context.Background(), vcs.Ref{})
			if err != nil {
				t.Fatal(err)
			}
			if model.callCount() == 0 || report.Notary.Replayed != 0 {
				t.Errorf("a record that is not this review's was replayed: calls=%d %+v", model.callCount(), report.Notary)
			}
		})
	}
}

func TestAPartialRecordMadeAtAnotherReasoningLevelIsNotResumed(t *testing.T) {
	store := fileStore(t)
	broken := notaryModel(t, notaryFinding())
	e, _ := notaryEngine(t, broken, store)
	e.Roles.Review.Spec.Reasoning = config.ReasoningLevel("low")
	e.Roles.Triage = llm.NewClientForTest(failingOn{broken, notaryTriageMarker}, e.Config.Models.Default)
	if _, err := e.Review(context.Background(), vcs.Ref{}); err != nil {
		t.Fatal(err)
	}

	later := notaryModel(t, notaryFinding())
	e2, _ := notaryEngine(t, later, store)
	e2.Roles.Review.Spec.Reasoning = config.ReasoningLevel("high")
	report, err := e2.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Notary.Replayed != 0 || report.Notary.Mode == NotaryResumed {
		t.Errorf("answers made at low effort were mixed with live ones at high: %+v", report.Notary)
	}
}

// spyStore remembers every record it was asked to keep.
type spyStore struct {
	wire.Store
	puts []wire.Record
}

func (s *spyStore) Put(ctx context.Context, r wire.Record) error {
	s.puts = append(s.puts, r)
	return s.Store.Put(ctx, r)
}

func TestALiveAnswerAddedToACompleteRecordIsNotWrittenAsComplete(t *testing.T) {
	store := fileStore(t)
	e, _ := notaryEngine(t, notaryModel(t, notaryFinding()), store)
	first, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	// The record loses its triage answer, so the next run must ask for it.
	rec, _, _ := store.Get(context.Background(), first.Notary.Hash)
	for key, raw := range rec.Answers {
		if strings.Contains(raw, "verdicts") || strings.Contains(raw, "Adds a retry path") {
			delete(rec.Answers, key)
		}
	}
	if err := store.Put(context.Background(), rec); err != nil {
		t.Fatal(err)
	}

	spy := &spyStore{Store: store}
	session := newNotarySession(spy, e.log(), "ci")
	loaded, ok, _ := store.Get(context.Background(), first.Notary.Hash)
	if !ok || !loaded.Complete {
		t.Fatal("setup: the stored record must be complete")
	}
	session.arm(context.Background(), wire.Inputs{}, "", false)
	session.rec = loaded
	session.status.Mode = NotaryReplayed
	session.Record(context.Background(), "a-new-request", &llms.Response{Content: "{}", FinishReason: llms.FinishReasonStop})
	for i, p := range spy.puts {
		if p.Complete {
			t.Errorf("write %d marked a record complete while it held an answer that no review of these inputs had finished with", i)
		}
	}
	if len(spy.puts) == 0 {
		t.Fatal("the live answer was not written at all")
	}
}

func TestTheInputHashDoesNotDependOnTheOrderBatchesWerePacked(t *testing.T) {
	e, _ := notaryEngine(t, notaryModel(t), fileStore(t))
	one := bundle.Batch{Assessment: "first batch"}
	two := bundle.Batch{Assessment: "second batch"}
	a, _, err := e.notaryInputs("ctx", &bundle.Plan{Batches: []bundle.Batch{one, two}})
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := e.notaryInputs("ctx", &bundle.Plan{Batches: []bundle.Batch{two, one}})
	if err != nil {
		t.Fatal(err)
	}
	if a.Hash() != b.Hash() {
		t.Error("the same change packed in another order is a different review")
	}
	c, _, _ := e.notaryInputs("ctx", &bundle.Plan{Batches: []bundle.Batch{one, {Assessment: "third batch"}}})
	if a.Hash() == c.Hash() {
		t.Error("a different batch kept the hash")
	}
}

func TestRequireNotaryWithholdsAReviewWithNoBatchesOnlyWhenThereIsNoNotary(t *testing.T) {
	empty := &stubProvider{diff: "diff --git a/x.png b/x.png\nBinary files a/x.png and b/x.png differ\n"}
	for _, tc := range []struct {
		name      string
		store     wire.Store
		wantAbort bool
	}{
		{"no notary configured", nil, true},
		{"a notary is configured and has nothing to certify", fileStore(t), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEngine(t, notaryModel(t), empty, nil)
			e.Notary, e.RequireNotary = tc.store, true
			report, err := e.Review(context.Background(), vcs.Ref{})
			if err != nil {
				t.Fatal(err)
			}
			if report.Unofficial != tc.wantAbort {
				t.Errorf("unofficial=%v, want %v (batches=%d)", report.Unofficial, tc.wantAbort, len(report.Plan.Batches))
			}
			if len(report.Plan.Batches) != 0 {
				t.Fatalf("setup: the plan must pack nothing, got %d batches", len(report.Plan.Batches))
			}
		})
	}
}

// armedSession returns a session armed on a store, with a client whose every
// request goes through it.
func armedSession(t *testing.T, store wire.Store, model *scriptedLLM) (*notarySession, *llm.Client) {
	t.Helper()
	s := newNotarySession(store, slog.New(slog.DiscardHandler), "ci")
	s.arm(context.Background(), wire.Inputs{Tool: "t", Batches: []string{"b"}}, "", false)
	c := llm.NewClientForTest(model, config.ModelSpec{Provider: "openai", Model: "m"})
	c.UseLedger(s)
	return s, c
}

func TestIdenticalRequestsKeepSeparateAnswersSoVotesStayIndependent(t *testing.T) {
	store := fileStore(t)
	ask := []llms.Message{{Role: llms.RoleUser, Content: "same question"}}
	model := &scriptedLLM{fallback: `{"v":1}`}
	s, c := armedSession(t, store, model)
	for range 3 {
		if _, err := c.LLM.GenerateContent(context.Background(), ask); err != nil {
			t.Fatal(err)
		}
	}
	if model.callCount() != 3 {
		t.Fatalf("a live run asked the model %d times for 3 votes, want 3: later votes were answered from the first", model.callCount())
	}
	if got := len(s.rec.Answers); got != 3 {
		t.Fatalf("the record holds %d answers, want 3", got)
	}

	replay := &scriptedLLM{fallback: `{"v":1}`}
	_, c2 := armedSession(t, store, replay)
	for range 3 {
		if _, err := c2.LLM.GenerateContent(context.Background(), ask); err != nil {
			t.Fatal(err)
		}
	}
	if replay.callCount() != 0 {
		t.Errorf("the replay of 3 recorded votes asked the model %d times", replay.callCount())
	}
	if _, err := c2.LLM.GenerateContent(context.Background(), ask); err != nil || replay.callCount() != 1 {
		t.Errorf("a fourth identical request must reach the model, got %d calls err=%v", replay.callCount(), err)
	}
}

func TestAnAnswerThatCannotBeEncodedLeavesTheRecordIncompleteNotComplete(t *testing.T) {
	store := fileStore(t)
	s, _ := armedSession(t, store, &scriptedLLM{})
	nan := math.NaN()
	s.Record(context.Background(), "k1", &llms.Response{Content: "{}", FinishReason: llms.FinishReasonStop, Usage: llms.Usage{Cost: &nan}})
	s.Record(context.Background(), "k2", &llms.Response{Content: "{}", FinishReason: llms.FinishReasonStop})
	status := s.finish(context.Background(), true)
	if status.Complete {
		t.Fatalf("a record missing an answer was marked complete: %+v", status)
	}
	if status.Err == "" {
		t.Error("the lost answer must be reported")
	}
	if rec, ok, _ := store.Get(context.Background(), status.Hash); ok && rec.Complete {
		t.Error("the stored record attests to answers it does not hold")
	}
}

func TestAReplayIsZeroCallsEvenWhenTheResidualJudgeRanInTheFirstReview(t *testing.T) {
	f := Finding{
		Path: "app.go", Line: 4, Severity: "info", Class: "correctness",
		Title: "document the ignored error", Rationale: "the blank identifier hides a failure mode",
	}
	script := func() *scriptedLLM {
		return &scriptedLLM{byPrompt: map[string]string{
			"Review the following changes":             mustJSON(t, Result{Findings: []Finding{f}}),
			"triaging findings":                        mustJSON(t, TriageResult{Verdicts: verdictsFor([]Finding{f})}),
			"You decide whether a pull request review": `{"approve":true,"reason":"advisory documentation only"}`,
		}}
	}
	tune := func(c *config.Config) {
		c.Review.Approve.Enabled = true
		c.Review.Approve.Residual.Enabled = true
		c.Review.MinSeverity = config.SeverityInfo
	}
	store := fileStore(t)

	first := script()
	e := newEngine(t, first, &stubProvider{diff: engineDiff}, tune)
	e.Notary, e.NotarySource = store, "ci"
	live, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	judged := false
	for _, p := range first.prompts() {
		judged = judged || strings.Contains(p, "You decide whether a pull request review")
	}
	if !judged || !live.ResidualApprove {
		t.Fatalf("setup: the residual judge must run and approve in the first review (judged=%v approve=%v)", judged, live.ResidualApprove)
	}
	if !live.Notary.Complete {
		t.Fatalf("the finished review left its record incomplete: %+v", live.Notary)
	}

	second := script()
	e2 := newEngine(t, second, &stubProvider{diff: engineDiff}, tune)
	e2.Notary, e2.NotarySource = store, "ci"
	replay, err := e2.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if second.callCount() != 0 || replay.Notary.Mode != NotaryReplayed {
		t.Errorf("the replay asked the model %d times: %+v", second.callCount(), replay.Notary)
	}
}

func TestAShortOrEmptyHashIsLoggedWithoutSlicingPastItsEnd(t *testing.T) {
	for in, want := range map[string]string{"": "", "abc": "abc", strings.Repeat("a", 64): strings.Repeat("a", 12)} {
		if got := shortHash(in); got != want {
			t.Errorf("shortHash(%q) = %q, want %q", in, got, want)
		}
	}
}

// flakyStore fails its first write, as one dropped request would.
type flakyStore struct {
	wire.Store
	failed atomic.Bool
}

func (s *flakyStore) Put(ctx context.Context, r wire.Record) error {
	if s.failed.CompareAndSwap(false, true) {
		return errors.New("connection reset")
	}
	return s.Store.Put(ctx, r)
}

func TestOneDroppedWriteDoesNotMakeAReviewUnofficialWhenALaterWriteLands(t *testing.T) {
	inner := fileStore(t)
	flaky := &flakyStore{Store: inner}
	e, provider := notaryEngine(t, notaryModel(t, notaryFinding()), flaky)
	e.RequireNotary = true
	report, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if !flaky.failed.Load() {
		t.Fatal("setup: no write was dropped, so nothing was tested")
	}
	rec, ok, _ := inner.Get(context.Background(), report.Notary.Hash)
	if !ok || !rec.Complete || len(rec.Answers) < 2 {
		t.Fatalf("setup: the notary must end up holding the complete record, got ok=%v %+v", ok, rec.Complete)
	}
	if report.Unofficial || provider.published == nil {
		t.Errorf("a review whose record the notary holds complete was withheld: %+v", report.Notary)
	}
	if !report.Notary.Complete || report.Notary.Err != "" {
		t.Errorf("status disagrees with the store: %+v", report.Notary)
	}
}

func TestADroppedWriteIsNotForgivenWhenTheRecordIsMissingAnAnswer(t *testing.T) {
	store := fileStore(t)
	s, _ := armedSession(t, store, &scriptedLLM{})
	nan := math.NaN()
	s.putFailed = true
	s.Record(context.Background(), "lost", &llms.Response{Content: "{}", FinishReason: llms.FinishReasonStop, Usage: llms.Usage{Cost: &nan}})
	s.Record(context.Background(), "kept", &llms.Response{Content: "{}", FinishReason: llms.FinishReasonStop})
	if s.status.Err == "" {
		t.Error("a later successful write cleared the error for an answer that was never recorded")
	}
}
