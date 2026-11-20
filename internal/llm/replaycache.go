package llm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gofrs/flock"
	"github.com/jdziat/open-nitpick/internal/config"
	llms "github.com/nocturnium/llm-go-sdk/v6"
)

// EnvResponseCache selects an operator-owned directory for completed responses.
const EnvResponseCache = "NITPICK_LLM_CACHE_DIR"

// EnvReplayOnly refuses cache misses before any provider dispatch.
const EnvReplayOnly = "NITPICK_LLM_REPLAY_ONLY"

// EnvRetryRequest authorizes one retry of request-hash:unique-approval-token.
const EnvRetryRequest = "NITPICK_LLM_RETRY_REQUEST"

// ErrRecordedAttempt means an earlier purchase did not yield a reusable answer.
var ErrRecordedAttempt = errors.New("prior provider attempt needs reconciliation")

// A build change alone does not invalidate an unchanged request.
const replayVersion = 1

type replayLLM struct {
	inner           llms.LLM
	billingProvider string
	dir             string
	hits            atomic.Int64
	misses          atomic.Int64
}

type replayEntry struct {
	Version  int
	Key      string
	Digest   string
	Response json.RawMessage
}

func (r *replayLLM) purchaseProvider() llms.Provider {
	if r.billingProvider != "" {
		return llms.Provider(r.billingProvider)
	}
	return r.Provider()
}

func newReplayLLM(inner llms.LLM, dir string) *replayLLM {
	return &replayLLM{inner: inner, dir: dir}
}

