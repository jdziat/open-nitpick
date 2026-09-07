package evals

// Re-judging recorded findings, so the judge can be changed without changing
// anything else.
//
// The judge is an OpenAI model, three of the contenders are OpenAI models, and
// ONE OF THEM IS THE JUDGE ITSELF, openai/gpt-5.6-terra appears in both
// DefaultModels and DefaultJudgeModel. Precision, MISSED and every J-* column in
// the published tables are that model's opinion of its own output among others',
// and LLM-as-judge self-preference is a documented effect, so a ranking built on
// it is a claim nobody has tested. VendorConflicts computes the current list
// rather than trusting this paragraph.
//
// Swapping the judge and re-running the reviews cannot test it either: that
// changes the findings and the judge at once, and the two are then inseparable.
// Judging the same recorded findings twice changes exactly one thing.
//
// The second thing this path is for is noise. The same cached Incumbent
// findings were scored 2 missed on one benchmark run and 5 on the next, same
// input, different verdict, and no number anywhere says how much of the table
// that accounts for. Pointing this at a dump with the same judge id measures
// that directly, because the only difference between the two judgements is the
// judge's own variance.
//
// Nothing here re-runs a review. The findings come out of the dump exactly as
// they went in, in the order they went in, because Verdict.Index is a POSITION
// and a reconstruction that reorders them misattributes every verdict it
// carries without ever looking wrong.
//
// "Exactly one thing changes" is a claim about the whole prompt, not only about
// the finding list, so the reconstruction refuses rather than approximates on
// each of the ways the other half could move underneath it: a fixture whose
// SOURCE was edited after collection (the name surviving is not evidence the
// change did), a truncated file, and a file holding two judgements of one
// finding. And where the dump cannot answer. A verdict it had no
// position to hang on, a persona it never recorded. The report says so instead
// of counting one side by a rule it did not apply to the other.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/review"
)

// EnvRejudgeDump names an existing dump file to re-judge.
//
// Deliberately not EnvDump. That variable names a file to WRITE, and NewDump
// truncates on open; one variable serving both roles means a re-judge launched
// from a shell that still has the benchmark's environment reads the file the
// benchmark is busy emptying.
const EnvRejudgeDump = "NITPICK_EVAL_REJUDGE_DUMP"

// EnvBaselineJudge names the judge that PRODUCED the dump.
//
// It is the caller's assertion, not a fact read from the file: DumpRecord
// carries the verdicts and not who made them. It exists so the report can name
// both judges and split the contenders by vendor, which is the whole question.
// Wrong here means the vendor cohorts are drawn around the wrong model, so the
// report prints the value it was given rather than assuming it.
const EnvBaselineJudge = "NITPICK_EVAL_BASELINE_JUDGE"

// ReadDump loads a dump file written by Dump.
func ReadDump(path string) ([]DumpRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	return DecodeDump(f)
}

// DecodeDump decodes dump records in file order.
//
// json.Decoder rather than a line scanner, because the writer is a
// json.Encoder and this is its exact inverse: a bufio.Scanner has a 64 KiB
// line ceiling, and one finding with a long rationale beside a long judge
// reasoning would turn into a parse error on a file that is perfectly valid.
//
// A MALFORMED RECORD IS STILL AN ERROR and THE RECORDS ABOVE IT are STILL
// RETURNED. This discarded everything it had decoded, which was defensible while
// a dump was an opt-in diagnostic and is not now that a battery retains its own
// findings by default: the file is the only record of a corpus that is spent
// once, and a short write is the ordinary way to damage one, Record returns the
// encode error, the battery demotes it to a line in its notes, and the run keeps
// appending after the mangled record. Every caller checks err, so refusing the
// file is unchanged; what changes is that refusing it costs the tail rather than
// the run. TestATruncatedTailCostsTheTailAndNotTheFile pins both halves.
func DecodeDump(r io.Reader) ([]DumpRecord, error) {
	dec := json.NewDecoder(r)

	var out []DumpRecord
	for {
		var rec DumpRecord
		err := dec.Decode(&rec)
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return out, fmt.Errorf("dump record %d: %w", len(out)+1, err)
		}
		out = append(out, rec)
	}
}

// RejudgeGroup is one recorded judgement reconstructed from a dump: the exact
// finding list one judge call was given, and what that judge said about it.
type RejudgeGroup struct {
	Model   string
	Variant string
	Run     int

	Fixture Fixture

	// Findings is the reconstructed list. Its ORDER is load-bearing: a verdict
	// identifies its finding by position, so re-judging a permuted list
	// attributes each new verdict to a different finding than the recorded one
	// it is compared against, and the resulting disagreement looks exactly like
	// two judges disagreeing.
	Findings []review.Finding

	// Baseline are the verdicts recorded against these findings.
	//
	// It can be shorter than Findings, and the two reasons are not the same. The
	// judge may have returned fewer, which is a reported problem and not
	// something to paper over with a zero value. Or the DUMP may have been
	// unable to carry them: it attaches at most one verdict per finding
	// position, so a judge that answered a position twice or answered a
	// position with no finding loses verdicts here that the published tables
	// counted. GroupDump compares this against the recorded raw count and warns
	// when the shortfall is the file's rather than the judge's, a distinction
	// that decides whether a baseline precision disagreeing with the published
	// table is a bug in the judge or a limit of the format.
	Baseline []Verdict

	// Silent marks a review that reported nothing.
	//
	// Dropping those would delete a contender that stayed silent from one
	// ranking and not the other, which is a change of sample dressed as a
	// change of judge. The dump records silence explicitly; only a dump written
	// before DumpRecord.Silent existed has it inferred from a hole in the
	// (contender, fixture, run) matrix, and GroupDump warns when it had to.
	Silent bool

	// Persona is the voice this review was CONFIGURED to use, and nil when it
	// is not known.
	//
	// judgeRequest shows the persona to the judge and asks it to score tone
	// against that voice, so re-judging a review under a different persona
	// changes the prompt in two places and measures both. A dump never fills
	// this in, it records no persona, and RejudgeReport says so, but the LIVE
	// corroboration path knows exactly which voice each review was produced
	// under, and the voice axis is four different personas. Judging
	// all four against the default would have scored three of them for adhering
	// to a voice they were never asked to use.
	Persona *config.Persona
}

