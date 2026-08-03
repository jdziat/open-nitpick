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

// variant is one persona configuration under test.
type variant struct {
	Name    string
	Persona config.Persona
}

func boolPtr(b bool) *bool { return &b }

// nitpickVariants isolate the nitpick axis with voice held constant, so a
// difference in the score is attributable to scope rather than wording.
func nitpickVariants() []variant {
	base := config.DefaultPersona()

	out := make([]variant, 0, 4)
	for _, level := range []config.NitpickLevel{
		config.NitpickOff, config.NitpickMinimal, config.NitpickNormal, config.NitpickPedantic,
	} {
		p := base
		p.Nitpick = level
		out = append(out, variant{Name: "nitpick=" + string(level), Persona: p})
	}
	return out
}

// voiceVariants isolate the wording axes with scope held constant.
func voiceVariants() []variant {
	base := config.DefaultPersona()

	mk := func(name string, mutate func(*config.Persona)) variant {
		p := base
		mutate(&p)
		return variant{Name: name, Persona: p}
	}

	return []variant{
		mk("voice=default", func(*config.Persona) {}),
		mk("voice=terse+blunt", func(p *config.Persona) {
			p.Verbosity = config.VerbosityTerse
			p.Politeness = config.PolitenessBlunt
		}),
		mk("voice=detailed+warm", func(p *config.Persona) {
			p.Verbosity = config.VerbosityDetailed
			p.Politeness = config.PolitenessWarm
		}),
		mk("voice=hedged+author", func(p *config.Persona) {
			p.Confidence = config.ConfidenceHedged
			p.Address = config.AddressAuthor
		}),
		mk("voice=warm+praise", func(p *config.Persona) {
			p.Politeness = config.PolitenessWarm
			p.Praise = boolPtr(true)
		}),
	}
}

// scored is one variant's judged outcome.
type scored struct {
	variant  string
	agg      Aggregate
	failures int
	notes    []string
}

// TestTunePersona measures the nitpick levels against one shared corpus.
//
// Each fixture is reviewed ONCE at the generation scope and judged ONCE. The
// levels are then applied offline with review.Filter, and each level's stats are
// derived from the same judged verdicts.
//
// This is the payoff of generate-once-then-filter, and the harness has to match
// the design or it measures nothing: running a separate review per level would
// send three byte-identical prompts, pay three times for it, and reintroduce
// exactly the model-variance confound the design removes — two levels would
// differ because the model answered differently, not because the filter did
// anything.
func TestTunePersona(t *testing.T) {
	opts := OptionsFromEnv()

	judge, err := NewJudge(strings.TrimSpace(os.Getenv(EnvJudgeModel)))
	if err != nil {
		t.Fatalf("build judge: %v", err)
	}

	model := opts.Models[0]

	// AXIS was exported by the Makefile and read by nothing, so `make tune
	// AXIS=voice` silently measured the nitpick axis instead — a paid flag that
	// did nothing, which is the worst kind of instrument.
	if axis := strings.TrimSpace(os.Getenv("NITPICK_EVAL_AXIS")); axis != "" &&
		axis != "nitpick" && axis != "voice" {
		t.Fatalf("NITPICK_EVAL_AXIS=%q is not one of: nitpick, voice", axis)
	}
	if strings.TrimSpace(os.Getenv("NITPICK_EVAL_AXIS")) == "voice" {
		runVoiceAxis(t, judge, model, opts)
		return
	}

	levels := []config.NitpickLevel{
		config.NitpickOff, config.NitpickMinimal, config.NitpickNormal, config.NitpickPedantic,
	}

	t.Logf("judge: %s   reviewer: %s   fixtures: %d   levels: %d (one review each, filtered offline)",
		judge.Model(), model.ID, len(opts.Fixtures), len(levels))

	results := runLevels(t, judge, model, levels, opts)
	reportVariants(t, results)
}

