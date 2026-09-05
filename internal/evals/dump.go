package evals

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jdziat/open-nitpick/internal/review"
)

// EnvDump names a file to write per-finding diagnostics into, as JSON Lines.
//
// The reports say how many findings were inflated or misclassified. They cannot
// say WHICH, and closing those gaps means reading the offending finding beside
// the code it was about and the judge's reasoning for the call. Unset, this
// costs nothing: OpenDump returns a nil *Dump and every method on a nil *Dump is
// a no-op, so no call site carries a conditional.
const EnvDump = "NITPICK_EVAL_DUMP"

// DumpRecord is one judged finding with everything needed to re-derive any
// number in the reports.
//
// One line per finding rather than per sample, because this file is read by a
// program that groups and filters rather than by a human who scrolls. A finding
// credited with reporting two planted defects gets one line per defect, so
// grouping by defect_why counts each located defect once.
//
// A sample whose reviewer said nothing writes one Silent line, not zero lines.
// Misses still live in the judge's `missed` list and in Score.Detected rather
// than here — what the marker records is that the sample was RUN, which is the
// one thing a reader cannot infer from an absence.
type DumpRecord struct {
	Model   string `json:"model"`
	Fixture string `json:"fixture"`
	Run     int    `json:"run"`

	// HeldOut marks a record produced against the held-out corpus.
	//
	// The dump is written to a path the operator chooses and every experiment
	// reuses those paths, so a held-out run can land in the file the tuner has
	// been iterating against — carrying defect_why, which is the planted
	// defect's own prose. Nothing else in the record distinguishes the two, and
	// "read the dump, edit the prompt" is the documented workflow, so the
	// distinction has to be in the data rather than in the filename.
	HeldOut bool `json:"held_out,omitempty"`

	// Variant names the persona configuration under test, empty for the model
	// benchmark where the persona is held constant.
	//
	// The nitpick axis derives every level from ONE review, so a finding that
	// survives several levels' filters is written once per level. Group by
	// variant before counting anything, or the same finding is counted four
	// times.
	Variant string `json:"variant,omitempty"`

	// Index is the finding's position in the list submitted to the judge, which
	// is what Verdict.Index refers to. It is -1 on a Silent record, because -1
	// is not a position: any reader that treats records as findings by position
	// fails on it rather than inserting a blank finding at 0.
	Index int `json:"index"`

	// Silent marks a review that reported NOTHING.
	//
	// Such a review used to write no lines at all, which made it indomitably
	// ambiguous: a contender that stayed correctly silent on a clean fixture
	// and a contender that was never given that fixture are the same absence.
	// GroupDump had to guess the matrix back from the records present, and
	// guessed wrong in both directions — it invented groups for corpora a
	// contender never reviewed, and lost whole runs a contender was silent
	// through. One marker line ends the guessing.
	Silent bool `json:"silent,omitempty"`

	// Findings is how many findings the judged list held, so a reconstruction
	// can tell a COMPLETE list from a truncated one.
	//
	// Without it a dump whose last line was lost — a killed run, a `head -n`,
	// a filter — rebuilds SHORT and silently, because the length was inferred
	// from the largest index present. A hole in the middle was refused and a
	// hole at the end was not, which is the worse of the two: the re-judged
	// review is shorter than the one the recorded verdicts were made against.
	Findings int `json:"findings,omitempty"`

	// Verdicts is how many verdicts the judge returned for the whole sample,
	// which is NOT recoverable from the lines themselves.
	//
	// This file attaches at most one verdict per finding position, so a judge
	// that answered index 0 twice, or answered an index that has no finding,
	// loses verdicts here that the published tables counted. Recording the raw
	// count lets a reader see that the reconstruction is lossy instead of
	// reporting the loss as the ORIGINAL judge having said too little.
	Verdicts int `json:"verdicts,omitempty"`

	// FixtureHash pins the source the reviewer actually read.
	//
	// A dump names its fixture and nothing else, and the fixtures are Go source
	// that gets edited. Re-judging resolves the name against the CURRENT
	// corpus, so an edit to a fixture's Head between the benchmark and the
	// re-judge changes the prompt as well as the judge — reintroducing, through
	// the corpus, the exact confound re-judging exists to remove. The name
	// surviving is not evidence the change did.
	FixtureHash string `json:"fixture_hash,omitempty"`

	Path    string `json:"path"`
	Line    int    `json:"line"`
	EndLine int    `json:"end_line,omitempty"`

	// AlsoAt are the further regions the finding claimed.
	//
	// IT IS RECORDED FOR THE SCORER, NOT FOR THE JUDGE, and it was omitted for
	// exactly that reason: judgeRequest shows one location per finding, so a
	// secondary span changes nothing about the prompt a re-judge rebuilds. What
	// it does change is every detection number. anchorDistance takes the minimum
	// over the primary span AND every region here, and coverInto unions them, so
	// ANCHOR and NOISE are both computed from this field — and Incumbent is the
	// reviewer that fills it, from its "Also applies to" lines.
	//
	// Without it a dump is not a sufficient record of a run: ANCHOR replays
	// understated and NOISE overstated, because a finding whose only near span
	// was secondary comes back as invented. Nothing distinguishes a dropped span
	// from a finding that had none, so the loss is silent in both directions.
	// TestASecondarySpanSurvivesTheDump scores a round trip rather than
	// inspecting the field, since the field matters only for what it changes.
	//
	// open-nitpick's own reviews leave it empty — review.Finding.AlsoAt says the
	// schema does not offer it — so this is omitempty on every one of our
	// records. The incumbent is the only reviewer here whose records can carry
	// it, and "can" is the accurate word: it is filled only where a review
	// printed an "Also applies to" line, which most of them do not. An earlier
	// version of this sentence said the field was "present on the incumbent's"
	// records, which reads as a property of all of them and is a property of a
	// small minority. That matters in the direction that decides whether the
	// round trip is worth testing: the field is exercised by few records, so its
	// loss would be invisible in most of the file and decisive in the rest.
	AlsoAt []review.LineSpan `json:"also_at,omitempty"`

	// Severity is the level THIS PROJECT recorded the finding at. It is not
	// necessarily a word the reviewer used, and the two fields below say which.
	//
	// THE BUG: it was written under a bare "severity" key beside
	// "model":"incumbent/cli", so the artifact every downstream reading is
	// re-derived from published our translation of a foreign vocabulary as the
	// reviewer's own severity. review.Finding.RawSeverity carries `json:"-"`, so
	// the word the reviewer actually printed could not reach this file at all —
	// the substitution the tables had been fixed for survived one layer down, in
	// the file a reader goes to when they doubt the tables.
	Severity string `json:"severity"`

	// SeveritySaid is the word the REVIEWER printed, when this project rewrote it
	// into Severity above. Absent when SeverityTranslated is false, because then
	// Severity IS the reviewer's word and repeating it would invent a second
	// source for one fact.
	//
	// Absent WITH SeverityTranslated true is the third state and it is a real
	// one: something rewrote the severity and the original is not recoverable —
	// a Incumbent cache entry collected before the raw review was retained, or
	// an analyzer that published no severity at all. A reader must be able to
	// tell that from "the reviewer said this", which is why the flag is written
	// rather than inferred from the word's absence.
	SeveritySaid       string `json:"severity_said,omitempty"`
	SeverityTranslated bool   `json:"severity_translated,omitempty"`

	Class string `json:"class"`

	// Category and Suggestion are scored by nothing and recorded anyway,
	// because judgeRequest SHOWS both to the judge.
	//
	// Re-judging a recorded finding has to rebuild the prompt the first judge
	// saw. A reconstruction missing two rendered fields measures those missing
	// fields as well as the change of judge, which is the exact confound the
	// re-judge path exists to remove. A dump written before these fields
	// existed carries neither, and RejudgeReport says so rather than quietly
	// comparing a judge against a shorter prompt.
	Category string `json:"category,omitempty"`

	Title      string `json:"title"`
	Rationale  string `json:"rationale,omitempty"`
	Suggestion string `json:"suggestion,omitempty"`

	// Verdict is the judge's assessment, absent when the judge returned none
	// for this index. Absent is not "the judge approved it" — a judge that
	// returns the wrong number of verdicts is already reported as suspect, and
	// a reader of this file must be able to tell the two apart.
	Verdict *Verdict `json:"verdict,omitempty"`

	// Matched reports whether this finding is the one CREDITED with reporting a
	// planted defect — the same finding recall counted and the same one the
	// severity columns graded. The fields below are populated only when it did:
	// nothing planted says what an unmatched finding's severity should have
	// been.
	//
	// It is deliberately the scorer's own association rather than a second
	// answer to the same question. Re-deriving it here let the dump disagree
	// with the table it exists to explain, which is worse than not having it.
	// A finding that also mentions a defect but was not the one credited
	// carries no ground truth, so grouping this file by defect_why counts each
	// located defect once.
	Matched      bool   `json:"matched"`
	WantSeverity string `json:"want_severity,omitempty"`

	// SeverityDelta is the objective verdict — accurate, inflated or
	// understated — against WantSeverity, comparing EXACT levels. Precomputed so
	// a reader does not reimplement the severity ordering to recover it.
	//
	// A second, banded verdict used to be written beside it as band_delta, for
	// the cross-tool columns that are now withdrawn. It is gone rather than
	// merely unprinted: a field carried in the dump is a number someone will
	// aggregate, and this one is maximised by rating everything critical. See
	// NoCrossToolSeverityScore.
	SeverityDelta string `json:"severity_delta,omitempty"`

	// DefectWhy is the planted defect's own description, so a line of this file
	// is legible without the fixture source beside it.
	DefectWhy string `json:"defect_why,omitempty"`
}