// shortHash renders a fixture fingerprint for an error message. The full 64 hex
// characters say nothing more than the first twelve: the reader is comparing
// two values for equality, not verifying a hash.
func shortHash(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// groupKey identifies one judge call.
//
// Variant is part of the key, not a label on it. The persona axis derives four
// levels from one review and dumps each level separately, so the same
// (model, fixture, run) appears four times with four different finding lists;
// merging them by ignoring Variant would collide four lists into one and
// scramble every position in it.
type groupKey struct {
	model   string
	variant string
	fixture string
	run     int
}

// contenderKey is one thing being ranked: a model, under one persona variant.
type contenderKey struct{ model, variant string }

// contenderLabel renders a contender for a report row.
//
// The variant is in the LABEL, not only in the key, because a row keyed on
// something the row does not print is a row a reader cannot attribute: four
// nitpick levels of one model would appear as four identical names.
func contenderLabel(model, variant string) string {
	if variant == "" {
		return model
	}
	return model + " [" + variant + "]"
}

// verdictsByPosition reduces a judge's answer to at most one verdict per
// finding position, returning how many it had to discard.
//
// A judge can answer the same position twice or answer a position that has no
// finding. Both are already reported as suspect, and neither can be COUNTED:
// there is one finding at each position, so a second verdict about it has
// nothing of its own to be right or wrong about. The dump applies this same
// reduction when it writes, which is why the baseline must go through it too,
// counting one side raw and the other reduced makes an unchanged judge look
// like it moved.
//
// Last write wins among duplicates, matching Dump.Record, so the reduction here
// and the reduction in the file agree about which verdict survives.
// The two kinds of discard are returned apart because they mean different
// things: a duplicate is a second answer about a finding that EXISTS, and an
// out-of-range verdict is an answer about one that does not. The second is the
// failure mode a silent group is submitted in order to expose, and averaging it
// into a single "dropped" count would hide it.
func verdictsByPosition(verdicts []Verdict, findings int) (kept []Verdict, duplicates, phantom int) {
	byIndex := make(map[int]Verdict, len(verdicts))

	for _, v := range verdicts {
		if v.Index < 0 || v.Index >= findings {
			phantom++
			continue
		}
		if _, seen := byIndex[v.Index]; seen {
			duplicates++
		}
		byIndex[v.Index] = v
	}

	kept = make([]Verdict, 0, len(byIndex))
	for i := range findings {
		if v, ok := byIndex[i]; ok {
			kept = append(kept, v)
		}
	}
	return kept, duplicates, phantom
}

// silentSuffix names the case that makes a phantom verdict interesting, so the
// note says why it matters rather than only that it happened.
func silentSuffix(silent bool) string {
	if silent {
		return " — the finding list handed to it was EMPTY"
	}
	return ""
}

// GroupDump reconstructs the judged reviews recorded in a dump.
//
// It returns the groups sorted for a stable report, plus warnings about what
// the file could not tell it. It fails rather than reconstructs when a group's
// positions cannot be recovered exactly, because every downstream number is
// keyed on position and a silently-shifted list produces a full set of
// plausible, wrong verdict pairings.
func GroupDump(records []DumpRecord) ([]RejudgeGroup, []string, error) {
	known := map[string]Fixture{}
	for _, f := range AllFixtures() {
		known[f.Name] = f
	}

	type builder struct {
		byIndex  map[int]DumpRecord
		verdicts map[int]Verdict
		maxIndex int

		silent bool

		// declared is the sample's own statement of how long the judged list
		// was, and rawVerdicts how many verdicts the judge returned. Both are
		// per-sample constants repeated on every line, so the last line to
		// carry them wins and any line will do.
		declared    int
		rawVerdicts int
	}

	var (
		builders = map[groupKey]*builder{}
		// corpora records which corpus each contender was given, so
		// the legacy matrix below cannot pair a tuning contender with a
		// held-out fixture it never reviewed.
		corpora  = map[contenderKey]map[bool]bool{}
		fixtures = map[bool]map[string]bool{false: {}, true: {}}
		runs     = map[contenderKey]map[int]bool{}

		withCategory   int
		withSuggestion int

		// newFormat is set by any field only the current writer emits. It
		// decides whether the silent groups are read from the file or guessed
		// from the holes in it, and guessing is wrong in both directions, so it
		// must not be reached for while the file can answer.
		newFormat bool
	)

	for i, rec := range records {
		fixture, ok := known[rec.Fixture]
		if !ok {
			return nil, nil, fmt.Errorf(
				"dump record %d names fixture %q, which is in neither corpus: the change it "+
					"reviewed cannot be rebuilt, so its findings cannot be re-judged", i+1, rec.Fixture)
		}

		// The fixture NAME surviving is not evidence its source did. Re-judging
		// resolves the name against the corpus as it stands now, so a Head that
		// was edited after the benchmark would put the new judge in front of a
		// different change than the recorded verdicts were made about, the
		// confound this whole path exists to remove, arriving through the back
		// door. A dump written before the field carries no hash and is only
		// warned about, because refusing it would delete every dump already on
		// disk.
		if rec.FixtureHash != "" && rec.FixtureHash != fixtureFingerprint(fixture) {
			return nil, nil, fmt.Errorf(
				"dump record %d reviewed fixture %q at content %s, and the corpus now holds %s: "+
					"the fixture was edited after this dump was collected, so re-judging it would "+
					"change the CHANGE UNDER REVIEW as well as the judge",
				i+1, rec.Fixture, shortHash(rec.FixtureHash), shortHash(fixtureFingerprint(fixture)))
		}

		key := groupKey{rec.Model, rec.Variant, rec.Fixture, rec.Run}

		b, ok := builders[key]
		if !ok {
			b = &builder{byIndex: map[int]DumpRecord{}, verdicts: map[int]Verdict{}}
			builders[key] = b
		}

		if rec.Findings > 0 {
			b.declared = rec.Findings
			newFormat = true
		}
		if rec.Verdicts > b.rawVerdicts {
			b.rawVerdicts = rec.Verdicts
		}

		if rec.Silent {
			// A silent record carries no Findings count. There were none, so
			// it is the other half of the format signal, and a dump of nothing
			// but silent samples would otherwise be mistaken for a legacy file.
			b.silent = true
			newFormat = true
		} else {
			if rec.Index < 0 {
				return nil, nil, fmt.Errorf("dump record %d has index %d", i+1, rec.Index)
			}
			b.maxIndex = max(b.maxIndex, rec.Index)

			// A finding credited with two planted defects is written twice, so a
			// repeat at one index is expected and the extra lines differ only in
			// ground truth. A repeat that describes a DIFFERENT finding is not: it
			// means two runs share a key, which the truncate-on-open writer cannot
			// produce and a concatenated file can. Reconstructing from it would
			// take one run's finding and the other run's verdict.
			if prev, seen := b.byIndex[rec.Index]; seen {
				if !sameFinding(prev, rec) {
					return nil, nil, fmt.Errorf(
						"dump has two different findings at %s/%s run %d index %d (%q and %q): "+
							"this file holds more than one run under one key and cannot be "+
							"reconstructed — dumps are written truncate-on-open, so it was concatenated",
						rec.Model, rec.Fixture, rec.Run, rec.Index, prev.Title, rec.Title)
				}
			} else {
				b.byIndex[rec.Index] = rec
			}

			if rec.Verdict != nil {
				if rec.Verdict.Index != rec.Index {
					return nil, nil, fmt.Errorf(
						"dump record %d carries a verdict for index %d on the finding at index %d: "+
							"the verdict cannot be attached to a finding", i+1, rec.Verdict.Index, rec.Index)
				}
				// Two runs concatenated under one key can agree about the
				// finding and disagree about the VERDICT, identical findings
				// judged differently is precisely the judge noise this path was
				// built to measure, so it is the one collision that must not be
				// resolved by file order. sameFinding cannot see it: it compares
				// the finding and the verdict is not part of the finding, which
				// TestGroupDumpRefusesTwoJudgementsOfOneFinding pins by
				// submitting the identical finding under both verdicts in both
				// orders.
				if prev, seen := b.verdicts[rec.Index]; seen && prev != *rec.Verdict {
					return nil, nil, fmt.Errorf(
						"dump has two different verdicts on the same finding at %s/%s run %d index %d "+
							"(%+v and %+v): this file holds more than one judgement under one key, "+
							"and keeping either one would report a judgement chosen by file order",
						rec.Model, rec.Fixture, rec.Run, rec.Index, prev, *rec.Verdict)
				}
				b.verdicts[rec.Index] = *rec.Verdict
			}

			if rec.Category != "" {
				withCategory++
			}
			if rec.Suggestion != "" {
				withSuggestion++
			}
		}

		fixtures[rec.HeldOut][rec.Fixture] = true

		ck := contenderKey{rec.Model, rec.Variant}
		if runs[ck] == nil {
			runs[ck] = map[int]bool{}
			corpora[ck] = map[bool]bool{}
		}
		runs[ck][rec.Run] = true
		corpora[ck][rec.HeldOut] = true
	}

	var (
		groups   []RejudgeGroup
		silent   int
		inferred int
		lossy    []string
	)

	for key, b := range builders {
		if b.silent {
			// One key cannot be both a review that said nothing and a review
			// that said something: the writer emits the marker INSTEAD of
			// finding lines. Both present means two runs were concatenated, and
			// which one the group came from would be decided by whichever
			// branch this code took.
			if len(b.byIndex) > 0 {
				return nil, nil, fmt.Errorf(
					"dump marks %s/%s run %d as silent and also holds %d finding record(s) for it: "+
						"this file holds more than one run under one key",
					key.model, key.fixture, key.run, len(b.byIndex))
			}

			silent++
			groups = append(groups, RejudgeGroup{
				Model:   key.model,
				Variant: key.variant,
				Run:     key.run,
				Fixture: known[key.fixture],
				Silent:  true,
			})
			continue
		}

		n := b.maxIndex + 1

		// A hole in the MIDDLE was always refused; a hole at the END used to
		// rebuild short and silently, because the length was inferred from the
		// records present. That is the worse of the two: the new judge is shown
		// a shorter review than the recorded verdicts were made against, and
		// the baseline precision then disagrees with the published table it is
		// printed beside. The writer states the length so the reader does not
		// have to guess it.
		if b.declared > 0 && b.declared != n {
			return nil, nil, fmt.Errorf(
				"dump says %s/%s run %d judged %d finding(s) and holds records for %d: the file is "+
					"truncated, and re-judging the short list would compare the new judge against "+
					"verdicts made about a longer review",
				key.model, key.fixture, key.run, b.declared, n)
		}

		findings := make([]review.Finding, n)
		for i := range n {
			rec, ok := b.byIndex[i]
			if !ok {
				return nil, nil, fmt.Errorf(
					"dump has no record at index %d of %d for %s/%s run %d: the judged list "+
						"cannot be rebuilt, and rebuilding it short would move every finding "+
						"after the hole onto another finding's verdict",
					i, n, key.model, key.fixture, key.run)
			}
			findings[i] = findingFromRecord(rec)
		}

		verdicts := make([]Verdict, 0, len(b.verdicts))
		for i := range n {
			if v, ok := b.verdicts[i]; ok {
				verdicts = append(verdicts, v)
			}
		}

		// The dump attaches at most one verdict per position, so a judge that
		// answered a position twice or answered a position that has no finding
		// loses verdicts here that the published tables counted. Reported as a
		// property of the FILE rather than of the original judge: the recorded
		// baseline is short because this format cannot carry those verdicts,
		// not because the judge declined to make them.
		if b.rawVerdicts > len(verdicts) {
			lossy = append(lossy, fmt.Sprintf("%s/%s run %d (%d of %d)",
				key.model, key.fixture, key.run, len(verdicts), b.rawVerdicts))
		}

		groups = append(groups, RejudgeGroup{
			Model:    key.model,
			Variant:  key.variant,
			Run:      key.run,
			Fixture:  known[key.fixture],
			Findings: findings,
			Baseline: verdicts,
		})
	}

	// Legacy dumps only. Before Silent records existed a silent review wrote no
	// lines, so the matrix had to be inferred, and inference is wrong in both
	// directions: it invents a group for every fixture the contender was never
	// given, and it cannot see a contender that was silent on every fixture in a
	// run. Restricting the cross product to the corpus each contender appears
	// in removes the worst of it; the rest is warned about, because each
	// fabricated group costs a real judge call on an empty list.
	if !newFormat {
		for ck, seenRuns := range runs {
			for heldOut := range corpora[ck] {
				for fixture := range fixtures[heldOut] {
					for run := range seenRuns {
						key := groupKey{ck.model, ck.variant, fixture, run}
						if _, ok := builders[key]; ok {
							continue
						}
						inferred++
						silent++
						groups = append(groups, RejudgeGroup{
							Model:   ck.model,
							Variant: ck.variant,
							Run:     run,
							Fixture: known[fixture],
							Silent:  true,
						})
					}
				}
			}
		}
	}

	sort.Slice(groups, func(i, j int) bool {
		a, b := groups[i], groups[j]
		if a.Model != b.Model {
			return a.Model < b.Model
		}
		if a.Variant != b.Variant {
			return a.Variant < b.Variant
		}
		if a.Fixture.Name != b.Fixture.Name {
			return a.Fixture.Name < b.Fixture.Name
		}
		return a.Run < b.Run
	})

	var warnings []string

	if silent > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"%d group(s) held an EMPTY finding list. The dump cannot tell a reviewer that stayed "+
				"silent from a review that failed before it said anything; both are re-judged as "+
				"silence, which is what the original judge saw for the first and not the second.", silent))
	}

	if inferred > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"%d of those silent group(s) were INFERRED, not recorded: this dump predates the silent "+
				"marker, so the (contender, fixture, run) matrix was guessed from the records "+
				"present. The guess is restricted to the corpus each contender appears in, and is "+
				"still wrong in one direction it cannot detect — a contender silent on every "+
				"fixture in a run leaves no trace of that run at all. Each inferred group also "+
				"costs a judge call on an empty list. Re-collect the dump to replace the guess "+
				"with the writer's own record.", inferred))
	}

	if len(lossy) > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"the recorded baseline is SHORTER than the judgement it came from for %s. The dump "+
				"attaches at most one verdict per finding position, so a judge that answered a "+
				"position twice or answered a position with no finding loses those verdicts here "+
				"— the published tables counted them. The missing verdicts are a limit of this "+
				"file, not the original judge saying less.", strings.Join(lossy, ", ")))
	}

	if len(records) > 0 && withCategory == 0 {
		warnings = append(warnings, "no record in this dump carries a category. Either it predates "+
			"the field or no reviewer set one; where the original prompt showed a category, the "+
			"re-judged prompt shows an empty one, and that difference is measured as if it were "+
			"the judge.")
	}
	if len(records) > 0 && withSuggestion == 0 {
		warnings = append(warnings, "no record in this dump carries a suggestion, which judgeRequest "+
			"renders when present. Same caveat as category: a dump written before the field "+
			"existed re-judges a slightly shorter prompt.")
	}

	return groups, warnings, nil
}