// runLevels reviews each fixture once and derives every level from that corpus.
func runLevels(t *testing.T, judge *Judge, model Model, levels []config.NitpickLevel, opts Options) []scored {
	t.Helper()

	ctx := context.Background()

	out := make([]scored, len(levels))
	for i, l := range levels {
		out[i] = scored{variant: "nitpick=" + string(l)}
	}

	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		sem = make(chan struct{}, 4)
	)

	for _, f := range opts.Fixtures {
		wg.Add(1)

		go func(f Fixture) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			// One review, at the generation scope. Pedantic is measured as a
			// filter here too: its extra style pass is a separate concern and
			// would confound the comparison.
			persona := config.DefaultPersona()
			persona.Nitpick = config.GenerationLevel

			result := RunWithPersona(ctx, model, f, 1, opts, persona)

			mu.Lock()
			if result.Err != nil {
				for i := range out {
					out[i].failures++
					out[i].notes = append(out[i].notes, fmt.Sprintf("%s: review failed: %v", f.Name, result.Err))
				}
				mu.Unlock()
				return
			}
			mu.Unlock()

			corpus := result.Report.Findings

			// One judgement of the whole corpus; each level reuses the verdicts
			// for the findings it keeps.
			assessment, err := judge.Judge(ctx, f, persona, corpus)

			mu.Lock()
			defer mu.Unlock()

			if err != nil {
				for i := range out {
					out[i].failures++
					out[i].notes = append(out[i].notes, fmt.Sprintf("%s: judge failed: %v", f.Name, err))
				}
				return
			}

			byIndex := map[int]Verdict{}
			for _, v := range assessment.Verdicts {
				byIndex[v.Index] = v
			}

			for i, level := range levels {
				kept, _ := review.Filter(corpus, level, config.SeverityNit)

				sub := &JudgeResult{
					Missed:        assessment.Missed,
					SignalToNoise: assessment.SignalToNoise,
					ToneAdherence: assessment.ToneAdherence,
					Grade:         assessment.Grade,
				}
				for idx, finding := range corpus {
					if !containsFinding(kept, finding) {
						continue
					}
					if v, ok := byIndex[idx]; ok {
						sub.Verdicts = append(sub.Verdicts, v)
					}
				}

				if problems := out[i].agg.Add(sub, len(sub.Verdicts)); len(problems) > 0 {
					for _, p := range problems {
						out[i].notes = append(out[i].notes, fmt.Sprintf("%s: JUDGE OUTPUT SUSPECT: %s", f.Name, p))
					}
				}

				if f.Clean() && len(kept) > 0 {
					out[i].notes = append(out[i].notes,
						fmt.Sprintf("%s: %d finding(s) on a clean change", f.Name, len(kept)))
				}
			}
		}(f)
	}

	wg.Wait()
	return out
}

// containsFinding reports whether a finding survived the filter, matched on its
// dedup key rather than by pointer.
func containsFinding(set []review.Finding, f review.Finding) bool {
	for _, s := range set {
		if s.Key() == f.Key() {
			return true
		}
	}
	return false
}

// reportVariants prints the comparison table.
func reportVariants(t *testing.T, results []scored) {
	t.Helper()

	// A variant with no findings has UNDEFINED precision, not perfect precision.
	// Sorting it to the top would make silence look like the best strategy.
	sort.Slice(results, func(i, j int) bool {
		a, b := results[i].agg, results[j].agg
		if a.HasFindings() != b.HasFindings() {
			return a.HasFindings()
		}
		if !a.HasFindings() {
			return results[i].variant < results[j].variant
		}
		return a.Precision() > b.Precision()
	})

	var b strings.Builder
	b.WriteString("\n")
	b.WriteString("VARIANT                 FINDINGS  REAL  WORTH  PRECISION  INFLATED  MISCLASS  TONE-OFF  MISSED  SIGNAL  GRADE\n")
	b.WriteString("-----------------------------------------------------------------------------------------------------------\n")

	for _, r := range results {
		a := r.agg
		precision := "n/a"
		if a.HasFindings() {
			precision = fmt.Sprintf("%.2f", a.Precision())
		}

		fmt.Fprintf(&b, "%-23s %-9d %-5d %-6d %-10s %-9d %-9d %-9d %-7d %-7.1f %.2f\n",
			truncate(r.variant, 23), a.Findings, a.Real, a.WorthRaising, precision,
			a.Inflated, a.Misclassed, a.ToneOff, a.Missed, a.MeanSignal(), a.MeanGrade())
	}

	t.Log(b.String())

	for _, r := range results {
		if len(r.notes) == 0 {
			continue
		}
		t.Logf("%s:", r.variant)
		for _, n := range r.notes {
			t.Logf("  %s", n)
		}
	}

	// The suite fails only when the judge says the reviewer is not worth
	// reading. A specific variant scoring poorly is information, not a defect —
	// that is what the knob is for.
	// Iterate every variant rather than guarding on the top row: the previous
	// form short-circuited whenever the top-sorted variant had no findings,
	// which is exactly the case that most needed catching.
	best, any := 0.0, false
	for _, r := range results {
		if !r.agg.HasFindings() {
			t.Errorf("%s produced no findings at all across the corpus; "+
				"silence is not a passing result", r.variant)
			continue
		}
		any = true
		if p := r.agg.Precision(); p > best {
			best = p
		}
	}
	if any && best < 0.5 {
		t.Errorf("best variant precision %.2f: a senior reviewer would not raise most of what this produces", best)
	}
}

