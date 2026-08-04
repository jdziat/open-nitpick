package evals

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	llms "github.com/nocturnium/llm-go-sdk"
	"gopkg.in/yaml.v3"

	"github.com/jdziat/open-nitpick/internal/llm"
)

// EnvPrices names a price file to use instead of the shipped one, for an
// operator who has fresher numbers and does not want to edit the repository.
const EnvPrices = "NITPICK_EVAL_PRICES"

// staleAfterDays is when a price stops being reported as merely dated and
// starts being reported as STALE.
//
// It is a display flag and it changes no number: every cost in the table is
// computed from the recorded rate whatever its age. It exists because "captured
// 47 days ago" is a fact a reader has to convert, and "STALE" is one they
// cannot skim past. A month is the horizon over which this catalog has actually
// moved — the gemini and qwen lines both re-tiered inside one — so it is the
// point at which the right action is to recapture rather than to trust.
const staleAfterDays = 30

// shippedPrices is the price table compiled in, so cost reporting works from
// any working directory. It is still a FILE the operator edits — see the header
// of testdata/pricing.yaml for why the rates are not Go constants.
//
//go:embed testdata/pricing.yaml
var shippedPrices []byte

// CallUsage is what ONE model call reported spending.
//
// The per-call granularity is not bookkeeping neatness, it is what makes a
// tiered price computable at all. OpenRouter bills 11 of the 20 models in the
// shipped table at a rate chosen by the size of THAT REQUEST's prompt, and a
// sum of calls cannot answer "how big was the prompt" — an aggregate of 60,000
// prompt tokens is one call over the qwen 32,000 step or thirty calls under it,
// at rates 3.3x apart. The previous version of this file recorded base rates
// only and left a comment saying tiered pricing was impossible here for exactly
// this reason. It was impossible because the usage was summed too early.
type CallUsage struct {
	Prompt     int
	Completion int
	CacheRead  int
	CacheWrite int

	// Reasoning is the part of Completion spent on internal reasoning, when the
	// provider separates it. It is a SUBSET of Completion, not an addition to
	// it, so it is reported and never summed into a total. Every model in the
	// shipped table that publishes an internal_reasoning rate publishes it
	// EQUAL to its completion rate, so billing these tokens inside completion is
	// exact rather than an approximation.
	Reasoning int
}

// PromptSize is the whole prompt the provider read for this call, which is what
// a tier threshold is measured against.
//
// Prompt alone is not it: the SDK normalizes PromptTokens to EXCLUDE the cached
// subset, so a call with 30,000 fresh and 30,000 cached prompt tokens sent a
// 60,000-token prompt and reports Prompt as 30,000. Billing that at the sub-32k
// tier would be a discount nobody offered.
//
// The vendor does not publish which of the two counts its threshold reads.
// Where the guess cannot be checked, this one takes the LARGER number, which
// selects the more expensive tier — the direction that cannot flatter a model
// into being the recommended default.
func (c CallUsage) PromptSize() int { return c.Prompt + c.CacheRead + c.CacheWrite }

// Total is every billed token in this call. Reasoning is excluded because the
// provider already counted it inside Completion.
func (c CallUsage) Total() int { return c.Prompt + c.Completion + c.CacheRead + c.CacheWrite }

// TokenUsage is what a run spent, as REPORTED by the provider, kept call by
// call.
//
// Nothing in here is estimated. internal/bundle estimates tokens to decide what
// fits in a request and that is the right instrument for budgeting: being wrong
// costs a repacked batch. A cost figure is a different object — a purchasing
// decision rests on it — so an estimate wearing a dollar sign would be a lie
// with a decimal point in it. Calls that came back carrying no usage are
// counted in Unreported rather than filled in from a guess.
//
// PerCall is the only store; every aggregate below is derived from it. Holding
// both a running sum and the calls behind it would let the two disagree, and
// the one a cost was computed from would be whichever the pricing code happened
// to read.
type TokenUsage struct {
	PerCall []CallUsage

	// Unreported is how many calls returned a response carrying no usage — the
	// state that has to stay visible, because a cost column that is sometimes
	// measured and sometimes guessed with no way to tell which is worse than no
	// cost column.
	Unreported int

	// Failed is how many calls returned an error, and it is a stated BLIND SPOT
	// rather than a term in Complete below.
	//
	// It is one of two, and the other leaves no trace at all: MeterClient wraps
	// the client OUTSIDE llms.NewResilientClient, so a request that failed twice
	// and succeeded on the third attempt is one entry here with one attempt's
	// tokens, and is not counted in Failed either. Metering the inner client
	// instead would mean rebuilding the resilience wrapper with retry settings
	// this package cannot read back out of it, which would measure a retry
	// policy production does not use — a worse error than the one it fixes.
	// Both blind spots run the same way: every amount is a LOWER bound, which is
	// the direction that cannot flatter a model into being the default. Stated
	// in CostReadingLegend so a reader sees it, since neither is countable.
	//
	// A failed call may still have been billed — a provider that generates a
	// response and then fails to deliver it charges for the generation — and
	// that charge is not visible from here at all. It is not used to discard the
	// run because failure here does not imply the run failed: in
	// StructuredAuto, a provider that rejects json_schema produces a capability
	// error and the client retries in JSON mode, so voiding the cost on a failed
	// call would silently drop every model without schema support out of the
	// table entirely. Reported, so the reader can see that this row's cost is a
	// lower bound.
	Failed int
}

// Add returns the sum of two usages.
func (u TokenUsage) Add(other TokenUsage) TokenUsage {
	sum := TokenUsage{
		PerCall:    make([]CallUsage, 0, len(u.PerCall)+len(other.PerCall)),
		Unreported: u.Unreported + other.Unreported,
		Failed:     u.Failed + other.Failed,
	}
	sum.PerCall = append(sum.PerCall, u.PerCall...)
	sum.PerCall = append(sum.PerCall, other.PerCall...)
	return sum
}

// Calls is how many model calls reported usage.
func (u TokenUsage) Calls() int { return len(u.PerCall) }

// Prompt is the reported prompt tokens across every call.
func (u TokenUsage) Prompt() int { return u.sum(func(c CallUsage) int { return c.Prompt }) }

// Completion is the reported completion tokens across every call.
func (u TokenUsage) Completion() int { return u.sum(func(c CallUsage) int { return c.Completion }) }

// CacheRead is the reported cache-read tokens across every call.
func (u TokenUsage) CacheRead() int { return u.sum(func(c CallUsage) int { return c.CacheRead }) }

// CacheWrite is the reported cache-write tokens across every call.
func (u TokenUsage) CacheWrite() int { return u.sum(func(c CallUsage) int { return c.CacheWrite }) }

// Reasoning is the reported reasoning tokens across every call.
func (u TokenUsage) Reasoning() int { return u.sum(func(c CallUsage) int { return c.Reasoning }) }

// Total is every billed token. Reasoning is excluded because the provider
// already counted it inside Completion.
func (u TokenUsage) Total() int { return u.sum(CallUsage.Total) }

// LargestPrompt is the biggest single prompt in this usage, which is the number
// that decides whether any tier threshold was crossed.
func (u TokenUsage) LargestPrompt() int {
	most := 0
	for _, c := range u.PerCall {
		most = max(most, c.PromptSize())
	}
	return most
}

func (u TokenUsage) sum(of func(CallUsage) int) int {
	total := 0
	for _, c := range u.PerCall {
		total += of(c)
	}
	return total
}

// Complete reports whether every call in this usage reported what it spent.
// An incomplete usage may not be priced: the sum of the calls that did report
// is not the cost of the run, it is a fraction of it with no way to know which.
func (u TokenUsage) Complete() bool { return len(u.PerCall) > 0 && u.Unreported == 0 }

// observe records one response's reported usage, or the absence of it.
func (u *TokenUsage) observe(resp *llms.Response, err error) {
	if err != nil {
		// The call did not come back. Whether it was billed is not knowable
		// from here, so it is counted as a blind spot rather than as either a
		// report or a zero.
		u.Failed++
		return
	}
	if resp == nil {
		// No error and no response is a broken client rather than a provider
		// fact, but it is still a call whose spend nothing here can see.
		u.Failed++
		return
	}

	// The presence test covers exactly the fields this function goes on to
	// PRICE. Testing the whole llms.Usage against its zero value instead was a
	// $0.00 generator: TotalTokens is a field nothing below reads, and
	// openaicompat.convertUsage copies it through verbatim from any
	// OpenAI-shaped endpoint, so a provider that reports only a total passed as
	// a complete report, contributed no priced tokens, and rendered as
	// $0.000000 across COST, $/REVIEW and $/DEFECT — Known, unfootnoted, and
	// the cheapest row in the table. "The provider said nothing about the
	// tokens we charge for" is the question, and only these fields answer it.
	if resp.Usage.PromptTokens == 0 &&
		resp.Usage.CompletionTokens == 0 &&
		resp.Usage.CacheReadTokens == 0 &&
		resp.Usage.CacheCreationTokens == 0 {
		u.Unreported++
		return
	}

	u.PerCall = append(u.PerCall, CallUsage{
		Prompt:     resp.Usage.PromptTokens,
		Completion: resp.Usage.CompletionTokens,
		CacheRead:  resp.Usage.CacheReadTokens,
		CacheWrite: resp.Usage.CacheCreationTokens,
		Reasoning:  resp.Usage.ReasoningTokens,
	})
}

// Meter accumulates reported usage across every model call made through one
// client.
type Meter struct {
	mu    sync.Mutex
	usage TokenUsage
}

