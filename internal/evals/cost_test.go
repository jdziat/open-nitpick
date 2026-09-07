package evals

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	llms "github.com/nocturnium/llm-go-sdk/v6"

	"github.com/jdziat/open-nitpick/internal/llm"
	"github.com/jdziat/open-nitpick/internal/review"
)

// The cost block had NO test coverage at all when this file was written, which
// is the reason it reads as it does: every test here pins a decision that was
// already made in cost.go and that nothing could have stopped being reversed.
// Two rounds of cost numbers were withdrawn for errors, a dropped tier ladder,
// then a per-endpoint rate read as a per-model one, and both were the kind a
// test catches and a reading does not.

// mustPrices parses a price table or fails, for the many tests below that vary
// one field of a valid one.
func mustPrices(t *testing.T, yaml string) *PriceTable {
	t.Helper()

	table, err := parsePrices([]byte(yaml), "testdata/prices_test.yaml")
	if err != nil {
		t.Fatalf("parsing the test price table: %v", err)
	}
	return table
}

// priceEntry renders one valid entry with a single endpoint, so a test can state
// the one thing it is about instead of restating a schema.
//
// Single-endpoint deliberately: it is the only shape where the recorded amount
// is exact, so a test about tiers or staleness is not also a test about bands.
func priceEntry(model, captured string, input, output float64, extra ...string) string {
	return fmt.Sprintf(`  %q:
    captured_on: %q
    source: "https://openrouter.ai/api/v1/models"
    endpoint: "only"
    input: %v
    output: %v
%s    routing:
      source: "https://openrouter.ai/api/v1/models/%s/endpoints"
      endpoints: 1
      cheapest: "only"
      dearest: "only"
      min_input: %v
      max_input: %v
      min_output: %v
      max_output: %v
      min_cache_read: %v
      max_cache_read: %v
      min_cache_write: %v
      max_cache_write: %v
`, model, captured, input, output, strings.Join(extra, ""), model,
		input, input, output, output, input, input, input, input)
}

// -----------------------------------------------------------------------------
// The shipped table, against the receipt beside it.
// -----------------------------------------------------------------------------

// vendorReceipt is testdata/pricing-source.json: the vendor's own response,
// captured verbatim, from which pricing.yaml is re-derived below.
type vendorReceipt struct {
	Note   []string `json:"_note"`
	Models map[string]struct {
		CapturedOn      string           `json:"captured_on"`
		Source          string           `json:"source"`
		EndpointsSource string           `json:"endpoints_source"`
		Pricing         map[string]any   `json:"pricing"`
		Endpoints       []vendorEndpoint `json:"endpoints"`
	} `json:"models"`
}

type vendorEndpoint struct {
	Tag      string         `json:"tag"`
	Provider string         `json:"provider_name"`
	Pricing  map[string]any `json:"pricing"`
}

// perMillion reads one of the vendor's per-TOKEN rates as per-million.
//
// It SHIFTS THE DECIMAL POINT rather than multiplying, which is what makes every
// check below exact. Multiplying by 1e6 in float64 does not round-trip:
// 0.00000003 * 1e6 is 0.030000000000000002, which is not the 0.03 pricing.yaml
// records, so a comparison written that way needs a tolerance, and a tolerance
// wide enough to absorb that is a tolerance nobody can justify against the 1.015x
// errors this table has shipped. Shifting first means the comparison is
// plain equality and there is no threshold to argue about.
//
// An exponent is refused rather than handled: the vendor publishes plain
// decimals, and the day that changes is a day somebody should look rather than
// one where a parser quietly does its best.
func perMillion(p map[string]any, key string) (float64, bool) {
	raw, ok := p[key]
	if !ok {
		return 0, false
	}

	text, ok := raw.(string)
	if !ok {
		// A JSON number has already been through float64 and cannot be shifted
		// exactly. The receipt records rates as strings, as the vendor sends
		// them, so this is a capture that lost precision before it was written.
		return 0, false
	}
	if strings.ContainsAny(text, "eE") {
		return 0, false
	}

	negative := strings.HasPrefix(text, "-")
	text = strings.TrimPrefix(text, "-")

	whole, frac, _ := strings.Cut(text, ".")
	for len(frac) < 6 {
		frac += "0"
	}
	whole, frac = whole+frac[:6], frac[6:]

	shifted := strings.TrimLeft(whole, "0")
	if shifted == "" {
		shifted = "0"
	}
	if frac != "" {
		shifted += "." + frac
	}
	if negative {
		shifted = "-" + shifted
	}

	value, err := strconv.ParseFloat(shifted, 64)
	if err != nil {
		return 0, false
	}
	return value, true
}

// TestPerMillionShiftsExactly pins the helper the re-derivation rests on, since
// a broken one would make every comparison it feeds pass vacuously.
func TestPerMillionShiftsExactly(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want float64
	}{
		{"0.00000003", 0.03},
		{"0.0000015", 1.5},
		{"0.000001", 1},
		{"0.0000000833333333333333", 0.0833333333333333},
		{"0.0000000058", 0.0058},
		{"0", 0},
	} {
		got, ok := perMillion(map[string]any{"r": tc.raw}, "r")
		if !ok {
			t.Errorf("%s did not parse", tc.raw)
			continue
		}
		if got != tc.want {
			t.Errorf("%s shifted to %v, want exactly %v", tc.raw, got, tc.want)
		}
	}

	if _, ok := perMillion(map[string]any{"r": "3e-8"}, "r"); ok {
		t.Error("an exponent form was accepted; it cannot be shifted by moving digits and should be " +
			"noticed rather than approximated")
	}
	if _, ok := perMillion(map[string]any{"r": 0.00000003}, "r"); ok {
		t.Error("a JSON number was accepted: it has already been through float64 and cannot be " +
			"shifted exactly, so the receipt that produced it lost precision at capture")
	}
}

func readReceipt(t *testing.T) vendorReceipt {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("testdata", "pricing-source.json"))
	if err != nil {
		t.Fatalf("reading the captured vendor response: %v", err)
	}

	var receipt vendorReceipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		t.Fatalf("decoding the captured vendor response: %v", err)
	}
	if len(receipt.Models) == 0 {
		t.Fatal("the captured vendor response prices no models, so every check against it is vacuous")
	}
	return receipt
}

// endpointRates applies one endpoint's own input fallback for the cache rates,
// which is what makes a band bound what could be BILLED rather than what happens
// to be published.
func endpointRates(t *testing.T, model, tag string, p map[string]any) Rates {
	t.Helper()

	input, ok := perMillion(p, "prompt")
	if !ok {
		t.Fatalf("%s endpoint %s publishes no prompt rate", model, tag)
	}
	output, ok := perMillion(p, "completion")
	if !ok {
		t.Fatalf("%s endpoint %s publishes no completion rate", model, tag)
	}

	rates := Rates{Input: input, Output: output, CacheRead: input, CacheWrite: input}
	if v, ok := perMillion(p, "input_cache_read"); ok {
		rates.CacheRead = v
	}
	if v, ok := perMillion(p, "input_cache_write"); ok {
		rates.CacheWrite = v
	}
	return rates
}

