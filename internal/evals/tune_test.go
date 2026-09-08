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
	// model is the reviewer the variant was produced by. It is not printed,
	// the persona axis holds one model and the table identifies its rows by
	// variant, and it is carried because Corroborate files the second judge's
	// aggregate under contenderLabel(model, variant). Asking for it by variant
	// alone finds nothing, pays for a corroboration, and prints "+?".
	model string

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
// exactly the model-variance confound the design removes, two levels would
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
	// AXIS=voice` silently measured the nitpick axis instead, a paid flag that
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

	t.Logf("judge: %s   reviewer: %s   fixtures: %d   levels: %d (one review each, filtered offline, "+
		"then judged once per DISTINCT filtered list so both judges score the same stimulus)",
		judge.Model(), model.ID, len(opts.Fixtures), len(levels))

	// The nitpick axis holds the persona constant except for its level, and the
	// judge is shown the GENERATION persona each level was filtered out of, so
	// one voice covers every group.
	results, samples := runLevels(t, judge, model, levels, opts, dump)
	reportVariants(t, results, corroborateVariants(t, judge, results, samples, nil))
}

// runLevels reviews each fixture once and derives every level from that
// corpus.
//
// The note behind it is in docs/harness-notes.md#runlevels.
func runLevels(
	t *testing.T, judge *Judge, model Model, levels []config.NitpickLevel, opts Options, dump *Dump,
) ([]scored, []DumpSample) {
	t.Helper()

	ctx := context.Background()

	out := make([]scored, len(levels))
	for i, l := range levels {
		out[i] = scored{model: model.ID, variant: "nitpick=" + string(l)}
		// Every level's findings come from our own engine under evalConfig, so
		// the row declares our scale from the same place the run does rather
		// than inheriting it by not being the incumbent.
		out[i].agg.Scale = ourSeverityScale(evalConfig(model))
	}

	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		sem = make(chan struct{}, 4)

		// Every level's judged list, kept for the second judge. Each level is
		// its own contender with its own finding list, the whole point of
		// generate-once-then-filter, so corroborating the axis means judging
		// four lists, not one. It is still judging only: no level is reviewed.
		samples []DumpSample

		// What the primary judging cost, counted rather than
		// estimated. The doc comment above says the bound is one call per level
		// per fixture and the usual case is fewer; a bound in a comment is the
		// kind of claim that stops being true quietly, so the run prints the
		// number it spent.
		calls int
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

			// What each level publishes, and its identity. The
			// identity is a fingerprint of the list, so two levels share a
			// judgement only when they present the same review, which is a
			// fact about those two levels, not an assumption about the axis.
			kept := make([][]review.Finding, len(levels))
			shown := make([]Stimulus, len(levels))
			for i, level := range levels {
				kept[i], _ = review.Filter(corpus, level, config.SeverityNit)
				shown[i] = JudgedOver(f.Name, kept[i])
			}

			// One judging call per distinct list. An UNRECORDED stimulus is
			// never shared: a fingerprint that could not be computed is not
			// evidence that two lists are equal, and sharing on it would put a
			// judgement of one level's findings into another level's row.
			type call struct {
				shown    Stimulus
				findings []review.Finding
				levels   []int
			}
			var batches []*call
			byStimulus := map[Stimulus]*call{}
			for i := range levels {
				if c, ok := byStimulus[shown[i]]; ok && shown[i].Recorded() {
					c.levels = append(c.levels, i)
					continue
				}
				c := &call{shown: shown[i], findings: kept[i], levels: []int{i}}
				batches = append(batches, c)
				if shown[i].Recorded() {
					byStimulus[shown[i]] = c
				}
			}

			for _, c := range batches {
				// A level that filters everything away is judged too, rather
				// than assumed to score nothing. That is the level's actual
				// output, its MISSED is the whole planted corpus, and a judge
				// that answers an empty list with verdicts is a failure the
				// report has to be able to show. Rejudge already judges silent
				// groups for the same reason, so both judges treat silence
				// alike.
				assessment, err := judge.Judge(ctx, f, persona, c.findings)

				mu.Lock()
				calls++
				if err != nil {
					for _, i := range c.levels {
						out[i].failures++
						out[i].notes = append(out[i].notes,
							fmt.Sprintf("%s: judge failed: %v", f.Name, err))
					}
					mu.Unlock()
					continue
				}

				for _, i := range c.levels {
					// The verdicts index into c.findings, which is this level's
					// published list, so nothing is re-indexed and Add's
					// expected count is the stimulus's own length. The previous
					// version filtered whole-corpus verdicts into each level and
					// had to renumber them; that renumbering is gone because the
					// judgement is no longer borrowed from another list.
					if problems := out[i].agg.Add(assessment, c.shown); len(problems) > 0 {
						for _, p := range problems {
							out[i].notes = append(out[i].notes,
								fmt.Sprintf("%s: JUDGE OUTPUT SUSPECT: %s", f.Name, p))
						}
					}
					out[i].agg.AddSeverity(f, ScoreSeverity(f, c.findings))

					sample := DumpSample{
						Model:    model.ID,
						Variant:  "nitpick=" + string(levels[i]),
						Run:      1,
						Fixture:  f,
						Findings: c.findings,
						Judged:   assessment,
					}
					samples = append(samples, sample)

					if derr := dump.Record(sample); derr != nil {
						out[i].notes = append(out[i].notes, fmt.Sprintf("%s: dump: %v", f.Name, derr))
					}

					if f.Clean() && len(c.findings) > 0 {
						out[i].notes = append(out[i].notes,
							fmt.Sprintf("%s: %d finding(s) on a clean change", f.Name, len(c.findings)))
					}
				}
				mu.Unlock()
			}
		}(f)
	}

	wg.Wait()

	t.Logf("primary judging: %d call(s) against a ceiling of %d (%d fixture(s) x %d level(s)). One per "+
		"DISTINCT filtered list: levels that publish the same findings share a judgement, levels that "+
		"do not get their own. %d call(s) were not spent — some shared, and some because a fixture "+
		"whose review failed is never judged, which the failure counts beside each row separate out. "+
		"This is what buys the second judge the SAME stimulus, and it is the difference between a "+
		"delta that is a confidence interval and one that is a change of question",
		calls, len(opts.Fixtures)*len(levels), len(opts.Fixtures), len(levels),
		len(opts.Fixtures)*len(levels)-calls)

	return out, samples
}

