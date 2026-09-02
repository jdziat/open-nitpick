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
// It prints deterministic columns only — RECALL, NOISE, ANCHOR, and the
// per-band located counts — because Rule 1 says those are the primary
// evidence and because the question this corpus asks (does reading the callee
// find the defect?) is answered by a keyword on a line, not by a grade.
//
// The default corpus is MultiFileFixtures; NITPICK_EVAL_FIXTURES selects any
// fixtures by name, so the same two-variant table can be printed for the
// tuning corpus to measure what related context costs where it is not needed.
func TestBenchmarkMultiFile(t *testing.T) {
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
		if cerr := dump.Close(); cerr != nil {
			t.Errorf("closing the run dump: %v", cerr)
		}
	}()
	t.Logf("retaining every review to %s", dumpPath)

	type row struct {
		name     string
		reviews  int
		lost     int
		plants   int
		located  int
		findings int
		noise    int
		anchor   int
		byBand   map[config.Severity][2]int // located, plants
		perFix   map[string]string          // fixture -> "located/plants (noise)"
	}
	var (
		mu   sync.Mutex
		rows = map[string]*row{}
	)
	record := func(name string, f Fixture, findings []review.Finding, runErr error) {
		mu.Lock()
		defer mu.Unlock()
		r, ok := rows[name]
		if !ok {
			r = &row{name: name, byBand: map[config.Severity][2]int{}, perFix: map[string]string{}}
			rows[name] = r
		}
		if runErr != nil {
			r.lost++
			r.perFix[f.Name] = "lost"
			return
		}
		r.reviews++
		d := ScoreDetection(f, findings)
		r.plants += len(f.Defects)
		r.located += d.Matched
		r.findings += len(findings)
		r.noise += d.Noise()
		r.anchor = max(r.anchor, d.WidestAnchor)
		for _, def := range f.Defects {
			b := r.byBand[def.WantSeverity]
			b[1]++
			if d.Detected[def.Why] {
				b[0]++
			}
			r.byBand[def.WantSeverity] = b
		}
		cell := fmt.Sprintf("%d/%d", d.Matched, len(f.Defects))
		if n := d.Noise(); n > 0 {
			cell += fmt.Sprintf(" +%dn", n)
		}
		if prev, ok := r.perFix[f.Name]; ok && prev != "" {
			cell = prev + " " + cell
		}
		r.perFix[f.Name] = cell
	}

	// Incumbents first, from cache where there is one. A missing cache is
	// collected live when the CLI is available, and reported as absent when
	// it is not — never scored as a clean review.
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
		{ContenderModel, contenderCacheDir, CachedContender, ContenderAvailable, func(ctx context.Context, fx []Fixture, dir string, log func(string)) error {
			_, _, err := CollectContender(ctx, fx, dir, opts.Timeout, log)
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
				record(inc.name, f, nil, fmt.Errorf("no review"))
				continue
			}
			record(inc.name, f, findings, nil)
			_ = dump.Record(DumpSample{Model: inc.name, Fixture: f, Run: 0, Findings: findings})
		}
	}

	// Our side: each model twice, with related context off and on.
	variants := []struct {
		suffix string
		tune   func(*config.Config)
	}{
		{"", func(c *config.Config) { c.Review.RelatedContext = false }},
		{" +ctx", func(c *config.Config) { c.Review.RelatedContext = true }},
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for _, model := range opts.Models {
		for _, v := range variants {
			name := model.ID + v.suffix
			runOpts := opts
			runOpts.Tune = v.tune
			for _, f := range opts.Fixtures {
				for i := range runs {
					wg.Add(1)
					go func(model Model, f Fixture, i int, name string, runOpts Options, v struct {
						suffix string
						tune   func(*config.Config)
					}) {
						defer wg.Done()
						sem <- struct{}{}
						defer func() { <-sem }()

						res := Run(ctx, model, f, i, runOpts)
						var findings []review.Finding
						if res.Err == nil && res.Report != nil {
							findings = res.Report.Findings
						}
						record(name, f, findings, res.Err)
						if res.Err != nil {
							t.Logf("%s on %s run %d: %v", name, f.Name, i, res.Err)
							return
						}
						_ = dump.Record(DumpSample{Model: model.ID, Variant: strings.TrimSpace(v.suffix), Fixture: f, Run: i, Findings: findings})
					}(model, f, i, name, runOpts, v)
				}
			}
		}
	}
	wg.Wait()

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
	fmt.Fprintf(&b, "\n%-42s %7s %7s %7s %6s %6s  %s\n", "contender", "RECALL", "NOISE", "ANCHOR", "REV", "LOST", "critical/error/warning/info/nit")
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
		fmt.Fprintf(&b, "%-42s %7s %7s %7d %6d %6d  %s\n", n,
			ratio(r.located, r.plants), ratio(r.noise, r.reviews), r.anchor, r.reviews, r.lost, strings.Join(bands, " "))
	}
	b.WriteString("\nRECALL = located/plants over every review; NOISE = findings explaining no plant, per review; ANCHOR = widest anchor in lines.\n")
	b.WriteString("Our side is listed once per model without related context and once with it (+ctx).\n\n")

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

// TestCollectContender collects Contender reviews for the selected fixtures,
// one at a time, caching each. Requires `contender login`.
func TestCollectContender(t *testing.T) {
	ctx := context.Background()
	if !ContenderAvailable(ctx) {
		t.Skip("contender CLI not installed or not signed in; run 'contender login'")
	}

	opts, err := OptionsFromEnv()
	if err != nil {
		t.Fatalf("options: %v", err)
	}
	if strings.TrimSpace(os.Getenv(EnvFixtures)) == "" {
		opts.Fixtures = MultiFileFixtures()
	}

	collected, remaining, err := CollectContender(ctx, opts.Fixtures, contenderCacheDir, opts.Timeout,
		func(line string) { t.Log(line) })
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	t.Logf("collected %d this pass, %d still outstanding", collected, remaining)
}
