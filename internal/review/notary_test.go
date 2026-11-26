package review

import (
	"context"
	"errors"
	"strings"
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