// reportVariants prints the comparison table.
//
// Same treatment as the judged model ranking: every judged cell is a
// JudgedFigure carrying its cross-judge delta, and a run with no second judge
// prints figures that say the disagreement was not measured. The persona axis is
// scored by the same judge on the same terms as the model battery, so it inherits
// the same vendor conflict and gets the same admission.
func reportVariants(t *testing.T, results []scored, panel JudgePanel) {
	t.Helper()

	// Looked up under contenderLabel, the key Corroborate files under, and
	// LABELLED by variant, which is what the table identifies its rows by. The
	// two being different strings is the whole reason JudgePanel.Unpaired
	// exists, and it is asserted below rather than assumed.
	cross := make([]CrossJudged, len(results))
	claimed := make([]string, 0, len(results))
	for i, r := range results {
		key := contenderLabel(r.model, r.variant)
		claimed = append(claimed, key)
		cross[i] = panel.Pair(key, r.agg)
	}
	if orphaned := panel.Unpaired(claimed); len(orphaned) > 0 {
		t.Errorf("the second judge scored %d contender(s) that no row in this table claimed: %s.\n"+
			"The judging was paid for and every figure below still says its disagreement was not "+
			"measured, which is indistinguishable from a run with no second judge",
			len(orphaned), strings.Join(orphaned, ", "))
	}

	// A variant with no findings has UNDEFINED precision, not perfect precision.
	// Sorting it to the top would make silence look like the best strategy.
	// Ordered through JudgedFigure.Compare, which puts an undefined figure last
	// and never hands the caller the number.
	order := make([]int, len(results))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(i, j int) bool {
		a, b := cross[order[i]], cross[order[j]]
		if c := a.PrecisionFigure().Compare(b.PrecisionFigure()); c != 0 {
			return c < 0
		}
		return results[order[i]].variant < results[order[j]].variant
	})

	sorted := make([]scored, len(results))
	sortedCross := make([]CrossJudged, len(results))
	for i, idx := range order {
		sorted[i], sortedCross[i] = results[idx], cross[idx]
	}
	results, cross = sorted, sortedCross

	var b strings.Builder
	b.WriteString("\n")
	// J-* is the judge's opinion of each severity; O-* is the same question
	// answered against the fixture's own planted WantSeverity. Both are shown and
	// neither replaces the other: they disagree, and which one a tuning decision
	// was made against is the difference between reducing inflation and merely
	// teaching the model to under-claim.
	//
	// A third group, B-*, used to sit between them: O-* recomputed after both
	// severities were coarsened into bands, offered as the honest cross-tool
	// reading. It is withdrawn, and no re-tuned replacement is coming, it was
	// maximised by a reviewer that stamped one blocking word on every finding,
	// and it could not see the parser bug it was written in response to. The
	// SEVERITY VOCABULARY block below is the description that replaces it. See
	// NoCrossToolSeverityScore.
	//
	// Every count column is a per-sample rate, and N and FAIL are printed
	// beside them. Raw sums next to PRECISION, SIGNAL and GRADE, which are
	// means, is the defect 595b0d4 fixed for the judged-model table, and it
	// reaches three more columns here. The denominator
	// is not constant across rows: on the voice axis each variant runs its own
	// reviews, and one failed review silently gives that row a total over fewer
	// samples than its neighbours. `failures` was counted and then read by
	// nothing, so the reader had no way to see it happen.
	b.WriteString(panel.Banner() + "\n\n")
	b.WriteString(CrossJudgedVariantTableHeader + "\n")
	b.WriteString(strings.Repeat("-", len(CrossJudgedVariantTableHeader)) + "\n")

	for i, r := range results {
		b.WriteString(JudgedVariantRow(r.variant, cross[i], r.failures))
	}

	t.Log(b.String())
	t.Log(CrossJudgeLegend)
	t.Log(SeverityColumnLegend)

	// The counts behind every rate above, for both judges. The variant table
	// never printed denominators at all, which is the same omission the judged
	// model ranking was fixed for: these are quotients of single-digit integers
	// and a rate hides its own resolution.
	var counts strings.Builder
	counts.WriteString("DENOMINATORS — every rate above, as the counts it was computed from:\n")
	for i, r := range results {
		counts.WriteString(cross[i].Denominators(r.variant) + "\n")
		fmt.Fprintf(&counts, "  %-36s objective:     %s\n", "", r.agg.ObjectiveSeverityCounts(r.variant))
	}
	t.Log(counts.String())

	rows := make([]VocabularyRow, 0, len(results))
	for _, r := range results {
		rows = append(rows, VocabularyRow{Name: r.variant, Usage: r.agg.SevUsage, Planted: r.agg.SevPlantedLevels})
	}
	t.Log(SeverityVocabularyBlock(rows))

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
	// reading. A specific variant scoring poorly is information, not a defect,
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
// find a bug we already knew about. That is a floor, not a ranking, it cannot
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

	// RETAINED WHETHER OR not ANYBODY ASKED, exactly as the head-to-head is. This
	// battery was described as a tuning axis over a corpus that can be reviewed
	// again, and it is not one: it prints the same reportJudgedModels table, and
	// `make judge-models FIXTURES=$(HELD_OUT)`, a documented invocation, with
	// HELD_OUT defined in the Makefile for it, points it at the corpus whose own
	// label says it is spent once. With DUMP unset that run used to retain
	// nothing, which is the original incident verbatim.
	dump, dumpPath, err := OpenRunDump("judge-models", opts.Fixtures)
	if err != nil {
		t.Fatalf("open the run dump: %v", err)
	}
	defer func() {
		if cerr := dump.Close(); cerr != nil {
			t.Errorf("closing the run dump: %v; the retained evidence for this run may be "+
				"incomplete, and this run spent the corpus to produce it", cerr)
		}
	}()
	t.Logf("RETAINING EVERY REVIEW to %s. The model-free columns — RECALL, NOISE, ANCHOR and L/DEF "+
		"— are recomputable from this file with no judge and no network. GRADE, MISSED and SIGNAL "+
		"are the judge's opinion and are NOT re-derivable from it without a judging pass "+
		"(`make rejudge REJUDGE=%s`).", dumpPath, dumpPath)

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

		// Every judged review, kept so the second judge can be handed the same
		// finding lists in the same positions. This is what makes the second
		// opinion cost judging only: nothing here is reviewed twice.
		samples []DumpSample
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
			// Every result's declaration, not just the first goroutine's: the
			// row is withheld when two adapters disagree rather than attributed
			// to whichever one won the race for the map.
			agg.DeclareScale(result.Scale)
			// Counted before the failure paths below, for the reason the
			// head-to-head's fold site gives: it is the only counter that knows
			// what did not arrive.
			agg.Attempted(j.fixture.Name)
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

			// Saw, which this battery did not call at all. Aggregate.Fixtures is
			// written only here, so every row's Coverage() was 0 and
			// reportJudgedModels' zero-coverage branch, the one that fails a run
			// in which a contender was never successfully judged, fired on every
			// row of every run. A guard that fires on everything reports nothing,
			// and this battery shares that guard with the head-to-head.
			agg.Saw(j.fixture.Name)

			// Retained before the judge is asked, for the reason spelled out at
			// the head-to-head's fold site: the review is paid for and the
			// model-free columns need no judge. Registered after `defer
			// mu.Unlock()` so it runs with the lock still held, and after the
			// review-error return above so an empty list is never written down as
			// a reviewer that said nothing.
			var judged *JudgeResult
			defer func() {
				if derr := dump.Record(DumpSample{
					Model: j.model.ID, Run: 1, Fixture: j.fixture, Findings: findings, Judged: judged,
				}); derr != nil {
					notes[j.model.ID] = append(notes[j.model.ID],
						fmt.Sprintf("%s: dump: %v", j.fixture.Name, derr))
				}
			}()

			if err != nil {
				notes[j.model.ID] = append(notes[j.model.ID],
					fmt.Sprintf("%s: judge failed: %v", j.fixture.Name, err))
				return
			}
			judged = assessment

			for _, p := range agg.Add(assessment, JudgedOver(j.fixture.Name, findings)) {
				notes[j.model.ID] = append(notes[j.model.ID],
					fmt.Sprintf("%s: JUDGE OUTPUT SUSPECT: %s", j.fixture.Name, p))
			}
			agg.AddSeverity(j.fixture, ScoreSeverity(j.fixture, findings))
			agg.AddDetection(ScoreDetection(j.fixture, findings))

			samples = append(samples, DumpSample{
				Model:    j.model.ID,
				Run:      1,
				Fixture:  j.fixture,
				Findings: findings,
				Judged:   assessment,
			})

			if j.fixture.Clean() && len(findings) > 0 {
				notes[j.model.ID] = append(notes[j.model.ID],
					fmt.Sprintf("%s: %d finding(s) on a clean change", j.fixture.Name, len(findings)))
			}
		}(j)
	}

	wg.Wait()

	panel := corroborate(t, ctx, judge, persona, nil, samples, notes)
	reportJudgedModels(t, opts.Fixtures, byModel, panel, notes)
}

