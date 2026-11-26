package review

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"sync"
	"time"

	llms "github.com/nocturnium/llm-go-sdk/v6"
	"gopkg.in/yaml.v3"

	"github.com/nitpick-oss/nitpick/internal/bundle"
	"github.com/nitpick-oss/nitpick/internal/config"
	"github.com/nitpick-oss/nitpick/internal/wire"
)

// NotaryMode says what the notary did for one review. The empty value is
// NotaryNone, so a report nobody filled in reads as "no notary", never as a
// replay.
type NotaryMode string

// The ways a review relates to its notary.
const (
	// NotaryNone is the zero value on purpose: a report that never reached the
	// notary must not read as a replay.
	NotaryNone NotaryMode = ""
	// NotaryLive: a notary was reachable and held no answers for these inputs.
	NotaryLive NotaryMode = "live"
	// NotaryResumed: a notary held some answers; the rest were asked live.
	NotaryResumed NotaryMode = "resumed"
	// NotaryReplayed: a complete record answered every model request.
	NotaryReplayed NotaryMode = "replayed"
	// NotaryUnreachable: a notary was configured and could not be used.
	NotaryUnreachable NotaryMode = "unreachable"
)

// NotaryStatus is what the notary did, kept on the report so that an empty
// result can be told apart from a review that never ran: a replay of a clean
// review and a review that asked nothing both have zero findings, and only
// this says which one it was.
type NotaryStatus struct {
	Mode NotaryMode
	// Hash is the pinned input hash the record is kept under.
	Hash string
	// Replayed counts requests answered from the record, Live those that
	// reached a model, and Recorded those the notary accepted.
	Replayed, Live, Recorded int
	// Complete is true when the record was complete at the end of this run.
	Complete bool
	// Fresh is true when -fresh cleared a record first.
	Fresh bool
	// Err is why the notary could not be used or written, when it could not.
	Err string
}

// Official reports whether this review may speak for the change. A review that
// did not go through a notary that kept its record is a local run, however
// complete it is.
func (n NotaryStatus) Official() bool {
	switch n.Mode {
	case NotaryReplayed:
		return true
	case NotaryLive, NotaryResumed:
		// The record must hold this review: nothing kept means a notary that
		// was there and recorded none of it.
		return n.Err == "" && n.Recorded+n.Replayed > 0
	default:
		return false
	}
}

// notarySession is the Ledger for one review. Its methods do nothing until
// arm has run, because the hash that names the record is not known until the
// batches are packed.
type notarySession struct {
	store wire.Store
	log   *slog.Logger
	by    string

	mu     sync.Mutex
	armed  bool
	rec    wire.Record
	status NotaryStatus
	// writable turns false after the store refuses a write, so a read-only
	// token costs one refused request and not one per answer.
	writable bool
	// used holds the answer slots this run has already taken, so a second
	// identical request is not served the first one's answer.
	used map[string]bool
	// gap is set when an answer could not be kept.
	gap bool
}

func newNotarySession(store wire.Store, log *slog.Logger, by string) *notarySession {
	return &notarySession{store: store, log: log, by: by, writable: true, used: map[string]bool{}}
}

// arm names the record, clears it first when asked, and loads what it holds.
func (n *notarySession) arm(ctx context.Context, in wire.Inputs, effort string, fresh bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	hash := in.Hash()
	n.status = NotaryStatus{Mode: NotaryLive, Hash: hash, Fresh: fresh}
	n.rec = wire.Record{
		Schema: wire.Schema, Hash: hash, Tool: in.Tool, Provider: in.Provider, Model: in.Model,
		Created: time.Now().UTC(), Answers: map[string]string{}, Source: n.by, Effort: effort,
	}
	n.armed = true

	if fresh {
		if err := n.store.Clear(ctx, hash, n.by); err != nil {
			n.fail(fmt.Errorf("clear the record: %w", err))
			return
		}
	}
	rec, ok, err := n.store.Get(ctx, hash)
	if err != nil {
		n.fail(fmt.Errorf("read the record: %w", err))
		return
	}
	if !ok || rec.Hash != hash || rec.Schema != wire.Schema {
		return
	}
	// A record the notary says is complete but holds no answers is a record of
	// nothing. Serving it as a replay would turn a failed write into a clean
	// review.
	if len(rec.Answers) == 0 {
		return
	}
	// A partial record made at another reasoning level is not resumed: its
	// answers would sit beside live ones from a different level, and the
	// record would describe neither. A complete one replays as recorded.
	if !rec.Complete && rec.Effort != effort {
		return
	}
	n.rec.Answers = rec.Answers
	n.rec.Clears = rec.Clears
	n.rec.ClearedBy = rec.ClearedBy
	// The effort that made the answers outranks what the configuration says
	// today. See wire.Record.Effort.
	if rec.Effort != "" {
		n.rec.Effort = rec.Effort
	}
	if rec.Complete {
		n.status.Mode = NotaryReplayed
		n.rec.Complete, n.rec.Batches, n.rec.Created = true, rec.Batches, rec.Created
		n.status.Complete = true
		return
	}
	n.status.Mode = NotaryResumed
}

// fail marks the notary unusable for this run, whatever the reason: a refused
// token cannot read the record any more than an absent server can. The review
// carries on without it, and says so. A refused write is not this: a read token
// replays, and put handles it.
func (n *notarySession) fail(err error) {
	n.status.Err = err.Error()
	n.status.Mode = NotaryUnreachable
	n.writable = false
	n.log.Warn("notary unavailable; this review is unofficial", "error", err)
}