// RejudgeInputProblem reports why re-judging a dump would measure something
// other than the judge, or "" when there is no such reason.
// TestAPartialDumpIsRefusedAsARejudgeInput exercises both of them.
//
// TWO WAYS TO BE HANDED A FILE SOMEBODY IS STILL WRITING, and until a battery
// retained its own findings there was only one. The first is the operator's:
// NewDump truncates on open and the environment that produced a dump is usually
// still exported in the shell that re-judges it, so writing and reading one path
// is the expected accident. The second arrived with retention, a battery that
// resolves its own path leaves EnvDump empty, so a check comparing the two
// variables reads "" and skips exactly when a default file is being filled.
// Dump.Close renames a retained run out of its in-progress suffix, which puts
// that answer in the name; this function reads it.
//
// It lives in non-test code so the default build can exercise it: the re-judge
// entry point is behind the `eval` build tag and a guard compiled only under
// that tag cannot run in `go test ./...`.
// TestAPartialDumpIsRefusedAsARejudgeInput covers both branches.
//
// writing is whatever EnvDump holds, passed in rather than read here so the
// refusal is a function of its arguments.
func RejudgeInputProblem(input, writing string) string {
	input = strings.TrimSpace(input)

	if strings.HasSuffix(input, dumpPartialSuffix) {
		// The recovery half is here because "wait for it to appear" is advice
		// that never comes true for the case an operator most often has: a run
		// that was killed leaves its partial behind and no process will ever
		// rename it. Renaming it by hand is the right move THEN and the wrong one
		// while a battery is still appending, so the condition is stated rather
		// than the instruction alone.
		return fmt.Sprintf("%s names %s, which is a dump still being written: a retained run writes "+
			"under %q and Dump.Close renames it away when the run finishes. Re-judging it would "+
			"measure whatever had been flushed. Wait for the run and point %s at %s once it appears; "+
			"if the run was killed and nothing is still writing that file, rename it to %s yourself "+
			"and re-judge that",
			EnvRejudgeDump, input, dumpPartialSuffix, EnvRejudgeDump,
			strings.TrimSuffix(input, dumpPartialSuffix), strings.TrimSuffix(input, dumpPartialSuffix))
	}

	if writing = strings.TrimSpace(writing); writing != "" {
		in, _ := filepath.Abs(input)
		out, _ := filepath.Abs(writing)
		if in == out {
			return fmt.Sprintf("%s and %s both name %s: the dump writer truncates on open, so this "+
				"would re-judge a file being emptied. Point %s at a different path, or unset it",
				EnvRejudgeDump, EnvDump, input, EnvDump)
		}
	}

	return ""
}