// DumpSample is one judged review: what was reviewed, by whom, and what the
// judge said about it.
type DumpSample struct {
	Model   string
	Variant string
	Run     int

	Fixture  Fixture
	Findings []review.Finding

	// Judged assesses exactly these findings, so Verdict.Index is a position in
	// Findings — and "assesses" means the judge was SHOWN this list, not merely
	// that the verdicts have been renumbered to fit it.
	//
	// The file cannot carry the difference, and a reader of it has no way to
	// check: a producer that judged some larger list and filtered the verdicts
	// down would look identical here, and every figure a re-judge derived from
	// it would compare two questions. The persona path was that producer — it
	// judged a shared corpus once and recorded each nitpick level's filtered
	// list beside those verdicts — and it no longer is; runLevels judges each
	// distinct filtered list. Any new producer owes the same.
	Judged *JudgeResult
}

// Dump writes DumpRecords as JSON Lines. A nil *Dump is a disabled dump.
type Dump struct {
	mu   sync.Mutex
	file *os.File
	enc  *json.Encoder

	// finalPath is the name this dump takes on when it is complete, empty for a
	// dump written straight to the path the operator named.
	//
	// A retained run writes to finalPath+dumpPartialSuffix and is renamed by
	// Close, so a file sitting under the final name has a writer that finished.
	// That is what a concurrent reader can check, and it holds for a run that was
	// killed as well as for one that is still going.
	finalPath string
}

