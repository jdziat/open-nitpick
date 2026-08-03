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
	opts, err := OptionsFromEnv()
	if err != nil {
		t.Fatalf("options: %v", err)
	}
	t.Logf("corpus: %s", CorpusLabel(opts.Fixtures))

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
	// One dump for the whole test, opened here rather than per axis so both
	// axes write into the same file with the same schema.
	dump, err := OpenDump()
	if err != nil {
		t.Fatalf("open %s: %v", EnvDump, err)
	}
	defer func() { _ = dump.Close() }()

	if strings.TrimSpace(os.Getenv("NITPICK_EVAL_AXIS")) == "voice" {
		runVoiceAxis(t, judge, model, opts, dump)
		return
	}

	levels := []config.NitpickLevel{
		config.NitpickOff, config.NitpickMinimal, config.NitpickNormal, config.NitpickPedantic,
	}

	t.Logf("judge: %s   reviewer: %s   fixtures: %d   levels: %d (one review each, filtered offline)",
		judge.Model(), model.ID, len(opts.Fixtures), len(levels))

	results := runLevels(t, judge, model, levels, opts, dump)
	reportVariants(t, results)
}

// runLevels reviews each fixture once and derives every level from that corpus.
func runLevels(t *testing.T, judge *Judge, model Model, levels []config.NitpickLevel, opts Options, dump *Dump) []scored {
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

			// The corpus position of each finding, so a filtered subset can find
			// its own verdicts. Keyed the way review.dedupe keys them, which is
			// what makes the key unique within a corpus.
			corpusIndex := map[string]int{}
			for idx, finding := range corpus {
				corpusIndex[finding.Key()] = idx
			}

			for i, level := range levels {
				kept, _ := review.Filter(corpus, level, config.SeverityNit)

				sub := &JudgeResult{
					Missed:        assessment.Missed,
					SignalToNoise: assessment.SignalToNoise,
					ToneAdherence: assessment.ToneAdherence,
					Grade:         assessment.Grade,
				}
				// Each verdict is re-indexed to its position in THIS level's
				// list. The verdicts carry indices into the whole corpus, and
				// Add validates them against the number of findings the level
				// kept — so any level that dropped a finding before its last
				// kept one reported "judge verdict index N is out of range"
				// against a judge that had done nothing wrong, and the noisiest
				// levels produced the most phantom complaints. Add's expected
				// count is len(kept) for the same reason: passing
				// len(sub.Verdicts) compared the slice against itself and could
				// never detect a verdict the judge actually failed to return.
				for pos, finding := range kept {
					if v, ok := byIndex[corpusIndex[finding.Key()]]; ok {
						v.Index = pos
						sub.Verdicts = append(sub.Verdicts, v)
					}
				}

				if problems := out[i].agg.Add(sub, len(kept)); len(problems) > 0 {
					for _, p := range problems {
						out[i].notes = append(out[i].notes, fmt.Sprintf("%s: JUDGE OUTPUT SUSPECT: %s", f.Name, p))
					}
				}
				out[i].agg.AddSeverity(ScoreSeverity(f, kept))

				if derr := dump.Record(DumpSample{
					Model:    model.ID,
					Variant:  "nitpick=" + string(level),
					Run:      1,
					Fixture:  f,
					Findings: kept,
					Judged:   sub,
				}); derr != nil {
					out[i].notes = append(out[i].notes, fmt.Sprintf("%s: dump: %v", f.Name, derr))
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
	// J-* is the judge's opinion of each severity; O-* is the same question
	// answered against the fixture's own planted WantSeverity. Both are shown,
	// and neither replaces the other: they disagree, and which one a tuning
	// decision was made against is the difference between reducing inflation and
	// merely teaching the model to under-claim.
	//
	// Every count column is a PER-SAMPLE RATE, and N and FAIL are printed
	// beside them. They used to be raw sums next to PRECISION, SIGNAL and GRADE
	// which are means — the defect 595b0d4 fixed for the judged-model table and
	// which had been reintroduced here for three more columns. The denominator
	// is not constant across rows: on the voice axis each variant runs its own
	// reviews, and one failed review silently gives that row a total over fewer
	// samples than its neighbours. `failures` was counted and then read by
	// nothing, so the reader had no way to see it happen.
	b.WriteString("VARIANT                 N     FAIL  FINDINGS  REAL  WORTH  PRECISION  J-INFL  J-UNDER  O-INFL  O-UNDER  O-ACC  MISCLASS  TONE-OFF  MISSED  SIGNAL  GRADE\n")
	b.WriteString("--------------------------------------------------------------------------------------------------------------------------------------------------------\n")

	for _, r := range results {
		a := r.agg
		precision := "n/a"
		if a.HasFindings() {
			precision = fmt.Sprintf("%.2f", a.Precision())
		}

		n := float64(max(len(a.Grades), 1))
		rate := func(v int) string { return fmt.Sprintf("%.2f", float64(v)/n) }

		fmt.Fprintf(&b, "%-23s %-5d %-5d %-9s %-5s %-6s %-10s %-7s %-8s %-7s %-8s %-6s %-9s %-9s %-7s %-7.1f %.2f\n",
			truncate(r.variant, 23), len(a.Grades), r.failures,
			rate(a.Findings), rate(a.Real), rate(a.WorthRaising), precision,
			rate(a.Inflated), rate(a.Understated), rate(a.SevInflated), rate(a.SevUnderstated), rate(a.SevAccurate),
			rate(a.Misclassed), rate(a.ToneOff), rate(a.Missed), a.MeanSignal(), a.MeanGrade())
	}

	t.Log(b.String())
	t.Log(severityColumnLegend)

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

// severityColumnLegend is printed under every table carrying both severity
// measurements, because the columns are useless to a reader who does not know
// they are two different instruments answering the same question.
//
// It states no arithmetic relation between the O-* columns and FIND. The
// previous wording asserted their sum was "smaller than FIND", which is not
// guaranteed and was already false on the shipped corpus — FIND counts judge
// VERDICTS and the O-* columns count located defects, so they have different
// sources and can be equal or inverted. Both tables that print this legend now
// express every count as a per-sample rate, so the claim about units is true of
// both rather than of one.
const severityColumnLegend = "SEVERITY IS MEASURED TWICE: J-INFL/J-UNDER are the JUDGE's opinion; " +
	"O-INFL/O-UNDER/O-ACC compare each LOCATED PLANTED DEFECT to the WantSeverity its fixture declares, " +
	"with no model involved. They disagree — the judge scored Incumbent as understating NOTHING on a " +
	"corpus whose ground truth says it understated four of the seven defects it located — and the " +
	"disagreement is a result, not a rounding error. The two are counted over DIFFERENT things: J-* is " +
	"per finding the judge returned a verdict for, O-* is per planted defect the review actually found, " +
	"so neither is a share of the other and a defect nobody reported appears in neither. Every count " +
	"column, J-* and O-* alike, is divided by N on the same row."

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
	opts, err := OptionsFromEnv()
	if err != nil {
		t.Fatalf("options: %v", err)
	}

	judge, err := NewJudge(strings.TrimSpace(os.Getenv(EnvJudgeModel)))
	if err != nil {
		t.Fatalf("build judge: %v", err)
	}
	t.Logf("corpus: %s", CorpusLabel(opts.Fixtures))
	t.Logf("judge: %s   models: %d   fixtures: %d", judge.Model(), len(opts.Models), len(opts.Fixtures))

	dump, err := OpenDump()
	if err != nil {
		t.Fatalf("open %s: %v", EnvDump, err)
	}
	defer func() { _ = dump.Close() }()

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
			agg.AddSeverity(ScoreSeverity(j.fixture, findings))

			if derr := dump.Record(DumpSample{
				Model:    j.model.ID,
				Run:      1,
				Fixture:  j.fixture,
				Findings: findings,
				Judged:   assessment,
			}); derr != nil {
				notes[j.model.ID] = append(notes[j.model.ID], fmt.Sprintf("%s: dump: %v", j.fixture.Name, derr))
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
	// J-INFL and J-UNDER are printed together, and never one without the other.
	//
	// Only INFLATED used to be shown, so severity error was visible in one
	// direction and invisible in the other -- and this comparison has a
	// contender that understates BY CONSTRUCTION: crSeverity maps Incumbent's
	// "critical" down to our "error" because its vocabulary carries no separate
	// error tier. Counting over-claiming while ignoring under-claiming hands a
	// free win to whichever reviewer is quieter about severity, which is the
	// opposite of the judgement a reader wants to make.
	//
	// O-INFL, O-UNDER and O-ACC answer the same question against the fixtures'
	// own WantSeverity. They sit beside the judge's columns rather than
	// replacing them because the two disagree: the judge scored the contender
	// that understates by construction as understating nothing at all. A prompt
	// tuned to move J-INFL down while O-UNDER climbs has not become more honest
	// about severity, it has become quieter, and only printing both makes that
	// visible.
	// The count columns are PER SAMPLE, not totals.
	//
	// GRADE has always been a mean while FIND, INFLATED, MISSED and the rest
	// were raw sums, so the moment two contenders had different sample counts
	// the columns stopped being readable side by side: three runs of a model
	// that misses one defect a run shows MISSED 3 against a single run's 1, and
	// looks three times worse for having been measured three times as hard.
	// COV is the fixture coverage that makes the comparison legitimate at all;
	// N is what the rates divide by.
	b.WriteString("MODEL                                GRADE  SPREAD  PREC   COV  N    FAIL  FIND  WORTH  J-INFL  J-UNDER  O-INFL  O-UNDER  O-ACC  MISCLASS  MISSED  SIGNAL\n")
	b.WriteString("---------------------------------------------------------------------------------------------------------------------------------------------------------\n")

	for _, r := range rows {
		a := r.agg

		prec := "n/a"
		if a.HasFindings() {
			prec = fmt.Sprintf("%.2f", a.Precision())
		}

		// A contender with no judged sample has no grade, and printing its
		// MeanGrade zero value as 0.00 reads as "graded, and terrible". The
		// benchmark reaches that state whenever Incumbent's every invocation
		// errors — an exhausted allowance, or a fixture with no cached review —
		// and the row it produced was a last-place entry in a table captioned as
		// a head-to-head.
		grade, signal := "n/a", "n/a"
		if len(a.Grades) > 0 {
			grade = fmt.Sprintf("%.2f", a.MeanGrade())
			signal = fmt.Sprintf("%.1f", a.MeanSignal())
		}

		// Fixtures completed and failures are printed because MeanGrade is a mean
		// over a VARIABLE denominator: a failed review contributes nothing, so a
		// model that fails the hard fixtures and completes only the easy ones
		// scores higher. Without these columns that artifact is invisible and
		// reads as model quality.
		failed := len(notes[r.model])

		// Spread is blank for a single sample rather than printed as 0.00,
		// which would read as "perfectly stable" when it means "not measured".
		spread := "n/a"
		if len(a.Grades) > 1 {
			spread = fmt.Sprintf("%.2f", a.GradeSpread())
		}

		// Divide by the sample count so every count column is a rate. n is never
		// zero here: a contender with no graded sample has no row.
		n := float64(max(len(a.Grades), 1))
		rate := func(v int) string { return fmt.Sprintf("%.2f", float64(v)/n) }

		fmt.Fprintf(&b, "%-36s %-6s %-7s %-6s %-4d %-4d %-5d %-5s %-6s %-7s %-8s %-7s %-8s %-6s %-9s %-7s %s\n",
			truncate(r.model, 36), grade, spread, prec, a.Coverage(), len(a.Grades), failed,
			rate(a.Findings), rate(a.WorthRaising), rate(a.Inflated), rate(a.Understated),
			rate(a.SevInflated), rate(a.SevUnderstated), rate(a.SevAccurate),
			rate(a.Misclassed), rate(a.Missed), signal)
	}

	t.Log(b.String())
	t.Log(severityColumnLegend)

	// A model judged on fewer fixtures than its peers is not comparable to
	// them, and quietly averaging it anyway is how a ranking becomes fiction.
	most := 0
	for _, r := range rows {
		most = max(most, r.agg.Coverage())
	}
	for _, r := range rows {
		n := r.agg.Coverage()
		switch {
		case n == 0:
			// Not a Logf. Zero coverage means every attempt failed, so this row
			// is not a weak result — it is no result, and the table around it is
			// not the comparison its caption claims. That has to fail the run,
			// or "we beat the incumbent" gets read off a run in which the
			// incumbent was never successfully invoked.
			t.Errorf("%s was never successfully judged on any fixture; the table is not a comparison "+
				"and its row is not a score", r.model)
		case n < most:
			t.Logf("NOT COMPARABLE: %s was judged on %d of %d fixtures; its grade is a mean over a "+
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

	opts, err := OptionsFromEnv()
	if err != nil {
		t.Fatalf("options: %v", err)
	}

	judge, err := NewJudge(strings.TrimSpace(os.Getenv(EnvJudgeModel)))
	if err != nil {
		t.Fatalf("build judge: %v", err)
	}

	dump, err := OpenDump()
	if err != nil {
		t.Fatalf("open %s: %v", EnvDump, err)
	}
	defer func() { _ = dump.Close() }()

	t.Logf("corpus: %s", CorpusLabel(opts.Fixtures))

	// Which fixtures Incumbent has a cached review for, stated BEFORE the run
	// rather than inferred from a low row afterwards. Anything listed here has
	// to be collected live against a rate-limited free allowance, and the
	// held-out corpus has no cache at all — a benchmark table whose Incumbent
	// column came from failed invocations is not a comparison, however it reads.
	var uncached []string
	for _, f := range opts.Fixtures {
		if _, ok := CachedIncumbent(crCacheDir, f); !ok {
			uncached = append(uncached, f.Name)
		}
	}
	if len(uncached) > 0 {
		t.Logf("NO CACHED INCUMBENT REVIEW for %d of %d fixture(s): %s — these will be collected live "+
			"on the free allowance; run `make collect-incumbent` first if the comparison has to be complete",
			len(uncached), len(opts.Fixtures), strings.Join(uncached, ", "))
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

	record := func(name string, fx Fixture, run int, findings []review.Finding, err error) {
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
		agg.Saw(fx.Name)

		assessment, jerr := judge.Judge(ctx, fx, persona, findings)
		if jerr != nil {
			notes[name] = append(notes[name], fmt.Sprintf("%s: judge failed: %v", fx.Name, jerr))
			return
		}
		for _, p := range agg.Add(assessment, len(findings)) {
			notes[name] = append(notes[name], fmt.Sprintf("%s: JUDGE OUTPUT SUSPECT: %s", fx.Name, p))
		}
		agg.AddSeverity(ScoreSeverity(fx, findings))

		if derr := dump.Record(DumpSample{
			Model:    name,
			Run:      run,
			Fixture:  fx,
			Findings: findings,
			Judged:   assessment,
		}); derr != nil {
			notes[name] = append(notes[name], fmt.Sprintf("%s: dump: %v", fx.Name, derr))
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

			// Every Incumbent sample is run 1: its side is one review per
			// fixture, cached, which is the asymmetry the report already
			// declares. Numbering them otherwise would imply a second sample
			// exists.
			//
			// Prefer the cache: collection is rate-limited and resumable, so a
			// previously collected review is both cheaper and more complete.
			if cached, ok := CachedIncumbent(crCacheDir, fx); ok {
				record(IncumbentModel, fx, 1, cached, nil)
				return
			}

			dir, err := os.MkdirTemp("", "cr-eval-")
			if err != nil {
				record(IncumbentModel, fx, 1, nil, err)
				return
			}
			defer func() { _ = os.RemoveAll(dir) }()

			if err := buildRepo(dir, fx); err != nil {
				record(IncumbentModel, fx, 1, nil, err)
				return
			}

			findings, _, err := RunIncumbent(ctx, dir, opts.Timeout)
			if IsRateLimited(err) {
				// Never let an exhausted allowance masquerade as a low score.
				if cached, ok := CachedIncumbent(crCacheDir, fx); ok {
					record(IncumbentModel, fx, 1, cached, nil)
					return
				}
			}
			if IsFreeTier(err) {
				// Loud, not a note: the comparison this whole test exists to make
				// is invalid if Incumbent reviewed on the free allowance, and a
				// table printed anyway would be read as a result.
				t.Errorf("%s: %v", fx.Name, err)
			}
			record(IncumbentModel, fx, 1, findings, err)
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
						record("nitpick/"+m.ID, fx, run, nil, result.Err)
						return
					}
					record("nitpick/"+m.ID, fx, run, result.Report.Findings, nil)
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

	opts, err := OptionsFromEnv()
	if err != nil {
		t.Fatalf("options: %v", err)
	}
	t.Logf("corpus: %s", CorpusLabel(opts.Fixtures))

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
func runVoiceAxis(t *testing.T, judge *Judge, model Model, opts Options, dump *Dump) {
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
				out[i].agg.AddSeverity(ScoreSeverity(f, result.Report.Findings))

				if derr := dump.Record(DumpSample{
					Model:    model.ID,
					Variant:  v.Name,
					Run:      1,
					Fixture:  f,
					Findings: result.Report.Findings,
					Judged:   assessment,
				}); derr != nil {
					out[i].notes = append(out[i].notes, fmt.Sprintf("%s: dump: %v", f.Name, derr))
				}
			}(i, v, f)
		}
	}

	wg.Wait()
	reportVariants(t, out)
}