// sameFinding reports whether two records describe the same finding, ignoring
// the ground-truth fields that legitimately differ between the duplicate lines
// one finding gets when it is credited with several planted defects.
//
// The note behind it is in docs/measurement.md#samefinding.
func sameFinding(a, b DumpRecord) bool {
	return a.Path == b.Path && a.Line == b.Line && a.EndLine == b.EndLine &&
		slices.Equal(a.AlsoAt, b.AlsoAt) &&
		a.Severity == b.Severity && a.Class == b.Class && a.Category == b.Category &&
		a.Title == b.Title && a.Rationale == b.Rationale && a.Suggestion == b.Suggestion
}

// findingFromRecord rebuilds the finding a dump line was written from.
//
// The note behind it is in docs/measurement.md#findingfromrecord.
func findingFromRecord(r DumpRecord) review.Finding {
	return review.Finding{
		Path:               r.Path,
		Line:               r.Line,
		EndLine:            r.EndLine,
		AlsoAt:             r.AlsoAt,
		Severity:           r.Severity,
		SeverityTranslated: r.SeverityTranslated,
		RawSeverity:        r.SeveritySaid,
		Category:           r.Category,
		Class:              r.Class,
		Title:              r.Title,
		Rationale:          r.Rationale,
		Suggestion:         r.Suggestion,
	}
}

// RejudgeOutcome is one recorded group put in front of a second judge.
type RejudgeOutcome struct {
	Group RejudgeGroup

	// Result is the new judge's assessment, nil when Err is set.
	Result *JudgeResult

	Err error
}

// rejudger is the one call Rejudge makes, named so it can be substituted.
//
// The note behind it is in docs/measurement.md#rejudger.
type rejudger interface {
	Judge(ctx context.Context, f Fixture, persona config.Persona, findings []review.Finding) (*JudgeResult, error)
}