// Usage returns what has been metered so far.
func (m *Meter) Usage() TokenUsage {
	m.mu.Lock()
	defer m.mu.Unlock()

	// A copy, so a caller that keeps the result cannot observe later calls
	// appending to the slice it holds.
	return TokenUsage{
		PerCall:    append([]CallUsage(nil), m.usage.PerCall...),
		Unreported: m.usage.Unreported,
		Failed:     m.usage.Failed,
	}
}

// meteredLLM records the usage of every response that passes through it.
//
// It wraps a client at the same seam recordingLLM does, OUTSIDE the SDK's
// resilience wrapper, so a request that was retried contributes the usage of
// the attempt that succeeded.
type meteredLLM struct {
	inner llms.LLM
	meter *Meter
}

func (m *meteredLLM) GenerateContent(ctx context.Context, msgs []llms.Message, opts ...llms.CallOption) (*llms.Response, error) {
	resp, err := m.inner.GenerateContent(ctx, msgs, opts...)

	m.meter.mu.Lock()
	m.meter.usage.observe(resp, err)
	m.meter.mu.Unlock()

	return resp, err
}

func (m *meteredLLM) Stream(ctx context.Context, msgs []llms.Message, opts ...llms.CallOption) (<-chan llms.StreamChunk, error) {
	// Nothing in the review path streams. Metering a stream means reading the
	// final chunk's usage, and a wrapper that silently passed the stream
	// through would report a streamed run as costing nothing.
	return m.inner.Stream(ctx, msgs, opts...)
}

func (m *meteredLLM) Provider() llms.Provider { return m.inner.Provider() }
func (m *meteredLLM) Model() string           { return m.inner.Model() }

// MeterClient installs usage metering on a client and returns the meter that
// accumulates it, replacing the client's LLM in place.
//
// Call it before the client is used: a meter attached to a client that is
// already answering requests would race with them and miss whatever it missed.
func MeterClient(c *llm.Client) *Meter {
	m := &Meter{}
	c.LLM = &meteredLLM{inner: c.LLM, meter: m}
	return m
}

// MeterJudge installs metering on the judge's client.
//
// The judge is metered separately and never folded into a contender's cost. It
// is a MEASUREMENT expense: nobody running open-nitpick pays for a judge, so
// adding its tokens to a review's would overstate what the tool costs by
// whatever the judge happens to charge — currently more than the input rate of
// more than half the battery.
func MeterJudge(j *Judge) *Meter { return MeterClient(j.client) }

// Rates is one price point: what a provider charges per million tokens for each
// kind of token, at one prompt-size tier.
type Rates struct {
	Input  float64
	Output float64

	// CacheRead and CacheWrite are omitted for providers that publish no
	// separate cache tier, and an omitted rate falls back to Input — which is
	// what such a provider actually charges for those tokens.
	//
	// A published rate of 0.0 is a DIFFERENT statement: free caching, which
	// some providers do publish. float64 alone cannot tell the two apart, so
	// the escape hatch the file header documents did not exist — an operator
	// who wrote `cache_read: 0.0` to record a free tier was billed at the input
	// rate, silently, by the fallback below. The two flags carry what the YAML
	// knew and this type could not; they are set only by parsePrices, so a
	// struct literal still means "omitted, fall back to Input".
	CacheRead  float64
	CacheWrite float64

	cacheReadFree  bool
	cacheWriteFree bool
}

// cost prices one call at these rates.
//
// PromptTokens EXCLUDES the cached subset by the SDK's normalization (see
// llms.Usage), so the cache terms are additions here and not a discount applied
// twice.
func (r Rates) cost(c CallUsage) float64 {
	const perMillion = 1_000_000.0

	read := r.CacheRead
	if read == 0 && !r.cacheReadFree {
		read = r.Input
	}
	write := r.CacheWrite
	if write == 0 && !r.cacheWriteFree {
		write = r.Input
	}

	return (float64(c.Prompt)*r.Input +
		float64(c.Completion)*r.Output +
		float64(c.CacheRead)*read +
		float64(c.CacheWrite)*write) / perMillion
}

// Tier is a rate that takes over once a single prompt reaches MinPromptTokens.
type Tier struct {
	MinPromptTokens int

	// Rates is fully resolved at load: a tier that restates only some rates
	// inherits the rest from the base entry, which is how the vendor encodes
	// them. Resolving at load rather than at pricing time means the loaded
	// table says what it will charge, instead of describing a diff.
	Rates
}

// Routing is what the OTHER endpoints serving this model charge.
//
// It exists because the round-2 error was not a wrong number, it was a wrong
// object. `/api/v1/models` returns one `pricing` block per model and every rate
// in the previous table was a correct copy of it — but that block is ONE
// ENDPOINT'S price. OpenRouter serves 14 of the 20 battery models from between
// 5 and 34 endpoints, spanning 22x on openai/gpt-5.6-luna and 3.9x on
// z-ai/glm-5.2, and open-nitpick pins no provider, so which one served a request
// is the router's choice and is recorded nowhere. A $/DEFECT figure was
// therefore a point inside a band whose width is a property of the model — and
// the five models with no band at all are the five qwen entries, so the error
// tracked model class, which is the axis the table is read along.
//
// Recording the band does not recover the missing measurement. It makes the
// missing measurement VISIBLE, and it gives an operator who needs a point
// estimate something to do about it: pin the provider, and the band collapses to
// a rate this file can name.
type Routing struct {
	// Source is the endpoints URL the band was read from, kept separately from
	// the model-level Source because they are two different requests and the
	// second one is the one that was missing for two rounds.
	Source string

	// Endpoints is how many serve this model. One means the recorded rate is
	// the only rate, which is a fact worth recording rather than an absence.
	Endpoints int

	// Cheapest and Dearest name the endpoints at the ends of the band, so the
	// footnote can say which provider a reader would be pinning to.
	Cheapest string
	Dearest  string

	// Low and High are the extremes across every endpoint, with each endpoint's
	// own cache fallback applied BEFORE the extremum is taken — so they bound
	// what could be billed rather than what happens to be published.
	//
	// They are base rates. An endpoint's own prompt-size overrides are not
	// folded in, which is why Band refuses to answer for a call billed above the
	// base tier instead of returning a bound it cannot support.
	Low  Rates
	High Rates
}

// Spread is how many times dearer the dearest endpoint's input rate is than the
// cheapest. It is 1 for a model with a single endpoint.
func (r Routing) Spread() float64 {
	if r.Low.Input <= 0 {
		return 1
	}
	return r.High.Input / r.Low.Input
}

// Price is one model's published rates, with provenance.
type Price struct {
	// Rates is the base tier, charged below the first threshold in Tiers.
	Rates

	// Tiers is ascending by MinPromptTokens, and may be empty.
	Tiers []Tier

	// CapturedOn is the day this rate was read from the vendor and Source is
	// where from. Both are required per model rather than once per file: rates
	// are refreshed one model at a time, and a file-level date is bumped by
	// whoever touched one entry, re-dating the rest as fresh.
	CapturedOn time.Time
	Source     string

	// Endpoint is WHICH of the vendor's endpoints Rates belongs to, and it is
	// required for the same reason Source is. Without it the recorded rate
	// cannot be told apart from the model's price, which is the confusion that
	// produced the round-2 error — and several of these turn out to be quantized
	// third-party hosts (alibaba/fp8, coreweave/fp4, ambient/int4) rather than
	// the vendor's own serving of the model.
	Endpoint string

	// Routing is the rest of the endpoint set.
	Routing Routing
}

// rateAt returns the rates a prompt of this size is billed at.
func (p Price) rateAt(promptTokens int) Rates {
	rates := p.Rates
	for _, t := range p.Tiers {
		if promptTokens < t.MinPromptTokens {
			break
		}
		rates = t.Rates
	}
	return rates
}

// Cost prices one usage in USD, choosing a tier per call.
func (p Price) Cost(u TokenUsage) float64 {
	total := 0.0
	for _, c := range u.PerCall {
		total += p.rateAt(c.PromptSize()).cost(c)
	}
	return total
}

// Band is what this usage would have cost at the cheapest and dearest endpoints
// serving the model — the interval the true amount lies in, given that nothing
// records which endpoint the router used.
//
// ok is false when the band would not be a band. Two cases, and both are
// refusals rather than approximations:
//
//   - No routing was captured, so the endpoint set is unknown. A model whose
//     alternatives were never looked at is not a model with no alternatives.
//   - A call was billed above the base tier. Routing.Low and Routing.High are
//     base rates; each endpoint publishes its own prompt-size overrides and this
//     file does not record 34 tier ladders per model. Returning the base band
//     anyway would be a bound that does not bound, which is worse than none.
func (p Price) Band(u TokenUsage) (low, high float64, ok bool) {
	if p.Routing.Endpoints == 0 || len(u.PerCall) == 0 {
		return 0, 0, false
	}

	for _, c := range u.PerCall {
		if len(p.Tiers) > 0 && c.PromptSize() >= p.Tiers[0].MinPromptTokens {
			return 0, 0, false
		}
		low += p.Routing.Low.cost(c)
		high += p.Routing.High.cost(c)
	}
	return low, high, true
}

// rawPrice is the decode target, with pointers so an omitted rate and a
// published 0.0 stay distinguishable long enough to be recorded.
type rawPrice struct {
	CapturedOn string `yaml:"captured_on"`
	Source     string `yaml:"source"`
	Endpoint   string `yaml:"endpoint"`

	Input      *float64 `yaml:"input"`
	Output     *float64 `yaml:"output"`
	CacheRead  *float64 `yaml:"cache_read"`
	CacheWrite *float64 `yaml:"cache_write"`

	Tiers   []rawTier   `yaml:"tiers"`
	Routing *rawRouting `yaml:"routing"`
}