// TestShippedPricesMatchTheCapturedVendorResponse re-derives every shipped rate
// from the receipt.
//
// pricing.yaml is a CLAIM and pricing-source.json is the evidence for it. Both
// errors this table has shipped were transcription or scope errors invisible to
// a reader of the claim alone: round 1 dropped `.pricing.overrides`, so eleven
// models lost every tier they are billed at, and round 2 recorded ONE endpoint's
// rates as the model's. Re-deriving is the only check that sees either, because
// each individual number in the wrong file was correct.
func TestShippedPricesMatchTheCapturedVendorResponse(t *testing.T) {
	receipt := readReceipt(t)

	table, err := Prices()
	if err != nil {
		t.Fatalf("loading the shipped price table: %v", err)
	}

	for model, price := range table.Models {
		captured, ok := receipt.Models[model]
		if !ok {
			t.Errorf("pricing.yaml prices %s and the receipt has no block for it: the rate is a claim "+
				"with the evidence for it missing, which is the state both withdrawn tables were in", model)
			continue
		}

		t.Run(model, func(t *testing.T) {
			if price.CapturedOn.Format(time.DateOnly) != captured.CapturedOn {
				t.Errorf("captured_on is %s in pricing.yaml and %s in the receipt: the two were not "+
					"refreshed together, so one describes rates the other does not",
					price.CapturedOn.Format(time.DateOnly), captured.CapturedOn)
			}
			if price.Source != captured.Source {
				t.Errorf("source is %q in pricing.yaml and %q in the receipt", price.Source, captured.Source)
			}

			// Base rates. Note which side is authoritative: a rate ABSENT from
			// the vendor block must be absent from the entry too, because
			// recording one there turns a fallback-to-input into a published
			// cache rate that nobody published.
			for _, c := range []struct {
				name string
				key  string
				have float64
				set  bool
			}{
				{"input", "prompt", price.Input, true},
				{"output", "completion", price.Output, true},
				{"cache_read", "input_cache_read", price.CacheRead, price.CacheRead != 0 || price.cacheReadFree},
				{"cache_write", "input_cache_write", price.CacheWrite, price.CacheWrite != 0 || price.cacheWriteFree},
			} {
				want, published := perMillion(captured.Pricing, c.key)
				switch {
				case published && !c.set:
					t.Errorf("the vendor publishes %s at %v/M and pricing.yaml records none, so those "+
						"tokens fall back to the input rate", c.name, want)
				case !published && c.set:
					t.Errorf("pricing.yaml records %s at %v/M and the vendor publishes none: a rate that "+
						"exists only in our file is the shape of a guess", c.name, c.have)
				case published && c.have != want:
					t.Errorf("%s is %v/M in pricing.yaml and %v/M in the receipt", c.name, c.have, want)
				}
			}

			// The 1-hour cache-write rate is recorded to be REFUSED on rather
			// than billed, and it is checked here for the same reason every
			// other rate is: an entry that quietly stopped carrying it would
			// start silently billing cache writes at the cheaper of two rates.
			alt, published := perMillion(captured.Pricing, "input_cache_write_1h")
			switch {
			case published && !price.cacheWrite1hKnown:
				t.Errorf("the vendor publishes a 1-hour cache-write rate (%v/M) and pricing.yaml records "+
					"none: a cache write on this model would be billed at the 5-minute rate, which is "+
					"the cheaper of two amounts nothing can choose between", alt)
			case !published && price.cacheWrite1hKnown:
				t.Errorf("pricing.yaml records cache_write_1h at %v/M and the vendor publishes none",
					price.CacheWrite1h)
			case published && price.CacheWrite1h != alt:
				t.Errorf("cache_write_1h is %v/M in pricing.yaml and %v/M in the receipt",
					price.CacheWrite1h, alt)
			}

			// Tiers, in order, resolved the way the vendor encodes them: a tier
			// inherits every rate it does not restate.
			overrides, _ := captured.Pricing["overrides"].([]any)
			if len(price.Tiers) != len(overrides) {
				t.Fatalf("pricing.yaml records %d tier(s) and the vendor publishes %d. Round 1 shipped a "+
					"table whose capture recipe dropped this key entirely",
					len(price.Tiers), len(overrides))
			}
			for i, raw := range overrides {
				o, _ := raw.(map[string]any)
				threshold, _ := o["min_prompt_tokens"].(float64)
				tier := price.Tiers[i]

				if float64(tier.MinPromptTokens) != threshold {
					t.Errorf("tier %d takes over at %d in pricing.yaml and %v in the receipt",
						i, tier.MinPromptTokens, threshold)
				}
				for _, c := range []struct {
					name string
					key  string
					have float64
					base float64
				}{
					{"input", "prompt", tier.Input, price.Input},
					{"output", "completion", tier.Output, price.Output},
					{"cache_read", "input_cache_read", tier.CacheRead, price.CacheRead},
					{"cache_write", "input_cache_write", tier.CacheWrite, price.CacheWrite},
				} {
					want, published := perMillion(o, c.key)
					if !published {
						// Inheritance, not absence. gemini-3.1-pro-preview's
						// 200,000 tier raises three rates and says nothing about
						// cache_write, which therefore stays at the base rate;
						// restating it in our file would invent a number.
						want = c.base
					}
					if c.have != want {
						t.Errorf("tier %d %s is %v/M in pricing.yaml and %v/M resolved from the receipt",
							i, c.name, c.have, want)
					}
				}
			}

			// Routing: the band, the count, and the two labels.
			if got := price.Routing.Endpoints; got != len(captured.Endpoints) {
				t.Errorf("routing declares %d endpoint(s) and the receipt captured %d",
					got, len(captured.Endpoints))
			}
			if price.Routing.Source != captured.EndpointsSource {
				t.Errorf("routing source is %q and the receipt was fetched from %q",
					price.Routing.Source, captured.EndpointsSource)
			}

			var low, high Rates
			var recorded []string
			for i, e := range captured.Endpoints {
				rates := endpointRates(t, model, e.Tag, e.Pricing)
				if i == 0 {
					low, high = rates, rates
				}
				low = Rates{
					Input:      math.Min(low.Input, rates.Input),
					Output:     math.Min(low.Output, rates.Output),
					CacheRead:  math.Min(low.CacheRead, rates.CacheRead),
					CacheWrite: math.Min(low.CacheWrite, rates.CacheWrite),
				}
				high = Rates{
					Input:      math.Max(high.Input, rates.Input),
					Output:     math.Max(high.Output, rates.Output),
					CacheRead:  math.Max(high.CacheRead, rates.CacheRead),
					CacheWrite: math.Max(high.CacheWrite, rates.CacheWrite),
				}
				if rates.Input == price.Input && rates.Output == price.Output {
					recorded = append(recorded, e.Tag)
				}
			}

			for _, c := range []struct {
				name string
				have float64
				want float64
				side string
			}{
				{"input", price.Routing.Low.Input, low.Input, "min"},
				{"output", price.Routing.Low.Output, low.Output, "min"},
				{"cache_read", price.Routing.Low.CacheRead, low.CacheRead, "min"},
				{"cache_write", price.Routing.Low.CacheWrite, low.CacheWrite, "min"},
				{"input", price.Routing.High.Input, high.Input, "max"},
				{"output", price.Routing.High.Output, high.Output, "max"},
				{"cache_read", price.Routing.High.CacheRead, high.CacheRead, "max"},
				{"cache_write", price.Routing.High.CacheWrite, high.CacheWrite, "max"},
			} {
				if c.have != c.want {
					t.Errorf("routing %s_%s is %v/M in pricing.yaml and %v/M derived from the receipt",
						c.side, c.name, c.have, c.want)
				}
			}

			if !slices.Contains(recorded, price.Endpoint) {
				t.Errorf("pricing.yaml says this rate belongs to endpoint %q, and the endpoint(s) in the "+
					"receipt charging %v/%v are %v. Recording one endpoint's rate as the model's is the "+
					"round-2 error", price.Endpoint, price.Input, price.Output, recorded)
			}
		})
	}
}

// TestRoutingLabelsNameAnEndpointAtTheExtremeTheyClaim is the round-3
// regression.
//
// THE BUG: `cheapest` and `dearest` were derived from an endpoint's POSITION in
// the vendor's array, on a claim written into both file headers as load-bearing,
// that `/api/v1/models/…/endpoints` returns them cheapest first. It does not. The
// array is unsorted by price for 8 of the 20 models, in every case because a
// half-price `/flex` service tier is listed after the standard one. So eight
// entries named a cheapest endpoint costing exactly 2.00x the min_input recorded
// three lines below it, and never one below, one-directional, on the eight
// first-party endpoints, which is the axis this table is read along.
//
// Nothing caught it because every NUMBER was right. The label is advice, "pin
// the cheapest and the band collapses", and it pointed at twice the floor.
func TestRoutingLabelsNameAnEndpointAtTheExtremeTheyClaim(t *testing.T) {
	receipt := readReceipt(t)

	table, err := Prices()
	if err != nil {
		t.Fatalf("loading the shipped price table: %v", err)
	}

	for model, price := range table.Models {
		captured, ok := receipt.Models[model]
		if !ok {
			continue // reported by the re-derivation test
		}

		rates := map[string]Rates{}
		for _, e := range captured.Endpoints {
			rates[e.Tag] = endpointRates(t, model, e.Tag, e.Pricing)
		}

		for _, c := range []struct {
			role  string
			named string
			want  float64
		}{
			{"cheapest", price.Routing.Cheapest, price.Routing.Low.Input},
			{"dearest", price.Routing.Dearest, price.Routing.High.Input},
		} {
			got, ok := rates[c.named]
			if !ok {
				t.Errorf("%s: routing names %q as the %s endpoint and the receipt has no endpoint by "+
					"that tag", model, c.named, c.role)
				continue
			}

			// Equality, not "within the band". Ties at an extreme are normal,
			// six of claude-opus-5's seven endpoints share its floor, so any
			// endpoint AT the extreme is a correct label and one merely near it
			// is not.
			if got.Input != c.want {
				t.Errorf("%s: routing names %q as the %s endpoint; it charges %v/M input and the band's "+
					"%s is %v/M (%.2fx). An operator following this label pins the wrong provider and "+
					"lands at a rate the row does not print",
					model, c.named, c.role, got.Input, c.role, c.want, got.Input/c.want)
			}
		}
	}
}

// TestEveryBatteryModelIsPriced keeps the table and the matrix from drifting
// apart.
//
// A model added to the battery with no entry reports its cost as unknown, which
// is the correct behaviour and an invisible one: the row prints, the cost cell
// says unknown, and nothing says the fix is one line of YAML.
func TestEveryBatteryModelIsPriced(t *testing.T) {
	table, err := Prices()
	if err != nil {
		t.Fatalf("loading the shipped price table: %v", err)
	}

	for _, m := range DefaultModels() {
		if _, ok := table.Price(m.ID); !ok {
			t.Errorf("%s is in DefaultModels and has no price entry, so every cost it reports is "+
				"unknown", m.ID)
		}
	}
	if _, ok := table.Price(DefaultJudgeModel); !ok {
		t.Errorf("the default judge %s has no price entry, so what MEASURING the battery cost cannot "+
			"be reported at all", DefaultJudgeModel)
	}
}