// TestJudgeModels ranks every model in the battery by JUDGED quality.
//
// TestPrompts measures keyword recall against planted defects: did the model
// find a bug we already knew about. That is a floor, not a ranking — it cannot
// say whether a finding was worth a colleague's attention, whether its severity
// was honest, or whether it labelled the problem correctly. Only a judgement
// call answers those, which is what this test is for.
//
// Each model reviews every fixture once, and a strong model assesses the result
// as a senior engineer would.
func TestJudgeModels(t *testing.T) {
	opts := OptionsFromEnv()

	judge, err := NewJudge(strings.TrimSpace(os.Getenv(EnvJudgeModel)))
	if err != nil {
		t.Fatalf("build judge: %v", err)
	}
	t.Logf("judge: %s   models: %d   fixtures: %d", judge.Model(), len(opts.Models), len(opts.Fixtures))

	ctx := context.Background()
	persona := config.DefaultPersona()

	type job struct {
		model   Model
		fixture Fixture
	}
	var jobs []job
	for _, m := range opts.Models {
		for _, f := range opts.Fixtures {
			jobs = append(jobs, job{m, f})
		}
	}

	var (
		mu      sync.Mutex
		byModel = map[string]*Aggregate{}
		notes   = map[string][]string{}
		wg      sync.WaitGroup
		sem     = make(chan struct{}, evalConcurrency)
	)

	for _, j := range jobs {
		wg.Add(1)

		go func(j job) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			result := RunWithPersona(ctx, j.model, j.fixture, 1, opts, persona)

			mu.Lock()
			agg, ok := byModel[j.model.ID]
			if !ok {
				agg = &Aggregate{}
				byModel[j.model.ID] = agg
			}
			mu.Unlock()

			if result.Err != nil {
				mu.Lock()
				notes[j.model.ID] = append(notes[j.model.ID],
					fmt.Sprintf("%s: review failed: %v", j.fixture.Name, result.Err))
				mu.Unlock()
				return
			}

			findings := result.Report.Findings

			assessment, err := judge.Judge(ctx, j.fixture, persona, findings)

			mu.Lock()
			defer mu.Unlock()

			if err != nil {
				notes[j.model.ID] = append(notes[j.model.ID],
					fmt.Sprintf("%s: judge failed: %v", j.fixture.Name, err))
				return
			}

			for _, p := range agg.Add(assessment, len(findings)) {
				notes[j.model.ID] = append(notes[j.model.ID],
					fmt.Sprintf("%s: JUDGE OUTPUT SUSPECT: %s", j.fixture.Name, p))
			}

			if j.fixture.Clean() && len(findings) > 0 {
				notes[j.model.ID] = append(notes[j.model.ID],
					fmt.Sprintf("%s: %d finding(s) on a clean change", j.fixture.Name, len(findings)))
			}
		}(j)
	}

	wg.Wait()
	reportJudgedModels(t, byModel, notes)
}