// replayNamespace separates endpoints and accounts without storing credentials.
func replayNamespace(spec config.ModelSpec, cfg llms.Config) (string, error) {
	identity := struct {
		Endpoint string
		Extra    map[string]string
	}{cfg.BaseURL, cfg.Extra}
	credential := sha256.Sum256([]byte(cfg.APIKey))
	raw, err := json.Marshal([]any{spec.Provider, hex.EncodeToString(credential[:]), identity})
	if err != nil {
		return "", fmt.Errorf("encode cache identity: %w", err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func (r *replayLLM) Provider() llms.Provider { return r.inner.Provider() }
func (r *replayLLM) Model() string           { return r.inner.Model() }

func (r *replayLLM) Stream(ctx context.Context, msgs []llms.Message, opts ...llms.CallOption) (<-chan llms.StreamChunk, error) {
	if os.Getenv(EnvReplayOnly) != "" {
		return nil, errors.New("offline replay does not support streaming; provider dispatch refused")
	}
	if os.Getenv(EnvPurchaseBudget) != "" {
		return nil, errors.New("streaming is not supported by the purchase budget")
	}
	return r.inner.Stream(ctx, msgs, opts...)
}

func (r *replayLLM) GenerateContent(ctx context.Context, msgs []llms.Message, opts ...llms.CallOption) (*llms.Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// The key is computed from the options after the price bound is applied, on
	// purpose: a response bought under one max_price must not be replayed as the
	// answer to a request under another. The cost is that editing a ledger's
	// tariffs changes every request's identity, so a ledger is frozen for as long
	// as its cache is in use. TestPurchaseRoutingCapParticipatesInRequestIdentity
	// pins this.
	var err error
	opts, err = purchaseOptions(r.purchaseProvider(), r.Model(), opts)
	if err != nil {
		return nil, err
	}
	key, ok := r.keyFor(ctx, msgs, opts)
	if !ok {
		return nil, errors.New("response cache requires serializable options and a named sample for positive temperature")
	}
	if resp := r.read(key); resp != nil {
		r.hits.Add(1)
		return resp, nil
	}
	path := r.path(key)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("prepare response cache: %w", err)
	}
	lock := flock.New(path + ".lock")
	held, err := lock.TryLockContext(ctx, 10*time.Millisecond)
	if err != nil {
		return nil, fmt.Errorf("lock response cache: %w", err)
	}
	if !held {
		return nil, errors.New("response cache lock was not acquired")
	}
	defer func() { _ = lock.Close() }()

	// A different process may finish this purchase while we wait. The OS
	// releases its lock even when that process crashes.
	if resp := r.read(key); resp != nil {
		r.hits.Add(1)
		return resp, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	pending := path + ".pending"
	approvedRetry := false
	if os.Getenv(EnvReplayOnly) != "" {
		return nil, errors.New("offline replay cache miss; provider dispatch refused")
	}
	if _, err := os.Stat(pending); err == nil {
		approval := strings.TrimSpace(os.Getenv(EnvRetryRequest))
		if !strings.HasPrefix(approval, key+":") || len(approval) <= len(key)+1 {
			return nil, fmt.Errorf("%w: request %s (%s)", ErrRecordedAttempt, key, pending)
		}
		token := sha256.Sum256([]byte(approval))
		consumed, err := os.OpenFile(path+".retry-"+hex.EncodeToString(token[:]), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, fmt.Errorf("%w: retry authorization already used for %s", ErrRecordedAttempt, key)
		}
		if err := consumed.Close(); err != nil {
			return nil, err
		}
		approvedRetry = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	reservation, err := reservePurchase(ctx, r.purchaseProvider(), r.Model(),
		filepath.Base(r.dir)+"/"+key, approvedRetry, msgs, opts)
	if err != nil {
		return nil, err
	}
	intent, err := os.OpenFile(pending, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("record provider intent: %w", err)
	}
	_, writeErr := intent.WriteString(time.Now().UTC().Format(time.RFC3339Nano))
	syncErr := intent.Sync()
	closeErr := intent.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return nil, fmt.Errorf("persist provider intent: %w", err)
	}
	r.misses.Add(1)
	resp, err := r.inner.GenerateContent(ctx, msgs, opts...)
	if err != nil || !cacheable(resp) {
		haltCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		// A charge can be settled even when the provider failed. Its receipt
		// must never become a reusable answer merely because it looks complete.
		raw, saveErr := json.Marshal(resp)
		if saveErr == nil {
			saveErr = os.WriteFile(path+".attempt", raw, 0o600)
		}
		if saveErr != nil {
			return resp, errors.Join(ErrRecordedAttempt, err, fmt.Errorf("save failed attempt: %w", saveErr),
				reservation.halt(haltCtx, "paid response persistence failed"))
		}
		if resp != nil && resp.Usage.Cost != nil {
			return resp, errors.Join(ErrRecordedAttempt, err, reservation.settle(haltCtx, resp))
		}
		return resp, errors.Join(ErrRecordedAttempt, err, reservation.halt(haltCtx, "provider outcome unresolved"))
	}
	if err := r.write(key, resp); err != nil {
		haltCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		return resp, errors.Join(fmt.Errorf("persist paid response: %w", err), reservation.halt(haltCtx, "paid response persistence failed"))
	}
	settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := reservation.settle(settleCtx, resp); err != nil {
		return resp, fmt.Errorf("settle paid response: %w", err)
	}
	// The pending marker stays on purpose. It is the durable record that this
	// request was paid for, so a later corrupt or lost entry is refused
	// instead of bought again.
	return resp, nil
}

func cacheable(resp *llms.Response) bool {
	if resp == nil {
		return false
	}
	switch resp.FinishReason {
	case llms.FinishReasonStop, llms.FinishReasonToolCalls:
		return strings.TrimSpace(resp.Content) != "" || len(resp.ToolCalls) > 0
	default:
		return false
	}
}

type responseSampleKey struct{}

// WithResponseSample keeps independently planned samples separate while a rerun
// of the same sample can reuse its exact requests across executable versions.
func WithResponseSample(ctx context.Context, sample string) context.Context {
	return context.WithValue(ctx, responseSampleKey{}, sample)
}

func (r *replayLLM) key(msgs []llms.Message, opts []llms.CallOption) (string, bool) {
	return r.keyFor(context.Background(), msgs, opts)
}

func (r *replayLLM) keyFor(ctx context.Context, msgs []llms.Message, opts []llms.CallOption) (string, bool) {
	applied := *llms.ApplyOptions(opts...)
	sample, _ := ctx.Value(responseSampleKey{}).(string)
	if applied.Temperature != nil && *applied.Temperature > 0 && sample == "" {
		return "", false
	}
	applied.Trace = nil
	identity := []any{replayVersion, string(r.Provider()), r.Model(), msgs, applied}
	if sample != "" {
		identity = append(identity, sample)
	}
	data, err := json.Marshal(identity)
	if err != nil {
		return "", false
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), true
}

func (r *replayLLM) path(key string) string { return filepath.Join(r.dir, key[:2], key+".json") }

func (r *replayLLM) read(key string) *llms.Response {
	data, err := os.ReadFile(r.path(key))
	if err != nil {
		return nil
	}
	var entry replayEntry
	if json.Unmarshal(data, &entry) != nil || entry.Version != replayVersion || entry.Key != key {
		return nil
	}
	sum := sha256.Sum256(entry.Response)
	if hex.EncodeToString(sum[:]) != entry.Digest {
		return nil
	}
	var resp llms.Response
	if json.Unmarshal(entry.Response, &resp) != nil || !cacheable(&resp) {
		return nil
	}
	return &resp
}

func (r *replayLLM) write(key string, resp *llms.Response) error {
	raw, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(raw)
	data, err := json.Marshal(replayEntry{Version: replayVersion, Key: key, Digest: hex.EncodeToString(sum[:]), Response: raw})
	if err != nil {
		return err
	}
	path := r.path(key)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "tmp-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	_, writeErr := tmp.Write(data)
	syncErr := tmp.Sync()
	closeErr := tmp.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// ResponseCacheStats reports reuse separately from billable provider attempts.
func (c *Client) ResponseCacheStats() (hits, misses int64) {
	if c.replay == nil {
		return 0, 0
	}
	return c.replay.hits.Load(), c.replay.misses.Load()
}

// ResponseCacheDirectory identifies the namespace used by this endpoint and account.
func (c *Client) ResponseCacheDirectory() string {
	if c.replay == nil {
		return ""
	}
	return c.replay.dir
}