// TestPricesRejectsAnEntryItCannotUse covers the parser's refusals as one table.
//
// Every case here is a state some earlier version of this file accepted and
// priced a run from. A parser that accepts a broken entry does not fail, it
// publishes a number.
func TestPricesRejectsAnEntryItCannotUse(t *testing.T) {
	valid := "models:\n" + priceEntry("test/model", "2026-08-01", 1, 10)

	if _, err := parsePrices([]byte(valid), "valid"); err != nil {
		t.Fatalf("the control case does not parse, so every rejection below proves nothing: %v", err)
	}

	for _, tc := range []struct {
		name  string
		yaml  string
		wants string
	}{
		{
			name:  "a misspelled rate key",
			yaml:  strings.Replace(valid, "    input: 1\n", "    imput: 1\n", 1),
			wants: "field imput not found",
		},
		{
			name:  "no capture date",
			yaml:  strings.Replace(valid, `    captured_on: "2026-08-01"`+"\n", "", 1),
			wants: "captured_on is required",
		},
		{
			name:  "no source",
			yaml:  strings.Replace(valid, `    source: "https://openrouter.ai/api/v1/models"`+"\n", "", 1),
			wants: "has no source",
		},
		{
			name:  "no endpoint",
			yaml:  strings.Replace(valid, `    endpoint: "only"`+"\n", "", 1),
			wants: "has no endpoint",
		},
		{
			name:  "output priced and input not",
			yaml:  strings.Replace(valid, "    input: 1\n", "", 1),
			wants: "has no input rate",
		},
		{
			name:  "a negative rate",
			yaml:  strings.Replace(valid, "    output: 10\n", "    output: -10\n", 1),
			wants: "negative output rate",
		},
		{
			name:  "no routing block",
			yaml:  strings.Split(valid, "    routing:\n")[0],
			wants: "has no routing block",
		},
		{
			name:  "a 1-hour cache rate with no 5-minute one",
			yaml:  strings.Replace(valid, "    output: 10\n", "    output: 10\n    cache_write_1h: 4\n", 1),
			wants: "records cache_write_1h with no cache_write",
		},
		{
			name: "a recorded rate outside its own band",
			yaml: strings.Replace(valid, "      min_input: 1\n      max_input: 1\n",
				"      min_input: 2\n      max_input: 3\n", 1),
			wants: "outside this model's own endpoint band",
		},
		{
			name:  "one endpoint and a band wider than a point",
			yaml:  strings.Replace(valid, "      max_output: 10\n", "      max_output: 20\n", 1),
			wants: "declares 1 endpoint and a band wider than a point",
		},
		{
			name: "an entry naming itself cheapest at a rate above the floor",
			yaml: strings.NewReplacer(
				"      endpoints: 1\n", "      endpoints: 2\n",
				"      min_input: 1\n", "      min_input: 0.5\n",
				"      min_cache_read: 1\n", "      min_cache_read: 0.5\n",
				"      min_cache_write: 1\n", "      min_cache_write: 0.5\n",
				"      min_output: 10\n", "      min_output: 5\n",
			).Replace(valid),
			wants: "the endpoints response is NOT sorted by price",
		},
		{
			name: "a tier that restates no rate",
			yaml: strings.Replace(valid, "    routing:\n",
				"    tiers:\n      - min_prompt_tokens: 1000\n    routing:\n", 1),
			wants: "changes no rate",
		},
		{
			name: "tiers out of order",
			yaml: strings.Replace(valid, "    routing:\n",
				"    tiers:\n      - min_prompt_tokens: 2000\n        input: 2\n"+
					"      - min_prompt_tokens: 1000\n        input: 3\n    routing:\n", 1),
			wants: "which does not exceed the",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parsePrices([]byte(tc.yaml), "testdata/prices_test.yaml")
			if err == nil {
				t.Fatalf("this entry parsed. Every case in this table is a state that would have priced "+
					"a run: %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wants) {
				t.Errorf("rejected for the wrong reason.\n  got:  %v\n  want: containing %q", err, tc.wants)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// Measured, not estimated.
// -----------------------------------------------------------------------------

// meterStub answers with a fixed usage and content, so a test can make the two
// disagree, which is the only way to tell a measurement from an estimate.
// The call count is atomic because the concurrency test below drives this stub
// from eight goroutines, and an unsynchronised counter here would report a race
// in the test's own bookkeeping and bury the one it is looking for.
type meterStub struct {
	usage   llms.Usage
	content string
	err     error
	calls   atomic.Int64
}

func (m *meterStub) GenerateContent(context.Context, []llms.Message, ...llms.CallOption) (*llms.Response, error) {
	m.calls.Add(1)
	if m.err != nil {
		return nil, m.err
	}

	// A fresh Response each time. Handing out one shared value would let a
	// caller that mutates Usage, which is exactly what wrapping the meter in
	// an estimator does, change what every other call reported.
	return &llms.Response{Content: m.content, Usage: m.usage}, nil
}

func (m *meterStub) Stream(context.Context, []llms.Message, ...llms.CallOption) (<-chan llms.StreamChunk, error) {
	return nil, fmt.Errorf("nothing in the review path streams")
}

func (m *meterStub) Provider() llms.Provider { return llms.Provider("stub") }
func (m *meterStub) Model() string           { return "test/model" }

// meter drives one call through a metered client and returns what was recorded.
func meter(t *testing.T, stub *meterStub) TokenUsage {
	t.Helper()

	client := &llm.Client{LLM: stub}
	m := MeterClient(client)

	_, _ = client.LLM.GenerateContent(t.Context(), []llms.Message{{Content: strings.Repeat("x", 100_000)}})
	if got := stub.calls.Load(); got != 1 {
		t.Fatalf("the metered client made %d call(s) to the inner one, not 1", got)
	}
	return m.Usage()
}

// TestUsageIsMeasuredAndNotEstimated is the load-bearing property of this whole
// file: a cost figure rests on what the PROVIDER said it billed.
//
// The temptation is real and one import away. The SDK ships
// EstimateUsageFromMessages and UsageOrEstimate, and internal/bundle already
// estimates tokens, correctly, because being wrong there costs a repacked
// batch. Being wrong here produces a dollar amount somebody buys a model on.
//
// The test makes the two answers differ by two orders of magnitude: a 100,000
// character prompt with a reported usage of 12 tokens. An estimate cannot
// produce 12.
func TestUsageIsMeasuredAndNotEstimated(t *testing.T) {
	usage := meter(t, &meterStub{
		usage: llms.Usage{
			PromptTokens: 7, CompletionTokens: 5,
			CacheReadTokens: 3, CacheCreationTokens: 2, ReasoningTokens: 4,
			TotalTokens: 999,
		},
		content: strings.Repeat("y", 50_000),
	})

	for _, c := range []struct {
		name string
		got  int
		want int
	}{
		{"calls", usage.Calls(), 1},
		{"prompt", usage.Prompt(), 7},
		{"completion", usage.Completion(), 5},
		{"cache read", usage.CacheRead(), 3},
		{"cache write", usage.CacheWrite(), 2},
		{"reasoning", usage.Reasoning(), 4},

		// TotalTokens is 999 and is not read: it is the provider's own sum,
		// which several report inconsistently with the parts, and every amount
		// here is computed from the parts that carry a rate.
		{"total", usage.Total(), 7 + 5 + 3 + 2},
	} {
		if c.got != c.want {
			t.Errorf("%s: metered %d, the provider reported %d. Any difference is an estimate",
				c.name, c.got, c.want)
		}
	}
}

// TestNothingInThisPackageEstimatesUsage is the structural half of the same
// rule, and it reads the source because the behavioural half cannot see the
// dangerous case.
//
// llms.UsageOrEstimate returns the provider's numbers UNCHANGED whenever they
// are present, so a meter wrapped in it passes every test that supplies a usage
// and silently invents one for every call that does not, turning the Unreported
// count, which exists precisely to keep that state visible, into a plausible
// number nobody can distinguish from a measurement. One import, no failing test
// except this one.
func TestNothingInThisPackageEstimatesUsage(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}

	banned := []string{"EstimateUsageFromMessages", "UsageOrEstimate"}
	var scanned int
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || e.Name() == "cost_test.go" {
			continue
		}
		source, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatalf("reading %s: %v", e.Name(), err)
		}
		scanned++

		for _, name := range banned {
			if strings.Contains(string(source), name) {
				t.Errorf("%s calls llms.%s. A cost figure rests on what the provider said it billed; "+
					"internal/bundle estimates tokens to decide what FITS in a request, which is the "+
					"right instrument for budgeting because being wrong there costs a repacked batch. "+
					"Being wrong here produces a dollar amount somebody buys a model on",
					e.Name(), name)
			}
		}
	}
	if scanned == 0 {
		t.Fatal("no source file was scanned, so this guard asserts nothing")
	}
}

// TestMeteringIsSafeUnderConcurrentCalls pins the lock.
//
// The battery runs models concurrently against one client, so an unsynchronised
// meter would not merely race. It would drop calls, and a dropped call is a
// silently cheaper row rather than a crash.
func TestMeteringIsSafeUnderConcurrentCalls(t *testing.T) {
	client := &llm.Client{LLM: &meterStub{usage: llms.Usage{PromptTokens: 10, CompletionTokens: 1}}}
	m := MeterClient(client)

	const workers, each = 8, 25
	done := make(chan struct{})
	for range workers {
		go func() {
			defer func() { done <- struct{}{} }()
			for range each {
				_, _ = client.LLM.GenerateContent(t.Context(), nil)
				_ = m.Usage()
			}
		}()
	}
	for range workers {
		<-done
	}

	usage := m.Usage()
	if got, want := usage.Calls(), workers*each; got != want {
		t.Errorf("metered %d call(s) of %d", got, want)
	}
	if got, want := usage.Prompt(), workers*each*10; got != want {
		t.Errorf("metered %d prompt token(s) of %d", got, want)
	}
}

// TestUsageReturnedByTheMeterIsACopy pins the aliasing.
//
// Usage returns a slice, and a caller that keeps it, every ledger does, would
// otherwise observe later calls appending to it, so a row's cost would depend on
// when it was rendered.
func TestUsageReturnedByTheMeterIsACopy(t *testing.T) {
	stub := &meterStub{usage: llms.Usage{PromptTokens: 10, CompletionTokens: 1}}
	client := &llm.Client{LLM: stub}
	m := MeterClient(client)

	_, _ = client.LLM.GenerateContent(t.Context(), nil)
	held := m.Usage()

	_, _ = client.LLM.GenerateContent(t.Context(), nil)
	if got := held.Calls(); got != 1 {
		t.Errorf("a usage captured after one call reports %d call(s) once a second one has been made", got)
	}
	if got := m.Usage().Calls(); got != 2 {
		t.Errorf("the meter itself reports %d call(s) after two", got)
	}
}

// TestReasoningIsNotBilledOnTopOfCompletion pins the one place double counting
// would be invisible.
//
// The SDK normalizes ReasoningTokens as a SUBSET of CompletionTokens, and every
// model in the shipped table that publishes an internal_reasoning rate publishes
// it EQUAL to its completion rate, so reasoning is already billed correctly
// inside completion, and adding a term for it would charge twice.
func TestReasoningIsNotBilledOnTopOfCompletion(t *testing.T) {
	c := CallUsage{Prompt: 100, Completion: 60, Reasoning: 50}

	if got := c.Total(); got != 160 {
		t.Errorf("a call of 100 prompt and 60 completion tokens, 50 of them reasoning, totals %d. "+
			"Reasoning is a subset of completion, so the answer is 160 and 210 is the same tokens "+
			"charged twice", got)
	}

	rates := Rates{Input: 1, Output: 10}
	if got, want := rates.cost(c), (100*1.0+60*10.0)/1_000_000; got != want {
		t.Errorf("priced at %v, want %v", got, want)
	}
}

// TestPromptSizeCountsTheCachedSubset pins which number a tier threshold reads.
//
// PromptTokens EXCLUDES the cached subset by the SDK's normalization, so a call
// with 30,000 fresh and 30,000 cached prompt tokens sent a 60,000-token prompt
// and reports Prompt as 30,000. Selecting a tier on that would apply the
// sub-32,000 rate to a 60,000-token prompt, a discount nobody offered.
func TestPromptSizeCountsTheCachedSubset(t *testing.T) {
	c := CallUsage{Prompt: 30_000, CacheRead: 25_000, CacheWrite: 5_000, Completion: 400}

	if got := c.PromptSize(); got != 60_000 {
		t.Errorf("PromptSize is %d for a call whose provider read 60,000 prompt tokens. The larger "+
			"number selects the more expensive tier, which is the direction that cannot flatter a "+
			"model into being the default", got)
	}
}

// TestACallReportingNoUsageIsNotPricedAsZero covers the state that produced a
// $0.00 row.
//
// THE BUG the presence test fixes: the whole llms.Usage was compared against its
// zero value, and TotalTokens is copied through verbatim from any OpenAI-shaped
// endpoint. A provider reporting only a total therefore passed as a complete
// report, contributed no priced tokens, and rendered as $0.000000 across every
// cost column, known, unfootnoted, and the cheapest row in the table.
func TestACallReportingNoUsageIsNotPricedAsZero(t *testing.T) {
	for _, tc := range []struct {
		name  string
		usage llms.Usage
	}{
		{"nothing at all", llms.Usage{}},
		{"only a total", llms.Usage{TotalTokens: 4321}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			usage := meter(t, &meterStub{usage: tc.usage, content: "hello"})

			if usage.Calls() != 0 {
				t.Errorf("%d call(s) were priced from a response that reported no billable token",
					usage.Calls())
			}
			if usage.Unreported != 1 {
				t.Errorf("Unreported is %d, want 1: a call whose spend nothing can see has to stay "+
					"visible, or the column is sometimes measured and sometimes guessed with no way "+
					"to tell which", usage.Unreported)
			}
			if usage.Complete() {
				t.Error("this usage reports itself complete, so a ledger would price the run from the " +
					"fraction of it that reported")
			}
		})
	}
}

// TestAFailedCallIsABlindSpotNotAZero pins the other half of the same rule.
//
// A failed call may still have been billed, a provider that generates a
// response and fails to deliver it charges for the generation, and that is not
// visible from a response. It is counted, never filled in, and never used to
// discard the run: in StructuredAuto a provider that rejects json_schema
// produces a capability error and the client retries in JSON mode, so voiding
// the cost on a failed call would drop every model without schema support out of
// the table entirely.
func TestAFailedCallIsABlindSpotNotAZero(t *testing.T) {
	usage := meter(t, &meterStub{err: fmt.Errorf("provider said no")})

	if usage.Failed != 1 {
		t.Errorf("Failed is %d, want 1", usage.Failed)
	}
	if usage.Calls() != 0 || usage.Total() != 0 {
		t.Errorf("a failed call contributed %d call(s) and %d token(s) to the priced total",
			usage.Calls(), usage.Total())
	}
	if usage.Complete() {
		t.Error("a usage of nothing but failures reports itself complete")
	}
}

// TestTheCostLegendStatesTheBlindSpots keeps the disclosure attached to the
// numbers.
//
// Neither blind spot is countable. A billed-but-undelivered response leaves no
// trace, and the meter sits outside the SDK's retry loop by design, so a request
// that succeeded on its second attempt contributes only the second attempt's
// tokens. Both run the same way, and a reader can only know that if the table
// says so.
func TestTheCostLegendStatesTheBlindSpots(t *testing.T) {
	for _, want := range []string{"LOWER BOUND", "FAILED", "retry"} {
		if !strings.Contains(CostReadingLegend, want) {
			t.Errorf("the cost legend does not mention %q. Every amount in the table is understated by "+
				"two effects nothing can count, and the legend is the only place that is said", want)
		}
	}
}

// -----------------------------------------------------------------------------
// Unknown, undefined and stale, the three things a float64 cannot say.
// -----------------------------------------------------------------------------

// costLedger builds a one-model ledger over a table with a single priced entry.
func costLedger(t *testing.T, model string, captured string) *CostLedger {
	t.Helper()
	return NewCostLedger(mustPrices(t, "models:\n"+priceEntry("test/priced", captured, 1, 10)))
}

// TestAModelWithNoPriceEntryReportsUnknownAndNotZero is the rule the whole Cost
// type exists for.
//
// A float64 cannot hold "we do not know", and every unset one in Go renders as
// $0.00, the single wrong answer a reader acts on without checking, because
// free is a reason to pick a model.
func TestAModelWithNoPriceEntryReportsUnknownAndNotZero(t *testing.T) {
	ledger := costLedger(t, "test/unpriced", "2026-08-01")
	ledger.Observe("test/unpriced", "fixture", TokenUsage{
		PerCall: []CallUsage{{Prompt: 1000, Completion: 200}},
	}, Detections{Matched: 2, Planted: 3})

	row := ledger.Row("test/unpriced")
	if row.Priced {
		t.Fatal("the row reports itself priced against a table with no entry for it")
	}

	for _, c := range []struct {
		name string
		cost Cost
	}{
		{"COST", row.Total()},
		{"$/REVIEW", row.PerReview()},
		{"$/DEFECT", row.PerDefect()},
	} {
		if c.cost.Known {
			t.Errorf("%s is known ($%v) for a model with no published rate", c.name, c.cost.USD)
		}
		if got := c.cost.String(); got != "unknown" {
			t.Errorf("%s prints as %q. Anything numeric here is a price we do not have", c.name, got)
		}
		if c.cost.Reason == "" {
			t.Errorf("%s is unknown and says nothing about why: a table of unknowns with no reasons is "+
				"indistinguishable from a broken instrument", c.name)
		}
	}
}

// TestPerDefectIsUndefinedAndNotInfiniteAtZeroDetections pins the division.
//
// Zero detections is a division by zero, not an infinite cost and not a free
// lunch. Both wrong answers are reachable in one line, Go yields +Inf, and a
// guard returning 0 reads as the best row in the table.
func TestPerDefectIsUndefinedAndNotInfiniteAtZeroDetections(t *testing.T) {
	ledger := costLedger(t, "test/priced", "2026-08-01")
	ledger.Observe("test/priced", "fixture", TokenUsage{
		PerCall: []CallUsage{{Prompt: 1000, Completion: 200}},
	}, Detections{Matched: 0, Planted: 4})

	row := ledger.Row("test/priced")

	if total := row.Total(); !total.Known {
		t.Fatalf("the run was measured and priced, so its total is known: %s", total.Reason)
	}

	perDefect := row.PerDefect()
	if perDefect.Known {
		t.Errorf("$/DEFECT is $%v for a review that detected nothing", perDefect.USD)
	}
	if math.IsInf(perDefect.USD, 0) || math.IsNaN(perDefect.USD) {
		t.Errorf("$/DEFECT carries %v, which is what dividing by zero leaves behind", perDefect.USD)
	}
	if !strings.Contains(perDefect.Reason, "undefined") {
		t.Errorf("the reason given is %q, and the distinction a reader needs is between an undefined "+
			"ratio and an expensive one", perDefect.Reason)
	}
}

// TestStalenessIsSurfacedOnTheAmountItself pins where the disclosure lives.
//
// THE BUG: the age was surfaced only by the table's own AGE column, so anything
// that formatted a row's $/DEFECT into a summary, the obvious next use of the
// type, carried a dollar figure with no indication that the rate behind it had
// moved on. Staleness is a property of the number, so it travels with the
// number.
func TestStalenessIsSurfacedOnTheAmountItself(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	fresh := now.AddDate(0, 0, -(staleAfterDays - 1)).Format(time.DateOnly)
	stale := now.AddDate(0, 0, -staleAfterDays).Format(time.DateOnly)

	for _, tc := range []struct {
		name      string
		captured  string
		wantStale bool
	}{
		{"one day inside the horizon", fresh, false},
		{"exactly at the horizon", stale, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ledger := costLedger(t, "test/priced", tc.captured)
			ledger.Observe("test/priced", "fixture", TokenUsage{
				PerCall: []CallUsage{{Prompt: 1000, Completion: 200}},
			}, Detections{Matched: 2, Planted: 2})

			row := ledger.rowAt("test/priced", now)
			for _, c := range []struct {
				name string
				cost Cost
			}{
				{"COST", row.Total()},
				{"$/REVIEW", row.PerReview()},
				{"$/DEFECT", row.PerDefect()},
			} {
				if c.cost.Stale != tc.wantStale {
					t.Errorf("%s reports Stale=%v, want %v", c.name, c.cost.Stale, tc.wantStale)
				}
				if got := strings.Contains(c.cost.String(), staleMark); got != tc.wantStale {
					t.Errorf("%s prints as %q, and the marker is what a reader skimming the column sees",
						c.name, c.cost)
				}
				if tc.wantStale && c.cost.StaleReason == "" {
					t.Errorf("%s is marked stale and carries no reason", c.name)
				}

				// Staleness is a disclosure, not a downgrade: the arithmetic is
				// right and the rows are still each other's peers.
				if !c.cost.Known || !c.cost.Comparable {
					t.Errorf("%s was downgraded to unknown or incomparable by its AGE", c.name)
				}
			}
		})
	}
}