// Rejudge submits each recorded group to a judge, unchanged.
//
// No review is run: the findings are the ones the dump recorded, in their
// recorded positions, and the only thing that differs from the original
// judgement is which model is asked. Outcomes come back in the order the groups
// went in, so a report over them is deterministic.
func Rejudge(ctx context.Context, judge rejudger, persona config.Persona, groups []RejudgeGroup, concurrency int) []RejudgeOutcome {
	out := make([]RejudgeOutcome, len(groups))

	if concurrency < 1 {
		concurrency = 1
	}

	var (
		wg  sync.WaitGroup
		sem = make(chan struct{}, concurrency)
	)

	for i, g := range groups {
		wg.Add(1)

		go func(i int, g RejudgeGroup) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			out[i].Group = g

			// The group's own persona when it has one, the caller's otherwise.
			// A dump carries none and the report says the voice section of the
			// prompt therefore differs from the original; the live path carries
			// the real one, which is the only way the voice axis can be
			// corroborated without also changing what tone is scored against.
			voice := persona
			if g.Persona != nil {
				voice = *g.Persona
			}

			// A silent group is judged too, rather than assumed to produce
			// nothing. The original judge was called with an empty list, and a
			// judge that answers an empty list with verdicts is a failure mode
			// the report has to be able to show rather than one this code hides
			// by never asking.
			result, err := judge.Judge(ctx, g.Fixture, voice, g.Findings)
			out[i].Result = result
			out[i].Err = err
		}(i, g)
	}

	wg.Wait()

	return out
}

// CorroborationGroups builds re-judge groups from samples a judge has just
// scored LIVE.
//
// It is the bridge that lets the second judge cost judging only. The re-judge
// path was built to re-score findings read back off a dump, and its property,
// the findings go to the second judge in the positions the first judge saw them
// in, with no review re-run, is exactly what a live battery needs to publish a
// disagreement. Going out through a file and back would work and would add a
// confound for nothing: the dump cannot carry a persona, and it attaches at most
// one verdict per position, so a round trip through it would lose whatever the
// first judge said twice.
//
// Sorted so that the notes a corroboration produces come out in the same order
// twice. The aggregates do not care, and a report that reorders its own warnings
// between runs is one nobody can diff.
func CorroborationGroups(samples []DumpSample) []RejudgeGroup {
	groups := make([]RejudgeGroup, 0, len(samples))

	for _, s := range samples {
		var baseline []Verdict
		if s.Judged != nil {
			baseline = s.Judged.Verdicts
		}

		groups = append(groups, RejudgeGroup{
			Model:    s.Model,
			Variant:  s.Variant,
			Run:      s.Run,
			Fixture:  s.Fixture,
			Findings: s.Findings,
			Baseline: baseline,

			// Recorded, not inferred later. A review that reported nothing is
			// still submitted to the second judge, see Rejudge, and a judge
			// that answers an empty finding list is a failure this has to be
			// able to show rather than one it hides by never asking.
			Silent: len(s.Findings) == 0,
		})
	}

	sort.Slice(groups, func(i, j int) bool {
		a, b := groups[i], groups[j]
		if a.Model != b.Model {
			return a.Model < b.Model
		}
		if a.Variant != b.Variant {
			return a.Variant < b.Variant
		}
		if a.Fixture.Name != b.Fixture.Name {
			return a.Fixture.Name < b.Fixture.Name
		}
		return a.Run < b.Run
	})

	return groups
}

// Corroborate scores groups with a second judge and folds the answer into one
// Aggregate per contender, keyed the way the report keys its rows.
//
// The note behind it is in docs/measurement.md#corroborate.
func Corroborate(
	ctx context.Context,
	judge rejudger,
	persona config.Persona,
	groups []RejudgeGroup,
	concurrency int,
) (map[string]*Aggregate, map[string][]string) {
	var (
		byContender = map[string]*Aggregate{}
		notes       = map[string][]string{}
	)

	for _, o := range Rejudge(ctx, judge, persona, groups, concurrency) {
		g := o.Group
		label := contenderLabel(g.Model, g.Variant)
		where := fmt.Sprintf("%s run %d", g.Fixture.Name, g.Run)

		if o.Err != nil || o.Result == nil {
			notes[label] = append(notes[label], fmt.Sprintf(
				"%s: SECOND JUDGE FAILED. This sample is in the primary figure and not in the "+
					"second's, so the two judges no longer scored the same lists and NO FIGURE ON "+
					"THIS ROW CARRIES A DELTA — every judged cell reads `+NC`. That is the honest "+
					"reading: a rate over eight samples minus a rate over seven is not a "+
					"disagreement between judges. Re-run to recover the corroboration: %v",
				where, o.Err))
			continue
		}

		agg, ok := byContender[label]
		if !ok {
			agg = &Aggregate{}
			byContender[label] = agg
		}
		agg.Saw(g.Fixture.Name)

		// The stimulus is the group's finding list, which is BY CONSTRUCTION the
		// list the second judge was shown, Rejudge passes g.Findings and
		// nothing else. That makes this side of every delta self-reporting: if
		// the primary was folded in over a different list, the two fingerprints
		// differ and the figure refuses to publish a delta rather than
		// publishing one that compares two questions.
		for _, p := range agg.Add(o.Result, JudgedOver(g.Fixture.Name, g.Findings)) {
			notes[label] = append(notes[label],
				fmt.Sprintf("%s: SECOND JUDGE OUTPUT SUSPECT: %s", where, p))
		}
	}

	return byContender, notes
}

// rejudgeStats is one contender's two judgements, side by side.
//
// Both sides are folded by Aggregate.countVerdicts so that a precision printed
// here and a precision printed by the benchmark cannot come from two
// implementations. Only the per-verdict half is available: the dump records no
// grade, no signal_to_noise and no missed, so those columns have no baseline to
// sit beside and this report prints none of them.
type rejudgeStats struct {
	baseline Aggregate
	updated  Aggregate

	groups int
	silent int
	failed int

	// pairs counts findings both judges returned a verdict for, which is the
	// only population on which agreement is defined.
	pairs         int
	agreeReal     int
	agreeWorth    int
	agreeSeverity int
	agreeClass    int

	// baselineOnly and updatedOnly count findings exactly one judge returned a
	// verdict for. Agreement over the intersection alone would read as perfect
	// for a judge that answered two findings out of twenty.
	baselineOnly int
	updatedOnly  int

	// phantom counts verdicts the new judge returned about a finding that does
	// not exist, overwhelmingly on a SILENT group, where the finding list
	// handed over was empty.
	//
	// They are counted here and nowhere else. Folding them into `updated` would
	// let a judge that invents findings on an empty list raise the contender's
	// new-judge precision and its rank, against a baseline that is structurally
	// zero because a review with no findings has no verdicts to record. That is
	// a difference in what was counted wearing the costume of a difference
	// between judges.
	phantom int
}