// reportJudgedModels prints the judged ranking.
func reportJudgedModels(t *testing.T, byModel map[string]*Aggregate, notes map[string][]string) {
	t.Helper()

	type row struct {
		model string
		agg   *Aggregate
	}

	rows := make([]row, 0, len(byModel))
	for m, a := range byModel {
		rows = append(rows, row{m, a})
	}

	// Rank by the judge's overall grade, then by the share of findings a senior
	// reviewer would actually raise. Silence does not win: a model with no
	// findings has undefined precision and sorts last.
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i].agg, rows[j].agg
		if a.HasFindings() != b.HasFindings() {
			return a.HasFindings()
		}
		if ag, bg := a.MeanGrade(), b.MeanGrade(); ag != bg {
			return ag > bg
		}
		return a.Precision() > b.Precision()
	})

	var b strings.Builder
	b.WriteString("\nJUDGED MODEL RANKING\n")
	b.WriteString("MODEL                                GRADE  SPREAD  PREC   FIX  FAIL  FIND  WORTH  INFLATED  MISCLASS  MISSED  SIGNAL\n")
	b.WriteString("------------------------------------------------------------------------------------------------------------------\n")

	for _, r := range rows {
		a := r.agg

		prec := "n/a"
		if a.HasFindings() {
			prec = fmt.Sprintf("%.2f", a.Precision())
		}

		// Fixtures completed and failures are printed because MeanGrade is a mean
		// over a VARIABLE denominator: a failed review contributes nothing, so a
		// model that fails the hard fixtures and completes only the easy ones
		// scores higher. Without these columns that artifact is invisible and
		// reads as model quality.
		fixtures := len(a.Grades)
		failed := len(notes[r.model])

		// Spread is blank for a single sample rather than printed as 0.00,
		// which would read as "perfectly stable" when it means "not measured".
		spread := "n/a"
		if len(a.Grades) > 1 {
			spread = fmt.Sprintf("%.2f", a.GradeSpread())
		}

		fmt.Fprintf(&b, "%-36s %-6.2f %-7s %-6s %-4d %-5d %-5d %-6d %-9d %-9d %-7d %.1f\n",
			truncate(r.model, 36), a.MeanGrade(), spread, prec, fixtures, failed, a.Findings, a.WorthRaising,
			a.Inflated, a.Misclassed, a.Missed, a.MeanSignal())
	}

	t.Log(b.String())

	// A model judged on fewer fixtures than its peers is not comparable to
	// them, and quietly averaging it anyway is how a ranking becomes fiction.
	most := 0
	for _, r := range rows {
		if n := len(r.agg.Grades); n > most {
			most = n
		}
	}
	for _, r := range rows {
		if n := len(r.agg.Grades); n < most {
			t.Logf("NOT COMPARABLE: %s completed %d/%d fixtures; its grade is a mean over a "+
				"smaller, easier sample and must not be ranked against the others", r.model, n, most)
		}
	}

	// Printed with the table, not left to a reader's memory of a commit message.
	// This harness's measured run-to-run spread reached 0.49 while the distance
	// from first to twelfth place was 0.28, so row order here is not a ranking
	// unless a gap clears the SPREAD beside it. Stating that next to the numbers
	// is the only thing that stops them being quoted as a leaderboard.
	t.Log("READ THE SPREAD COLUMN: a GRADE gap smaller than the spread beside it is noise, " +
		"not a ranking. Rows are sorted so the table is legible, not because the order is a result.")

	if _, ok := byModel[IncumbentModel]; ok {
		t.Logf("ASYMMETRIC SAMPLE: %s is served from a cache of one review per fixture, so its "+
			"spread is unmeasured rather than zero. Our side may have several runs per fixture.",
			IncumbentModel)
	}

	for _, r := range rows {
		if n := notes[r.model]; len(n) > 0 {
			t.Logf("%s:", r.model)
			for _, x := range n {
				t.Logf("  %s", x)
			}
		}
	}
}