// TestACaptureDateInTheFutureIsReportedNotSwallowed keeps a typo visible.
//
// captured_on is the one field a reader uses to decide whether to trust the
// numbers, and a clamp to zero would render the typo as "captured today".
func TestACaptureDateInTheFutureIsReportedNotSwallowed(t *testing.T) {
	if got := describeAge(-3); !strings.Contains(got, "FUTURE") {
		t.Errorf("an age of -3 days renders as %q", got)
	}
}

// TestProvenanceReportsTheOldestEntryNotTheNewest pins which date a table's age
// is.
//
// Reporting the newest, or a file-level date, lets one refreshed entry describe
// every other one as fresh, which is the failure the per-model dates exist to
// prevent, so summarizing them away here would put it straight back.
func TestProvenanceReportsTheOldestEntryNotTheNewest(t *testing.T) {
	now := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	table := mustPrices(t, "models:\n"+
		priceEntry("test/fresh", "2026-08-03", 1, 10)+
		priceEntry("test/ancient", "2026-01-01", 2, 20))

	line := table.provenanceAt(now)
	if !strings.Contains(line, "2026-01-01") {
		t.Errorf("the provenance line does not name the oldest capture date: %s", line)
	}
	if !strings.Contains(line, "STALE") {
		t.Errorf("a table with a seven-month-old entry does not report itself stale: %s", line)
	}

	oldest, newest, err := table.Captured()
	if err != nil {
		t.Fatalf("Captured: %v", err)
	}
	if oldest.After(newest) {
		t.Errorf("Captured returned oldest %v after newest %v", oldest, newest)
	}
}

// TestAgeIsWholeCalendarDays pins the unit.
//
// THE BUG: the age was elapsed hours from a timestamp, which made a table
// captured this morning report itself a day old wherever the local zone sits
// behind UTC.
func TestAgeIsWholeCalendarDays(t *testing.T) {
	captured := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	local := time.FixedZone("behind", -8*3600)

	if got := ageInDays(time.Date(2026, 8, 3, 9, 0, 0, 0, local), captured); got != 0 {
		t.Errorf("a rate captured today is %d day(s) old when read from a zone behind UTC", got)
	}
	if got := ageInDays(time.Date(2026, 8, 5, 1, 0, 0, 0, time.UTC), captured); got != 2 {
		t.Errorf("two days after capture reads as %d", got)
	}
}

// -----------------------------------------------------------------------------
// The judge is a measurement expense.
// -----------------------------------------------------------------------------

// TestJudgeSpendIsNotAContenderCost keeps the two ledgers apart.
//
// Nobody running open-nitpick pays for a judge. Folding its tokens into a
// contender's cost would overstate what the tool costs by whatever the judge
// happens to charge, currently more than the input rate of more than half the
// battery.
func TestJudgeSpendIsNotAContenderCost(t *testing.T) {
	ledger := NewCostLedger(mustPrices(t, "models:\n"+
		priceEntry("test/contender", "2026-08-01", 1, 10)+
		priceEntry("test/judge", "2026-08-01", 100, 1000)))

	usage := TokenUsage{PerCall: []CallUsage{{Prompt: 1000, Completion: 100}}}
	ledger.Observe("test/contender", "fixture", usage, Detections{Matched: 2, Planted: 2})
	ledger.ObserveJudge("test/judge", "fixture", usage)

	rows := ledger.Rows()
	if len(rows) != 1 || rows[0].Model != "test/contender" {
		t.Fatalf("the contender rows are %v: a judge appearing among them is a row nobody pays for, "+
			"ranked against rows they do", rows)
	}

	judges := ledger.JudgeRows()
	if len(judges) != 1 || judges[0].Model != "test/judge" {
		t.Fatalf("the judge rows are %v", judges)
	}

	// The judge's rate is 100x the contender's, so folding the two would be
	// unmissable in the total and is exactly what this asserts did not happen.
	want := (1000*1.0 + 100*10.0) / 1_000_000
	if got := rows[0].Total(); !got.Known || got.USD != want {
		t.Errorf("the contender cost %s, want $%v — the judge's tokens are in this number", got, want)
	}

	// A judge detects nothing, so its per-defect ratio does not exist. It must
	// not read as a shortcoming, and the table prints a dash rather than an
	// unknown for it.
	if judges[0].Detected != 0 || judges[0].Planted != 0 {
		t.Errorf("the judge row carries %d detection(s) of %d planted",
			judges[0].Detected, judges[0].Planted)
	}

	table := ledger.Table()
	if !strings.Contains(table, "JUDGE — a measurement expense") {
		t.Error("the cost table does not separate the judge block from the contenders; a judge row " +
			"below a rule is eventually read as part of the total above it")
	}
}