// rawRouting is the endpoint band. Every field is a pointer or checked for
// emptiness because a routing block that half-decoded would narrow the band,
// and a narrower band is the direction that lets two rows be ordered when they
// should not be.
type rawRouting struct {
	Source    string `yaml:"source"`
	Endpoints *int   `yaml:"endpoints"`
	Cheapest  string `yaml:"cheapest"`
	Dearest   string `yaml:"dearest"`

	MinInput      *float64 `yaml:"min_input"`
	MaxInput      *float64 `yaml:"max_input"`
	MinOutput     *float64 `yaml:"min_output"`
	MaxOutput     *float64 `yaml:"max_output"`
	MinCacheRead  *float64 `yaml:"min_cache_read"`
	MaxCacheRead  *float64 `yaml:"max_cache_read"`
	MinCacheWrite *float64 `yaml:"min_cache_write"`
	MaxCacheWrite *float64 `yaml:"max_cache_write"`
}

// rawTier is one published prompt-size override.
type rawTier struct {
	MinPromptTokens *int `yaml:"min_prompt_tokens"`

	Input      *float64 `yaml:"input"`
	Output     *float64 `yaml:"output"`
	CacheRead  *float64 `yaml:"cache_read"`
	CacheWrite *float64 `yaml:"cache_write"`
}

// PriceTable is the pricing data a run reports against.
type PriceTable struct {
	// RawModels is the decoded file. Models is what the ledger reads: the same
	// entries after parsePrices has rejected the unusable ones, resolved tier
	// inheritance, and recorded which cache rates were published rather than
	// merely absent.
	RawModels map[string]rawPrice `yaml:"models"`

	Models map[string]Price `yaml:"-"`

	// origin is the file this table was read from, for the provenance line.
	origin string
}

// Prices returns the price table for this run: the file named by
// NITPICK_EVAL_PRICES when set, otherwise the shipped table.
func Prices() (*PriceTable, error) {
	if path := strings.TrimSpace(os.Getenv(EnvPrices)); path != "" {
		table, err := LoadPrices(path)
		if err != nil {
			// Deliberately not a fallback to the shipped table. An operator who
			// named a price file did so because the shipped rates are wrong for
			// them, and quietly costing the run at the stale rates instead is
			// the exact failure this file exists to prevent.
			return nil, fmt.Errorf("%s=%s: %w", EnvPrices, path, err)
		}
		return table, nil
	}

	return parsePrices(shippedPrices, "testdata/pricing.yaml")
}

// LoadPrices reads a price table from disk.
func LoadPrices(path string) (*PriceTable, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parsePrices(data, path)
}

// parsePrices decodes and validates a price table.
func parsePrices(data []byte, origin string) (*PriceTable, error) {
	var table PriceTable

	// KnownFields, not plain Unmarshal. yaml.v3 drops keys it does not
	// recognize, so `imput: 5.0` decoded to a Price with Input at its zero
	// value, the table reported Priced=true, and every prompt token in the
	// battery was billed at $0 — the same "$0.00 a reader acts on" that the
	// missing-entry rule exists to prevent, arriving through the file this
	// design deliberately made hand-editable. It also rejects a file-level
	// `captured_on`, which no longer means anything and would otherwise sit
	// there being read as the age of rates it does not describe.
	table.Models = map[string]Price{}

	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&table); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse prices: %w", err)
	}
	table.origin = origin

	for id, raw := range table.RawModels {
		price, err := parsePrice(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: model %q: %w", origin, id, err)
		}
		table.Models[id] = price
	}

	return &table, nil
}

// parsePrice validates and resolves one entry.
func parsePrice(raw rawPrice) (Price, error) {
	price := Price{Source: strings.TrimSpace(raw.Source)}

	// A rate with no date is rejected rather than reported as fresh. The date is
	// the only thing standing between a reader and rates that moved months ago,
	// so an entry that cannot say when it was captured cannot be used to put a
	// dollar sign in front of anything.
	captured, err := parseCaptureDate(raw.CapturedOn)
	if err != nil {
		return Price{}, err
	}
	price.CapturedOn = captured

	if price.Source == "" {
		return Price{}, fmt.Errorf("has no source: a price with no provenance is a guess with a decimal point, and the next capture becomes an archaeology exercise")
	}

	price.Endpoint = strings.TrimSpace(raw.Endpoint)
	if price.Endpoint == "" {
		return Price{}, fmt.Errorf("has no endpoint: `/api/v1/models` returns ONE endpoint's rates and not the model's, " +
			"and an entry that cannot say which one is the confusion that produced a whole round of cost numbers")
	}

	// An entry has to price BOTH sides or it is not an entry. A half-written one
	// is worse than a missing one: missing reports unknown, half-written reports
	// a confident dollar figure with one rate silently at zero — and output is
	// 4-6x input across this table, so the half that goes missing costs the
	// most.
	base, err := resolveRates(Rates{}, raw.Input, raw.Output, raw.CacheRead, raw.CacheWrite, true)
	if err != nil {
		return Price{}, err
	}
	price.Rates = base

	previous := 0
	for i, rawT := range raw.Tiers {
		if rawT.MinPromptTokens == nil {
			return Price{}, fmt.Errorf("tier %d has no min_prompt_tokens: a rate that does not say when it takes over cannot be selected", i)
		}
		mpt := *rawT.MinPromptTokens
		if mpt <= 0 {
			return Price{}, fmt.Errorf("tier %d has min_prompt_tokens %d: a threshold at or below zero restates the base rate rather than overriding it", i, mpt)
		}
		if mpt <= previous {
			// rateAt walks the slice in order and stops at the first threshold
			// above the prompt. Out of order, it would stop early and bill a
			// large prompt at a small prompt's rate.
			return Price{}, fmt.Errorf("tier %d has min_prompt_tokens %d, which does not exceed the %d before it: tiers are selected by walking them in order, so an unsorted list bills a large prompt at a small prompt's rate", i, mpt, previous)
		}
		previous = mpt

		if rawT.Input == nil && rawT.Output == nil && rawT.CacheRead == nil && rawT.CacheWrite == nil {
			return Price{}, fmt.Errorf("tier %d (min_prompt_tokens %d) changes no rate: it is a typo, not a tier", i, mpt)
		}

		// Inherited from the BASE entry, not from the tier below, because that
		// is how the vendor encodes an override: the highest matching one is
		// applied over the published base. gemini-3.1-pro-preview's 200,000
		// tier raises input, output and cache_read and says nothing about
		// cache_write, which therefore stays at the base rate.
		rates, err := resolveRates(base, rawT.Input, rawT.Output, rawT.CacheRead, rawT.CacheWrite, false)
		if err != nil {
			return Price{}, fmt.Errorf("tier %d (min_prompt_tokens %d): %w", i, mpt, err)
		}
		price.Tiers = append(price.Tiers, Tier{MinPromptTokens: mpt, Rates: rates})
	}

	routing, err := parseRouting(raw.Routing, base)
	if err != nil {
		return Price{}, err
	}
	price.Routing = routing

	return price, nil
}

// parseRouting validates the endpoint band and checks the recorded rate against
// it.
//
// base is the entry's own rates, and the containment check below is the one that
// would have caught the round-2 error at the moment of capture: a rate that is
// not inside its own model's endpoint band is a rate that belongs to some other
// model, some other date, or nothing at all.
func parseRouting(raw *rawRouting, base Rates) (Routing, error) {
	if raw == nil {
		return Routing{}, fmt.Errorf("has no routing block: OpenRouter serves most of this battery from " +
			"several endpoints at different rates and open-nitpick pins none of them, so an entry that " +
			"records one rate and says nothing about the others reports a point as though it were the price")
	}

	out := Routing{
		Source:   strings.TrimSpace(raw.Source),
		Cheapest: strings.TrimSpace(raw.Cheapest),
		Dearest:  strings.TrimSpace(raw.Dearest),
	}
	if out.Source == "" || out.Cheapest == "" || out.Dearest == "" {
		return Routing{}, fmt.Errorf("routing needs source, cheapest and dearest: the band is only actionable " +
			"if it names the endpoint an operator would pin to")
	}
	if raw.Endpoints == nil || *raw.Endpoints < 1 {
		return Routing{}, fmt.Errorf("routing needs endpoints >= 1: zero would mean the model is served by " +
			"nobody, and an absent count reads as an unmeasured band rather than a narrow one")
	}
	out.Endpoints = *raw.Endpoints

	low, err := resolveRates(Rates{}, raw.MinInput, raw.MinOutput, raw.MinCacheRead, raw.MinCacheWrite, true)
	if err != nil {
		return Routing{}, fmt.Errorf("routing min: %w", err)
	}
	high, err := resolveRates(Rates{}, raw.MaxInput, raw.MaxOutput, raw.MaxCacheRead, raw.MaxCacheWrite, true)
	if err != nil {
		return Routing{}, fmt.Errorf("routing max: %w", err)
	}

	// Every cache extremum is required rather than optional, unlike the base
	// entry's. An omitted cache rate on a base entry means "this provider bills
	// cache at input", which is a statement; an omitted one here would silently
	// set that end of the band to zero, and a band reaching zero makes every
	// amount unrankable against everything for a reason that is a decode bug.
	if raw.MinCacheRead == nil || raw.MaxCacheRead == nil || raw.MinCacheWrite == nil || raw.MaxCacheWrite == nil {
		return Routing{}, fmt.Errorf("routing needs min/max for cache_read and cache_write: they are DERIVED " +
			"by applying each endpoint's own input fallback before taking the extremum, so an absent one is " +
			"an uncaptured band and not a provider that charges nothing")
	}

	for _, c := range []struct {
		name     string
		lo, hi   float64
		recorded float64
	}{
		{"input", low.Input, high.Input, base.Input},
		{"output", low.Output, high.Output, base.Output},
	} {
		if c.lo > c.hi {
			return Routing{}, fmt.Errorf("routing min_%s (%v) exceeds max_%s (%v)", c.name, c.lo, c.name, c.hi)
		}
		if c.recorded < c.lo || c.recorded > c.hi {
			return Routing{}, fmt.Errorf("the recorded %s rate %v is outside this model's own endpoint band "+
				"[%v, %v]: the rate belongs to an endpoint that does not serve this model, so one of the two "+
				"captures is of something else", c.name, c.recorded, c.lo, c.hi)
		}
	}

	if out.Endpoints == 1 && (low != high) {
		return Routing{}, fmt.Errorf("routing declares 1 endpoint and a band wider than a point (%v..%v input): "+
			"one of the two was not recaptured with the other", low.Input, high.Input)
	}

	out.Low, out.High = low, high
	return out, nil
}