// OpenDump opens the dump named by EnvDump, returning nil when it is unset.
func OpenDump() (*Dump, error) {
	path := strings.TrimSpace(os.Getenv(EnvDump))
	if path == "" {
		return nil, nil
	}
	return NewDump(path)
}

// NewDump writes records to path.
//
// It truncates rather than appends: the records are keyed by (model, fixture,
// run) and every experiment reuses those keys, so an accumulating file cannot
// be told apart from one run that produced twice the findings.
func NewDump(path string) (*Dump, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	return &Dump{file: f, enc: json.NewEncoder(f)}, nil
}

// runDumpDir is where a run's own dump lands when the operator named no path.
//
// It sits beside the package rather than under testdata: testdata holds
// collected evidence that is committed and read back — the Incumbent cache —
// and a run artifact written on every invocation does not belong in it.
const runDumpDir = ".eval-runs"

// OpenRunDump opens the dump a battery writes its own findings to, whether or
// not anybody asked for one.
//
// THE RUN IT EXISTS FOR HAS ALREADY HAPPENED. The held-out battery that produced
// the Rule 14 evidence ran with EnvDump unset, so OpenDump returned nil, every
// Record call was a no-op, and the finding lists sat in memory for the whole of
// a paid run and were written nowhere. Two of Rule 14's four conditions then
// needed a re-run to evaluate — against a corpus whose own label says it is
// spent once. Retention is not a diagnostic convenience here; it is what makes
// the next held-out spend the last one required for a model-free column, because
// RECALL, NOISE, ANCHOR and L/DEF are pure functions of (findings, fixture) and
// need no judge and no network to recompute.
//
// THE RECORDING IS NOT GATED ON THE JUDGE, and saying so is load-bearing rather
// than decorative: the arithmetic needing no judge is worth nothing if the write
// happens after a judge call that can fail. It did, and a review whose judge call
// errored was discarded — findings already paid for, and on the incumbent's side
// drawn from a rate-limited allowance the benchmark's live path does not even
// cache. TestEveryPaidReviewIsRetainedWhateverTheJudgeSays holds the write above
// every return that follows the judge.
//
// It does NOT change OpenDump. That function's nil-on-unset contract is shared
// by the remaining callers and pinned by TestDumpDisabledCostsNothing, and the
// nil no-op is what lets every call site drop a record unconditionally; a
// default resolved inside it would also leave EnvDump empty, which is what the
// re-judge path's collision check used to be the whole of. Batteries that want
// retention ask for it here.
//
// WHICH BATTERIES THOSE ARE IS DERIVED, NOT LISTED, and the earlier version of
// this sentence is why. It said the tuning axes still used OpenDump deliberately
// because "their corpus can be reviewed again" — true of TestTunePersona, the
// one battery that remains on OpenDump, and false of TestJudgeModels, which
// prints the same judged table the head-to-head does and which
// `make judge-models FIXTURES=$(HELD_OUT)` points at the spent-once corpus. A
// class of caller was named where a property of one was meant.
// TestEveryJudgedBatteryRetainsItsFindingsWithoutBeingAsked now derives the list
// from the table: anything calling reportJudgedModels must open through here.
//
// battery names the caller, so a directory of retained runs says which produced
// each file.
func OpenRunDump(battery string, fixtures []Fixture) (*Dump, string, error) {
	return openRunDumpAt(runDumpDir, battery, fixtures, time.Now().UTC(), os.Getpid())
}