// -----------------------------------------------------------------------------
// Tiers and cache pricing.
// -----------------------------------------------------------------------------

// TestATierIsChosenPerCallAndNotFromAnAggregate is why usage is kept call by
// call.
//
// THE BUG this shape fixes: an earlier version recorded base rates only, with a
// comment asserting that every review was billed one tier up and was therefore
// understated by up to 3.3x. Nobody had measured a prompt. A sum of calls cannot
// answer "how big was the prompt", 60,000 aggregate prompt tokens is one call
// over a 32,000 step or thirty calls under it, at rates 3.3x apart.
func TestATierIsChosenPerCallAndNotFromAnAggregate(t *testing.T) {
	table := mustPrices(t, "models:\n"+priceEntry("test/tiered", "2026-08-01", 1, 10,
		"    tiers:\n      - min_prompt_tokens: 32000\n        input: 3\n        output: 30\n"))

	price, ok := table.Price("test/tiered")
	if !ok {
		t.Fatal("the tiered entry did not load")
	}

	// Thirty small calls totalling 60,000 prompt tokens. Every one of them is
	// under the step, so every one is billed at the base rate.
	//
	// The expectation is accumulated call by call rather than computed from the
	// totals, because that is how the amount is built and float64 addition is
	// not associative, writing (60_000*1 + 3_000*10)/1e6 here fails by 4e-17,
	// which is a fact about arithmetic and not about tiers.
	var many TokenUsage
	wantMany := 0.0
	for range 30 {
		many.PerCall = append(many.PerCall, CallUsage{Prompt: 2000, Completion: 100})
		wantMany += (2000*1.0 + 100*10.0) / 1_000_000
	}
	if got := price.Cost(many); got != wantMany {
		t.Errorf("thirty 2,000-token calls cost $%v, want $%v. Priced off the aggregate they cross a "+
			"32,000-token step none of them reached", got, wantMany)
	}

	// One call of the same total, which does cross it.
	one := TokenUsage{PerCall: []CallUsage{{Prompt: 60_000, Completion: 3_000}}}
	wantOne := (60_000*3.0 + 3_000*30.0) / 1_000_000
	if got := price.Cost(one); got != wantOne {
		t.Errorf("one 60,000-token call cost $%v, want $%v", got, wantOne)
	}

	if wantOne <= wantMany {
		t.Fatal("the two arrangements cost the same, so this test would pass against an aggregate")
	}
}

// TestTiersReportWhereTheCallsActuallyLanded pins the reporting, not the
// arithmetic.
//
// The previous version of this file asserted in a comment which tier the battery
// was billed at. The fixture corpus runs at 1,500 to 2,200 prompt tokens per
// call against a 32,000-token first step, so "correcting" the qwen rates upward
// on that assertion would have overstated the three cheapest models by 1.8x to
// 3.3x.
func TestTiersReportWhereTheCallsActuallyLanded(t *testing.T) {
	table := mustPrices(t, "models:\n"+priceEntry("test/tiered", "2026-08-01", 1, 10,
		"    tiers:\n      - min_prompt_tokens: 32000\n        input: 3\n"))

	price, _ := table.Price("test/tiered")
	row := CostRow{
		Model: "test/tiered", Price: price, Priced: true,
		Usage: TokenUsage{PerCall: []CallUsage{
			{Prompt: 2000, Completion: 100},
			{Prompt: 40_000, Completion: 100},
			{Prompt: 1500, Completion: 100},
		}},
	}

	used := row.Tiers()
	if len(used) != 2 {
		t.Fatalf("the calls landed in %d tier(s), want 2: %v", len(used), used)
	}
	if used[0] != (TierUse{MinPromptTokens: 0, Calls: 2}) || used[1] != (TierUse{MinPromptTokens: 32000, Calls: 1}) {
		t.Errorf("tier use is %v, want 2 calls at the base rate and 1 at the 32,000-token step", used)
	}
	if got := row.Usage.LargestPrompt(); got != 40_000 {
		t.Errorf("the largest prompt is reported as %d", got)
	}
}

// TestAPublishedFreeCacheRateIsNotTheInputFallback separates two statements a
// float64 cannot tell apart.
//
// An OMITTED cache rate means "this provider bills those tokens at input", which
// is what such a provider charges. A PUBLISHED 0.0 means free caching,
// which some providers do publish. The escape hatch the file header documents
// did not exist: an operator who wrote `cache_read: 0.0` to record a free tier
// was billed at the input rate, silently.
func TestAPublishedFreeCacheRateIsNotTheInputFallback(t *testing.T) {
	usage := TokenUsage{PerCall: []CallUsage{{Prompt: 100, Completion: 10, CacheRead: 1000}}}

	omitted := mustPrices(t, "models:\n"+priceEntry("test/model", "2026-08-01", 1, 10))
	free := mustPrices(t, "models:\n"+priceEntry("test/model", "2026-08-01", 1, 10,
		"    cache_read: 0.0\n"))

	omittedPrice, _ := omitted.Price("test/model")
	freePrice, _ := free.Price("test/model")

	wantOmitted := (100*1.0 + 10*10.0 + 1000*1.0) / 1_000_000
	if got := omittedPrice.Cost(usage); got != wantOmitted {
		t.Errorf("with cache_read omitted the call cost $%v, want $%v at the input rate", got, wantOmitted)
	}

	wantFree := (100*1.0 + 10*10.0) / 1_000_000
	if got := freePrice.Cost(usage); got != wantFree {
		t.Errorf("with cache_read published as 0.0 the call cost $%v, want $%v. A published free tier "+
			"billed at the input rate is the operator's own number being ignored", got, wantFree)
	}
}

// TestTwoPublishedCacheWriteRatesMakeTheCallUnpriceable is the honesty rule for
// a charge this table cannot represent.
//
// The two anthropic entries publish a 1-hour cache TTL at 1.6x the 5-minute
// rate. A usage report says how many cache-creation tokens were written and not
// which TTL they were written at, so the amount is one of two numbers 1.6x apart
// and nothing here can say which. It was billed silently at the 5-minute rate,
// the cheaper of the two, which is the flattering direction.
//
// Nothing in open-nitpick enables prompt caching today, which is why this is
// encoded rather than left as a comment: "no path sends these tokens" is a
// property of this month's call sites.
func TestTwoPublishedCacheWriteRatesMakeTheCallUnpriceable(t *testing.T) {
	table := mustPrices(t, "models:\n"+priceEntry("test/ttl", "2026-08-01", 1, 10,
		"    cache_write: 4\n    cache_write_1h: 6.4\n"))
	price, ok := table.Price("test/ttl")
	if !ok {
		t.Fatal("the entry did not load")
	}

	// No cache writes: the ambiguity does not arise and the row prices normally.
	plain := TokenUsage{PerCall: []CallUsage{{Prompt: 1000, Completion: 100, CacheRead: 500}}}
	if why := price.Unpriceable(plain); why != "" {
		t.Errorf("a run that wrote no cache tokens was refused: %s", why)
	}

	wrote := TokenUsage{PerCall: []CallUsage{{Prompt: 1000, Completion: 100, CacheWrite: 500}}}
	why := price.Unpriceable(wrote)
	if why == "" {
		t.Fatal("a run that wrote cache tokens on a model publishing two cache-write rates was priced " +
			"anyway. The number it produces is the cheaper of two the vendor publishes")
	}
	for _, want := range []string{"5-minute", "1 hour"} {
		if !strings.Contains(why, want) {
			t.Errorf("the refusal does not mention %q: %s", want, why)
		}
	}

	ledger := NewCostLedger(table)
	ledger.Observe("test/ttl", "fixture", wrote, Detections{Matched: 1, Planted: 1})
	row := ledger.Row("test/ttl")

	if cost := row.Total(); cost.Known {
		t.Errorf("the row priced the run at %s. The rule for a charge this table cannot represent is "+
			"unknown, not a plausible-looking approximation", cost)
	}
}

// TestABandIsRefusedAboveTheBaseTier pins a refusal that is easy to talk
// yourself out of.
//
// Routing.Low and Routing.High are BASE rates; each endpoint publishes its own
// prompt-size overrides and this file does not record 34 tier ladders per model.
// Returning the base band for a call billed above the step would be a bound that
// does not bound, which is worse than none.
func TestABandIsRefusedAboveTheBaseTier(t *testing.T) {
	table := mustPrices(t, `models:
  "test/banded":
    captured_on: "2026-08-01"
    source: "https://openrouter.ai/api/v1/models"
    endpoint: "standard"
    input: 1
    output: 10
    tiers:
      - min_prompt_tokens: 32000
        input: 2
        output: 20
    routing:
      source: "https://openrouter.ai/api/v1/models/test/banded/endpoints"
      endpoints: 3
      cheapest: "flex"
      dearest: "priority"
      min_input: 0.5
      max_input: 2
      min_output: 5
      max_output: 20
      min_cache_read: 0.5
      max_cache_read: 2
      min_cache_write: 0.5
      max_cache_write: 2
`)
	price, ok := table.Price("test/banded")
	if !ok {
		t.Fatal("the banded entry did not load")
	}

	under := TokenUsage{PerCall: []CallUsage{{Prompt: 2000, Completion: 100}}}
	low, high, ok := price.Band(under)
	if !ok {
		t.Fatal("no band was offered for a call billed at the base rate, where the recorded extremes apply")
	}
	if low >= high {
		t.Errorf("the band is [%v, %v] for a model served by three endpoints spanning 4x", low, high)
	}

	over := TokenUsage{PerCall: []CallUsage{{Prompt: 40_000, Completion: 100}}}
	if _, _, ok := price.Band(over); ok {
		t.Error("a band was offered for a call billed above the base tier. The recorded extremes are " +
			"base rates, so the interval would not contain the amount it claims to bound")
	}

	// And the row says so rather than silently printing an unbanded amount:
	// "no interval is claimed" and "this amount is exact" are different states.
	ledger := NewCostLedger(table)
	ledger.Observe("test/banded", "fixture", over, Detections{Matched: 1, Planted: 1})
	if reason := ledger.Row("test/banded").Total().BandReason; !strings.Contains(reason, "no interval is claimed") {
		t.Errorf("the row's band reason is %q", reason)
	}
}