// slot names the n-th answer to one request. Identical requests are separate
// answers: review.votes asks the same question several times to compare the
// replies, and one shared key would hand every vote the first vote's answer.
func slot(key string, n int) string { return key + "#" + strconv.Itoa(n) }

// Lookup implements llm.Ledger. It serves the lowest recorded answer to this
// request that this run has not used yet. Requests that are identical are
// interchangeable, so which of two concurrent callers gets which does not
// matter.
func (n *notarySession) Lookup(_ context.Context, key string) (*llms.Response, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if !n.armed || n.status.Mode == NotaryUnreachable {
		return nil, false
	}
	for i := 0; ; i++ {
		name := slot(key, i)
		raw, ok := n.rec.Answers[name]
		if !ok {
			return nil, false
		}
		if n.used[name] {
			continue
		}
		n.used[name] = true
		var resp llms.Response
		if json.Unmarshal([]byte(raw), &resp) != nil {
			// An answer that does not decode costs a request, never a wrong
			// answer.
			return nil, false
		}
		n.status.Replayed++
		return &resp, true
	}
}

// Record implements llm.Ledger.
func (n *notarySession) Record(ctx context.Context, key string, resp *llms.Response) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if !n.armed {
		return
	}
	n.status.Live++
	if n.status.Mode == NotaryUnreachable {
		return
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		// The answer is missing from the record, so the record must never be
		// called complete: that would attest to answers it does not hold.
		n.gap = true
		n.status.Err = "an answer could not be encoded: " + err.Error()
		n.log.Warn("notary could not encode an answer; the record stays incomplete", "error", err)
		return
	}
	i := 0
	for ; ; i++ {
		if _, taken := n.rec.Answers[slot(key, i)]; !taken {
			break
		}
	}
	name := slot(key, i)
	n.rec.Answers[name] = string(raw)
	n.used[name] = true
	// A replayed record that took a live call is a different review from the
	// one that was recorded, so it can no longer be called complete.
	n.rec.Complete = false
	n.status.Complete = false
	if n.status.Mode == NotaryReplayed {
		n.status.Mode = NotaryResumed
	}
	n.put(ctx)
}

// put writes the record as it stands. Callers hold the lock.
func (n *notarySession) put(ctx context.Context) {
	if !n.writable {
		return
	}
	n.rec.Batches = len(n.rec.Answers)
	if err := n.store.Put(ctx, n.rec); err != nil {
		n.status.Err = err.Error()
		if errors.Is(err, wire.ErrForbidden) {
			n.writable = false
			return
		}
		n.log.Warn("notary did not keep an answer", "error", err)
		return
	}
	n.status.Recorded++
}

// finish marks the record complete when the review it belongs to was. A review
// that lost a batch leaves the record partial, which is what lets the next run
// resume instead of replaying a hole.
func (n *notarySession) finish(ctx context.Context, complete bool) NotaryStatus {
	n.mu.Lock()
	defer n.mu.Unlock()
	if !n.armed {
		return n.status
	}
	if complete && !n.gap && n.status.Mode != NotaryUnreachable && len(n.rec.Answers) > 0 && !n.rec.Complete {
		n.rec.Complete = true
		n.put(ctx)
		n.status.Complete = n.status.Err == ""
	}
	return n.status
}

// snapshot returns the status without changing it.
func (n *notarySession) snapshot() NotaryStatus {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.status
}

// notaryInputs is everything that decides what this review says, in the shape
// the hash wants. The batches are the packed diff text, which is fixed by the
// change and the policy, not the model prompts, which carry retrieved context
// that may differ between machines without changing the review.
func (e *Engine) notaryInputs(prContext string, plan *bundle.Plan) (wire.Inputs, string, error) {
	cfg, err := yaml.Marshal(e.Config)
	if err != nil {
		return wire.Inputs{}, "", fmt.Errorf("encode the policy: %w", err)
	}
	policy := sha256.Sum256(cfg)
	system, err := e.reviewPromptFor(e.Roles.Review)
	if err != nil {
		return wire.Inputs{}, "", err
	}
	spec := e.Roles.Review.Spec
	effort := string(spec.Reasoning)
	settings := []string{
		"policy=" + hex.EncodeToString(policy[:]),
		fmt.Sprintf("deep=%t", e.Deep),
		fmt.Sprintf("fast=%t:%d", e.FastReview, e.FastLimit),
		fmt.Sprintf("skip-triage=%t", e.SkipTriage),
		"instruction=" + e.Instruction,
	}
	var batches []string
	for _, b := range plan.Batches {
		batches = append(batches, prContext+"\x00"+bundle.RenderBatch(b))
	}
	sort.Strings(batches)
	return wire.Inputs{
		Tool: config.Version, Provider: spec.Provider, Model: spec.Model,
		System: system, Settings: settings, Batches: batches,
	}, effort, nil
}

// beginNotary arms the notary for this review. It returns the session so the
// caller can finish it, or nil when no notary is configured.
func (e *Engine) beginNotary(ctx context.Context, prContext string, plan *bundle.Plan) *notarySession {
	if e.Notary == nil {
		return nil
	}
	session := newNotarySession(e.Notary, e.log(), e.NotarySource)
	inputs, effort, err := e.notaryInputs(prContext, plan)
	if err != nil {
		session.armed = true
		session.fail(err)
		return session
	}
	session.arm(ctx, inputs, effort, e.Fresh)
	e.Roles.UseLedger(session)
	s := session.snapshot()
	e.log().Info("notary", "mode", string(s.Mode), "hash", s.Hash[:12], "answers", len(session.rec.Answers))
	return session
}