// RejudgeReport renders the two rankings side by side.
//
// It prints no bias score. The question is whether a contender's rank moves
// when the judge changes, and specifically whether the contenders sharing the
// judge's vendor move together, a number claiming to summarize that would be
// quoted in place of the evidence, and eight fixtures cannot support it. The
// cohorts are printed apart so the pattern is either visible in the rows or is
// not there.
func RejudgeReport(baselineJudge, newJudge string, outcomes []RejudgeOutcome, warnings []string) string {
	var (
		b     strings.Builder
		stats = map[string]*rejudgeStats{}
		notes []string

		seenFixture = map[string]bool{}
		fixtures    []Fixture

		silent, failed, paired int
		variants               = map[string]bool{}
	)

	statsFor := func(model string) *rejudgeStats {
		s, ok := stats[model]
		if !ok {
			s = &rejudgeStats{}
			stats[model] = s
		}
		return s
	}

	for _, o := range outcomes {
		g := o.Group
		// Keyed on the contender, which is a model UNDER ONE VARIANT, the same
		// key GroupDump groups on. Keying on the model alone re-merged what
		// GroupDump had deliberately separated: the nitpick axis derives four
		// levels from ONE review, so a finding surviving all four was counted
		// four times in one row, and the persona axis, four separate reviews,
		// collapsed into a single contender, leaving the
		// axis the dump exists to compare absent from the table.
		s := statsFor(contenderLabel(g.Model, g.Variant))
		s.groups++

		// Collected so the header can name the corpus. A re-judge of a
		// held-out dump is still a held-out measurement, and the label is the
		// only thing that tells the two tables apart afterwards.
		if !seenFixture[g.Fixture.Name] {
			seenFixture[g.Fixture.Name] = true
			fixtures = append(fixtures, g.Fixture)
		}
		if g.Variant != "" {
			variants[g.Variant] = true
		}
		if g.Silent {
			s.silent++
			silent++
		}

		where := fmt.Sprintf("%s/%s run %d", contenderLabel(g.Model, g.Variant), g.Fixture.Name, g.Run)

		// A group the new judge could not assess is dropped from both sides.
		// Keeping its recorded verdicts would put the two precisions over
		// different samples, and a difference in sample is exactly what this
		// whole path exists to eliminate.
		if o.Err != nil || o.Result == nil {
			s.failed++
			failed++
			notes = append(notes, fmt.Sprintf(
				"%s: re-judge failed, EXCLUDED FROM BOTH SIDES: %v", where, o.Err))
			continue
		}
		paired++

		s.baseline.Saw(g.Fixture.Name)
		s.updated.Saw(g.Fixture.Name)

		// Both sides are attributed the same stimulus, in one statement, because
		// in this report they have one: the group IS a finding list,
		// the new judge was handed exactly it, and the recorded baseline is the
		// verdicts the dump filed against exactly it. Recording them apart would
		// be two chances to attribute one list two ways.
		//
		// What this cannot verify is the dump's own claim, DumpSample.Judged
		// says it "assesses exactly these findings", and a producer that wrote a
		// judgement formed over some other list would be believed here. The
		// persona axis is such a producer whenever it judges the whole corpus
		// once and dumps a filtered list beside those verdicts. See runLevels,
		// which judges each distinct filtered list instead.
		shown := JudgedOver(g.Fixture.Name, g.Findings)
		s.baseline.sawStimulus(shown)
		s.updated.sawStimulus(shown)

		// Both sides are reduced by the same rule before either is counted.
		//
		// The recorded baseline has already been through this reduction, because
		// the dump attaches at most one verdict per finding position; the new
		// judge's list had not, so a duplicate or out-of-range index inflated
		// PREC-B and nothing else. Handing one judge's answer back verbatim as
		// the other's then produced a non-zero DELTA and a rank move with no
		// judge having changed, which is the only thing this report claims to
		// measure. Whatever the reduction discards is reported, on both sides.
		base, baseDupes, basePhantom := verdictsByPosition(g.Baseline, len(g.Findings))
		updated, updatedDupes, updatedPhantom := verdictsByPosition(o.Result.Verdicts, len(g.Findings))

		// Validated on the list the judge RETURNED and counted on the reduced
		// one. Validating the reduced list would report nothing: the reduction
		// is what removed the malformed entries, so a judge's bad answer would
		// be laundered into a good one on its way to the table.
		for _, p := range verdictProblems(g.Baseline, len(g.Findings)) {
			notes = append(notes, fmt.Sprintf("%s: RECORDED VERDICTS SUSPECT: %s", where, p))
		}
		for _, p := range verdictProblems(o.Result.Verdicts, len(g.Findings)) {
			notes = append(notes, fmt.Sprintf("%s: NEW JUDGE OUTPUT SUSPECT: %s", where, p))
		}
		s.baseline.countVerdicts(base)
		s.updated.countVerdicts(updated)

		s.phantom += updatedPhantom
		if updatedPhantom > 0 {
			notes = append(notes, fmt.Sprintf(
				"%s: the new judge returned %d verdict(s) about a finding that does not exist%s; "+
					"counted in PHAN and excluded from PREC-B, which has no baseline counterpart "+
					"to compare them against", where, updatedPhantom, silentSuffix(g.Silent)))
		}
		if baseDupes+basePhantom > 0 {
			notes = append(notes, fmt.Sprintf(
				"%s: %d recorded verdict(s) were duplicate or out of range and are counted on neither side",
				where, baseDupes+basePhantom))
		}
		if updatedDupes > 0 {
			notes = append(notes, fmt.Sprintf(
				"%s: %d new-judge verdict(s) answered a position twice; the last is counted, matching "+
					"how the dump recorded the baseline", where, updatedDupes))
		}

		before := map[int]Verdict{}
		for _, v := range base {
			before[v.Index] = v
		}
		after := map[int]Verdict{}
		for _, v := range updated {
			after[v.Index] = v
		}

		// Iterated by POSITION, not over either verdict list, so a verdict with
		// an out-of-range index cannot pair with a finding that does not exist.
		// verdictProblems has already reported it as suspect.
		for i := range g.Findings {
			x, okBefore := before[i]
			y, okAfter := after[i]

			switch {
			case okBefore && okAfter:
				s.pairs++
				if x.Real == y.Real {
					s.agreeReal++
				}
				if x.WorthRaising == y.WorthRaising {
					s.agreeWorth++
				}
				if x.SeverityVerdict == y.SeverityVerdict {
					s.agreeSeverity++
				}
				if x.ClassCorrect == y.ClassCorrect {
					s.agreeClass++
				}
			case okBefore:
				s.baselineOnly++
			case okAfter:
				s.updatedOnly++
			}
		}
	}

	baseVendor := contenderVendor(baselineJudge)
	newVendor := contenderVendor(newJudge)

	b.WriteString("\nRE-JUDGED FROM A RECORDED DUMP — NO REVIEW WAS RE-RUN\n\n")
	fmt.Fprintf(&b, "corpus:                  %s\n", CorpusLabel(fixtures))
	fmt.Fprintf(&b, "baseline judge:          %s   (asserted by the caller; the dump records verdicts, not who made them)\n", baselineJudge)
	fmt.Fprintf(&b, "new judge:               %s\n", newJudge)
	fmt.Fprintf(&b, "groups:                  %d reconstructed, %d of them empty; %d paired, %d excluded after a failed re-judge\n",
		len(outcomes), silent, paired, failed)

	// Named, not described. "the judge shares a vendor with three contenders"
	// is a sentence that was true when somebody wrote it; this is recomputed
	// from the battery, so a contender added under either judge's vendor shows
	// up in the report that the comparison is read from.
	writeConflicts := func(role, judge string) {
		conflicts := VendorConflicts(judge)
		if len(conflicts) == 0 {
			fmt.Fprintf(&b, "%s judge vendor conflict: NONE — %s shares a vendor with no contender in the battery\n",
				role, judge)
			return
		}
		fmt.Fprintf(&b, "%s judge vendor conflict: %s scores %d contender(s) of its own vendor: %s\n",
			role, judge, len(conflicts), strings.Join(conflicts, ", "))
	}
	writeConflicts("baseline", baselineJudge)
	writeConflicts("new     ", newJudge)

	if baseVendor == newVendor {
		fmt.Fprintf(&b, "\nBOTH JUDGES ARE %s. This run measures the judge's OWN VARIANCE, not vendor preference:\n"+
			"whatever the same-vendor cohort does here, a second %s judge is not an independent\n"+
			"opinion of it. Read the disagreement below as the noise floor that any vendor\n"+
			"comparison has to clear, and re-run with a judge from another vendor to answer the\n"+
			"self-preference question.\n", strings.ToUpper(baseVendor), baseVendor)
	}

	b.WriteString("\nWHAT THIS CANNOT SAY\n")
	b.WriteString("  - MISSED, GRADE and SIGNAL are review-level fields the dump does not record, so the\n" +
		"    baseline has no value for them and none is printed. The 2-vs-5 missed instability that\n" +
		"    motivated this path is NOT measured here; only the per-finding verdicts are.\n")
	if len(variants) > 0 {
		fmt.Fprintf(&b, "  - %d variant(s) appear in this dump. Each is ranked as its own contender, because\n"+
			"    the nitpick axis derives every level from ONE review and merging them counts a\n"+
			"    surviving finding once per level. What the dump does NOT record is the persona\n"+
			"    itself: every group was re-judged under the DEFAULT persona, so for those groups\n"+
			"    the prompt differs from the original in the voice section as well as in the judge.\n", len(variants))
	}
	for _, w := range warnings {
		fmt.Fprintf(&b, "  - %s\n", w)
	}

	models := make([]string, 0, len(stats))
	for m := range stats {
		models = append(models, m)
	}
	sort.Strings(models)

	rankBefore := rankByPrecision(stats, func(s *rejudgeStats) Aggregate { return s.baseline })
	rankAfter := rankByPrecision(stats, func(s *rejudgeStats) Aggregate { return s.updated })

	// Cohorts, not a score. Same vendor as the judge that produced the dump
	// first, because that is the cohort under suspicion.
	cohorts := []struct {
		title  string
		vendor string
	}{
		{fmt.Sprintf("SAME VENDOR AS THE BASELINE JUDGE (%s)", baseVendor), baseVendor},
	}
	if newVendor != baseVendor {
		cohorts = append(cohorts, struct {
			title  string
			vendor string
		}{fmt.Sprintf("SAME VENDOR AS THE NEW JUDGE (%s)", newVendor), newVendor})
	}

	assigned := map[string]bool{}
	for _, c := range cohorts {
		fmt.Fprintf(&b, "\n%s\n", c.title)
		var rows []string
		for _, m := range models {
			if contenderVendor(m) != c.vendor {
				continue
			}
			assigned[m] = true
			rows = append(rows, precisionRow(m, stats[m], rankBefore, rankAfter))
		}
		writeRows(&b, rows)
	}

	fmt.Fprintf(&b, "\nEVERY OTHER VENDOR\n")
	var rest []string
	for _, m := range models {
		if assigned[m] {
			continue
		}
		rest = append(rest, precisionRow(m, stats[m], rankBefore, rankAfter))
	}
	writeRows(&b, rest)

	b.WriteString("\nRANK is over ALL contenders, not within the block: a cohort that moves together moves\n" +
		"against the models printed in the other blocks. MOVE is positive when the new judge\n" +
		"ranks the contender higher. V is verdicts COUNTED — one per finding position, the same\n" +
		"reduction the dump applies — and the two V columns differing is itself a result. PHAN is\n" +
		"verdicts about a finding that does not exist; they are counted in no precision, because\n" +
		"the other judge has nothing to be compared against there.\n")

	b.WriteString("\nRANK-A and RANK-B RANK BY PRECISION ALONE, and the published table does not. That table\n" +
		"ranks by the judge's GRADE first and uses precision only to break ties (reportJudgedModels),\n" +
		"and the dump records no grade, so this ranking cannot be reproduced here. MOVE +0 therefore\n" +
		"means the PRECISION order held, NOT that the published ranking did — and a contender can\n" +
		"move here without moving there, or the reverse.\n")

	b.WriteString("\nPER-VERDICT AGREEMENT, ON FINDINGS BOTH JUDGES ANSWERED\n")
	writeHeader(&b, agreementHeader)

	var total rejudgeStats
	for _, m := range models {
		s := stats[m]
		total.pairs += s.pairs
		total.agreeReal += s.agreeReal
		total.agreeWorth += s.agreeWorth
		total.agreeSeverity += s.agreeSeverity
		total.agreeClass += s.agreeClass
		total.baselineOnly += s.baselineOnly
		total.updatedOnly += s.updatedOnly

		fmt.Fprintf(&b, "%-*s %-6d %-6s %-6s %-6s %-6s %-7d %d\n",
			contenderWidth, truncate(m, contenderWidth), s.pairs,
			share(s.agreeReal, s.pairs), share(s.agreeWorth, s.pairs),
			share(s.agreeSeverity, s.pairs), share(s.agreeClass, s.pairs),
			s.baselineOnly, s.updatedOnly)
	}

	fmt.Fprintf(&b, "%-*s %-6d %-6s %-6s %-6s %-6s %-7d %d\n",
		contenderWidth, "ALL CONTENDERS", total.pairs,
		share(total.agreeReal, total.pairs), share(total.agreeWorth, total.pairs),
		share(total.agreeSeverity, total.pairs), share(total.agreeClass, total.pairs),
		total.baselineOnly, total.updatedOnly)

	b.WriteString("\nA-ONLY and B-ONLY are findings only one judge returned a verdict for. They are excluded\n" +
		"from the agreement rates and included in that judge's own precision, which is how the\n" +
		"published tables count them.\n")

	// The same legend the published tables carry. PREC-A/PREC-B/DELTA are three
	// cells of ONE JudgedFigure here, see precisionRow, so a reader moving
	// between this table and the ranking meets the same instrument described the
	// same way, rather than two spellings of one idea.
	b.WriteString("\n" + CrossJudgeLegend + "\n")

	if len(notes) > 0 {
		b.WriteString("\nNOTES\n")
		for _, n := range notes {
			fmt.Fprintf(&b, "  %s\n", n)
		}
	}

	return b.String()
}