// TestTwoAmountsWithOverlappingBandsAreNotOrderable does the comparison a reader
// would otherwise attempt by eye.
//
// A footnote saying "this amount is one point in a 3.9x band" leaves the reader
// to intersect two intervals across a wide table, and the habit the whole cost
// block is fighting is that they will not. They will sort the column.
func TestTwoAmountsWithOverlappingBandsAreNotOrderable(t *testing.T) {
	entry := func(model string, input, output, lo, hi float64) string {
		return fmt.Sprintf(`  %q:
    captured_on: "2026-08-01"
    source: "https://openrouter.ai/api/v1/models"
    endpoint: "standard"
    input: %v
    output: %v
    routing:
      source: "https://openrouter.ai/api/v1/models/%s/endpoints"
      endpoints: 4
      cheapest: "cheap"
      dearest: "dear"
      min_input: %v
      max_input: %v
      min_output: %v
      max_output: %v
      min_cache_read: %v
      max_cache_read: %v
      min_cache_write: %v
      max_cache_write: %v
`, model, input, output, model, lo, hi, lo*10, hi*10, lo, hi, lo, hi)
	}

	ledger := NewCostLedger(mustPrices(t, "models:\n"+
		entry("test/wide", 1, 10, 0.2, 4)+
		entry("test/alsowide", 1.2, 12, 0.3, 5)))

	usage := TokenUsage{PerCall: []CallUsage{{Prompt: 10_000, Completion: 1000}}}
	for _, model := range []string{"test/wide", "test/alsowide"} {
		ledger.Observe(model, "fixture", usage, Detections{Matched: 2, Planted: 2})
	}

	notes := ledger.OrderingNotes()
	if len(notes) == 0 {
		t.Fatal("two rows whose routing bands overlap were reported as orderable on $/DEFECT. The " +
			"ordering between them is a fact about which endpoints the router picked")
	}
	if !strings.Contains(notes[0], "NOT ORDERABLE") {
		t.Errorf("the note reads %q", notes[0])
	}

	// A row whose band is a point IS orderable against a disjoint one, or the
	// note would fire on every pair and teach a reader to skip it.
	exact := NewCostLedger(mustPrices(t, "models:\n"+
		priceEntry("test/cheap", "2026-08-01", 1, 10)+
		priceEntry("test/dear", "2026-08-01", 100, 1000)))
	for _, model := range []string{"test/cheap", "test/dear"} {
		exact.Observe(model, "fixture", usage, Detections{Matched: 2, Planted: 2})
	}
	if notes := exact.OrderingNotes(); len(notes) != 0 {
		t.Errorf("two single-endpoint rows 100x apart were reported as not orderable: %v", notes)
	}
}

// -----------------------------------------------------------------------------
// What maximises this?
// -----------------------------------------------------------------------------

// costFixture is one fixture of the battery the strategies below are run over:
// a real fixture, plus what reviewing it costs to send.
//
// The note behind it is in docs/measurement.md#costfixture.
type costFixture struct {
	Fixture

	// prompt is what one review of this fixture sends, standing in for the
	// bundle the harness really builds. Only the RATIO between fixtures is under
	// test here. The cheapest cost per defect is bought by completing the cheap
	// fixtures and dying on the dear ones, and a corpus where every fixture costs
	// the same cannot express that strategy at all, so it is derived from the
	// fixture's own bytes rather than declared, and stays honest as the corpus
	// grows.
	prompt int
}

// costCorpus is every fixture, priced by size.
func costCorpus() []costFixture {
	all := AllFixtures()
	out := make([]costFixture, 0, len(all))

	for _, f := range all {
		bytes := 0
		for _, src := range f.Base {
			bytes += len(src)
		}
		for _, src := range f.Head {
			bytes += len(src)
		}
		// A floor, because a prompt is a system message and a policy before it is
		// any of the diff, and a fixture reviewed for nothing would make the
		// dollar columns degenerate for reasons having nothing to do with the
		// strategy under test.
		out = append(out, costFixture{Fixture: f, prompt: 2_000 + bytes})
	}
	return out
}

// dearFixture reports whether a fixture is one of the expensive half of the
// corpus, split at the median so the coverage strategies keep meaning what they
// mean as fixtures are added.
func dearFixture(f costFixture) bool {
	sizes := make([]int, 0, len(costCorpus()))
	for _, c := range costCorpus() {
		sizes = append(sizes, c.prompt)
	}
	sort.Ints(sizes)
	return f.prompt > sizes[len(sizes)/2]
}

// costRunsPerFixture is more than one so that a row can be short on DEPTH
// without being short on coverage, the gap a set-based check could not see.
const costRunsPerFixture = 2

// costPrices is the rate table the degenerate-strategy rows are priced
// against.
//
// The note behind it is in docs/measurement.md#costprices.
func costPrices(t *testing.T) *PriceTable {
	t.Helper()
	return mustPrices(t, "models:\n"+
		priceEntry("test/reference", "2026-08-01", 1, 10)+
		priceEntry("test/candidate", "2026-08-01", 1, 10))
}

// costStrategy is one way of spending money on a review that nobody would ship,
// crossed against every published cost reading.
type costStrategy struct {
	name string
	why  string

	// model overrides the priced id, for the one strategy that is about not
	// being in the table at all.
	model string

	// run returns what one run of one fixture PUBLISHED and what it spent. The
	// findings go through ScoreRun exactly as a model's do; only the usage is
	// declared, because usage is reported by a provider rather than derived from
	// a review. reported is false when the provider reported nothing for the
	// call, which is the state a run has to be in to disappear from a cost row
	// while still having happened.
	run func(f costFixture, run int) (findings []review.Finding, usage CallUsage, reported bool)

	// maxes declares, for every published cost reading by name, whether this
	// strategy is expected to score at least as well as the calibrated reviewer.
	// Every cell must be filled: publishing a new cost column means answering,
	// for each of these, "can this behaviour score as well as being useful?"
	maxes map[string]bool

	// caughtBy names the columns that are JOINTLY necessary to catch this
	// strategy: remove all of them and it maxes the reading out. Empty means the
	// row is not scored at all, because it is not comparable to its peers, or
	// because its cost is unknown, or because its per-defect ratio is undefined.
	//
	// It exists because the `why` prose above claims things like "NOISE is what
	// sees it", and a claim in a comment is not a test. Without it a strategy
	// caught three ways over teaches nothing about any of them, and a column
	// nothing depends on can be deleted with every guard in this file still
	// passing, which is how the withdrawn severity columns lived as long as
	// they did.
	caughtBy []string
}

// calibratedCostRun is the reference: it publishes the calibrated review of
// every fixture, and its provider reports what it spent. A cost reading is
// worth
// publishing only if being useful beats being degenerate on it.
//
// The note behind it is in docs/measurement.md#calibratedcostrun.
func calibratedCostRun(f costFixture, _ int) ([]review.Finding, CallUsage, bool) {
	findings := calibratedReview(f.Fixture)
	return findings, CallUsage{Prompt: f.prompt, Completion: explainedTokens * len(findings)}, true
}

// explainedTokens is what one EXPLAINED finding costs to write, and spamTokens
// what one empty one costs.
//
// The note behind it is in docs/measurement.md#explainedtokens-is-what-one-explained-finding-costs-to-write.
const (
	explainedTokens = 120
	spamTokens      = 12
)

// degenerateCostStrategies is the table.
func degenerateCostStrategies() []costStrategy {
	return []costStrategy{
		{
			name: "quit on the fixtures that cost the most",
			why: "reviews the cheap half of the corpus perfectly and dies on the dear half. Every " +
				"surviving run is priced correctly and the ratio is computed correctly; the answer " +
				"is a lie about the corpus, and the corpus is what the reader thinks they are " +
				"comparing. COVERAGE is the only thing in the row that can see it",
			run: func(f costFixture, run int) ([]review.Finding, CallUsage, bool) {
				if dearFixture(f) {
					return nil, CallUsage{}, false
				}
				return calibratedCostRun(f, run)
			},
			maxes:    map[string]bool{"cost effectiveness": false},
			caughtBy: nil,
		},
		{
			name: "report no usage on the runs that found nothing",
			why: "covers every fixture at least once, so a coverage check keyed on the SET of " +
				"fixtures passes it. It finds everything on its first run of each and nothing on " +
				"its second, and the barren run reports no usage — so it is priced on exactly the " +
				"runs that went well: recall 1.00 at the calibrated price. The dropped runs are not " +
				"a random subset. DEPTH is what sees it",
			run: func(f costFixture, run int) ([]review.Finding, CallUsage, bool) {
				if run > 0 {
					return nil, CallUsage{}, false
				}
				return calibratedCostRun(f, run)
			},
			maxes:    map[string]bool{"cost effectiveness": false},
			caughtBy: nil,
		},
		{
			name: "read a quarter of the diff and stop at the first hit",
			why: "truncates its context and reports the one defect it happened to see. This is the " +
				"strategy $/DEFECT alone is minimised by, and the reason it is not a column: a " +
				"quarter of the prompt is a quarter of the bill, so it is cheaper per review AND " +
				"cheaper per LOCATED DEFECT than a reviewer that reads everything and finds " +
				"everything. Its dollars are real, its detections are real, the division is correct " +
				"and it heads any table sorted on cost. RECALL is what sees it",
			run: func(f costFixture, _ int) ([]review.Finding, CallUsage, bool) {
				findings := calibratedReview(f.Fixture)
				if len(findings) > 1 {
					findings = findings[:1]
				}
				return findings, CallUsage{Prompt: f.prompt / 4, Completion: explainedTokens}, true
			},
			maxes:    map[string]bool{"cost effectiveness": false},
			caughtBy: []string{"RECALL"},
		},
		{
			name: "never explain anything, and guess",
			why: "finds every defect, names each in as few words as it can, and scatters one-line " +
				"guesses through the rest of the file. Output is billed per finding and a comment " +
				"carrying no reason is a fraction of one that does, so it is CHEAPER per review and " +
				"per defect than a reviewer that explains itself, while tying it on recall and on " +
				"anchor width — it does not merely tie the pair ($/DEFECT, RECALL), it beats it. " +
				"NOISE is what sees it",
			run: func(f costFixture, _ int) ([]review.Finding, CallUsage, bool) {
				findings := terseGuessReview(f.Fixture)
				return findings, CallUsage{Prompt: f.prompt, Completion: spamTokens * len(findings)}, true
			},
			maxes: map[string]bool{"cost effectiveness": false},
			// NOISE and ANCHOR, and the second name is here because this guard
			// put it here. The row declared NOISE alone, and once ANCHOR began
			// counting the union of every finding claiming ONE defect, the
			// guesses this strategy scatters through the file started landing in
			// that union: its ANCHOR is 7 against a calibrated 1, and the
			// subset check below reports that either column alone now catches it.
			// Left as NOISE, the declaration would have claimed NOISE was
			// load-bearing here when it no longer solely is, which is the
			// stale-declaration failure the whole table exists to make visible.
			caughtBy: []string{"NOISE", "ANCHOR"},
		},
		{
			name: "a line-precise reviewer that also points at every other line",
			why: "the calibrated review with one extra one-line region bolted onto every finding, " +
				"which is the shape Incumbent's 'Also applies to' list actually parses into. It is " +
				"right about every defect and points at the whole file, and the regions are free: " +
				"same comments, same words, same price, noise for nothing. It ties a calibrated " +
				"reviewer on $/DEFECT, $/REVIEW, RECALL and NOISE exactly. Only ANCHOR tells them " +
				"apart, and only once ANCHOR counts DISTINCT LINES rather than the widest single " +
				"region, which is what it used to do and why this strategy walked through the guard",
			run: func(f costFixture, run int) ([]review.Finding, CallUsage, bool) {
				_, usage, _ := calibratedCostRun(f, run)
				return scatteredAnchorReview(f.Fixture), usage, true
			},
			maxes:    map[string]bool{"cost effectiveness": false},
			caughtBy: []string{"ANCHOR"},
		},
		{
			name: "the same review for ten times the money",
			why: "reviews correctly and burns ten times the tokens doing it — the reasoning-heavy " +
				"model that reaches the same answer for ten times the bill. It is not a bad " +
				"reviewer, it is the one this whole block exists to reject, and the two cost " +
				"columns are the only thing that can: it ties a calibrated reviewer on RECALL, " +
				"NOISE and ANCHOR exactly",
			run: func(f costFixture, run int) ([]review.Finding, CallUsage, bool) {
				findings, usage, _ := calibratedCostRun(f, run)
				usage.Prompt *= 10
				usage.Completion *= 10
				return findings, usage, true
			},
			maxes:    map[string]bool{"cost effectiveness": false},
			caughtBy: []string{"$/DEFECT", "$/REVIEW"},
		},
		{
			name: "say nothing, cheaply",
			why:  "the global optimum of any cost column that divides by what was found",
			run: func(f costFixture, _ int) ([]review.Finding, CallUsage, bool) {
				return nil, CallUsage{Prompt: f.prompt, Completion: 5}, true
			},
			maxes:    map[string]bool{"cost effectiveness": false},
			caughtBy: nil,
		},
		{
			name: "a model nobody priced",
			why: "spends exactly what the calibrated reviewer spends and has no entry in the price " +
				"table. Its cost is unknown, and unknown must not be scored as free — which is the " +
				"one wrong answer a reader acts on without checking",
			model:    "test/unpriced",
			run:      calibratedCostRun,
			maxes:    map[string]bool{"cost effectiveness": false},
			caughtBy: nil,
		},
		{
			name: "genuinely cheaper and just as good",
			why: "half the tokens, same recall, no noise, same anchors. It maxes the reading out and " +
				"that is CORRECT — this is the behaviour the column exists to reward, and a reading " +
				"nothing can max out is unfalsifiable rather than strict",
			run: func(f costFixture, run int) ([]review.Finding, CallUsage, bool) {
				findings, usage, _ := calibratedCostRun(f, run)
				usage.Prompt /= 2
				usage.Completion /= 2
				return findings, usage, true
			},
			maxes:    map[string]bool{"cost effectiveness": true},
			caughtBy: nil,
		},
	}
}