// corroborate scores the same judged reviews again with a second judge from a
// vendor no contender shares, and returns the panel the report renders from.
//
// The note behind it is in docs/harness-notes.md#corroborate.
func corroborate(
	t *testing.T,
	ctx context.Context,
	primary *Judge,
	persona config.Persona,
	byVariant map[string]config.Persona,
	samples []DumpSample,
	notes map[string][]string,
) JudgePanel {
	t.Helper()

	panel := JudgePanel{Primary: primary.Model()}

	model := SecondJudgeFromEnv()
	if model == "" {
		t.Logf("NO SECOND JUDGE. %s is unset, so every judged figure below is one opinion from %s, "+
			"which shares a vendor with %d contender(s) it scores. Set %s=default to corroborate "+
			"with %s; it re-judges these findings and runs no review.",
			EnvSecondJudge, primary.Model(), len(VendorConflicts(primary.Model())),
			EnvSecondJudge, SecondJudgeModel)
		return panel
	}

	second, err := NewJudge(model)
	if err != nil {
		// Not fatal. A run whose second judge could not be built is still a run,
		// and failing it here would throw away the primary judgements that have
		// already been paid for. It degrades to the stated single-judge case.
		t.Errorf("SECOND JUDGE UNAVAILABLE, the table below is single-judge: build %s: %v", model, err)
		return panel
	}

	// Stated before the calls rather than only in the table, because this is the
	// line that says whether the corroboration is worth anything. A second judge
	// sharing the primary's vendor measures that vendor's own variance, which is
	// a real and useful number and is not an answer to the self-preference
	// question.
	if conflicts := VendorConflicts(second.Model()); len(conflicts) > 0 {
		t.Logf("SECOND JUDGE %s SHARES A VENDOR WITH %d CONTENDER(S) IT SCORES: %s. The deltas below "+
			"are still a disagreement, but they do not answer the self-preference question — for "+
			"that the second judge must share a vendor with nothing in the battery.",
			second.Model(), len(conflicts), strings.Join(conflicts, ", "))
	}

	groups := CorroborationGroups(samples)
	for i := range groups {
		if p, ok := byVariant[groups[i].Variant]; ok {
			groups[i].Persona = &p
		}
	}
	t.Logf("second judge: %s   re-judging %d recorded review(s), running no review", second.Model(), len(groups))

	aggregates, secondNotes := Corroborate(ctx, second, persona, groups, evalConcurrency)
	for contender, ns := range secondNotes {
		notes[contender] = append(notes[contender], ns...)
	}

	if len(aggregates) == 0 {
		// Distinct from "no second judge was configured", and worth failing over:
		// the operator paid for a corroboration and received none, and a table
		// that silently reverted to single-judge would look identical to one
		// where they never asked.
		t.Errorf("the second judge %s assessed none of the %d review(s); this is not a corroboration, "+
			"it is an outage, and every figure below is uncorroborated", second.Model(), len(groups))
		return panel
	}

	panel.Second = second.Model()
	panel.SecondAggregates = aggregates
	return panel
}