// resolveRates overlays the stated rates onto a starting point.
//
// requireBoth is set for a base entry, where input and output must be present.
// A tier inherits anything it does not restate, so the same check there would
// reject the vendor's own encoding.
func resolveRates(from Rates, input, output, cacheRead, cacheWrite *float64, requireBoth bool) (Rates, error) {
	out := from

	for _, r := range []struct {
		name     string
		val      *float64
		required bool
		into     *float64
		free     *bool
	}{
		{"input", input, requireBoth, &out.Input, nil},
		{"output", output, requireBoth, &out.Output, nil},
		{"cache_read", cacheRead, false, &out.CacheRead, &out.cacheReadFree},
		{"cache_write", cacheWrite, false, &out.CacheWrite, &out.cacheWriteFree},
	} {
		if r.val == nil {
			if r.required {
				return Rates{}, fmt.Errorf("has no %s rate: an entry that prices only one side reports a confident number with the other side free, which is worse than the unknown a missing entry reports", r.name)
			}
			continue
		}
		if *r.val < 0 {
			// -$4.00 is a data-entry slip, and it wins every ranking this
			// column exists to produce.
			return Rates{}, fmt.Errorf("has a negative %s rate (%v)", r.name, *r.val)
		}

		*r.into = *r.val
		if r.free != nil {
			// A free tier on input or output is real and allowed; it is the
			// SILENT zero that is not. Requiring the key to be present is what
			// separates "this model is free" from "somebody forgot".
			*r.free = *r.val == 0
		}
	}

	return out, nil
}

// parseCaptureDate reads a YYYY-MM-DD capture date.
func parseCaptureDate(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, fmt.Errorf("captured_on is required: a price that cannot say when it was captured cannot be used to cost a run")
	}

	day, err := time.Parse(time.DateOnly, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("captured_on %q is not a %s date: %w", raw, time.DateOnly, err)
	}
	return day, nil
}

// Captured returns the OLDEST and newest capture dates in the table.
//
// The oldest is what the table's age is reported as. Reporting the newest, or a
// file-level date, would let one refreshed entry describe nineteen that nobody
// looked at — which is the failure the per-model dates exist to prevent, so
// summarizing them away here would put it straight back.
func (p *PriceTable) Captured() (oldest, newest time.Time, err error) {
	if p == nil {
		return time.Time{}, time.Time{}, fmt.Errorf("no price table loaded")
	}
	if len(p.Models) == 0 {
		return time.Time{}, time.Time{}, fmt.Errorf("%s prices no models", p.origin)
	}

	for _, price := range p.Models {
		if oldest.IsZero() || price.CapturedOn.Before(oldest) {
			oldest = price.CapturedOn
		}
		if price.CapturedOn.After(newest) {
			newest = price.CapturedOn
		}
	}
	return oldest, newest, nil
}

// Sources names the distinct places the rates came from.
func (p *PriceTable) Sources() []string {
	if p == nil {
		return nil
	}

	seen := map[string]bool{}
	var out []string
	for _, price := range p.Models {
		if seen[price.Source] {
			continue
		}
		seen[price.Source] = true
		out = append(out, price.Source)
	}
	sort.Strings(out)
	return out
}

// Provenance is the line a report prints above any cost column: which prices
// these are, where they came from, and how old they are.
func (p *PriceTable) Provenance() string { return p.provenanceAt(time.Now()) }

// provenanceAt takes the clock as an argument so the age is testable.
func (p *PriceTable) provenanceAt(now time.Time) string {
	oldest, newest, err := p.Captured()
	if err != nil {
		// A ledger built with no table still prints its rows, every cost cell
		// reading unknown. Saying so here is what stops that reading as an
		// outage in the models rather than a missing price file.
		if p == nil {
			return "no price table loaded; every cost is unknown"
		}
		return fmt.Sprintf("prices from %s: %v", p.origin, err)
	}

	span := oldest.Format(time.DateOnly)
	age := describeAge(ageInDays(now, oldest))
	if !newest.Equal(oldest) {
		span = fmt.Sprintf("%s to %s", oldest.Format(time.DateOnly), newest.Format(time.DateOnly))
		age = fmt.Sprintf("%s to %s", describeAge(ageInDays(now, newest)), describeAge(ageInDays(now, oldest)))
	}

	sources := p.Sources()
	from := "an unnamed source"
	if len(sources) > 0 {
		from = strings.Join(sources, ", ")
	}

	return fmt.Sprintf("prices captured %s (%s) from %s (%s), %d model(s) priced",
		span, age, from, p.origin, len(p.Models))
}

// ageInDays is whole calendar days between a capture date and now.
//
// Not elapsed hours: captured_on is a date, and subtracting a timestamp from it
// made a table captured this morning report itself as a day old wherever the
// local zone sits behind UTC.
func ageInDays(now, captured time.Time) int {
	y, m, d := now.Date()
	return int(time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Sub(captured).Hours() / 24)
}

// describeAge renders an age for a reader who has to decide whether to act on
// the number beside it.
func describeAge(days int) string {
	switch {
	case days < 0:
		// Not swallowed. A capture date in the future is a typo in the one
		// field a reader uses to decide whether to trust the numbers.
		return fmt.Sprintf("dated %d day(s) in the FUTURE", -days)
	case days >= staleAfterDays:
		return fmt.Sprintf("%d day(s) old — STALE, recapture before quoting", days)
	default:
		return fmt.Sprintf("%d day(s) old", days)
	}
}

// Price returns a model's rates, and whether the table has any.
func (p *PriceTable) Price(model string) (Price, bool) {
	if p == nil {
		return Price{}, false
	}
	price, ok := p.Models[model]
	return price, ok
}

// Cost is a USD amount that may be UNKNOWN or merely NOT COMPARABLE, and knows
// which and why.
//
// A float64 cannot hold "we do not know", and every unset one in Go renders as
// $0.00 — the single wrong answer a reader will act on without checking, since
// free is a reason to pick a model. So not-known is a distinct state that prints
// as such and carries the reason with it.
type Cost struct {
	USD   float64
	Known bool

	// Comparable is false when the amount is real but was measured over a
	// SMALLER set of fixtures than the rows beside it. Such an amount is not
	// wrong, it is an answer to a different question, and the difference is
	// invisible in a column of dollar signs.
	Comparable bool

	// Reason explains an unknown or incomparable amount, for the footnote under
	// a table. It is empty when the amount is both known and comparable.
	Reason string

	// Stale is set when the RATE this amount was computed from is older than
	// staleAfterDays, and StaleReason says how old.
	//
	// It rides on the amount rather than only on the table because a Cost is a
	// value a caller can print anywhere, and the age was only ever surfaced in
	// one place: CostLedger.Table's provenance line and AGE column. Anything
	// that formatted row.PerDefect() into a summary — the obvious next use of
	// this type — carried a dollar figure with no indication that the rate
	// behind it had moved on. Staleness is a property of the number, so it
	// travels with the number.
	//
	// It is deliberately NOT a downgrade of Known or Comparable: the arithmetic
	// is right and the rows are still each other's peers. It is a disclosure.
	Stale       bool
	StaleReason string

	// Low and High bound the amount across the endpoints that could have served
	// the request, and Banded says the two differ. USD stays the amount computed
	// from the RECORDED endpoint's rate — a point inside [Low, High] — because
	// substituting a midpoint would invent a rate no endpoint charges.
	//
	// Like Stale, this is a disclosure and not a downgrade: the arithmetic is
	// right. What it disqualifies is a specific reader habit, sorting the column,
	// and the rule it implies is stated rather than left to be inferred — two
	// amounts may be ordered only if their bands are disjoint. See
	// CostLedger.OrderingNotes, which does that check rather than leaving it to
	// the reader.
	Low, High  float64
	Banded     bool
	BandReason string
}

// knownCost is a measured amount that can be read against its neighbours.
func knownCost(usd float64) Cost { return Cost{USD: usd, Known: true, Comparable: true} }

// incomparableCost is a measured amount that may not be ranked.
func incomparableCost(usd float64, format string, args ...any) Cost {
	return Cost{USD: usd, Known: true, Reason: fmt.Sprintf(format, args...)}
}

// unknownCost is an amount that must not be printed as a number.
func unknownCost(format string, args ...any) Cost {
	return Cost{Reason: fmt.Sprintf(format, args...)}
}

// dated stamps the age of the rate an amount was computed from.
//
// An unknown amount is left alone: there is no rate behind it to be old, and a
// staleness marker on "unknown" would describe nothing.
func (c Cost) dated(model string, days int) Cost {
	if !c.Known || days < staleAfterDays {
		return c
	}
	c.Stale = true
	c.StaleReason = fmt.Sprintf("%s is priced from a rate captured %d day(s) ago (STALE at %d): "+
		"recapture testdata/pricing.yaml before quoting this number", model, days, staleAfterDays)
	return c
}