// ledgerFor runs a strategy and the calibrated reviewer through ONE ledger, and
// returns both rows.
//
// One ledger because comparability is a property of the row's PEERS: a strategy
// that quits on the expensive fixtures is only visible as a quitter beside a row
// that did not quit. Scoring it against a reference computed in isolation would
// make every coverage defence in the file vacuous.
func ledgerFor(t *testing.T, s costStrategy) (strategy, reference CostRow) {
	t.Helper()

	model := s.model
	if model == "" {
		model = "test/candidate"
	}

	ledger := NewCostLedger(costPrices(t))
	for _, f := range costCorpus() {
		for run := range costRunsPerFixture {
			observeCostRun(ledger, "test/reference", f, run, calibratedCostRun)
			observeCostRun(ledger, model, f, run, s.run)
		}
	}

	return ledger.Row(model), ledger.Row("test/reference")
}

// observeCostRun scores one published review and books it, along the same path
// a
// real run takes: findings into ScoreRun, the Score into ObserveScore.
//
// The note behind it is in docs/measurement.md#observecostrun.
func observeCostRun(l *CostLedger, model string, f costFixture, run int,
	publish func(costFixture, int) ([]review.Finding, CallUsage, bool)) {
	findings, usage, reported := publish(f, run)

	result := RunResult{
		Model:   model,
		Fixture: f.Name,
		Run:     run + 1,
		Report:  &review.Report{Findings: findings},
		Usage:   TokenUsage{PerCall: []CallUsage{usage}},
	}
	if !reported {
		// A run that happened and reported nothing. Recorded as a review with an
		// incomplete usage, which is exactly what the harness produces and what
		// the ledger has to survive.
		result.Usage = TokenUsage{Unreported: 1}
	}

	l.ObserveScore(ScoreRun(result, f.Fixture))
}

// costScoreOf renders a reading for a failure message, so a reader sees the
// numbers rather than being told they disagreed.
func costScoreOf(c CostReading, r CostRow) string {
	got, ok := c.Score(r)
	if !ok {
		return "undefined"
	}

	parts := make([]string, 0, len(got))
	for i, v := range got {
		name := "?"
		if i < len(c.Columns) {
			name = c.Columns[i]
		}
		parts = append(parts, fmt.Sprintf("%s=%.6f", name, v))
	}
	return strings.Join(parts, " ")
}

// TestNoDegenerateCostStrategyCanMaxOutAPublishedCostReading asks of every cost
// number these reports publish the question nobody asked of the severity column
// that was withdrawn: WHAT MAXIMISES this?
//
// Cost has an ugly answer available to it that severity does not. A reviewer
// that fails most runs and succeeds cheaply on the easy ones is priced only on
// the runs it survived, and its $/DEFECT, real dollars, real detections, a
// correct division, describes an easier corpus than the row beneath it. Every
// term of the ratio is right and the ranking is wrong.
//
// Each cell below is DECLARED. A reading a degenerate strategy can score as well
// as a useful reviewer on does not measure what its name claims and must not be
// published; the failure names which reading and which strategy.
func TestNoDegenerateCostStrategyCanMaxOutAPublishedCostReading(t *testing.T) {
	readings := PublishedCostReadings()
	if len(readings) == 0 {
		t.Fatal("no cost reading is registered, so this test proves nothing about the cost table")
	}

	// The reference has to be a reviewer this corpus can reward, or
	// "no strategy beats it" is satisfied by it being unbeatable-because-broken.
	_, reference := ledgerFor(t, costStrategy{name: "control", run: calibratedCostRun})
	if !reference.Comparable() {
		t.Fatalf("the calibrated reviewer is not comparable against itself (missing %v, shallow %v)",
			reference.Missing, reference.Shallow)
	}
	if share, ok := reference.Recall(); !ok || share != 1 {
		t.Fatalf("the calibrated reviewer's recall is %v (defined: %v) over %d planted; every "+
			"comparison below is against a crippled reference", share, ok, reference.Planted)
	}
	for _, c := range readings {
		if _, ok := c.Score(reference); !ok {
			t.Fatalf("the cost reading %q is undefined for a calibrated reviewer over the whole "+
				"corpus; it cannot be compared against anything", c.Name)
		}
	}

	for _, s := range degenerateCostStrategies() {
		t.Run(s.name, func(t *testing.T) {
			got, ref := ledgerFor(t, s)

			for _, c := range readings {
				want, declared := s.maxes[c.Name]
				if !declared {
					t.Errorf("the cost reading %q (columns %s) is published and this table does not "+
						"say whether %q can max it out. Fill the cell: if the answer is yes, the "+
						"reading does not measure what it claims",
						c.Name, strings.Join(c.Columns, "/"), s.name)
					continue
				}

				maxed, defined := c.Maxes(got, ref)

				switch {
				case maxed && !want:
					t.Errorf("THE COST READING %q (columns %s) IS MAXED OUT BY %q, which %s.\n"+
						"  calibrated: %s\n  degenerate: %s\n"+
						"That reviewer is not better than a useful one and this reading cannot tell "+
						"them apart, so it does not measure %q and MUST NOT BE PUBLISHED. Withdraw "+
						"the column or change what it measures; do not re-tune it.",
						c.Name, strings.Join(c.Columns, "/"), s.name, s.why,
						costScoreOf(c, ref), costScoreOf(c, got), c.Doc)
				case !maxed && want && defined:
					t.Errorf("this table declares that %q maxes out %q and it does not (%s against a "+
						"calibrated %s). A stale declaration hides the next real one",
						s.name, c.Name, costScoreOf(c, got), costScoreOf(c, ref))
				case !defined && want:
					t.Errorf("this table declares that %q maxes out %q, but the reading is UNDEFINED "+
						"for it — it produced nothing to measure", s.name, c.Name)
				}
			}
		})
	}

	// Every reading must be falsifiable by SOMETHING here, or the crossing above
	// is decoration.
	for _, c := range readings {
		falsifiable := false
		for _, s := range degenerateCostStrategies() {
			if !s.maxes[c.Name] {
				falsifiable = true
			}
		}
		if !falsifiable {
			t.Errorf("every strategy in the degenerate cost table is declared able to max out %q, so "+
				"the table asserts nothing about it. Either it is not a score, or a reviewer that "+
				"would break it is missing from the table", c.Name)
		}
	}
}

// withoutColumns returns the reading with some columns removed, so a test can
// ask what the remaining ones can still tell apart.
func withoutColumns(c CostReading, drop []string) CostReading {
	keep := make([]int, 0, len(c.Columns))
	cols := make([]string, 0, len(c.Columns))
	for i, col := range c.Columns {
		if !slices.Contains(drop, col) {
			keep = append(keep, i)
			cols = append(cols, col)
		}
	}

	return CostReading{
		Name: c.Name, Columns: cols, Doc: c.Doc,
		Score: func(r CostRow) ([]float64, bool) {
			got, ok := c.Score(r)
			if !ok {
				return nil, false
			}
			out := make([]float64, 0, len(keep))
			for _, i := range keep {
				out = append(out, got[i])
			}
			return out, true
		},
	}
}