// reportJudgedModels prints the judged ranking.
//
// It takes TWO judges' aggregates, and the second may be absent. Every judged
// cell is a JudgedFigure carrying its cross-judge delta, so this function has no
// route to a bare judged number and a reader has no route to half a result. When
// there is no second judge the figures render "+?" and the table says so above
// itself, which is a weaker publication rather than a quieter one.
func reportJudgedModels(
	t *testing.T,
	fixtures []Fixture,
	byModel map[string]*Aggregate,
	panel JudgePanel,
	notes map[string][]string,
) {
	t.Helper()

	type row struct {
		model string
		agg   *Aggregate
		cross CrossJudged
	}

	rows := make([]row, 0, len(byModel))
	claimed := make([]string, 0, len(byModel))
	for m, a := range byModel {
		claimed = append(claimed, m)
		rows = append(rows, row{m, a, panel.Pair(m, *a)})
	}
	if orphaned := panel.Unpaired(claimed); len(orphaned) > 0 {
		t.Errorf("the second judge scored %d contender(s) that no row in this table claimed: %s.\n"+
			"The judging was paid for and every figure below still says its disagreement was not "+
			"measured, which is indistinguishable from a run with no second judge",
			len(orphaned), strings.Join(orphaned, ", "))
	}

	// Rank by the judge's overall grade, then by the share of findings a senior
	// reviewer would raise. Silence does not win: a model with no
	// findings has undefined precision and sorts last.
	//
	// Compared through JudgedFigure.Compare, which orders by the PRIMARY judge
	// and never yields the number. That the primary judge decides the order is a
	// limitation, not an oversight. It is the judge whose ranking has been
	// published, and it is why the second judge's own order is printed below
	// the table rather than left to a reader to reconstruct from the deltas.
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i].cross, rows[j].cross
		if c := a.GradeFigure().Compare(b.GradeFigure()); c != 0 {
			return c < 0
		}
		return a.PrecisionFigure().Compare(b.PrecisionFigure()) < 0
	})

	var b strings.Builder
	b.WriteString("\nJUDGED MODEL RANKING\n")
	// J-INFL and J-UNDER are printed together, and never one without the other.
	//
	// Showing only INFLATED makes severity error visible in one direction and
	// invisible in the other. Counting over-claiming while
	// ignoring under-claiming hands a free win to whichever reviewer is quieter
	// about severity, which is the opposite of the judgement a reader wants to
	// make.
	//
	// O-INFL, O-UNDER and O-ACC answer the same question against the fixtures'
	// own WantSeverity. They sit beside the judge's columns rather than
	// replacing them because the two disagree: the judge scored a contender the
	// ground truth says understates as understating nothing at all. A prompt
	// tuned to move J-INFL down while O-UNDER climbs has not become more honest
	// about severity, it has become quieter, and only printing both makes that
	// visible.
	//
	// THERE IS NO CROSS-TOOL SEVERITY COLUMN IN this TABLE, and that is a
	// deliberate withdrawal rather than an omission. B-INFL/B-UNDER/B-ACC used to
	// sit here, O-* recomputed after both severities were coarsened into bands,
	// and were the columns the head-to-head was read from. They were maximised by
	// a reviewer that stamps one blocking word on every finding, and they could
	// not see the parser bug they were introduced to fix. The SEVERITY VOCABULARY
	// block under this table is what a cross-vocabulary reader gets instead: a
	// description. See NoCrossToolSeverityScore.
	// The count columns are PER SAMPLE, not totals.
	//
	// GRADE has always been a mean while FIND, INFLATED, MISSED and the rest
	// were raw sums, so the moment two contenders had different sample counts
	// the columns stopped being readable side by side: three runs of a model
	// that misses one defect a run shows MISSED 3 against a single run's 1, and
	// looks three times worse for having been measured three times as hard.
	// COV is the fixture coverage that makes the comparison legitimate at all;
	// N is what the rates divide by.
	//
	// The header is CrossJudgedModelTableHeader, which is JudgedModelTableHeader
	// with the judge-supplied columns widened to hold a figure and its
	// cross-judge delta. Same columns, same order, derived from the declared
	// header rather than written out again, see WidenJudgedColumns.
	b.WriteString(panel.Banner() + "\n\n")
	b.WriteString(CrossJudgedModelTableHeader + "\n")
	b.WriteString(strings.Repeat("-", len(CrossJudgedModelTableHeader)) + "\n")

	for _, r := range rows {
		// Rendered by JudgedModelRow, in non-test code, which is the only thing
		// that puts this table in front of a guard the default build can run.
		// Fixtures completed and failures reach it because the mean grade is a
		// mean over a VARIABLE denominator: a failed review contributes nothing,
		// so a model that fails the hard fixtures and completes only the easy
		// ones scores higher, and without those columns the artifact is
		// invisible and reads as model quality.
		//
		// FAIL is the row's lost-review count rather than the length of its
		// notes list: notes are appended for a clean-change finding, a suspect
		// judge output and a dump error as well, none of which is a lost review
		// and all of which fold normally. The notes themselves
		// are printed under the table.
		b.WriteString(JudgedModelRow(r.model, r.cross, r.agg.Lost()))
	}

	t.Log(b.String())
	t.Log(CrossJudgeLegend)

	// WHETHER THE ORDER SURVIVES THE OTHER JUDGE.
	//
	// The note behind it is in docs/harness-notes.md#mismatched.
	var mismatched []string
	for _, r := range rows {
		if r.cross.HaveSecond && !r.cross.SameStimulus() {
			mismatched = append(mismatched, r.model)
		}
	}

	switch {
	case !panel.Corroborated():
		// Nothing to compare; the banner above has already said so.
	case len(mismatched) > 0:
		t.Logf("NO SECOND-JUDGE ORDER. %d of %d contender(s) were not scored on the same finding "+
			"lists by the two judges (%s), so the second judge's ranking is not a re-ranking of "+
			"this table and is not printed. Every judged cell on those rows reads `+NC` for the "+
			"same reason. See the DENOMINATORS block below for which side is short.",
			len(mismatched), len(rows), strings.Join(mismatched, ", "))
	default:
		second := make([]row, len(rows))
		copy(second, rows)
		sort.Slice(second, func(i, j int) bool {
			a := CrossJudged{Primary: second[i].cross.Second, HaveSecond: false}
			b := CrossJudged{Primary: second[j].cross.Second, HaveSecond: false}
			if c := a.GradeFigure().Compare(b.GradeFigure()); c != 0 {
				return c < 0
			}
			return a.PrecisionFigure().Compare(b.PrecisionFigure()) < 0
		})

		moved := 0
		order := make([]string, 0, len(second))
		for i, r := range second {
			if rows[i].model != r.model {
				moved++
			}
			order = append(order, fmt.Sprintf("%d. %s", i+1, r.model))
		}

		t.Logf("SECOND-JUDGE ORDER (%s), by the same rule the rows above are sorted by: %s\n"+
			"%d of %d contender(s) sit in a different position. Both judges scored the same "+
			"finding lists for every row, so this is a re-ranking and not a re-measurement. The "+
			"rows above are the PRIMARY judge's ranking; this line is how much of it is that "+
			"judge's opinion.",
			panel.Second, strings.Join(order, "   "), moved, len(rows))
	}

	// Every RATE IN THE TABLE ABOVE, AS THE COUNTS IT CAME FROM.
	//
	// The note behind it is in docs/harness-notes.md#counts.
	var counts strings.Builder
	counts.WriteString("DENOMINATORS — every rate above, as the counts it was computed from:\n")
	for _, r := range rows {
		counts.WriteString(r.cross.Denominators(r.model) + "\n")

		// The objective-severity counts come from the gated renderer, not from
		// the fields. A foreign contender's triple is withdrawn in the table, and
		// printing it here would restore the comparison the cells refused. They
		// are judge-free, so they are printed once and not per judge.
		fmt.Fprintf(&counts, "  %-36s objective:     %s\n", "", r.agg.ObjectiveSeverityCounts(r.model))

		// RECALL, NOISE, ANCHOR and L/DEF, as the counts they came from. Rendered by
		// Aggregate.DetectionCounts for the same reason the line above is
		// rendered by a method: a report that formatted these itself would be a
		// report holding the raw counters, and the RECALL pair printed here has
		// to be the one the cell was divided by rather than a second reading of
		// the same corpus.
		fmt.Fprintf(&counts, "  %-36s detection:     %s\n", "", r.agg.DetectionCounts())

		// The footnote the "*" on this row's detection cells points at. Rendered
		// by the aggregate through the same sentence the cost table prints, so
		// the two reports cannot describe one loss two ways, which is what they
		// did while this table printed no sentence at all.
		if why := r.agg.CoverageShortfall(r.model); why != "" {
			fmt.Fprintf(&counts, "  %-36s %s: %s\n", "", shortSampleMark, why)
		}
	}
	t.Log(counts.String())

	t.Log(CorpusResolution(fixtures))
	t.Log(RateLegend)
	t.Log(SeverityColumnLegend)

	vocab := make([]VocabularyRow, 0, len(rows))
	for _, r := range rows {
		vocab = append(vocab, VocabularyRow{Name: r.model, Usage: r.agg.SevUsage, Planted: r.agg.SevPlantedLevels})
	}
	t.Log(SeverityVocabularyBlock(vocab))

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
			// is not a weak result. It is no result, and the table around it is
			// not the comparison its caption claims. That has to fail the run,
			// or "we beat the incumbent" gets read off a run where no judge
			// call for that row ever succeeded.
			t.Errorf("%s was never successfully judged on any fixture; the table is not a comparison "+
				"and its row is not a score", r.model)
		case n < most:
			t.Logf("NOT COMPARABLE: %s was judged on %d of %d fixtures; its grade is a mean over a "+
				"smaller, easier sample and must not be ranked against the others", r.model, n, most)
		}

		// A row that was judged and never had its detection folded prints n/a in
		// RECALL, NOISE, ANCHOR and L/DEF, the columns the ship decision is read off,
		// and an n/a is otherwise the honest rendering of "no review here". The
		// two spellings of a blank are indistinguishable to a reader, so the one
		// that means "a caller forgot to wire this" is failed rather than
		// printed. This function is shared by two batteries and both fold; a
		// third would inherit the table and not the fold.
		if len(r.agg.Grades) > 0 && r.agg.DetReviews == 0 {
			t.Errorf("%s was judged on %d sample(s) and had its detection folded for none of them, "+
				"so RECALL, NOISE, ANCHOR and L/DEF read n/a on its row for a reason that is not "+
				"about the reviewer. Call AddDetection beside AddSeverity at this battery's fold site",
				r.model, len(r.agg.Grades))
		}

		// A row that never said what it ATTEMPTED cannot be short of it, so its
		// cells go unmarked however many reviews it lost. That is a silent
		// blind spot rather than a clean row, and silence is what this whole
		// note is about.
		if r.agg.DetReviews > 0 && r.agg.Attempts() == 0 {
			t.Errorf("%s folded %d review(s) and stated no attempted count, so a lost run cannot "+
				"mark its cells. Call Attempted at the TOP of this battery's fold site, before the "+
				"error return", r.model, r.agg.DetReviews)
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

		// WHAT that ASYMMETRY does TO THE THREE DETECTION COLUMNS, stated on the
		// row because this is the table the head-to-head is read off and because
		// two headlines quoted from here have already been retracted.
		//
		// RECALL and NOISE are rates over each side's own counts, so one review
		// per fixture and three answer the same question and the counts are
		// printed. ANCHOR is a MAXIMUM, and a maximum over more draws is weakly
		// larger, our side draws runs x fixtures where the cache draws fixtures,
		// so with RUNS above 1 the column is biased AGAINST us. That is the
		// conservative direction for a "no wider than theirs" reading, and it is
		// a bias rather than a comparability, which is why it is written down
		// instead of left for a reader to derive.
		//
		// NONE OF THE FOUR IS WITHDRAWN FOR THE CACHED SIDE, and the reason is
		// that all four are defined on one review: the cache retains the raw
		// text and is re-parsed through the current parser, so the secondary
		// spans an "Also applies to" line carries reach anchorDistance and
		// anchoredLines exactly as they did live. What is undefined for
		// a one-review side is any VARIANCE of them, and no such reading is
		// offered here, the same refusal the ASYMMETRIC VOCABULARY note makes
		// about severity, for the same reason.
		//
		// THE RATES SURVIVE A DIFFERENT NUMBER OF REVIEWS PER ROW only WHILE each
		// ROW'S REVIEWS are BALANCED ACROSS FIXTURES, and this note used to assert
		// their survival unconditionally. A rate over reviews is a mean weighted
		// by how many reviews of each fixture SURVIVED, so an unbalanced loss
		// reweights the fixture mix, and the FAIL column beside this line is the
		// proof that losses happen. Only our side can lose depth without losing
		// coverage (the cache is one review per fixture, so any loss there removes
		// the fixture and moves COV), so the reweighting runs in the challenger's
		// favour. A row short of the reviews it attempted marks all four cells and
		// prints the reason in the DENOMINATORS block.
		t.Logf("ASYMMETRIC DRAW COUNT: RECALL, NOISE and L/DEF are per-defect and per-review RATES "+
			"and survive the one-review-per-fixture cache — the counts are in the DENOMINATORS "+
			"block — PROVIDED each row folded every review it attempted. ANCHOR IS A WORST CASE, "+
			"NOT A RATE: it is the maximum over the reviews behind each row, so a row folded from "+
			"more reviews took its maximum over more chances, and with more than one run per fixture "+
			"this column is biased AGAINST us and IN FAVOUR of %s. A ROW THAT LOST RUNS IS MARKED "+
			"%q ON ALL FOUR CELLS: the survivors are not a random subset, and only our side can lose "+
			"depth without losing coverage, so that bias runs the other way. No variance of any of "+
			"the four is published: a spread over one cached review is undefined, and none is "+
			"offered rather than printed as zero.", IncumbentModel, shortSampleMark)

		// Stated on the row rather than left to the legend, because this is the
		// specific comparison the table is captioned as making and the specific
		// place a headline gets quoted from. Two have been quoted from here and
		// both were retracted: the first off O-ACC against a parser that had
		// demoted the incumbent's severities, the second off a banded column that
		// a reviewer stamping "critical" on everything scored perfectly.
		//
		// This note used to end "Compare it on B-*". That instruction is deleted
		// rather than repointed at another column: there is no column to send a
		// reader to, and inventing a third one is how the first two happened.
		t.Logf("ASYMMETRIC VOCABULARY: %s publishes ~3 severity tiers to our 5, so ITS O-* COLUMNS "+
			"ARE NOT A RESULT — its one 'critical' covers our critical and error, and it cannot be "+
			"accurate on both kinds of plant. NO SEVERITY COLUMN IN THIS TABLE COMPARES IT TO US: "+
			"read the SEVERITY VOCABULARY block, which describes what each contender said without "+
			"scoring one vocabulary against the other, and see why no such score is offered.",
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
// The setup is deliberately like-for-like, identical fixture repositories, the
// same uncommitted working-tree change, the same judge, the same scoring, and
// deliberately not equalized on the thing being compared, which is each
// reviewer's own prompt and model.
//
// One asymmetry is neither, and reading the GRADE column without it is a
// mistake: judgeRequest shows the judge open-nitpick's configured persona and
// restricts `missed` to that persona's scope, for both contenders. Incumbent
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

	// RETAINED WHETHER OR not ANYBODY ASKED. The two batteries that produced the
	// Rule 14 evidence both ran with NITPICK_EVAL_DUMP unset, so their findings
	// were held in memory for the whole paid run and written nowhere, and two of
	// Rule 14's four conditions then needed a re-run of a corpus whose own label
	// says it is spent once. RECALL, NOISE, ANCHOR and L/DEF are pure functions of
	// (findings, fixture), so a retained file answers them offline for nothing.
	dump, dumpPath, err := OpenRunDump("benchmark", opts.Fixtures)
	if err != nil {
		t.Fatalf("open the run dump: %v", err)
	}
	defer func() {
		if cerr := dump.Close(); cerr != nil {
			t.Errorf("closing the run dump: %v; the retained evidence for this run may be "+
				"incomplete, and this run spent the corpus to produce it", cerr)
		}
	}()

	t.Logf("corpus: %s", CorpusLabel(opts.Fixtures))
	t.Logf("RETAINING EVERY REVIEW THAT PRODUCED FINDINGS to %s, whether or not the judge could "+
		"grade it. RECALL, NOISE, ANCHOR and L/DEF are recomputable from this file with no judge and "+
		"no network, so the columns in the table below can be re-derived — and any new model-free "+
		"column can be answered — without reviewing this corpus again. A review that FAILED is not "+
		"in the file and has nothing to retain. GRADE, MISSED and SIGNAL are the judge's opinion and "+
		"are NOT in the record: re-deriving those still costs a judging pass "+
		"(`make rejudge REJUDGE=%s`).", dumpPath, dumpPath)

	// Which fixtures Incumbent has a cached review for, stated before the run
	// rather than inferred from a low row afterwards. Anything listed here has
	// to be collected live against a rate-limited free allowance, and the
	// held-out corpus has no cache at all, a benchmark table whose Incumbent
	// column came from failed invocations is not a comparison, however it reads.
	var uncached []string
	for _, f := range opts.Fixtures {
		if _, ok := CachedIncumbent(crCacheDir, f); ok {
			continue
		}
		uncached = append(uncached, f.Name)

		// A cached review the parser can no longer read is a different problem
		// from one never collected, and re-collecting hides it. It is reported
		// as an error because the alternative serves the previous parser's
		// reading of those bytes into the table with no marker at all.
		if stale, why := StaleIncumbentCache(crCacheDir, f); stale {
			t.Errorf("%s: a cached Incumbent review exists and matches the fixture, but this parser "+
				"can no longer read it (%v). The parser and the evidence have diverged; fix the parser "+
				"or re-collect deliberately, and do not let a previous parser's reading be scored as "+
				"this one's", f.Name, why)
		}
	}
	if len(uncached) > 0 {
		t.Logf("NO USABLE CACHED INCUMBENT REVIEW for %d of %d fixture(s): %s — these will be collected "+
			"live on the free allowance; run `make collect-incumbent` first if the comparison has to be "+
			"complete", len(uncached), len(opts.Fixtures), strings.Join(uncached, ", "))
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

		// The judged reviews, kept for the second judge. This is the table the
		// head-to-head against the incumbent is printed in, so it is the one
		// where a single judge's opinion carries the most weight, and the
		// judge shares a vendor with three of the contenders on our side of it.
		samples []DumpSample
	)

	// scale is declared by the CALLER, because the caller is the one that knows
	// which adapter produced the findings. A contender whose adapter says
	// nothing is withheld from the O-* columns rather than published at our
	// resolution by virtue of not being named IncumbentModel; see SeverityScale.
	record := func(name string, scale SeverityScale, fx Fixture, run int, findings []review.Finding, err error) {
		mu.Lock()
		defer mu.Unlock()

		agg, ok := byName[name]
		if !ok {
			agg = &Aggregate{}
			byName[name] = agg
		}
		// Before THE ERROR RETURN, because this is the only counter that knows
		// what did not arrive. Every other column on the row is folded over the
		// reviews that survived, and a lost run is not a random one, so without
		// this the three rates would be means over a reweighted fixture mix with
		// nothing on the cells saying so, and the coverage guard below cannot see
		// it: it reads a set of fixture NAMES, and losing two of three runs of a
		// fixture leaves that set alone.
		agg.Attempted(fx.Name)
		// Two adapters folded into one row: the row is on no single scale, so
		// its severity cells are withheld rather than attributed to whichever
		// declaration arrived first. Written out at one call site rather than on
		// the aggregate, the other judged path goes without it.
		agg.DeclareScale(scale)
		if err != nil {
			notes[name] = append(notes[name], fmt.Sprintf("%s: %v", fx.Name, err))
			return
		}
		agg.Saw(fx.Name)

		// Retained before the judge is asked, and retained whatever it answers.
		// The review is already paid for and RECALL, NOISE, ANCHOR and L/DEF are
		// pure functions of (findings, fixture), so recording it after the judge
		// lets a judge failure discard the one artifact that needs no judge.
		//
		// The note behind it is in docs/harness-notes.md#judged.
		var judged *JudgeResult
		defer func() {
			if derr := dump.Record(DumpSample{
				Model: name, Run: run, Fixture: fx, Findings: findings, Judged: judged,
			}); derr != nil {
				notes[name] = append(notes[name], fmt.Sprintf("%s: dump: %v", fx.Name, derr))
			}
		}()

		assessment, jerr := judge.Judge(ctx, fx, persona, findings)
		if jerr != nil {
			notes[name] = append(notes[name], fmt.Sprintf("%s: judge failed: %v", fx.Name, jerr))
			return
		}
		judged = assessment
		for _, p := range agg.Add(assessment, JudgedOver(fx.Name, findings)) {
			notes[name] = append(notes[name], fmt.Sprintf("%s: JUDGE OUTPUT SUSPECT: %s", fx.Name, p))
		}
		agg.AddSeverity(fx, ScoreSeverity(fx, findings))

		// Beside AddSeverity rather than beside agg.Saw above, so RECALL, NOISE,
		// ANCHOR and L/DEF are folded over exactly the reviews every other column on
		// the row is folded over. The cost is stated where it lands: a review
		// whose JUDGE call failed returned above and is scored by nothing, for
		// four columns that need no judge, and the reviews behind them
		// are printed as the DENOMINATORS block's review count.
		agg.AddDetection(ScoreDetection(fx, findings))

		samples = append(samples, DumpSample{
			Model:    name,
			Run:      run,
			Fixture:  fx,
			Findings: findings,
			Judged:   assessment,
		})

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
			// review already on disk is both cheaper and more complete.
			if cached, ok := CachedIncumbent(crCacheDir, fx); ok {
				record(IncumbentModel, IncumbentSeverityScale, fx, 1, cached, nil)
				return
			}

			dir, err := os.MkdirTemp("", "cr-eval-")
			if err != nil {
				record(IncumbentModel, IncumbentSeverityScale, fx, 1, nil, err)
				return
			}
			defer func() { _ = os.RemoveAll(dir) }()

			if err := buildRepo(dir, fx); err != nil {
				record(IncumbentModel, IncumbentSeverityScale, fx, 1, nil, err)
				return
			}

			findings, _, err := RunIncumbent(ctx, dir, opts.Timeout)
			if IsRateLimited(err) {
				// Never let an exhausted allowance masquerade as a low score.
				if cached, ok := CachedIncumbent(crCacheDir, fx); ok {
					record(IncumbentModel, IncumbentSeverityScale, fx, 1, cached, nil)
					return
				}
			}
			if IsFreeTier(err) {
				// Loud, not a note: the comparison this whole test exists to make
				// is invalid if Incumbent reviewed on the free allowance, and a
				// table printed anyway would be read as a result.
				t.Errorf("%s: %v", fx.Name, err)
			}
			record(IncumbentModel, IncumbentSeverityScale, fx, 1, findings, err)
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
						record("nitpick/"+m.ID, result.Scale, fx, run, nil, result.Err)
						return
					}
					record("nitpick/"+m.ID, result.Scale, fx, run, result.Report.Findings, nil)
				}(m, fx, run)
			}
		}
	}

	wg.Wait()

	panel := corroborate(t, ctx, judge, persona, nil, samples, notes)
	reportJudgedModels(t, opts.Fixtures, byName, panel, notes)
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
// Unlike the nitpick levels, voice changes the prompt, so each
// variant needs its own review. There is no shared corpus to filter.
func runVoiceAxis(t *testing.T, judge *Judge, model Model, opts Options, dump *Dump) {
	t.Helper()

	ctx := context.Background()
	variants := voiceVariants()

	out := make([]scored, len(variants))
	for i, v := range variants {
		out[i] = scored{model: model.ID, variant: v.Name}
		// Same declaration as the nitpick axis and from the same source: every
		// variant is our own engine under evalConfig, differing only in persona.
		out[i].agg.Scale = ourSeverityScale(evalConfig(model))
	}

	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		sem = make(chan struct{}, evalConcurrency)

		// Each voice variant is its own review and its own persona, both of
		// which the second judge has to be given: the tone verdict is scored
		// against the voice the review was CONFIGURED to use.
		samples  []DumpSample
		personas = map[string]config.Persona{}
	)
	for _, v := range variants {
		personas[v.Name] = v.Persona
	}

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

				for _, p := range out[i].agg.Add(assessment, JudgedOver(f.Name, result.Report.Findings)) {
					out[i].notes = append(out[i].notes, fmt.Sprintf("%s: JUDGE OUTPUT SUSPECT: %s", f.Name, p))
				}
				out[i].agg.AddSeverity(f, ScoreSeverity(f, result.Report.Findings))

				sample := DumpSample{
					Model:    model.ID,
					Variant:  v.Name,
					Run:      1,
					Fixture:  f,
					Findings: result.Report.Findings,
					Judged:   assessment,
				}
				samples = append(samples, sample)

				if derr := dump.Record(sample); derr != nil {
					out[i].notes = append(out[i].notes, fmt.Sprintf("%s: dump: %v", f.Name, derr))
				}
			}(i, v, f)
		}
	}

	wg.Wait()
	reportVariants(t, out, corroborateVariants(t, judge, out, samples, personas))
}

// corroborateVariants runs the second judge over a persona axis and folds its
// complaints back onto the rows they belong to.
//
// The note behind it is in docs/harness-notes.md#corroboratevariants.
func corroborateVariants(
	t *testing.T, judge *Judge, results []scored, samples []DumpSample, personas map[string]config.Persona,
) JudgePanel {
	t.Helper()

	notes := map[string][]string{}
	panel := corroborate(t, context.Background(), judge, config.DefaultPersona(), personas, samples, notes)

	for i := range results {
		results[i].notes = append(results[i].notes, notes[contenderLabel(results[i].model, results[i].variant)]...)
	}
	return panel
}