// contenderWidth is wide enough for "nitpick/<vendor>/<model> [nitpick=pedantic]".
//
// 36 was not, and the truncation fell in the worst possible place: every
// nitpick level shares the prefix "[nitpick=", so four rows that the report had
// just been fixed to separate printed as four copies of "… [nitpick…".
const contenderWidth = 48

// The table headers, built from that width rather than hand-spaced, so a column
// cannot drift out of line with the format string that fills it. The rule under
// each is measured from the header itself: a hand-drawn one stops matching the
// moment a column is added, and these tables are printed three times each.
var (
	precisionHeader = fmt.Sprintf("%-*s GRP  SIL  FAIL  V-A   V-B   PHAN  PREC-A  PREC-B  DELTA   RANK-A  RANK-B  MOVE",
		contenderWidth, "CONTENDER")
	agreementHeader = fmt.Sprintf("%-*s PAIRS  REAL   WORTH  SEV    CLASS  A-ONLY  B-ONLY",
		contenderWidth, "CONTENDER")
)

// writeHeader prints a table header and a rule the same width.
func writeHeader(b *strings.Builder, header string) {
	b.WriteString(header)
	b.WriteString("\n")
	b.WriteString(strings.Repeat("-", len(header)))
	b.WriteString("\n")
}

// writeRows prints a cohort's rows, or says the cohort is empty.
//
// An empty block is printed rather than skipped: "no contender shares the
// judge's vendor" and "this report forgot to include them" look identical when
// the heading is absent.
func writeRows(b *strings.Builder, rows []string) {
	writeHeader(b, precisionHeader)

	if len(rows) == 0 {
		b.WriteString("(none)\n")
		return
	}
	for _, r := range rows {
		b.WriteString(r)
	}
}