// TestBenchmarkAgainstIncumbent runs Incumbent and open-nitpick over the same
// fixtures and has the same judge assess both.
//
// This is the comparison that matters: Incumbent is the incumbent this project
// exists to replace, and every other number here is self-referential without it.
// The setup is deliberately like-for-like — identical fixture repositories, the
// same uncommitted working-tree change, the same judge, the same scoring — and
// deliberately NOT equalized on the thing being compared, which is each
// reviewer's own prompt and model.
//
// One asymmetry is neither, and reading the GRADE column without it is a
// mistake: judgeRequest shows the judge open-nitpick's configured persona and
// restricts `missed` to that persona's scope, for BOTH contenders. Incumbent
// never received that specification, so ToneAdherence, ToneOff and Missed grade
// it on adherence to a document only its opponent was given. Treat the tone and
// scope components of its grade as a measure of house-style fit, not quality.
func TestBenchmarkAgainstIncumbent(t *testing.T) {
	ctx := context.Background()

	if !IncumbentAvailable(ctx) {
		t.Skip("incumbent CLI not installed or not authenticated; run 'incumbent auth login'")
	}

	opts := OptionsFromEnv()

	judge, err := NewJudge(strings.TrimSpace(os.Getenv(EnvJudgeModel)))
	if err != nil {
		t.Fatalf("build judge: %v", err)
	}

	persona := config.DefaultPersona()
	// The run count is printed because it was silently dropped once: `make
	// benchmark RUNS=2` did not forward NITPICK_EVAL_RUNS, so the SPREAD column
	// measured fixture-to-fixture difficulty while reading as run-to-run
	// variance. A paid flag that does nothing is the worst kind of instrument,
	// and this is the second time this Makefile has had one.
	t.Logf("judge: %s   fixtures: %d   runs per fixture (our side): %d   contenders: incumbent + %d model(s)",
		judge.Model(), len(opts.Fixtures), max(opts.Runs, 1), len(opts.Models))

	var (
		mu     sync.Mutex
		byName = map[string]*Aggregate{}
		notes  = map[string][]string{}
		wg     sync.WaitGroup
		// Incumbent's CLI has a free-tier allowance; keep concurrency low so a
		// rate limit does not read as a quality result.
		sem = make(chan struct{}, 3)
	)

	record := func(name string, fx Fixture, findings []review.Finding, err error) {
		mu.Lock()
		defer mu.Unlock()

		agg, ok := byName[name]
		if !ok {
			agg = &Aggregate{}
			byName[name] = agg
		}
		if err != nil {
			notes[name] = append(notes[name], fmt.Sprintf("%s: %v", fx.Name, err))
			return
		}

		assessment, jerr := judge.Judge(ctx, fx, persona, findings)
		if jerr != nil {
			notes[name] = append(notes[name], fmt.Sprintf("%s: judge failed: %v", fx.Name, jerr))
			return
		}
		for _, p := range agg.Add(assessment, len(findings)) {
			notes[name] = append(notes[name], fmt.Sprintf("%s: JUDGE OUTPUT SUSPECT: %s", fx.Name, p))
		}
		if fx.Clean() && len(findings) > 0 {
			notes[name] = append(notes[name],
				fmt.Sprintf("%s: %d finding(s) on a clean change", fx.Name, len(findings)))
		}
	}

	for _, fx := range opts.Fixtures {
		// Incumbent needs its own checkout: it reviews the working tree in
		// place, exactly as open-nitpick does.
		wg.Add(1)
		go func(fx Fixture) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			// Prefer the cache: collection is rate-limited and resumable, so a
			// previously collected review is both cheaper and more complete.
			if cached, ok := CachedIncumbent(crCacheDir, fx); ok {
				record(IncumbentModel, fx, cached, nil)
				return
			}

			dir, err := os.MkdirTemp("", "cr-eval-")
			if err != nil {
				record(IncumbentModel, fx, nil, err)
				return
			}
			defer func() { _ = os.RemoveAll(dir) }()

			if err := buildRepo(dir, fx); err != nil {
				record(IncumbentModel, fx, nil, err)
				return
			}

			findings, _, err := RunIncumbent(ctx, dir, opts.Timeout)
			if IsRateLimited(err) {
				// Never let an exhausted allowance masquerade as a low score.
				if cached, ok := CachedIncumbent(crCacheDir, fx); ok {
					record(IncumbentModel, fx, cached, nil)
					return
				}
			}
			if IsFreeTier(err) {
				// Loud, not a note: the comparison this whole test exists to make
				// is invalid if Incumbent reviewed on the free allowance, and a
				// table printed anyway would be read as a result.
				t.Errorf("%s: %v", fx.Name, err)
			}
			record(IncumbentModel, fx, findings, err)
		}(fx)

		for _, m := range opts.Models {
			// RUNS reviews each fixture more than once, so the SPREAD column
			// has something to measure. It applies to our side only: Incumbent
			// is served from a cache collected one review per fixture, and
			// re-reviewing to match would spend the account's allowance to
			// measure a competitor's variance rather than our own. The report
			// is therefore asymmetric by construction, and says so.
			runs := max(opts.Runs, 1)

			for run := 1; run <= runs; run++ {
				wg.Add(1)
				go func(m Model, fx Fixture, run int) {
					defer wg.Done()
					sem <- struct{}{}
					defer func() { <-sem }()

					result := RunWithPersona(ctx, m, fx, run, opts, persona)
					if result.Err != nil {
						record("nitpick/"+m.ID, fx, nil, result.Err)
						return
					}
					record("nitpick/"+m.ID, fx, result.Report.Findings, nil)
				}(m, fx, run)
			}
		}
	}

	wg.Wait()
	reportJudgedModels(t, byName, notes)
}

