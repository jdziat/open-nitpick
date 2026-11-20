//go:build eval

package evals

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/review"
)

// TestBenchmarkMultiFile is the judge-free head-to-head on the multi-file
// corpus: every model in the battery, with and without related context,
// against every incumbent that has a cached or collectable review.
//
// It prints deterministic columns only, RECALL, NOISE, ANCHOR, and the
// per-band located counts, because Rule 1 says those are the primary
// evidence and because the question this corpus asks (does reading the callee
// find the defect?) is answered by a keyword on a line, not by a grade.
//
// The default corpus is MultiFileFixtures; NITPICK_EVAL_FIXTURES selects any
// fixtures by name, so the same two-variant table can be printed for the
// tuning corpus to measure what related context costs where it is not needed.
func TestBenchmarkMultiFile(t *testing.T) {
	if err := requireQualityPurchaseSetup(); err != nil {
		t.Fatal(err)
	}
	variants, err := qualityVariants(os.Getenv("NITPICK_EVAL_VARIANTS"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	opts, err := OptionsFromEnv()
	if err != nil {
		t.Fatalf("options: %v", err)
	}
	if strings.TrimSpace(os.Getenv(EnvFixtures)) == "" {
		opts.Fixtures = MultiFileFixtures()
	}
	runs := max(opts.Runs, 1)

	t.Logf("corpus: %s   runs per fixture (our side): %d   models: %d", CorpusLabel(opts.Fixtures), runs, len(opts.Models))

	dump, dumpPath, err := OpenRunDump("multifile", opts.Fixtures)
	if err != nil {
		t.Fatalf("open the run dump: %v", err)
	}
	defer func() {
		if cerr := FinalizeQualityDump(dump, dumpPath, opts.Fixtures); cerr != nil {
			t.Errorf("finalize the run evidence: %v", cerr)
		}
	}()
	t.Logf("retaining every review to %s", dumpPath)

	var (
		mu   sync.Mutex
		rows = map[string]*evaluationCell{}
	)
	record := func(name string, f Fixture, findings []review.Finding, runErr error, cost Cost, res RunResult) {
		mu.Lock()
		defer mu.Unlock()
		r := rows[name]
		if r == nil {
			r = &evaluationCell{}
			rows[name] = r
		}
		r.record(f, findings, runErr, cost, res)
	}

	// Incumbents first, from cache where there is one. A missing cache is
	// collected live when the CLI is available, and reported as absent when
	// it is not, never scored as a clean review.
	type incumbent struct {
		name      string
		cacheDir  string
		cached    func(string, Fixture) ([]review.Finding, bool)
		available func(context.Context) bool
		collect   func(context.Context, []Fixture, string, func(string)) error
	}
	incumbents := []incumbent{
		{IncumbentModel, crCacheDir, CachedIncumbent, IncumbentAvailable, func(ctx context.Context, fx []Fixture, dir string, log func(string)) error {
			_, _, err := CollectIncumbent(ctx, fx, dir, opts.Timeout, log)
			return err
		}},
	}
	for _, inc := range incumbents {
		var missing []Fixture
		for _, f := range opts.Fixtures {
			if _, ok := inc.cached(inc.cacheDir, f); !ok {
				missing = append(missing, f)
			}
		}
		if len(missing) > 0 {
			if inc.available(ctx) {
				t.Logf("%s: collecting %d uncached fixture(s) live", inc.name, len(missing))
				if err := inc.collect(ctx, missing, inc.cacheDir, func(line string) { t.Logf("%s: %s", inc.name, line) }); err != nil {
					t.Logf("%s: collection stopped: %v", inc.name, err)
				}
			} else {
				t.Logf("%s: CLI not installed or not signed in; %d fixture(s) have no review and are reported as absent", inc.name, len(missing))
			}
		}
		for _, f := range opts.Fixtures {
			findings, ok := inc.cached(inc.cacheDir, f)
			if !ok {
				record(inc.name, f, nil, fmt.Errorf("no review"), Cost{}, RunResult{})
				continue
			}
			record(inc.name, f, findings, nil, Cost{}, RunResult{})
			_ = dump.Record(DumpSample{Model: inc.name, Fixture: f, Run: 0, Findings: findings})
		}
	}

	// Our side: retain both ablations and measure the shipped production default.
	var routing routeTally

	registeredVariants := map[string][2]bool{}
	for index := range variants {
		if os.Getenv("NITPICK_EVAL_GENERATION_LOOKUPS") == "1" {
			variants[index].suffix += " +lookup"
		}
		registeredVariants[strings.TrimSpace(variants[index].suffix)] = variants[index].context
	}
	if err := WriteExperimentManifest(dumpPath+".manifest.json", opts, registeredVariants, opts.SampleOffset, manifestTypeFromEnv(), manifestCorpusFromEnv(), manifestStagesFromEnv()); err != nil {
		t.Fatalf("freeze experiment: %v", err)
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for _, model := range opts.Models {
		for _, v := range variants {
			name := model.ID + v.suffix
			runOpts := opts
			runOpts.Tune = func(c *config.Config) {
				c.Review.RelatedContext, c.Review.RelatedContextCallers = v.context[0], v.context[1]
			}
			for _, f := range opts.Fixtures {
				for i := range runs {
					wg.Add(1)
					go func(model Model, f Fixture, i int, name string, runOpts Options, v qualityVariant) {
						defer wg.Done()
						sem <- struct{}{}
						defer func() { <-sem }()

						res := Run(ctx, model, f, i, runOpts)
						var findings []review.Finding
						if res.Err == nil && res.Report != nil {
							findings = res.Report.Findings
						}
						var cost Cost
						if model.Composite() {
							cost = compositeCost(opts.Prices, res.UsageByModel)
						} else if price, ok := opts.Prices.Price(model.ID); ok {
							cost = Cost{USD: price.Cost(res.Usage), Known: res.Usage.Complete() && res.Usage.Failed == 0}
						}
						if res.Report != nil && len(res.Report.Routes) > 0 {
							routing.note(name, res.Report.Routes)
						}
						measurement := &SampleMeasurement{SampleID: fmt.Sprintf("%s/%s/%d", model.ID, f.Name, res.Run),
							Duration: res.Duration, Usage: res.Usage, Cost: cost, CacheHits: res.Replay.Hits, CacheMisses: res.Replay.Misses}
						if res.Err != nil {
							measurement.Failure = res.Err.Error()
						}
						if err := CaptureSample(opts.CaptureDir, v.suffix, f, res); err != nil {
							t.Errorf("capture %s/%s: %v", name, f.Name, err)
						}
						if err := dump.Record(DumpSample{Model: model.ID, Variant: strings.TrimSpace(v.suffix), Fixture: f,
							Run: res.Run, Findings: findings, Measurement: measurement}); err != nil {
							t.Errorf("record %s/%s: %v", name, f.Name, err)
						}
						record(name, f, findings, res.Err, cost, res)
						if res.Err != nil {
							t.Logf("%s on %s run %d: %v", name, f.Name, i, res.Err)
							return
						}
					}(model, f, i, name, runOpts, v)
				}
			}
		}
	}
	wg.Wait()

	if text := routing.String(); text != "" {
		t.Log(text)
	}

	// The table, sorted by recall then noise.
	names := make([]string, 0, len(rows))
	for n := range rows {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		a, b := rows[names[i]], rows[names[j]]
		ra, rb := frac(a.located, a.plants), frac(b.located, b.plants)
		if ra != rb {
			return ra > rb
		}
		return frac(a.noise, a.reviews) < frac(b.noise, b.reviews)
	})

	var b strings.Builder
	fmt.Fprintf(&b, "\n%-42s %7s %7s %5s %7s %6s %6s %9s %10s  %s\n", "contender", "RECALL", "NOISE", "NEAR", "ANCHOR", "REV", "LOST", "$/REVIEW", "$/LOCATED", "critical/error/warning/info/nit")
	fmt.Fprintf(&b, "%-42s %7s %9s %9s\n", "", "SEC/REV", "THINK/REV", "OUT/REV")
	for _, n := range names {
		r := rows[n]
		bands := make([]string, 0, 5)
		for _, lvl := range []config.Severity{config.SeverityCritical, config.SeverityError, config.SeverityWarning, config.SeverityInfo, config.SeverityNit} {
			c := r.byBand[lvl]
			if c[1] == 0 {
				bands = append(bands, "-")
				continue
			}
			bands = append(bands, fmt.Sprintf("%d/%d", c[0], c[1]))
		}
		perReview, perLocated := "n/a", "n/a"
		if r.priced > 0 {
			perReview = fmt.Sprintf("$%.4f", r.usd/float64(r.priced))
			if r.located > 0 && r.priced == r.reviews {
				perLocated = fmt.Sprintf("$%.4f", r.usd/float64(r.located))
			}
		}
		fmt.Fprintf(&b, "%-42s %7s %7s %5d %7d %6d %6d %9s %10s  %s\n", n,
			ratio(r.located, r.plants), ratio(r.noise, r.reviews), r.near, r.anchor, r.reviews, r.lost, perReview, perLocated, strings.Join(bands, " "))
		sec, think, out := "n/a", "n/a", "n/a"
		if r.timed > 0 {
			sec = fmt.Sprintf("%.1f", r.seconds/float64(r.timed))
		}
		if r.metered > 0 {
			think = fmt.Sprintf("%d", r.reason/r.metered)
			out = fmt.Sprintf("%d", r.complete/r.metered)
		}
		fmt.Fprintf(&b, "%-42s %7s %9s %9s\n", "", sec, think, out)
	}
	b.WriteString("\nRECALL = located/plants over every review; NOISE = findings explaining no plant, per review; NEAR = of those, findings naming a plant's keywords in its file beyond the anchor tolerance; ANCHOR = widest anchor in lines.\n")
	b.WriteString("SEC/REV is wall time per scheduled review, including failures; THINK/REV and OUT/REV are provider-reported reasoning and completion tokens per review (reasoning is a subset of completion), over reviews whose every call reported usage.\n")
	b.WriteString("$/REVIEW is the provider-reported spend per review at the shipped rate table; $/LOCATED divides the run's spend by the plants it located, and is n/a when any review went unpriced.\n")
	b.WriteString("Our side: diff-only, definitions and callers (+ctx), and definitions only (+default). REV counts scheduled reviews; LOST are included in its denominator.\n\n")

	fmt.Fprintf(&b, "%-32s", "fixture")
	for _, n := range names {
		fmt.Fprintf(&b, " %-26s", trimName(n))
	}
	b.WriteString("\n")
	for _, f := range opts.Fixtures {
		fmt.Fprintf(&b, "%-32s", f.Name)
		for _, n := range names {
			fmt.Fprintf(&b, " %-26s", rows[n].perFix[f.Name])
		}
		b.WriteString("\n")
	}
	t.Log(b.String())
}

func frac(n, d int) float64 {
	if d == 0 {
		return 0
	}
	return float64(n) / float64(d)
}

func ratio(n, d int) string {
	if d == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.2f", float64(n)/float64(d))
}

func trimName(n string) string {
	// An incumbent is "vendor/cli"; the vendor is the informative half.
	if strings.HasSuffix(n, "/cli") {
		return strings.TrimSuffix(n, "/cli")
	}
	if i := strings.Index(n, "/"); i >= 0 {
		n = n[i+1:]
	}
	if len(n) > 26 {
		n = n[:26]
	}
	return n
}

// compositeCost prices a composite run: every model's usage at its own rate,
// known only when every model is priced and reported its usage.
func compositeCost(prices *PriceTable, byModel map[string]TokenUsage) Cost {
	if prices == nil || len(byModel) == 0 {
		return Cost{}
	}
	total := 0.0
	for name, u := range byModel {
		if u.Calls() == 0 {
			continue
		}
		price, ok := prices.Price(name)
		if !ok || !u.Complete() {
			return Cost{}
		}
		total += price.Cost(u)
	}
	return Cost{USD: total, Known: true}
}

// routeTally counts where a composite run's batches went, per contender.
type routeTally struct {
	mu     sync.Mutex
	counts map[string]map[string]int // contender, then "route/model [+ensemble]", then batches
}

func (r *routeTally) note(contender string, routes []review.RouteDecision) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.counts == nil {
		r.counts = map[string]map[string]int{}
	}
	if r.counts[contender] == nil {
		r.counts[contender] = map[string]int{}
	}
	for _, d := range routes {
		key := d.Route
		if key == "" {
			key = "default"
		}
		key += " → " + d.Reviewer
		if len(d.Ensemble) > 0 {
			key += " + " + strings.Join(d.Ensemble, ", ")
		}
		if len(d.Kinds) > 0 {
			key += " [" + strings.Join(d.Kinds, ",") + "]"
		}
		r.counts[contender][key]++
	}
}

func (r *routeTally) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.counts) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\nWhere the batches went:\n")
	names := make([]string, 0, len(r.counts))
	for n := range r.counts {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(&b, "  %s\n", n)
		keys := make([]string, 0, len(r.counts[n]))
		for k := range r.counts[n] {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "    %4d  %s\n", r.counts[n][k], k)
		}
	}
	return b.String()
}