// crossJudged is this contender's two judgements as ONE value.
//
// HaveSecond is unconditionally true: a re-judge that produced no second
// judgement for a contender is a failed group, excluded from both sides before
// it reaches here, so a stats row exists only where two judges were asked. What
// the second judge had nothing to say about arrives as an undefined value and
// renders as such.
func (s *rejudgeStats) crossJudged() CrossJudged {
	return CrossJudged{Primary: s.baseline, Second: s.updated, HaveSecond: true}
}

// precisionRow renders one contender under both judges.
//
// PREC-A, PREC-B and DELTA come from ONE JudgedFigure, through SplitCells, and
// V-A/V-B from one VerdictCells. This table is the one place both absolute
// precisions belong on the page. Its whole subject is the two judges, and a
// reader checking PREC-A against the published number needs the number, but
// they still cannot be obtained separately. Three cells arrive from one call or
// none do.
func precisionRow(model string, s *rejudgeStats, before, after map[string]int) string {
	cj := s.crossJudged()

	prec := cj.PrecisionFigure()
	pa, pb, delta := prec.SplitCells()
	va, vb := cj.VerdictCells()

	// MOVE is a rank difference, so it is meaningless unless both ranks exist.
	// A contender with no verdict under one judge has no precision to rank.
	move := "n/a"
	if prec.Corroborated() {
		move = fmt.Sprintf("%+d", before[model]-after[model])
	}

	return fmt.Sprintf("%-*s %-4d %-4d %-5d %-5s %-5s %-5d %-7s %-7s %-7s %-7d %-7d %s\n",
		contenderWidth, truncate(model, contenderWidth), s.groups, s.silent, s.failed,
		va, vb, s.phantom,
		pa, pb, delta,
		before[model], after[model], move)
}

// share formats an agreement rate, blank when nothing was compared.
func share(n, d int) string {
	if d == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.2f", float64(n)/float64(d))
}

// rankByPrecision ranks contenders 1..n by the precision one judge gave them.
//
// A contender with no judged finding sorts last rather than first, matching
// reportJudgedModels: undefined precision is not a perfect score, and a
// ranking that rewards silence rewards the one behaviour this project is
// trying to avoid.
func rankByPrecision(stats map[string]*rejudgeStats, pick func(*rejudgeStats) Aggregate) map[string]int {
	models := make([]string, 0, len(stats))
	for m := range stats {
		models = append(models, m)
	}

	sort.Slice(models, func(i, j int) bool {
		a, b := pick(stats[models[i]]), pick(stats[models[j]])
		if a.HasFindings() != b.HasFindings() {
			return a.HasFindings()
		}
		if a.HasFindings() && a.Precision() != b.Precision() {
			return a.Precision() > b.Precision()
		}
		return models[i] < models[j]
	})

	ranks := make(map[string]int, len(models))
	for i, m := range models {
		ranks[m] = i + 1
	}
	return ranks
}

// contenderVendor extracts the vendor a contender's id names.
//
// The benchmark labels our side "nitpick/<vendor>/<model>", so reading the
// first path segment would call every one of them "nitpick" and leave the
// same-vendor cohort, the entire question, permanently empty.
func contenderVendor(name string) string {
	name = strings.TrimPrefix(strings.TrimSpace(name), "nitpick/")
	if vendor, _, ok := strings.Cut(name, "/"); ok {
		return vendor
	}
	return name
}
