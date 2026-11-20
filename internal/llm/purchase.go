package llm

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
	llms "github.com/nocturnium/llm-go-sdk/v6"
)

// EnvPurchaseBudget names a predeclared ledger shared by all experiment purchases.
const EnvPurchaseBudget = "NITPICK_LLM_PURCHASE_BUDGET"

// PurchaseTariff bounds routed input/output prices in millionths of a USD per million tokens.
type PurchaseTariff struct {
	Input  int64 `json:"input_micro_usd_per_million"`
	Output int64 `json:"output_micro_usd_per_million"`
}

// PurchaseEntry retains a reservation until an authoritative charge is known.
type PurchaseEntry struct {
	Request  string `json:"request"`
	Reserved int64  `json:"reserved_micro_usd"`
	Settled  *int64 `json:"settled_micro_usd,omitempty"`
}

// PurchaseBudget is operator-owned; missing or corrupt ledgers never authorize spend.
type PurchaseBudget struct {
	Version int                       `json:"version"`
	Limit   int64                     `json:"limit_micro_usd"`
	Tariffs map[string]PurchaseTariff `json:"tariffs"`
	Entries map[string]PurchaseEntry  `json:"entries"`
	Halted  string                    `json:"halted,omitempty"`
}

type purchaseReservation struct{ path, id string }

func loadPurchaseBudget(path string) (PurchaseBudget, error) {
	var budget PurchaseBudget
	data, err := os.ReadFile(path)
	if err != nil {
		return budget, fmt.Errorf("read purchase budget: %w", err)
	}
	if err := json.Unmarshal(data, &budget); err != nil {
		return budget, fmt.Errorf("decode purchase budget: %w", err)
	}
	if budget.Version != 1 || budget.Limit <= 0 || budget.Entries == nil || len(budget.Tariffs) == 0 {
		return budget, errors.New("invalid purchase budget")
	}
	return budget, nil
}

func (b PurchaseBudget) committed() (int64, error) {
	var total int64
	for _, entry := range b.Entries {
		charge := entry.Reserved
		if entry.Settled != nil {
			charge = *entry.Settled
		}
		if charge < 0 || entry.Reserved <= 0 || charge > b.Limit-total {
			return 0, errors.New("invalid or exhausted purchase budget")
		}
		total += charge
	}
	return total, nil
}

func purchaseOptions(provider llms.Provider, model string, opts []llms.CallOption) ([]llms.CallOption, error) {
	path := os.Getenv(EnvPurchaseBudget)
	if path == "" {
		return opts, nil
	}
	if string(provider) != ProviderOpenRouter {
		return nil, errors.New("purchase bounds require OpenRouter max_price routing")
	}
	budget, err := loadPurchaseBudget(path)
	if err != nil {
		return nil, err
	}
	tariff, ok := budget.Tariffs[string(provider)+"/"+model]
	if !ok || tariff.Input <= 0 || tariff.Output <= 0 {
		return nil, fmt.Errorf("missing bounded tariff for %s/%s", provider, model)
	}
	applied := llms.ApplyOptions(opts...)
	if applied.MaxTokens == nil || *applied.MaxTokens <= 0 {
		return nil, errors.New("purchase bounds require an explicit output token limit")
	}
	routing := map[string]any{}
	if current, ok := applied.ExtraBody["provider"]; ok {
		data, err := json.Marshal(current)
		if err != nil {
			return nil, fmt.Errorf("encode bounded provider routing: %w", err)
		}
		if err := json.Unmarshal(data, &routing); err != nil || routing == nil {
			return nil, errors.New("invalid bounded provider routing")
		}
	}
	routing["max_price"] = map[string]any{
		"prompt": float64(tariff.Input) / 1e6, "completion": float64(tariff.Output) / 1e6,
		"request": 0,
	}
	return append(opts, llms.WithExtraBodyParam("provider", routing),
		llms.WithExtraBodyParam("usage", map[string]any{"include": true})), nil
}