// Notes are the footnotes this amount carries, each already labelled with the
// marker a reader would have seen in the cell.
func (c Cost) Notes() []string {
	var out []string
	if c.Reason != "" {
		label := "unknown"
		if c.Known {
			label = "*"
		}
		out = append(out, label+": "+c.Reason)
	}
	if c.StaleReason != "" {
		out = append(out, staleMark+": "+c.StaleReason)
	}
	if c.BandReason != "" {
		// Carries the band marker only when there IS a band. The other case is
		// a stated refusal to compute one, which has no cell marker because the
		// cell is an ordinary point estimate — the reader needs to be told the
		// bound is missing, not that it is wide.
		mark := "band"
		if c.Banded {
			mark = bandMark
		}
		out = append(out, mark+": "+c.BandReason)
	}
	return out
}

// staleMark flags an amount computed from a rate old enough to recapture. It is
// one character for the same reason "*" is: a table cell is width-formatted, and
// a marker that widens the column moves every number beside it.
const staleMark = "!"

// bandMark flags an amount that is one point inside a routing band.
//
// The band is not printed in the cell. Rendering "$0.000123–$0.000486" in three
// columns of a width-formatted table pushes every number out of alignment, which
// costs more comprehension than it buys — so the marker goes in the cell, the
// span goes in the footnote where there is room for the endpoint names, and
// OrderingNotes does the comparison the reader would otherwise attempt by eye.
const bandMark = "~"

// String renders the amount for a table cell. Unknown prints as "unknown", not
// as a number; an incomparable amount carries a "*" so it cannot be ranked by
// eye, and one priced off an aged rate carries a "!". The reasons belong in a
// footnote where there is room for them — see Notes.
//
// The scale spans four orders of magnitude across the battery — a review by the
// cheapest model costs less than a hundredth of a cent — so the precision
// follows the number rather than fixing two decimal places that would round
// most of the table to zero.
func (c Cost) String() string {
	if !c.Known {
		return "unknown"
	}

	var amount string
	switch {
	case c.USD >= 1:
		amount = fmt.Sprintf("$%.2f", c.USD)
	case c.USD >= 0.01:
		amount = fmt.Sprintf("$%.4f", c.USD)
	default:
		amount = fmt.Sprintf("$%.6f", c.USD)
	}

	if !c.Comparable {
		amount += "*"
	}
	if c.Stale {
		amount += staleMark
	}
	if c.Banded {
		amount += bandMark
	}
	return amount
}

// CostLedger accumulates what a battery actually spent, per model.
//
// Reviews and judge calls are kept apart on purpose: one is what the tool costs
// its user, the other is what measuring it cost us, and a single total would be
// neither number.
type CostLedger struct {
	prices *PriceTable

	mu     sync.Mutex
	models map[string]*modelSpend
	judges map[string]*modelSpend
}

// modelSpend is one model's running total.
type modelSpend struct {
	reviews  int
	measured int
	detected int
	planted  int
	usage    TokenUsage

	// attempted and priced count RUNS PER FIXTURE: how many times this model was
	// run on each fixture, and how many of those runs could actually be priced.
	//
	// Coverage is tracked because cost per defect is otherwise maximised by the
	// worst behaviour available: a model that dies on the hard fixtures and
	// completes the easy ones is priced only on the runs it survived, and its
	// ratio — real, correctly computed, drawn from a numerator and denominator
	// of the same reviews — describes an easier corpus than the row beneath it.
	//
	// They are COUNTS and not sets because the set was not enough, and the gap
	// was reachable by the strategy this whole mechanism exists to stop. Held as
	// sets, a row cleared the comparability test by being priced on each fixture
	// at least ONCE, whatever happened to the other runs. So a reviewer that
	// finds a defect on one run in three, and whose two barren runs report no
	// usage, is priced on exactly the runs that found something: RECALL 1.00,
	// $/DEFECT at the calibrated reviewer's, full fixture coverage, and
	// indistinguishable from a reviewer that finds everything every time. The
	// numerator and the denominator are drawn from the same reviews, which is
	// what the earlier check tested, and both are drawn from a set the strategy
	// SELECTED ON THE OUTCOME. Depth is what sees that.
	attempted map[string]int
	priced    map[string]int
}

// NewCostLedger builds a ledger against a price table.
func NewCostLedger(prices *PriceTable) *CostLedger {
	return &CostLedger{
		prices: prices,
		models: map[string]*modelSpend{},
		judges: map[string]*modelSpend{},
	}
}

// PriceTable returns the table this ledger costs against, so a report can print
// its provenance line above the columns it produced.
func (l *CostLedger) PriceTable() *PriceTable { return l.prices }

// Observe records one review of one fixture by one model, together with how
// many planted defects it detected and how many the fixture planted.
//
// A review whose usage is incomplete counts toward Reviews and nothing else:
// its detections are excluded along with its tokens, so the per-defect ratio
// divides a numerator and a denominator drawn from the same reviews. Counting
// the defects but not the tokens would make a model look cheaper for the runs
// its provider failed to report — an error in the flattering direction, which
// is the kind this harness has shipped before.
//
// The fixture is required rather than optional: without it the ledger cannot
// tell a model measured on eight fixtures from one measured on the four it did
// not crash on, and those two rows are not comparable.
//
// `planted` is recorded for the same reason `detected` is, and it arrived a
// round later: cost per defect ALONE is maximised by doing the least work that
// still lands one cheap hit, so it is only a score beside the recall it was
// bought at. Without the denominator here the ledger could not state that pair,
// and $/DEFECT would have been published as a standalone ranking. See
// PublishedCostReadings.
func (l *CostLedger) Observe(model, fixture string, usage TokenUsage, detected, planted int) {
	l.mu.Lock()
	defer l.mu.Unlock()

	spend := l.spend(l.models, model)
	spend.reviews++
	spend.attempted[fixture]++

	if !usage.Complete() {
		return
	}

	spend.measured++
	spend.detected += detected
	spend.planted += planted
	spend.priced[fixture]++
	spend.usage = spend.usage.Add(usage)
}

// ObserveScore records a scored run, which is where every term of the cost
// reading already sits side by side.
func (l *CostLedger) ObserveScore(s Score) {
	l.Observe(s.Model, s.Fixture, s.Usage, s.Matched, s.Total)
}

// ObserveJudge records one judge call's usage, kept out of every model's cost.
func (l *CostLedger) ObserveJudge(model, fixture string, usage TokenUsage) {
	l.mu.Lock()
	defer l.mu.Unlock()

	spend := l.spend(l.judges, model)
	spend.reviews++
	spend.attempted[fixture]++

	if !usage.Complete() {
		return
	}

	spend.measured++
	spend.priced[fixture]++
	spend.usage = spend.usage.Add(usage)
}

// spend returns the running total for a model, creating it on first sight.
// The caller holds the lock.
func (l *CostLedger) spend(in map[string]*modelSpend, model string) *modelSpend {
	s, ok := in[model]
	if !ok {
		s = &modelSpend{attempted: map[string]int{}, priced: map[string]int{}}
		in[model] = s
	}
	return s
}

// Row returns one model's spend.
func (l *CostLedger) Row(model string) CostRow { return l.rowAt(model, time.Now()) }

// rowAt takes the clock as an argument so the staleness a row reports is
// testable, following tableAt.
func (l *CostLedger) rowAt(model string, now time.Time) CostRow {
	l.mu.Lock()
	defer l.mu.Unlock()

	fixtures, depth := l.referenceCorpus(l.models)
	return l.row(l.models, model, fixtures, depth, now)
}

// Rows returns every model's spend, ordered by model id so two runs of the
// same battery print in the same order.
func (l *CostLedger) Rows() []CostRow { return l.rowsAt(l.models, time.Now()) }

// JudgeRows returns the judges' spend. It is a list because a re-judge run
// deliberately involves two of them.
func (l *CostLedger) JudgeRows() []CostRow { return l.rowsAt(l.judges, time.Now()) }

func (l *CostLedger) rowsAt(in map[string]*modelSpend, now time.Time) []CostRow {
	l.mu.Lock()
	defer l.mu.Unlock()

	names := make([]string, 0, len(in))
	for name := range in {
		names = append(names, name)
	}
	sort.Strings(names)

	fixtures, depth := l.referenceCorpus(in)
	out := make([]CostRow, 0, len(names))
	for _, name := range names {
		out = append(out, l.row(in, name, fixtures, depth, now))
	}
	return out
}

// referenceCorpus is the standard a row has to have met before its amounts may
// be ranked against the others, in two parts: the largest fixture SET any row
// was priced on, and the DEPTH — priced runs per fixture — the most thorough row
// reached on each of them.
//
// The set is a set, not a count. Keyed on the count — which is what this was —
// two models that each completed two of four fixtures AND FAILED DIFFERENT ONES
// both report Covered=2 against a peer coverage of 2, both pass as comparable,
// and their $/DEFECT figures describe disjoint corpora. That is the same "priced
// on an easier subset" error the coverage check was added to catch, wearing a
// number that matches, and it is reachable by ordinary flakiness rather than by
// anything adversarial: one timeout each on different fixtures produces it.
//
// The depth is per fixture and is the MAXIMUM any row reached there, rather than
// one chosen row's profile. What it defends against is a row assembled from the
// runs that went well: being priced on a fixture ONCE satisfies the set, so a
// reviewer whose barren runs happen not to report usage is priced only on the
// runs that found something. The comparison a reader makes is between rows, so
// the standard is what a peer actually achieved on that fixture — no synthetic
// number, and no row is asked for work none of its peers managed.
//
// Ties on set size are broken on the sorted fixture list rather than on map
// order, so two runs of the same battery choose the same reference and print the
// same table. Where two rows hold equal-sized different sets, at most one of them
// can contain the reference, so the pair is never declared mutually comparable.
// The caller holds the lock.
func (l *CostLedger) referenceCorpus(in map[string]*modelSpend) (fixtures []string, depth map[string]int) {
	depth = map[string]int{}

	for _, spend := range in {
		names := make([]string, 0, len(spend.priced))
		for fixture, runs := range spend.priced {
			names = append(names, fixture)
			depth[fixture] = max(depth[fixture], runs)
		}
		sort.Strings(names)

		switch {
		case len(names) > len(fixtures):
			fixtures = names
		case len(names) == len(fixtures) && len(names) > 0 && strings.Join(names, "\x00") < strings.Join(fixtures, "\x00"):
			fixtures = names
		}
	}

	return fixtures, depth
}