// openRunDumpAt is OpenRunDump with the directory, clock and pid supplied, so
// the naming and the refusal are testable without waiting for a second to pass.
func openRunDumpAt(dir, battery string, fixtures []Fixture, now time.Time, pid int) (*Dump, string, error) {
	// An operator who named a path gets exactly that path, truncation and all:
	// the Makefile documents it, and second-guessing an explicit argument is how
	// a tool becomes unpredictable.
	if path := strings.TrimSpace(os.Getenv(EnvDump)); path != "" {
		d, err := NewDump(path)
		return d, path, err
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, "", err
	}

	final := filepath.Join(dir, runDumpName(battery, fixtures, now, pid))

	// O_EXCL, not os.Create. The name makes a collision improbable and this makes
	// it impossible to be SILENT, which is the property that matters: the file a
	// second run would land on may be the only record of a held-out run, and
	// os.Create would truncate it and report success. A refusal costs a rerun with
	// a different name and is raised before this battery has spent anything.
	f, err := os.OpenFile(final+dumpPartialSuffix, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return nil, "", fmt.Errorf("a run is already being written to %s%s; this one would have to "+
			"overwrite it, and the corpus behind it may not be re-collectable",
			final, dumpPartialSuffix)
	}
	if err != nil {
		return nil, "", err
	}
	if _, err := os.Stat(final); err == nil {
		_ = f.Close()
		_ = os.Remove(final + dumpPartialSuffix)
		return nil, "", fmt.Errorf("a finished run is already retained at %s; this run would have "+
			"to overwrite it, and the corpus behind it may not be re-collectable", final)
	}
	return &Dump{file: f, enc: json.NewEncoder(f), finalPath: final}, final, nil
}