func reservePurchase(ctx context.Context, provider llms.Provider, model, request string, approvedRetry bool,
	msgs []llms.Message, opts []llms.CallOption) (*purchaseReservation, error) {
	path := os.Getenv(EnvPurchaseBudget)
	if path == "" {
		return nil, nil
	}
	lock := flock.New(path + ".lock")
	held, err := lock.TryLockContext(ctx, 10*time.Millisecond)
	if err != nil || !held {
		_ = lock.Close()
		return nil, fmt.Errorf("purchase budget lock: %w", errors.Join(err, ctx.Err()))
	}
	defer func() { _ = lock.Close() }()
	budget, err := loadPurchaseBudget(path)
	if err != nil {
		return nil, err
	}
	if budget.Halted != "" {
		return nil, fmt.Errorf("purchase budget halted: %s", budget.Halted)
	}
	if !approvedRetry {
		for _, entry := range budget.Entries {
			if entry.Request == request {
				return nil, fmt.Errorf("%w: paid request lost its response cache", ErrRecordedAttempt)
			}
		}
	}
	tariff, ok := budget.Tariffs[string(provider)+"/"+model]
	if !ok || tariff.Input <= 0 || tariff.Output <= 0 {
		return nil, errors.New("bounded purchase tariff missing")
	}
	applied := *llms.ApplyOptions(opts...)
	applied.Trace = nil
	var routing struct {
		MaxPrice map[string]float64 `json:"max_price"`
	}
	data, err := json.Marshal(applied.ExtraBody["provider"])
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &routing); err != nil ||
		routing.MaxPrice["prompt"] != float64(tariff.Input)/1e6 ||
		routing.MaxPrice["completion"] != float64(tariff.Output)/1e6 ||
		routing.MaxPrice["request"] != 0 {
		return nil, errors.New("purchase routing and reserved tariff differ")
	}
	if applied.MaxTokens == nil || *applied.MaxTokens <= 0 {
		return nil, errors.New("unbounded purchase output")
	}
	for _, msg := range msgs {
		for _, part := range msg.Parts {
			if part.Type != llms.PartTypeText {
				return nil, errors.New("purchase token bound supports text requests only")
			}
		}
	}
	raw, err := json.Marshal([]any{msgs, applied})
	if err != nil {
		return nil, fmt.Errorf("encode purchase token bound: %w", err)
	}
	// UTF-8 bytes bound text tokenization; the slack covers provider framing.
	input := int64(len(raw)) + 4096 + int64(len(msgs))*256
	output := int64(*applied.MaxTokens)
	if input > 10_000_000 || output > 1_000_000 || tariff.Input > 1e9 || tariff.Output > 1e9 {
		return nil, errors.New("purchase request exceeds supported bounds")
	}
	reserved := (input*tariff.Input + output*tariff.Output + 999_999) / 1_000_000
	committed, err := budget.committed()
	if err != nil {
		return nil, err
	}
	if reserved <= 0 || reserved > budget.Limit-committed {
		return nil, fmt.Errorf("purchase cap reached: committed=%d reservation=%d limit=%d micro USD",
			committed, reserved, budget.Limit)
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, fmt.Errorf("name purchase reservation: %w", err)
	}
	id := hex.EncodeToString(nonce[:])
	budget.Entries[id] = PurchaseEntry{Request: request, Reserved: reserved}
	if err := writePurchaseBudget(path, budget); err != nil {
		return nil, err
	}
	return &purchaseReservation{path: path, id: id}, nil
}

func (r *purchaseReservation) settle(ctx context.Context, resp *llms.Response) error {
	if r == nil {
		return nil
	}
	if resp == nil || resp.Usage.Cost == nil {
		return r.halt(ctx, "authoritative charge unavailable")
	}
	cost := *resp.Usage.Cost
	if math.IsNaN(cost) || math.IsInf(cost, 0) || cost < 0 || cost > float64(math.MaxInt64)/1e6 {
		return errors.New("invalid authoritative purchase charge; reservation retained")
	}
	charge := int64(math.Ceil(cost * 1e6))
	lock := flock.New(r.path + ".lock")
	held, err := lock.TryLockContext(ctx, 10*time.Millisecond)
	if err != nil || !held {
		_ = lock.Close()
		return errors.New("cannot settle purchase; reservation retained")
	}
	defer func() { _ = lock.Close() }()
	budget, err := loadPurchaseBudget(r.path)
	if err != nil {
		return err
	}
	entry, ok := budget.Entries[r.id]
	if !ok {
		return errors.New("purchase reservation missing")
	}
	entry.Settled = &charge
	budget.Entries[r.id] = entry
	if charge > entry.Reserved {
		budget.Halted = "provider exceeded reserved charge"
	}
	if err := writePurchaseBudget(r.path, budget); err != nil {
		return err
	}
	if charge > entry.Reserved {
		return errors.New("provider exceeded purchase bound; stop experiment")
	}
	return nil
}

func (r *purchaseReservation) halt(ctx context.Context, reason string) error {
	if r == nil {
		return nil
	}
	lock := flock.New(r.path + ".lock")
	held, err := lock.TryLockContext(ctx, 10*time.Millisecond)
	if err != nil || !held {
		_ = lock.Close()
		return errors.New("cannot record halted purchase budget")
	}
	defer func() { _ = lock.Close() }()
	budget, err := loadPurchaseBudget(r.path)
	if err != nil {
		return err
	}
	budget.Halted = reason
	return writePurchaseBudget(r.path, budget)
}

func writePurchaseBudget(path string, budget PurchaseBudget) error {
	data, err := json.MarshalIndent(budget, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".purchase-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	_, writeErr := tmp.Write(append(data, '\n'))
	syncErr := tmp.Sync()
	closeErr := tmp.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}