// row builds a row. The caller holds the lock.
func (l *CostLedger) row(in map[string]*modelSpend, model string, reference []string, depth map[string]int, now time.Time) CostRow {
	row := CostRow{Model: model, PeerCoverage: len(reference), asOf: now}

	if spend, ok := in[model]; ok {
		row.Reviews = spend.reviews
		row.Measured = spend.measured
		row.Detected = spend.detected
		row.Planted = spend.planted
		row.Usage = spend.usage
		row.Attempted = len(spend.attempted)
		row.Covered = len(spend.priced)

		for _, fixture := range reference {
			switch runs := spend.priced[fixture]; {
			case runs == 0:
				row.Missing = append(row.Missing, fixture)
			case runs < depth[fixture]:
				row.Shallow = append(row.Shallow, ShortFixture{
					Fixture: fixture, Priced: runs, Peer: depth[fixture],
				})
			}
		}
	}

	row.Price, row.Priced = l.prices.Price(model)
	return row
}

// ShortFixture is a fixture a row was priced on FEWER times than the most
// thorough row in the same ledger, with both counts so a reader can see the gap
// rather than being told there is one.
type ShortFixture struct {
	Fixture string
	Priced  int
	Peer    int
}

// String renders one shortfall for the footnote.
func (s ShortFixture) String() string {
	return fmt.Sprintf("%s (%d of %d)", s.Fixture, s.Priced, s.Peer)
}

// CostRow is one model's spend across a run, and the two ratios a default is
// chosen on.
type CostRow struct {
	Model string

	// Reviews is every review attributed to this model. Measured is how many of
	// them reported complete usage and therefore fed the cost below; the gap
	// between the two is the part of the run that could not be priced.
	Reviews  int
	Measured int

	// Attempted and Covered are DISTINCT FIXTURES, which is what comparability
	// is about — running one model three times over four fixtures and another
	// once over eight leaves the first with more samples and less coverage.
	// PeerCoverage is the size of the reference corpus this row is read against.
	Attempted    int
	Covered      int
	PeerCoverage int

	// Missing names the fixtures in that reference corpus this row was NOT
	// priced on, and is what comparability actually turns on. The count alone
	// cannot see two rows priced on equal numbers of DIFFERENT fixtures; see
	// CostLedger.referenceCorpus.
	Missing []string

	// Shallow names the fixtures this row WAS priced on but fewer times than its
	// most thorough peer. It is the second half of the same defence and it is the
	// half that stops the reading being maxed out: a row priced on every fixture
	// at least once passes Missing, and a reviewer whose empty runs report no
	// usage is then priced on exactly the runs that found something.
	Shallow []ShortFixture

	// Detected counts planted defects found in the MEASURED reviews, and
	// Planted how many those reviews contained. Both are needed because
	// Detected alone is the numerator of a ratio a lazy reviewer wins: see
	// PublishedCostReadings.
	Detected int
	Planted  int

	// Usage is the measured usage those reviews reported.
	Usage TokenUsage

	// Price is the model's rates and Priced whether the table had any. An
	// unpriced model reports unknown rather than zero.
	Price  Price
	Priced bool

	// asOf is when this row was built, which is what the rate's age is measured
	// against. It is stamped by the ledger rather than read from the clock at
	// each call so that every cell in one row — and every row in one table —
	// reports its age against the same instant.
	//
	// A hand-built CostRow leaves it zero and falls back to the wall clock; the
	// alternative is an age of two thousand years on a struct literal.
	asOf time.Time
}

// PriceAgeDays is how old the rate behind this row's amounts is, and whether
// there is a rate at all.
func (r CostRow) PriceAgeDays() (int, bool) {
	if !r.Priced {
		return 0, false
	}

	now := r.asOf
	if now.IsZero() {
		now = time.Now()
	}
	return ageInDays(now, r.Price.CapturedOn), true
}

// Comparable reports whether this row's amounts may be read against the others.
//
// Zero coverage is not comparable because it is not a result at all; otherwise
// the test is that the row covers the whole reference corpus, fixture by fixture
// rather than by count, AND to the depth its peers reached on each.
func (r CostRow) Comparable() bool {
	return r.Covered > 0 && len(r.Missing) == 0 && len(r.Shallow) == 0
}

// Recall is the share of planted defects this row's MEASURED reviews found.
//
// It is a cost method, not a duplicate of the score table's column, because it
// is the other half of the only cost reading this file publishes: it is
// computed over exactly the reviews the dollar amount was computed over, so the
// pair describes one set of runs. Reading the score table's recall — which
// includes reviews that reported no usage and are therefore absent from the
// cost — beside this row's $/DEFECT would pair a numerator and a denominator
// drawn from different corpora, which is the error this whole file exists to
// avoid making with dollars.
func (r CostRow) Recall() (float64, bool) {
	if r.Planted == 0 {
		return 0, false
	}
	return float64(r.Detected) / float64(r.Planted), true
}

// coverageReason states why a row may not be ranked.
//
// It names the fixtures rather than only counting them: "priced on 6 of 8" sends
// a reader looking for which two, and the pair it is missing is usually the pair
// that explains the cheap number beside it.
func (r CostRow) coverageReason() string {
	switch {
	case r.Covered == 0:
		// Reached when NO row in the ledger was priced on anything, so there is
		// no reference corpus to be missing fixtures from. Without this case the
		// cell carried a "*" and no footnote, which is the one combination a
		// reader cannot act on.
		return fmt.Sprintf("%s was priced on no fixture at all in %d review(s): its cost is not low, it is absent",
			r.Model, r.Reviews)

	case len(r.Missing) > 0:
		return fmt.Sprintf("%s was priced on %d of the %d fixture(s) its peers were (missing %s): its cost describes a smaller, easier corpus and must not be ranked against them",
			r.Model, r.Covered, r.PeerCoverage, strings.Join(r.Missing, ", "))

	case len(r.Shallow) > 0:
		// Deliberately worded as SELECTION rather than as thin sampling. Fewer
		// priced runs on a fixture its peers completed is not merely a smaller
		// sample: the runs that dropped out are the ones whose usage went
		// unreported, and nothing here can rule out that they are also the ones
		// that found nothing. Read as "n=2 instead of n=3" a reader shrugs; read
		// as "priced on the runs that went well" they do not.
		short := make([]string, 0, len(r.Shallow))
		for _, s := range r.Shallow {
			short = append(short, s.String())
		}
		return fmt.Sprintf("%s covers every fixture but was priced on fewer runs of %s than its peers were: "+
			"the runs missing from its cost are the ones that reported no usage, which is a subset it did not "+
			"choose at random, so its RECALL and $/DEFECT describe the runs that went well and must not be ranked",
			r.Model, strings.Join(short, ", "))
	}

	return ""
}

// qualify attaches to a known amount everything a reader needs before acting on
// it: whether it may be ranked against its neighbours, and how old the rate
// behind it is.
//
// Every amount this row publishes goes through here, which is the point. The
// age used to be added by the table's formatter, so it existed only for a
// reader of that one table.
func (r CostRow) qualify(usd float64) Cost {
	cost := knownCost(usd)
	if !r.Comparable() {
		cost = incomparableCost(usd, "%s", r.coverageReason())
	}

	if days, ok := r.PriceAgeDays(); ok {
		cost = cost.dated(r.Model, days)
	}
	return r.banded(cost)
}

// banded attaches the routing interval to an amount.
//
// The scaling is exact rather than approximate: Total, PerReview and PerDefect
// are all the same total divided by a count, so the band divides by the same
// count. Computing a separate band per ratio would recompute the same quotient
// with more chances to get one of them wrong.
func (r CostRow) banded(c Cost) Cost {
	if !r.Priced || !c.Known {
		return c
	}

	low, high, ok := r.Price.Band(r.Usage)
	if !ok {
		if r.Price.Routing.Endpoints > 1 && r.Usage.Calls() > 0 {
			c.BandReason = fmt.Sprintf("%s is served by %d endpoints and at least one call was billed above "+
				"its base tier; the recorded band is base rates only, so no interval is claimed for this row",
				r.Model, r.Price.Routing.Endpoints)
		}
		return c
	}
	if high <= low {
		// One endpoint, or several charging the same. The amount is exact and
		// says so by carrying no marker — which is the distinction that makes
		// the marker mean something on the rows that do.
		return c
	}

	total := r.Price.Cost(r.Usage)
	if total <= 0 {
		return c
	}

	scale := c.USD / total
	c.Low, c.High, c.Banded = low*scale, high*scale, true
	c.BandReason = fmt.Sprintf("%s costs $%.6f to $%.6f here depending on which of its %d endpoints served the "+
		"request (cheapest %s, dearest %s); the printed amount is the %s rate. open-nitpick pins no provider, "+
		"so nothing records which one it was — pin one to collapse this to a single number",
		r.Model, c.Low, c.High, r.Price.Routing.Endpoints,
		r.Price.Routing.Cheapest, r.Price.Routing.Dearest, r.Price.Endpoint)
	return c
}