// dumpPartialSuffix marks a dump that is still being written.
//
// A file under it is one whose writer has not finished, so a reader handed one
// is reading a prefix of a run. Close renames it away, which makes completeness
// a property of the NAME rather than of an environment variable the writer no
// longer sets. See RejudgeInputProblem, which refuses one.
const dumpPartialSuffix = ".partial"

// runDumpName is the file name a retained run gets.
//
// Every part of it is load-bearing against ONE hazard: NewDump truncates, for a
// reason its own comment gives, so a fixed default name would let the second
// held-out benchmark destroy the first one's evidence with no warning and no
// test failure. The corpus token is here because held-out and tuning runs are
// the distinction the Makefile's truncation warning is actually about, and the
// timestamp and pid are what make two runs land on two files.
//
// Uniqueness by name is not relied on alone: openRunDumpAt creates the file
// exclusively, so a collision is a refusal rather than an overwrite.
// TestARetainedRunRefusesToOverwriteAnEarlierOne covers the residual.
func runDumpName(battery string, fixtures []Fixture, now time.Time, pid int) string {
	corpus := "empty"
	kinds := map[string]int{}
	for _, f := range fixtures {
		switch {
		case HeldOut(f.Name):
			kinds["heldout"]++
		case MultiFile(f.Name):
			kinds["multifile"]++
		case Info(f.Name):
			kinds["info"]++
		case Caller(f.Name):
			kinds["callers"]++
		case Slop(f.Name):
			kinds["slop"]++
		default:
			kinds["tuning"]++
		}
	}
	switch {
	case len(kinds) > 1:
		corpus = "mixed"
	case len(kinds) == 1:
		for k := range kinds {
			corpus = k
		}
	}

	return fmt.Sprintf("%s-%s-%s-%d.jsonl",
		sanitizeDumpName(battery), corpus, now.UTC().Format("20060102T150405Z"), pid)
}

// sanitizeDumpName reduces a caller-supplied label to something safe in a file
// name, so a battery named with a slash cannot write outside the run directory.
func sanitizeDumpName(s string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	if b.Len() == 0 {
		return "run"
	}
	return b.String()
}