// TestCollectIncumbent gathers Incumbent's reviews one fixture at a time,
// caching each so a rate limit costs a wait rather than lost progress.
//
// Run it repeatedly until it reports nothing outstanding; it skips whatever is
// already cached. The free CLI allowance is small enough that a single pass is
// unlikely to finish, which is exactly why this is separate from scoring.
func TestCollectIncumbent(t *testing.T) {
	ctx := context.Background()

	if !IncumbentAvailable(ctx) {
		t.Skip("incumbent CLI not installed or not authenticated")
	}

	opts := OptionsFromEnv()

	collected, remaining, err := CollectIncumbent(ctx, opts.Fixtures, crCacheDir, opts.Timeout,
		func(line string) { t.Log(line) })
	if err != nil {
		t.Fatalf("collect: %v", err)
	}

	t.Logf("collected %d this pass, %d still outstanding", collected, remaining)

	var missing []string
	for _, f := range opts.Fixtures {
		if _, ok := CachedIncumbent(crCacheDir, f); !ok {
			missing = append(missing, f.Name)
		}
	}
	if len(missing) > 0 {
		t.Logf("not yet collected: %s — re-run `make collect-incumbent` when the allowance resets",
			strings.Join(missing, ", "))
	} else {
		t.Log("all fixtures collected; `make benchmark` can now score a complete comparison")
	}
}

// runVoiceAxis compares the wording axes, holding scope constant.
//
// Unlike the nitpick levels, voice genuinely changes the prompt, so each
// variant needs its own review — there is no shared corpus to filter.
func runVoiceAxis(t *testing.T, judge *Judge, model Model, opts Options) {
	t.Helper()

	ctx := context.Background()
	variants := voiceVariants()

	out := make([]scored, len(variants))
	for i, v := range variants {
		out[i] = scored{variant: v.Name}
	}

	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		sem = make(chan struct{}, evalConcurrency)
	)

	for i, v := range variants {
		for _, f := range opts.Fixtures {
			wg.Add(1)

			go func(i int, v variant, f Fixture) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				result := RunWithPersona(ctx, model, f, 1, opts, v.Persona)

				mu.Lock()
				defer mu.Unlock()

				if result.Err != nil {
					out[i].failures++
					out[i].notes = append(out[i].notes, fmt.Sprintf("%s: review failed: %v", f.Name, result.Err))
					return
				}

				assessment, err := judge.Judge(ctx, f, v.Persona, result.Report.Findings)
				if err != nil {
					out[i].failures++
					out[i].notes = append(out[i].notes, fmt.Sprintf("%s: judge failed: %v", f.Name, err))
					return
				}

				for _, p := range out[i].agg.Add(assessment, len(result.Report.Findings)) {
					out[i].notes = append(out[i].notes, fmt.Sprintf("%s: JUDGE OUTPUT SUSPECT: %s", f.Name, p))
				}
			}(i, v, f)
		}
	}

	wg.Wait()
	reportVariants(t, out)
}