// Total is what this model's measured reviews cost in total.
func (r CostRow) Total() Cost {
	if !r.Priced {
		return unknownCost("no price entry for %s; add one to the price file", r.Model)
	}
	if r.Measured == 0 {
		if r.Reviews == 0 {
			return unknownCost("no reviews recorded for %s", r.Model)
		}
		return unknownCost("no usage reported by %s in %d review(s)", r.Model, r.Reviews)
	}
	return r.qualify(r.Price.Cost(r.Usage))
}

// PerReview is what one review by this model costs — the price of running the
// tool once, judge excluded.
func (r CostRow) PerReview() Cost {
	total := r.Total()
	if !total.Known {
		return total
	}
	return r.qualify(total.USD / float64(r.Measured))
}

// PerDefect is what this model costs per planted defect it actually found.
//
// This is the number a default is chosen on, and it is the reason cost per
// review is not: a model at a fifth of the price that finds half as much is not
// cheaper, and only this ratio says so. When nothing was detected it is
// UNDEFINED — a division by zero, not an infinite cost, and not the free lunch
// a zero would read as.
//
// It is also the number with a degenerate maximum, which is what Comparable
// defends. The cheapest possible cost per defect is bought by failing every run
// that is expensive or hard and completing only the cheap ones the model finds
// easy: the surviving runs are priced correctly, the ratio is computed
// correctly, and the answer is a lie about the corpus. Coverage is the only
// thing in the row that can see it.
func (r CostRow) PerDefect() Cost {
	total := r.Total()
	if !total.Known {
		return total
	}
	if r.Detected == 0 {
		return unknownCost("undefined: %s detected no planted defect in %d measured review(s)", r.Model, r.Measured)
	}
	return r.qualify(total.USD / float64(r.Detected))
}

// CostReading is one READING the cost table publishes — the set of columns that
// must be read together to mean anything.
//
// It is the same instrument PublishedMetric is, applied to the cost columns,
// and it exists for the same reason: a column nobody asked "what maximises
// this?" of is how the withdrawn banded severity score shipped. Cost has its own
// type rather than reusing PublishedMetric because the two score different
// objects — a CorpusTally has no dollars in it and a CostRow has no judge
// verdicts — but the contract is deliberately identical, down to Maxes treating
// a TIE as a failure.
//
// The answer for cost turned out to be the same shape as the answer for
// severity. Asked of $/DEFECT alone, "what maximises this?" has an ugly answer:
// a reviewer that does the least work that still lands one cheap hit. Its
// dollars are real, its detections are real, the division is correct, and it
// ranks first. So $/DEFECT is not a metric — the PAIR ($/DEFECT, RECALL) is,
// and this registry is what stops the pair being split back apart by whoever
// next wants a single number to sort a table on.
type CostReading struct {
	// Name is how the reading is referred to in a failure message.
	Name string

	// Columns are the headings it is printed as, so a failure can name the
	// column a reader would have quoted.
	Columns []string

	// Doc says what the reading claims to measure. A degenerate strategy that
	// maxes it out is a counterexample to exactly this sentence.
	Doc string

	// Score returns the components ORIENTED SO THAT HIGHER IS BETTER, so
	// "maxed out" is componentwise >= without each caller re-deriving which way
	// a dollar amount points. ok is false when the row gives the reading nothing
	// to measure — including when the row is NOT COMPARABLE, which is the whole
	// defence against the strategy that quits on the expensive fixtures.
	Score func(CostRow) (components []float64, ok bool)
}

// Maxes reports whether a strategy's row is at least as good as a reference row
// on EVERY component.
//
// The reference is a reviewer that actually works, so this answers "did this
// strategy do as well as being useful?". A TIE counts as maxing it out: a
// degenerate strategy the reading cannot tell apart from a useful one has
// already broken it, and a tie is exactly what the withdrawn banded severity
// column scored.
func (c CostReading) Maxes(strategy, reference CostRow) (maxed, comparable bool) {
	got, ok := c.Score(strategy)
	if !ok {
		return false, false
	}

	want, ok := c.Score(reference)
	if !ok || len(got) != len(want) {
		return false, false
	}

	for i := range got {
		if got[i] < want[i] {
			return false, true
		}
	}
	return true, true
}

// PublishedCostReadings is every score the cost table publishes.
//
// COST and TOKENS are absent because they are not scores: a row is not better
// for having spent less in total than a row that reviewed a different number of
// fixtures, and the table prints them as evidence for the ratios rather than as
// a ranking. REVIEWS, PRICED, COV, FAILED, DEFECTS and AGE are descriptive for
// the same reason — COV in particular is not an achievement, it is the
// precondition under which the ratios may be read at all.
func PublishedCostReadings() []CostReading {
	return []CostReading{
		{
			Name: "cost effectiveness",

			// Every column named here is scored below, and that is a rule rather
			// than tidiness. $/REVIEW was listed as part of this reading and left
			// out of Score, so the degenerate table never asked what maximises
			// it — and the answer is "a review that does nothing", which is the
			// same unasked question that shipped the banded severity column.
			Columns: []string{"$/DEFECT", "$/REVIEW", "RECALL"},
			Doc: "what one located defect and one whole review cost, read against how much of " +
				"what was planted the same priced reviews actually found",
			Score: func(r CostRow) ([]float64, bool) {
				// An incomparable row is not scored at all. Its dollars are
				// correct and describe a smaller, easier corpus, which is
				// precisely the shape of the cheapest possible answer here.
				if !r.Comparable() {
					return nil, false
				}

				perDefect := r.PerDefect()
				perReview := r.PerReview()
				if !perDefect.Known || !perReview.Known {
					return nil, false
				}

				// Recall is the component that makes this a reading rather than
				// a column. Without it, "one cheap hit and stop" is the optimum.
				recall, ok := r.Recall()
				if !ok {
					return nil, false
				}

				// Negated so that higher is better in every position: cheaper is
				// better, and more of the corpus found is better.
				return []float64{-perDefect.USD, -perReview.USD, recall}, true
			},
		},
	}
}

// DescriptiveCostColumns identify a row, state how much measurement is behind
// it, or disclose a limit on it. None of them is a score.
//
// The distinction is the whole mechanism: a column here is never asked what
// maximises it, so putting a score here by mistake is how one escapes the
// degenerate-strategy table. COST and TOKENS are the clearest case — a row is
// not better for having spent less in total than a row that reviewed a
// different number of fixtures — and COV is not an achievement but the
// precondition under which the ratios may be compared at all.
func DescriptiveCostColumns() []string {
	return []string{
		"MODEL", "REVIEWS", "PRICED", "COV", "FAILED", "DEFECTS", "TOKENS", "COST", "SPREAD", "AGE",
	}
}

// TierUse is how many calls this row was billed at one tier.
type TierUse struct {
	// MinPromptTokens is 0 for the base tier.
	MinPromptTokens int
	Calls           int
}

// Tiers reports which published tiers this row's calls actually landed in.
//
// It is reported rather than assumed because the assumption is where the last
// version of this file went wrong: it recorded base rates with a comment
// asserting every review was billed a tier up, and nobody had measured a prompt.
func (r CostRow) Tiers() []TierUse {
	if !r.Priced {
		return nil
	}

	counts := map[int]int{}
	for _, c := range r.Usage.PerCall {
		tier := 0
		for _, t := range r.Price.Tiers {
			if c.PromptSize() < t.MinPromptTokens {
				break
			}
			tier = t.MinPromptTokens
		}
		counts[tier]++
	}

	out := make([]TierUse, 0, len(counts))
	for threshold, calls := range counts {
		out = append(out, TierUse{MinPromptTokens: threshold, Calls: calls})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MinPromptTokens < out[j].MinPromptTokens })
	return out
}

// tierNote describes where this row sat relative to its published tiers, for a
// reader deciding whether the headline rate is the operative one.
func (r CostRow) tierNote() string {
	if !r.Priced || len(r.Price.Tiers) == 0 || r.Usage.Calls() == 0 {
		return ""
	}

	used := r.Tiers()
	if len(used) == 1 && used[0].MinPromptTokens == 0 {
		return fmt.Sprintf("%s: all %d call(s) billed at the base rate; largest prompt %d tokens, first step at %d",
			r.Model, used[0].Calls, r.Usage.LargestPrompt(), r.Price.Tiers[0].MinPromptTokens)
	}

	parts := make([]string, 0, len(used))
	for _, u := range used {
		where := "base"
		if u.MinPromptTokens > 0 {
			where = fmt.Sprintf("the %d-token tier", u.MinPromptTokens)
		}
		parts = append(parts, fmt.Sprintf("%d at %s", u.Calls, where))
	}
	return fmt.Sprintf("%s: %s; largest prompt %d tokens",
		r.Model, strings.Join(parts, ", "), r.Usage.LargestPrompt())
}

// ComparabilityNotes lists the rows that may not be ranked against the rest,
// and why.
//
// Separate from Table so a caller that fails a run on them can, following the
// shape the judged table already uses: zero coverage is not a weak result, it
// is no result, and a table containing such a row is not the comparison its
// caption claims.
func (l *CostLedger) ComparabilityNotes() []string {
	var notes []string
	for _, r := range l.Rows() {
		switch {
		case r.Covered == 0 && r.Reviews > 0:
			notes = append(notes, fmt.Sprintf("NO RESULT: %s was attempted on %d fixture(s) and priced on none; "+
				"its cost is not cheap, it is absent", r.Model, r.Attempted))
		case !r.Comparable():
			notes = append(notes, "NOT COMPARABLE: "+r.coverageReason())
		}
	}
	return notes
}