// Record writes one line per finding in the sample, and one line per credited
// defect for a finding that reported more than one.
//
// The duplicate line is what makes the file groupable both ways: by
// (model, fixture, run, index) it is still one comment, and by defect_why every
// located defect is counted exactly once even when a reviewer folded two of
// them into a single comment. Emitting one line and dropping the second defect
// would make a merged comment look like a miss.
//
// It takes its own lock rather than relying on the caller's: every experiment
// here fans reviews out across goroutines into one shared sink, and a sink that
// serializes itself is the only version that stays correct when a call site
// later moves out from under the mutex it happened to be written beneath.
func (d *Dump) Record(s DumpSample) error {
	if d == nil {
		return nil
	}

	byIndex := map[int]Verdict{}
	if s.Judged != nil {
		for _, v := range s.Judged.Verdicts {
			byIndex[v.Index] = v
		}
	}

	// The scorer's own calls, keyed by the finding it credited, so this file
	// reports exactly what the tables counted rather than a second answer to
	// the same question that is free to disagree with them.
	calls := map[int][]SeverityCall{}
	for _, c := range ScoreSeverity(s.Fixture, s.Findings).Calls {
		calls[c.FindingIndex] = append(calls[c.FindingIndex], c)
	}

	heldOut := HeldOut(s.Fixture.Name)

	var rawVerdicts int
	if s.Judged != nil {
		rawVerdicts = len(s.Judged.Verdicts)
	}

	// The per-sample fields, identical on every line the sample writes. They are
	// repeated rather than emitted once because this file is JSON Lines: a
	// reader that filters it with grep or jq keeps whole lines, and a header
	// record would not survive the filtering the format exists to allow.
	sample := DumpRecord{
		Model:       s.Model,
		Fixture:     s.Fixture.Name,
		HeldOut:     heldOut,
		Run:         s.Run,
		Variant:     s.Variant,
		Findings:    len(s.Findings),
		Verdicts:    rawVerdicts,
		FixtureHash: fixtureFingerprint(s.Fixture),
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	// A review that said nothing writes ONE line rather than none. Silence is a
	// result — it is the correct answer on a clean fixture — and recording it as
	// an absence made it indistinguishable from a sample that was never run.
	if len(s.Findings) == 0 {
		rec := sample
		rec.Index = -1
		rec.Silent = true
		return d.enc.Encode(rec)
	}

	for i, f := range s.Findings {
		rec := sample
		rec.Index = i
		rec.Path = f.Path
		rec.Line = f.Line
		rec.EndLine = f.EndLine
		rec.AlsoAt = f.AlsoAt
		rec.Severity = f.Severity

		// Routed through severityAsSaid so this file and the published
		// vocabulary block cannot disagree about who said what. Deriving it here
		// from the finding's fields would be a second answer to one question, and
		// the whole reason this record exists is to be the evidence behind the
		// tables rather than a rival to them.
		said := severityAsSaid(f)
		rec.SeverityTranslated = severityWasTranslated(f)
		rec.SeveritySaid = ""
		if rec.SeverityTranslated {
			rec.SeveritySaid = said.Said
		}

		rec.Class = f.Class
		rec.Category = f.Category
		rec.Title = f.Title
		rec.Rationale = f.Rationale
		rec.Suggestion = f.Suggestion

		if v, ok := byIndex[i]; ok {
			rec.Verdict = &v
		}

		credited := calls[i]
		if len(credited) == 0 {
			if err := d.enc.Encode(rec); err != nil {
				return err
			}
			continue
		}

		for _, c := range credited {
			rec.Matched = true
			rec.WantSeverity = c.Defect.WantSeverity.String()
			rec.SeverityDelta = c.Verdict
			rec.DefectWhy = c.Defect.Why

			if err := d.enc.Encode(rec); err != nil {
				return err
			}
		}
	}

	return nil
}

// Close releases the file, and for a retained run promotes it out of its
// in-progress name.
//
// The rename is the completeness marker. A dump is written by a battery that
// takes tens of minutes and is read by `make rejudge`, and the only guard
// against reading one mid-write compares two environment variables — which a
// path this file resolved for itself does not set. Renaming on Close moves that
// guarantee into the file name, where a reader can check it. See
// RejudgeInputProblem.
//
// THE DESTINATION IS CHECKED HERE TOO, and not because openRunDumpAt's check is
// insufficient at the moment it runs. Between that check and this one sits a
// battery that takes tens of minutes, and os.Rename is silent: a run that landed
// on this name in the meantime would be replaced with no error and no trace. The
// window is not reachable from anything in this package — that is why the
// refusal below is a latent guard rather than an observed bug — but the argument
// for the open-time check ("the file it would land on may be the only copy of a
// held-out run") does not weaken while the run is going.
// TestAFinishedRunIsNotRenamedOverAnother covers it.
func (d *Dump) Close() error {
	if d == nil {
		return nil
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if err := d.file.Close(); err != nil {
		return err
	}
	if d.finalPath == "" {
		return nil
	}
	if _, err := os.Stat(d.finalPath); err == nil {
		return fmt.Errorf("a run appeared at %s while this one was writing; %s%s is left in place "+
			"rather than renamed over it, because the corpus behind either may not be re-collectable",
			d.finalPath, d.finalPath, dumpPartialSuffix)
	}
	return os.Rename(d.finalPath+dumpPartialSuffix, d.finalPath)
}