// TestEachCostStrategyIsCaughtByTheColumnItClaims turns the prose in the
// degenerate table into assertions.
//
// Every strategy above says which column sees it, "NOISE is what sees it",
// "only ANCHOR tells them apart". Those are the sentences that justify
// publishing five columns instead of one, and until this test they were
// comments. Removing the named columns must let the strategy through; if it does
// not, either the column is not doing the work the comment claims or the
// strategy does not embody the behaviour it describes, and both are things this
// table is supposed to know about itself.
//
// It found one on the way in. The line-by-line spammer was written spending MORE
// output tokens than a calibrated reviewer, so it was caught by $/DEFECT and
// NOISE could have been deleted without any guard noticing. The strategy did
// not embody its own justification.
func TestEachCostStrategyIsCaughtByTheColumnItClaims(t *testing.T) {
	for _, c := range PublishedCostReadings() {
		covered := map[string]bool{}

		for _, s := range degenerateCostStrategies() {
			if s.maxes[c.Name] {
				continue // declared able to max the whole reading, correctly
			}

			got, ref := ledgerFor(t, s)

			if len(s.caughtBy) == 0 {
				// Not caught by any column: the reading refuses to score the row
				// at all, because it is not comparable to its peers or its cost
				// is unknown or its ratio is undefined. That is a stronger
				// defence than a low score, an incomparable row's dollars are
				// CORRECT, so any number it produced would still sort.
				if _, ok := c.Score(got); ok {
					t.Errorf("%q declares that no column catches it, so the reading must refuse to "+
						"score it — and it scored %s", s.name, costScoreOf(c, got))
				}
				continue
			}

			for _, col := range s.caughtBy {
				if !slices.Contains(c.Columns, col) {
					t.Errorf("%q declares it is caught by %s and the reading %q has no such column",
						s.name, col, c.Name)
					continue
				}
				covered[col] = true
			}

			if maxed, _ := withoutColumns(c, s.caughtBy).Maxes(got, ref); !maxed {
				t.Errorf("%q is declared to be caught by %s, and the reading still catches it with "+
					"those column(s) removed. Something else is doing the work, so that column is "+
					"not load-bearing for this strategy and could be deleted with every guard here "+
					"still passing.\n  calibrated: %s\n  degenerate: %s",
					s.name, strings.Join(s.caughtBy, " and "),
					costScoreOf(c, ref), costScoreOf(c, got))
			}

			// And no PROPER subset of the named columns is enough, or the
			// declaration is wider than the truth and hides which one matters.
			for _, col := range s.caughtBy {
				if len(s.caughtBy) < 2 {
					break
				}
				if maxed, _ := withoutColumns(c, []string{col}).Maxes(got, ref); maxed {
					t.Errorf("%q declares it needs %s together, and removing %s alone already lets "+
						"it through", s.name, strings.Join(s.caughtBy, " and "), col)
				}
			}
		}

		for _, col := range c.Columns {
			if !covered[col] {
				t.Errorf("no strategy in the degenerate cost table is caught by %s, so the reading "+
					"%q asserts nothing about that column and it can be deleted without any guard "+
					"noticing. Either it is not a score, or the behaviour it exists to catch is "+
					"missing from the table", col, c.Name)
			}
		}
	}
}

// TestIncomparableRowsAreNotScoredAtAll pins the mechanism the two coverage
// strategies rest on.
//
// An incomparable row's dollars are CORRECT. What is wrong is the corpus they
// describe, so the defence cannot be a smaller score. It has to be a refusal to
// score, or a reader sorting the column still gets an ordering out of it.
func TestIncomparableRowsAreNotScoredAtAll(t *testing.T) {
	for _, name := range []string{
		"quit on the fixtures that cost the most",
		"report no usage on the runs that found nothing",
	} {
		var found bool
		for _, s := range degenerateCostStrategies() {
			if s.name != name {
				continue
			}
			found = true

			row, _ := ledgerFor(t, s)
			if row.Comparable() {
				t.Errorf("%q produced a comparable row (covered %d of %d, missing %v, shallow %v)",
					name, row.Covered, row.PeerCoverage, row.Missing, row.Shallow)
			}
			if row.coverageReason() == "" {
				t.Errorf("%q is not comparable and the row says nothing about why", name)
			}
			for _, c := range PublishedCostReadings() {
				if _, ok := c.Score(row); ok {
					t.Errorf("%q is not comparable and %q scored it anyway", name, c.Name)
				}
			}
		}
		if !found {
			t.Errorf("the strategy %q is gone from the degenerate table; this test asserts nothing", name)
		}
	}
}

// TestCostReadingsTreatATieAsAFailure pins the comparison itself.
//
// A degenerate strategy that merely TIES a useful reviewer has already shown the
// reading cannot tell them apart, and a tie is exactly what the withdrawn banded
// severity column scored. Maxes must therefore be componentwise >=, not >.
func TestCostReadingsTreatATieAsAFailure(t *testing.T) {
	tie, reference := ledgerFor(t, costStrategy{name: "identical", run: calibratedCostRun})

	for _, c := range PublishedCostReadings() {
		maxed, defined := c.Maxes(tie, reference)
		if !defined {
			t.Fatalf("%q is undefined for a calibrated reviewer", c.Name)
		}
		if !maxed {
			t.Errorf("%q reports that a reviewer scoring identically to the reference did not max it "+
				"out. A tie means the reading cannot tell them apart", c.Name)
		}
	}
}

// -----------------------------------------------------------------------------
// Registration: every column in the cost table is declared.
// -----------------------------------------------------------------------------

// TestEveryCostColumnIsRegistered is the structural half of the guard, and it is
// separate from the score-table one on purpose.
//
// score.go registers CostTableHeader as a table whose columns another track
// classifies, so the guard that runs over the SCORED headers deliberately does
// not read it. That leaves the cost columns declared nowhere unless this test
// exists, which is precisely the hole the withdrawn banded columns went
// through: added to a header and a legend, with nothing anywhere asking what
// maximised them.
func TestEveryCostColumnIsRegistered(t *testing.T) {
	known := map[string]string{}
	for _, c := range PublishedCostReadings() {
		for _, col := range c.Columns {
			known[col] = "cost reading " + c.Name
		}
	}
	for _, col := range DescriptiveCostColumns() {
		if from, ok := known[col]; ok {
			t.Errorf("the column %q is declared both descriptive and part of %s. Descriptive columns "+
				"are never asked what maximises them, so a score listed there escapes the degenerate "+
				"table", col, from)
		}
		known[col] = "descriptive"
	}

	for _, col := range strings.Fields(CostTableHeader) {
		if _, ok := known[col]; !ok {
			t.Errorf("the published column %q in the cost table is declared nowhere. A score must be "+
				"registered in PublishedCostReadings, where the degenerate-strategy table asks what "+
				"maximises it", col)
		}
	}
}

// TestTheCostHeaderPrintsEveryReadingWhole is the other half of registration.
//
// A declared column is not enough: these columns are only a score read TOGETHER.
// Deleting NOISE from the header while leaving it in the reading would publish
// $/DEFECT beside a recall figure that a line-by-line reviewer maxes out, and
// every guard in this file would still pass.
func TestTheCostHeaderPrintsEveryReadingWhole(t *testing.T) {
	printed := map[string]bool{}
	for _, col := range strings.Fields(CostTableHeader) {
		printed[col] = true
	}

	for _, c := range PublishedCostReadings() {
		touches, missing := false, []string(nil)
		for _, col := range c.Columns {
			if printed[col] {
				touches = true
			} else {
				missing = append(missing, col)
			}
		}
		if touches && len(missing) > 0 {
			t.Errorf("the cost table prints part of the reading %q and is missing %s. Its columns are "+
				"a score only read together — %s — so a header offering a subset offers a number "+
				"whose meaning depends on one nobody printed",
				c.Name, strings.Join(missing, " "), c.Doc)
		}
	}
}

// columnStarts returns the offset at which each run of non-space text begins.
func columnStarts(line string) []int {
	var out []int
	for i, r := range line {
		if r == ' ' {
			continue
		}
		if i == 0 || line[i-1] == ' ' {
			out = append(out, i)
		}
	}
	return out
}

// TestTheCostTableRendersEveryHeaderColumn keeps the header and the rows lined
// up, by OFFSET rather than by count.
//
// A header column with no cell under it is a caveat a reader never sees, and the
// widths here are hand-maintained in two places, the header string and a printf
// format, with nothing tying them together.
//
// THE BUG this offset check exists for: RECALL was formatted eight wide and its
// cell is "14/14 1.00", which is ten characters, so every column to its right
// printed two characters out of line on every row of the table. Counting fields
// could not see it, and neither could a reader who was not looking for it.
func TestTheCostTableRendersEveryHeaderColumn(t *testing.T) {
	ledger := NewCostLedger(costPrices(t))
	for _, f := range costCorpus() {
		for run := range costRunsPerFixture {
			observeCostRun(ledger, "test/reference", f, run, calibratedCostRun)
		}
	}

	table := ledger.Table()
	var row string
	for _, line := range strings.Split(table, "\n") {
		if strings.HasPrefix(line, "test/reference") {
			row = line
		}
	}
	if row == "" {
		t.Fatalf("no row was rendered for the only model in the ledger:\n%s", table)
	}

	// The reference row is deliberately the one whose every cell is populated:
	// a row full of dashes would line up under any widths at all.
	starts := columnStarts(row)
	for i, at := range columnStarts(CostTableHeader) {
		if i >= len(starts) {
			t.Errorf("the header declares %d columns and the row starts only %d. A column with no "+
				"cell under it is a disclosure nobody reads.\n  header: %s\n  row:    %s",
				len(columnStarts(CostTableHeader)), len(starts), CostTableHeader, row)
			break
		}
		if !slices.Contains(starts, at) {
			t.Errorf("the header's column %d begins at offset %d and no cell in the row does. One of "+
				"the widths in CostTableHeader and the row's format string moved without the other, "+
				"so every column to the right of it prints out of line.\n  header: %s\n  row:    %s",
				i+1, at, CostTableHeader, row)
			break
		}
	}

	// The legend is printed every time, not only when a row looks suspicious: a
	// caveat that appears only sometimes teaches a reader that its absence means
	// the column is safe to sort on.
	if !strings.Contains(table, CostReadingLegend) {
		t.Error("the cost table was rendered without its legend")
	}

	// And the counts behind the rates. RECALL carries its pair in the cell;
	// NOISE cannot, because a rate and its counts do not fit in six characters,
	// so the block underneath is the only place a reader learns that a NOISE of
	// 0.33 is one invented finding in three reviews rather than a hundred in
	// three hundred. Those two rows are the same number and not the same result.
	if !strings.Contains(table, "DENOMINATORS") {
		t.Errorf("the cost table prints rates with no counts beside them:\n%s", table)
	}
	planted := 0
	for _, f := range costCorpus() {
		planted += len(f.Defects) * costRunsPerFixture
	}
	if !strings.Contains(table, fmt.Sprintf("RECALL %d/%d defects", planted, planted)) {
		t.Errorf("the denominators block does not state the calibrated row's recall counts "+
			"(%d/%d):\n%s", planted, planted, table)
	}
	if !strings.Contains(table, "priced review(s)") {
		t.Errorf("NOISE is published with no denominator anywhere:\n%s", table)
	}
}

// TestDescriptiveCostColumnsAreNotScores states the classification's own risk.
//
// A column listed as descriptive is never asked what maximises it, so putting a
// score there by mistake is how one escapes the degenerate table entirely. COST
// and TOKENS are the clearest case: a row is not better for having spent less in
// total than a row that reviewed a different number of fixtures.
func TestDescriptiveCostColumnsAreNotScores(t *testing.T) {
	descriptive := DescriptiveCostColumns()
	for _, want := range []string{"COST", "TOKENS", "COV", "PRICED"} {
		if !slices.Contains(descriptive, want) {
			t.Errorf("%q is not declared descriptive, and it is not a score either", want)
		}
	}

	sorted := slices.Clone(descriptive)
	sort.Strings(sorted)
	for i := 1; i < len(sorted); i++ {
		if sorted[i] == sorted[i-1] {
			t.Errorf("%q is listed twice", sorted[i])
		}
	}
}