// OrderingNotes names the pairs of rows a reader must NOT order on $/DEFECT
// because their routing bands overlap.
//
// This is the half of the routing disclosure that does work rather than
// informing. A footnote saying "this amount is one point in a 3.9x band" leaves
// the reader to intersect two intervals by eye across a wide table, and the
// habit the whole cost block is fighting is that they will not — they will sort
// the column. So the intersection is done here, and the pairs that fail it are
// printed under the table beside the amounts themselves.
//
// Only ADJACENT pairs in the sorted order are reported. A reader draws a
// conclusion from a row and the one above it; reporting every overlapping pair
// in a 17-model battery would be a wall of text that is skipped whole, and
// overlap is not transitive, so the adjacent list is where a false ordering
// actually gets made. Rows with no band, or an unknown or incomparable
// $/DEFECT, are left out: their reason for not being rankable is already stated
// somewhere else and repeating it here would bury this one.
func (l *CostLedger) OrderingNotes() []string {
	type banded struct {
		model string
		cost  Cost
	}

	var rows []banded
	for _, r := range l.Rows() {
		c := r.PerDefect()
		if !c.Known || !c.Comparable {
			continue
		}
		rows = append(rows, banded{r.Model, c})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].cost.USD < rows[j].cost.USD })

	var notes []string
	for i := 1; i < len(rows); i++ {
		lower, upper := rows[i-1], rows[i]

		// An unbanded amount is a point, which the interval test handles
		// unchanged: a point overlaps an interval that contains it.
		lowHigh, upHigh := lower.cost.High, upper.cost.High
		lowLow, upLow := lower.cost.Low, upper.cost.Low
		if !lower.cost.Banded {
			lowLow, lowHigh = lower.cost.USD, lower.cost.USD
		}
		if !upper.cost.Banded {
			upLow, upHigh = upper.cost.USD, upper.cost.USD
		}
		if lowHigh <= upLow {
			continue
		}

		notes = append(notes, fmt.Sprintf("NOT ORDERABLE: %s ($%.6f, band $%.6f-$%.6f) reads cheaper per defect "+
			"than %s ($%.6f, band $%.6f-$%.6f), but the bands overlap — the ordering is a fact about which "+
			"endpoints the router happened to pick, not about the models",
			lower.model, lower.cost.USD, lowLow, lowHigh,
			upper.model, upper.cost.USD, upLow, upHigh))
	}
	return notes
}

// CostTableHeader is the cost block's header.
//
// It is a package-level const, beside the other published table headers, so a
// guard running in the DEFAULT build can check that every column in it is
// declared — the reports themselves are behind the `eval` tag, where no
// ordinary `go test ./...` would reach them. That is the mechanism that would
// have stopped the withdrawn banded severity columns being added to a header
// and a legend with nothing anywhere asking what maximised them.
//
// PRICED is the number of reviews that fed the cost, and COV the number of
// distinct fixtures behind them. Both are printed beside REVIEWS rather than
// hidden: when PRICED and REVIEWS disagree every cost cell on the row describes
// a subset of the run, and when COV disagrees between rows the rows describe
// different corpora. FAILED is calls that errored, whose charge — if the
// provider raised one — is not visible from here at all, which makes the cost on
// that row a lower bound. RECALL is printed immediately left of $/DEFECT and not
// at the far end of the row, because the two are one reading and a reader who
// has to look for the second half will not.
// SPREAD is the width of the model's routing band, printed beside the amounts
// rather than only in a footnote: it is the difference between a figure that is
// exact and one that could be several times either way, and a reader comparing
// two rows needs it in the same glance as the dollars.
const CostTableHeader = "MODEL                                REVIEWS  PRICED  COV  FAILED  DEFECTS  TOKENS     COST        $/REVIEW    RECALL   $/DEFECT     SPREAD     AGE"

// CostReadingLegend is printed under the cost table, for the same reason
// SeverityColumnLegend is printed under the severity ones: the number a reader
// most wants to sort on is the one that must not be read alone.
const CostReadingLegend = "$/DEFECT IS NOT A RANKING ON ITS OWN. It is minimised by a reviewer that does the " +
	"least work that still lands one cheap hit — its dollars are real, its detections are real, and it " +
	"would head this table. Read it ONLY beside RECALL, which is computed over exactly the PRICED " +
	"reviews the dollar amounts came from, and ONLY between rows with equal COV AND equal PRICED: a model " +
	"priced on the fixtures it did not fail describes a smaller, easier corpus, and one priced on fewer " +
	"RUNS of a fixture its peers completed describes the runs that went well — the dropped runs are the " +
	"ones that reported no usage, which is not a random subset. Either marks the cost * and it must not " +
	"be ranked. An amount marked ! was priced from a rate old enough to recapture. " +
	"AN AMOUNT MARKED ~ IS ONE POINT INSIDE A ROUTING BAND. OpenRouter serves most of this battery from " +
	"several endpoints at different rates — SPREAD is how many times dearer the dearest is — and " +
	"open-nitpick pins no provider, so which one served a request is unrecorded. Two such amounts may be " +
	"ordered only if their bands are disjoint; the pairs that fail that test are listed above as NOT " +
	"ORDERABLE. The five qwen rows have a spread of 1.0x and are exact, which is why the marker matters: " +
	"the column mixes exact figures with figures that could be several times either way. Pin a provider " +
	"and the band collapses. " +
	"EVERY AMOUNT HERE IS A LOWER BOUND, by two stated blind spots: a call that errored (FAILED) may " +
	"still have been billed for a response that was never delivered, and the meter sits outside the " +
	"SDK's retry loop, so a request that succeeded on its second attempt contributes only the second " +
	"attempt's tokens. Neither is knowable from a response, and neither is filled in from a guess. " +
	"See PublishedCostReadings."

// Table renders the cost block a report prints under its score table.
//
// Formatting lives here rather than at each call site because the decisions in
// it are the accounting, not presentation: which cell says "unknown", where the
// reason for it goes, and that the judge gets its own block instead of a row
// among the contenders.
func (l *CostLedger) Table() string { return l.tableAt(time.Now()) }

// tableAt takes the clock as an argument so the printed ages are testable.
func (l *CostLedger) tableAt(now time.Time) string {
	rows := l.rowsAt(l.models, now)
	judges := l.rowsAt(l.judges, now)
	if len(rows) == 0 && len(judges) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(l.prices.provenanceAt(now))
	b.WriteString("\n\n")

	b.WriteString(CostTableHeader + "\n")
	b.WriteString(strings.Repeat("-", len(CostTableHeader)) + "\n")

	var notes []string
	age := func(r CostRow) string {
		days, ok := r.PriceAgeDays()
		if !ok {
			return "—"
		}
		return describeAge(days)
	}

	spread := func(r CostRow) string {
		if !r.Priced || r.Price.Routing.Endpoints == 0 {
			return "—"
		}
		return fmt.Sprintf("%.1fx/%d", r.Price.Routing.Spread(), r.Price.Routing.Endpoints)
	}

	// A judge has no defect count and no per-defect ratio: it detects nothing,
	// and a dash is the honest cell for a ratio that does not exist rather than
	// one that is merely unknown. Its PerDefect reason is dropped with it, or
	// the footnotes would carry "the judge detected no planted defect" as though
	// that were a shortcoming.
	writeRow := func(r CostRow, rated bool) {
		defects, perDefect, recall := "—", "—", "—"
		qualified := []Cost{r.Total(), r.PerReview()}
		if rated {
			defects, perDefect = fmt.Sprint(r.Detected), r.PerDefect().String()
			qualified = append(qualified, r.PerDefect())

			// Blank rather than 0.00 when nothing was planted: zero recall and
			// no corpus to have recall over are different states, and the second
			// one printed as the first is how "found nothing" becomes a score.
			if share, ok := r.Recall(); ok {
				recall = fmt.Sprintf("%d/%d %.2f", r.Detected, r.Planted, share)
			}
		}

		fmt.Fprintf(&b, "%-36s %-8d %-7d %-4d %-7d %-8s %-10d %-11s %-11s %-8s %-12s %-10s %s\n",
			truncate(r.Model, 36), r.Reviews, r.Measured, r.Covered, r.Usage.Failed, defects,
			r.Usage.Total(), r.Total(), r.PerReview(), recall, perDefect, spread(r), age(r))

		for _, c := range qualified {
			notes = append(notes, c.Notes()...)
		}
		if note := r.tierNote(); note != "" {
			notes = append(notes, "tiers: "+note)
		}
	}

	for _, r := range rows {
		writeRow(r, true)
	}

	if len(judges) > 0 {
		b.WriteString("\n")
		// Rule 12 in docs/measurement.md. Printed every time, because a judge
		// row that merely sat below a rule separator would eventually be read
		// as part of the total above it.
		b.WriteString("JUDGE — a measurement expense, not part of what running this tool costs a user:\n")
		for _, r := range judges {
			writeRow(r, false)
		}
	}

	// Every qualified cell above says why, once. A table full of unknowns with
	// no reasons is indistinguishable from a broken instrument.
	seen := map[string]bool{}
	notes = append(notes, l.ComparabilityNotes()...)
	notes = append(notes, l.OrderingNotes()...)
	for _, note := range notes {
		if seen[note] {
			continue
		}
		seen[note] = true
		fmt.Fprintf(&b, "  %s\n", note)
	}

	// Printed every time, not only when a row looks suspicious: the reading this
	// warns about is broken by a reader's habits rather than by any particular
	// row, and a caveat that appears only sometimes teaches a reader that its
	// absence means the column is safe to sort on.
	b.WriteString("\n" + CostReadingLegend + "\n")

	return b.String()
}
