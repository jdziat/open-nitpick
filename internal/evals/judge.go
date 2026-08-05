package evals

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"math"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	llms "github.com/nocturnium/llm-go-sdk"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/llm"
	"github.com/jdziat/open-nitpick/internal/prompt"
	"github.com/jdziat/open-nitpick/internal/review"
)

// DefaultJudgeModel stands in for a senior human reviewer.
//
// The judge must be stronger than anything under test, because its job is to
// say whether a finding is worth a colleague's attention — a question keyword
// matching cannot answer. Keyword scoring tells you a bug was found; only a
// judgement call tells you the review was worth reading.
//
// DEFAULTJUDGEMODEL SHARES A VENDOR WITH CONTENDERS IT SCORES, and that is not
// a defect this constant can fix on its own. DefaultModels carries three OpenAI
// entries — gpt-5.6-luna, gpt-5.4, and THIS MODEL. The judge is not merely from
// the same vendor as three contenders; it is one of them, grading its own
// output. LLM-as-judge self-preference is a documented effect and every judged
// column inherits whatever preference it carries.
//
// The exact list is not written down here, because a list in a comment is wrong
// the first time somebody edits the battery — the brief that commissioned the
// second judge named three OpenAI contenders that are not in DefaultModels at
// all, and named three CLEAN vendors that are. VendorConflicts computes it.
//
// Swapping this constant for a clean vendor would move the conflict rather than
// measure it: the new judge would have its own preferences and nothing would say
// how much either one moved the table. SecondJudgeModel is the answer instead —
// the same findings scored twice, with the disagreement published beside every
// judged figure. VendorConflicts is what refuses to let the conflict go
// unstated, and it is computed from DefaultModels rather than described here,
// because a battery edit is what makes a sentence like this one wrong.
const DefaultJudgeModel = "openai/gpt-5.6-terra"

// SecondJudgeModel corroborates the primary judge from a vendor NO contender
// shares.
//
// x-ai has no entry in DefaultModels — checked against the live OpenRouter
// catalog (GET /api/v1/models) on 2026-08-04, which listed 338 models across 8
// contender vendors and 49 others. TestTheSecondJudgeSharesNoVendorWithAny
// Contender recomputes that from the battery on every run, so adding an x-ai
// contender fails the suite rather than quietly re-creating the conflict this
// judge exists to remove.
//
// grok-4.5 specifically, on three requirements the judge has:
//
//   - STRONGER THAN THE FIELD. DefaultJudgeModel's doc comment states the
//     requirement and it is not negotiable — the judge decides whether a finding
//     was worth a colleague's attention. grok-4.5 is x-ai's flagship ("frontier
//     performance on coding, knowledge work, and STEM"), and ~x-ai/grok-latest
//     redirects to it. The clean vendors that are NOT this are all weaker: the
//     brief that commissioned this work named mistral as a candidate, and
//     mistralai/mistral-medium-3.1 was DROPPED from this battery for judging
//     last at 2.74 with 7 inflated findings of 11.
//   - STRUCTURED OUTPUT. The judge extracts a hand-authored JSON schema, so a
//     model without json_schema support falls back to JSON mode and the verdict
//     list stops being a reliable shape. grok-4.5 advertises both
//     response_format and structured_outputs.
//   - ENOUGH CONTEXT FOR judgeRequest. It renders every file of the fixture at
//     Head AND at Base, plus the persona and every finding. grok-4.5 carries
//     500k tokens, against 1.05M for the primary judge — comfortably above the
//     largest fixture, and the multi-file corpus is the axis to re-check this on.
//
// It is also, unlike the primary judge, a model that accepts `temperature`. The
// harness pins 0 on both; on the primary that pin is silently ignored, which is
// one of the reasons the same cached findings scored 3.66, 3.90, 3.95 and 3.98
// across four runs at "temperature 0".
const SecondJudgeModel = "x-ai/grok-4.5"

// EnvJudgeModel overrides the judge.
const EnvJudgeModel = "NITPICK_EVAL_JUDGE"

// EnvSecondJudge names the corroborating judge, and is the switch that turns
// every judged figure from one opinion into two.
//
// Unset is a supported state and NOT a silent one: every figure then renders
// with its disagreement marked UNMEASURED rather than omitted, so a
// single-judge table cannot be mistaken for a corroborated one. See
// JudgedFigure.
const EnvSecondJudge = "NITPICK_EVAL_JUDGE2"

// SecondJudgeFromEnv resolves the corroborating judge, returning "" when there
// is none.
//
//	unset or empty   no second judge. Every judged figure renders "+?" and the
//	                 report states that it is one opinion.
//	"default"        SecondJudgeModel, with its vendor re-checked against the
//	                 battery by the suite.
//	anything else    that model id, used as given.
//
// The "default" spelling exists so the vetted id does not have to be copied
// into the Makefile. A judge named in a Makefile is a judge no test can see: it
// would not pass through VendorConflicts, and the conflict this whole path was
// built to remove would be one shell variable away from coming back.
func SecondJudgeFromEnv() string {
	raw := strings.TrimSpace(os.Getenv(EnvSecondJudge))
	if raw == "default" {
		return SecondJudgeModel
	}
	return raw
}

// Verdict is the judge's assessment of one finding.
type Verdict struct {
	// Index identifies the finding by its position in the reviewed list.
	Index int `json:"index"`

	// Real reports whether the finding describes a genuine problem in the code.
	Real bool `json:"real"`

	// WorthRaising reports whether a senior reviewer would actually leave this
	// comment. A finding can be technically true and still not worth the
	// reader's time, which is the distinction that separates a useful reviewer
	// from an exhausting one.
	WorthRaising bool `json:"worth_raising"`

	// SeverityVerdict is one of "accurate", "inflated", or "understated".
	SeverityVerdict string `json:"severity_verdict"`

	// ToneVerdict is one of "matches", "too_harsh", "too_soft", or "too_wordy".
	ToneVerdict string `json:"tone_verdict"`

	// ClassCorrect reports whether the finding's class matches what the problem
	// actually is.
	//
	// Class drives which findings a repository publishes, so a mislabelled one
	// is silently filtered away at the stricter levels. Without this field the
	// harness cannot tell "the filter worked" from "the model mislabelled it
	// and a real defect vanished" — they produce the identical observation of a
	// lower finding count.
	ClassCorrect bool `json:"class_correct"`

	// ExpectedClass is the class the judge would have assigned.
	ExpectedClass string `json:"expected_class"`

	// Reasoning is one sentence justifying the call.
	Reasoning string `json:"reasoning"`
}

// JudgeResult is the judge's assessment of a whole review.
type JudgeResult struct {
	Verdicts []Verdict `json:"verdicts"`

	// Missed lists defects a senior reviewer would have raised that the review
	// did not. This is the only way to measure recall against defects nobody
	// planted deliberately.
	Missed []string `json:"missed"`

	// SignalToNoise grades the review 0-10 on whether reading it was worth the
	// time.
	SignalToNoise int `json:"signal_to_noise"`

	// ToneAdherence grades 0-10 how well the review matched the requested voice.
	ToneAdherence int `json:"tone_adherence"`

	// Grade is a letter grade for the review overall.
	Grade string `json:"grade"`

	// Summary is one paragraph a human would read.
	Summary string `json:"summary"`
}

// judgeSchema constrains the judge's output. Hand-authored for the same reason
// the review schema is: reflected schemas mark every field required, and the
// judge must be able to return an empty missed list.
func judgeSchema() (json.RawMessage, error) {
	verdict := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"index":            map[string]any{"type": "integer"},
			"real":             map[string]any{"type": "boolean"},
			"worth_raising":    map[string]any{"type": "boolean"},
			"severity_verdict": map[string]any{"type": "string", "enum": []string{"accurate", "inflated", "understated"}},
			"tone_verdict":     map[string]any{"type": "string", "enum": []string{"matches", "too_harsh", "too_soft", "too_wordy"}},
			"class_correct":    map[string]any{"type": "boolean"},
			"expected_class":   map[string]any{"type": "string", "enum": config.ClassNames()},
			"reasoning":        map[string]any{"type": "string"},
		},
		"required":             []string{"index", "real", "worth_raising", "severity_verdict", "tone_verdict", "class_correct", "expected_class", "reasoning"},
		"additionalProperties": false,
	}

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"verdicts":        map[string]any{"type": "array", "items": verdict},
			"missed":          map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"signal_to_noise": map[string]any{"type": "integer", "minimum": 0, "maximum": 10},
			"tone_adherence":  map[string]any{"type": "integer", "minimum": 0, "maximum": 10},
			"grade":           map[string]any{"type": "string", "enum": []string{"A+", "A", "A-", "B+", "B", "B-", "C+", "C", "C-", "D", "F"}},
			"summary":         map[string]any{"type": "string"},
		},
		"required":             []string{"verdicts", "missed", "signal_to_noise", "tone_adherence", "grade", "summary"},
		"additionalProperties": false,
	}

	return json.Marshal(schema)
}

// Judge evaluates a review the way a senior engineer would.
type Judge struct {
	client *llm.Client
	model  string
}

// NewJudge builds a judge backed by the configured model.
func NewJudge(model string) (*Judge, error) {
	if model == "" {
		model = DefaultJudgeModel
	}

	spec := config.ModelSpec{
		// Same provider the harness and production use; see evalConfig.
		Provider: llm.ProviderOpenRouter,
		Model:    model,

		Temperature:      floatPtr(0),
		MaxTokens:        8192,
		Timeout:          5 * time.Minute,
		StructuredOutput: config.StructuredAuto,
	}

	client, err := llm.Build(spec)
	if err != nil {
		return nil, fmt.Errorf("build judge %s: %w", model, err)
	}

	return &Judge{client: client, model: model}, nil
}

// Model returns the judge's model id.
func (j *Judge) Model() string { return j.model }

// Judge assesses one review against the change it reviewed.
func (j *Judge) Judge(ctx context.Context, f Fixture, persona config.Persona, findings []review.Finding) (*JudgeResult, error) {
	schema, err := judgeSchema()
	if err != nil {
		return nil, err
	}

	msgs := []llms.Message{
		{Role: llms.RoleSystem, Content: judgeSystemPrompt()},
		{Role: llms.RoleUser, Content: judgeRequest(f, persona, findings)},
	}

	result, err := llm.Extract[JudgeResult](ctx, j.client, msgs,
		llms.WithJSONSchema("review_assessment", schema, true))
	if err != nil {
		return nil, err
	}

	return &result, nil
}

// judgeSystemPrompt casts the judge as the reviewer whose opinion the tool is
// trying to match.
func judgeSystemPrompt() string {
	return `You are a staff engineer with fifteen years of experience, assessing the quality of
an automated code review. You are the person whose job the bot is trying to do.

You will be shown a code change, the voice the review was configured to use, and
the findings the bot produced. Judge them the way you would judge a junior
reviewer on your team whose comments land on your colleagues' pull requests.

For each finding decide two SEPARATE things, and do not conflate them:

1. real — is the claim technically correct about this code?
2. worth_raising — would YOU leave this comment on a colleague's pull request?

The second is the harder and more important question. A finding can be perfectly
true and still not worth raising: it restates the obvious, it is a style opinion
dressed as a defect, it warns about a condition that cannot occur here, or it
costs the reader more attention than the risk justifies. Be strict. The failure
mode of review bots is not being wrong — it is being exhausting.

Also judge:

- severity_verdict: is the assigned severity right for the demonstrable
  consequence? Reviewers inflate to be heard; call that out.
- class_correct / expected_class: each finding carries a "class" from a closed
  set (correctness, concurrency, security, resource, data-loss, contract, tests,
  maintainability, style). This is not cosmetic — a repository publishes only
  certain classes, so a mislabelled finding is silently discarded. Say whether
  the class fits the problem, and what you would have assigned. A security bug
  labelled "style" is the failure that matters most here.
- tone_verdict: does the wording match the CONFIGURED voice shown to you? Judge
  against the requested voice, not your personal preference. A blunt review is
  not "too_harsh" when bluntness was requested; it is too_harsh only if it is
  contemptuous or personal.

Then judge the review as a whole:

- missed: defects YOU would have raised that the bot did not — but ONLY ones
  that fall inside the configured scope shown to you. If the configuration says
  naming and missing tests are out of scope, then not reporting them is correct
  behavior and must NOT be listed as missed. Judging a reviewer for obeying its
  own configuration would make the scope setting unmeasurable. Be concrete and
  specific. An empty list is a fine answer.
- signal_to_noise (0-10): would a busy engineer feel this review respected their
  time? 10 means every comment earned its place. 0 means they would mute the bot.
- tone_adherence (0-10): how closely the wording followed the configured voice.
- grade: the review overall. Reserve A+ for a review you would be glad to
  receive: every finding real and worth raising, severities accurate, tone
  correct, and nothing important missed. Most real reviews are B or C. Do not
  inflate — an A+ that is not earned makes this whole exercise worthless.

Judge only what is in front of you. Do not invent problems to seem rigorous.`
}

// judgeRequest renders the change, the configured voice, and the findings.
func judgeRequest(f Fixture, persona config.Persona, findings []review.Finding) string {
	var b strings.Builder

	// Sorted, not map order. `range` over a map permutes on every call, so two
	// judgements of the SAME review were two different prompts whenever the
	// fixture touched more than one file, and the resulting variance was
	// indistinguishable from the judge changing its mind. Judging identical
	// recorded findings twice is how this harness now separates judge
	// disagreement from judge noise, and that separation is only meaningful if
	// the prompt is a function of its inputs. Nothing about file order carries
	// meaning here, which is exactly why it must not vary.
	b.WriteString("# The change under review\n\n")
	for _, path := range slices.Sorted(maps.Keys(f.Head)) {
		fmt.Fprintf(&b, "## %s (after the change)\n\n```\n%s```\n\n", path, numbered(f.Head[path]))
	}

	b.WriteString("### The same files before the change\n\n")
	for _, path := range slices.Sorted(maps.Keys(f.Base)) {
		fmt.Fprintf(&b, "#### %s\n\n```\n%s```\n\n", path, f.Base[path])
	}

	b.WriteString("# The voice the review was configured to use\n\n")
	b.WriteString(prompt.Persona(persona))
	b.WriteString("\n")

	b.WriteString("# The findings the bot produced\n\n")
	if len(findings) == 0 {
		b.WriteString("(none — the bot reported no findings at all)\n\n")
	}
	for i, finding := range findings {
		fmt.Fprintf(&b, "## Finding %d\n", i)
		fmt.Fprintf(&b, "- location: %s:%d\n", finding.Path, finding.Line)
		fmt.Fprintf(&b, "- severity: %s\n", finding.Severity)
		fmt.Fprintf(&b, "- class: %s\n", finding.Class)
		fmt.Fprintf(&b, "- category: %s\n", finding.Category)
		fmt.Fprintf(&b, "- title: %s\n", finding.Title)
		fmt.Fprintf(&b, "- rationale: %s\n", finding.Rationale)
		if finding.Suggestion != "" {
			fmt.Fprintf(&b, "- suggestion: %s\n", finding.Suggestion)
		}
		b.WriteString("\n")
	}

	b.WriteString("Assess these findings. Use the finding numbers above as `index`.\n")

	return b.String()
}

// numbered prefixes each line with its number, matching what the reviewer saw.
func numbered(content string) string {
	var b strings.Builder

	lines := strings.Split(content, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	for i, line := range lines {
		fmt.Fprintf(&b, "%6d  %s\n", i+1, line)
	}
	return b.String()
}

// Stimulus is the finding list a judge was SHOWN, as an identity.
//
// It exists because a cross-judge delta is only a confidence interval when both
// judges answered the SAME question, and this package published one that did
// not. The nitpick axis judged the whole corpus once and handed the second
// judge each level's FILTERED list, so GRADE, SIGNAL, TONE and MISSED compared
// a whole-corpus judgement against a subset judgement and printed the
// difference under a legend calling it the confidence interval on the figure
// beside it. MISSED was biased in a known direction on top of that: filtering
// more findings legitimately raises the second judge's missed count against a
// primary frozen at the whole-corpus value.
//
// The identity is DERIVED FROM THE FINDINGS, not declared by the caller. A
// boolean saying "these matched" is the kind of convention this package has
// watched fail twice; a fingerprint cannot be wrong about what it covers, and
// it self-corrects — two nitpick levels that filter to the same list produce
// the same fingerprint and are corroborable, which is true of them rather than
// assumed.
//
// The whole finding is hashed rather than review.Finding.Key(), which is only
// path, line and title. A judge that is shown the same three findings with
// different rationales is being shown a different prompt. Over-covering can
// only refuse a delta that was legitimate; under-covering would publish one
// that was not, and only one of those two errors is survivable.
type Stimulus struct {
	// n is how many findings were shown, and is the number of verdicts a
	// judge owes back.
	n int

	// print is the fingerprint. EMPTY MEANS UNRECORDED, and an unrecorded
	// stimulus matches NOTHING — not even another unrecorded one. That is the
	// safe direction: a judgement folded in without stating what produced it
	// costs a delta, where the alternative would let two unstated stimuli
	// compare equal and publish exactly the fake confidence interval this type
	// was built to stop.
	print string
}

// JudgedOver fingerprints the findings one judge was shown for one fixture.
func JudgedOver(fixture string, findings []review.Finding) Stimulus {
	encoded, err := json.Marshal(findings)
	if err != nil {
		// Unrecorded, so it matches nothing and the delta is withheld. A
		// fingerprint this function could not compute is not a fingerprint that
		// happens to equal another.
		return Stimulus{n: len(findings)}
	}

	sum := sha256.Sum256(append([]byte(fixture+"\x00"), encoded...))
	return Stimulus{n: len(findings), print: hex.EncodeToString(sum[:])}
}

// Len is how many findings the judge was shown.
func (s Stimulus) Len() int { return s.n }

// Recorded reports whether this stimulus has a fingerprint at all.
func (s Stimulus) Recorded() bool { return s.print != "" }

// stimulusTrace is every stimulus behind one Aggregate, one per judgement
// folded in.
//
// A multiset and not a single value: an aggregate spans a corpus, and two
// judges have seen the same stimulus only when they have seen the same
// fixtures with the same findings in each. Comparing a single rolled-up hash
// would work as well, but the multiset also makes the LENGTHS visible, which is
// how a second judge that failed on two fixtures is caught — its aggregate
// covers six samples where the primary's covers eight, and the difference
// between two rates over different sample sets is not a disagreement.
type stimulusTrace struct {
	prints []string
}

func (t *stimulusTrace) record(s Stimulus) { t.prints = append(t.prints, s.print) }

// matches reports whether two traces are the same stimulus.
//
// It is deliberately conservative in three ways, each of which was a way to
// publish a delta nobody measured: an EMPTY trace matches nothing, because an
// aggregate assembled without stating its stimulus has not shown that it shares
// one; traces of DIFFERENT LENGTHS match nothing, because the two judges did not
// see the same number of samples; and an UNRECORDED entry on either side poisons
// the whole comparison rather than being skipped.
//
// Sorted before comparing because the two sides are assembled in different
// orders — the primary from a goroutine pool, the second from Rejudge's ordered
// pass — and order of assembly is not a difference in stimulus.
func (t stimulusTrace) matches(o stimulusTrace) bool {
	if len(t.prints) == 0 || len(t.prints) != len(o.prints) {
		return false
	}

	a := slices.Sorted(slices.Values(t.prints))
	b := slices.Sorted(slices.Values(o.prints))
	for i := range a {
		if a[i] == "" || a[i] != b[i] {
			return false
		}
	}
	return true
}

// Aggregate folds judge results into the numbers used to compare variants.
type Aggregate struct {
	Findings     int
	Real         int
	WorthRaising int
	Inflated     int
	Understated  int
	ToneOff      int
	Misclassed   int
	Missed       int

	// SevAccurate, SevInflated and SevUnderstated answer the same question
	// Inflated and Understated answer, but against each fixture's planted
	// WantSeverity instead of the judge's opinion.
	//
	// Both are carried, and the report prints both, because they disagree: the
	// judge scored Incumbent as understating nothing on a corpus whose ground
	// truth says it understated defects it had located. Replacing the judge's
	// columns with these would hide that disagreement, and the disagreement is
	// itself the result — one of the two instruments is wrong about this corpus
	// and a reader has to be able to see which.
	//
	// They are counted per LOCATED DEFECT while Inflated and Understated are
	// counted per judged FINDING, so the two groups do not share a denominator
	// and neither is a share of the other. The report divides both by the sample
	// count for that reason.
	SevAccurate    int
	SevInflated    int
	SevUnderstated int

	// SevPlanted is how many defects the fixtures behind those counts planted —
	// the denominator printed as O-COV.
	//
	// It is carried because the triple above is graded only over what the
	// reviewer LOCATED, so without it the columns are maximised by selective
	// silence: a reviewer that reports only the defects it is already certain of
	// scores a perfect triple over the handful it chose. See the O-COV component
	// of the objective severity metric in PublishedMetrics.
	SevPlanted int

	// SevUsage is which severity words this contender used against which planted
	// levels. It is a DESCRIPTION printed beside the table, not a column, and it
	// is what a reader comparing two vocabularies gets instead of a cross-tool
	// accuracy figure.
	//
	// A banded cross-tool accuracy triple used to be carried here and printed as
	// B-INFL/B-UNDER/B-ACC. It is withdrawn: it was maximised by a reviewer that
	// picked which defects to mention and stamped one blocking word on those,
	// and it could not see the parser bug it was written in response to. See
	// NoCrossToolSeverityScore.
	SevUsage SeverityUsage

	// SevPlantedLevels is SevPlanted split by level: the denominator each row of
	// the vocabulary block is read against, including the levels this contender
	// located nothing at.
	//
	// Without it the block omitted the levels nobody reached, which made the most
	// selective reviewer's page a proper substring of a calibrated one. See
	// SeverityUsage.Lines.
	SevPlantedLevels PlantedLevels

	// Scale is the severity vocabulary this contender publishes on, DECLARED by
	// whatever adapter produced its findings. Undeclared is the zero value and
	// withholds every O-* cell; see SeverityScale.
	//
	// Set it through DeclareScale rather than by assignment. A row is fed one
	// result at a time and a literal takes whichever declaration arrived first,
	// which is precisely what commonScale refuses on the un-judged path.
	Scale SeverityScale

	// scaleDeclared distinguishes "nobody has declared anything yet" from
	// "somebody declared undeclared", which a SeverityScale cannot: both are the
	// empty string. Without it the first declaration on a fresh row would be
	// read as a disagreement with the zero value and withdraw every row.
	scaleDeclared bool

	SignalToNoise []int
	ToneAdherence []int
	Grades        []string

	// Fixtures names the distinct fixtures this contender was judged on.
	//
	// Comparability is about COVERAGE, not sample count. Judging one contender
	// once per fixture and another three times over the same fixtures leaves
	// both with identical coverage and very different len(Grades), and a guard
	// that reads the length calls that incomparable -- which it is not, and
	// which fires as a false alarm the moment RUNS is raised on one side.
	Fixtures map[string]bool

	// shown is the finding list behind every judgement folded in here.
	//
	// UNEXPORTED, so no caller can set it to whatever would make its delta
	// print. It is written only by sawStimulus, which Add calls, and read only
	// by CrossJudged to decide whether a delta between two aggregates is a
	// disagreement or a change of question.
	shown stimulusTrace
}

// sawStimulus records the finding list one folded-in judgement was produced
// from.
//
// It is called by Add, and directly by RejudgeReport, which folds the two sides
// with countVerdicts rather than Add because it must validate one list and count
// another. Both call sites record the SAME value on both sides in one place, so
// there is no arrangement of them in which the two judges are attributed
// different stimuli by accident.
func (a *Aggregate) sawStimulus(s Stimulus) { a.shown.record(s) }

// SameStimulusAs reports whether another aggregate was produced from the same
// finding lists as this one.
func (a Aggregate) SameStimulusAs(o Aggregate) bool { return a.shown.matches(o.shown) }

// Saw records that this contender was judged on a fixture.
func (a *Aggregate) Saw(fixture string) {
	if a.Fixtures == nil {
		a.Fixtures = map[string]bool{}
	}
	a.Fixtures[fixture] = true
}

// Coverage is how many distinct fixtures this contender was judged on.
func (a Aggregate) Coverage() int { return len(a.Fixtures) }

// Add folds one judgement in, over the finding list the judge was SHOWN.
//
// shown is both halves of what a caller used to pass as a bare count. Its
// length is how many findings were submitted, so a judge that returns a
// different number of verdicts, or repeats an index, cannot compute precision
// over an arbitrary subset without a signal. Its fingerprint is what lets
// CrossJudged tell a disagreement between two judges from a difference in what
// they were asked.
//
// The stimulus is a REQUIRED argument rather than an optional one recorded by a
// second call, because the cheaper spelling is the one that gets used: a method
// that folded a judgement in without stating its stimulus would leave every
// future axis one forgotten line away from a delta the legend describes as a
// confidence interval and that is nothing of the kind. Callers that genuinely
// cannot state it pass a stimulus with no fingerprint and get no delta.
func (a *Aggregate) Add(r *JudgeResult, shown Stimulus) []string {
	if r == nil {
		return []string{"judge returned no result"}
	}

	a.sawStimulus(shown)
	problems := a.AddVerdicts(r.Verdicts, shown.Len())

	if r.SignalToNoise < 0 || r.SignalToNoise > 10 {
		problems = append(problems, fmt.Sprintf("signal_to_noise %d is out of range", r.SignalToNoise))
	}
	if r.ToneAdherence < 0 || r.ToneAdherence > 10 {
		problems = append(problems, fmt.Sprintf("tone_adherence %d is out of range", r.ToneAdherence))
	}

	a.Missed += len(r.Missed)
	a.SignalToNoise = append(a.SignalToNoise, r.SignalToNoise)
	a.ToneAdherence = append(a.ToneAdherence, r.ToneAdherence)
	a.Grades = append(a.Grades, r.Grade)

	return problems
}

// AddVerdicts folds the per-finding half of a judgement in, WITHOUT the
// review-level fields, and returns the same suspicions Add reports about the
// verdict list.
//
// It is the pair verdictProblems + countVerdicts, kept together because that is
// what Add wants and separating them at every call site would let the two drift
// apart. Callers that must apply them to DIFFERENT lists — the re-judge report
// validates what the judge returned and counts what the dump could carry — use
// the two directly.
//
// The counting lives here rather than in each caller so that a number derived
// from a dump and the same number in the published table cannot come from two
// implementations. That guarantee covers the CODE and not the data: a dump
// attaches at most one verdict per finding position, so a judge that answered a
// position twice, or answered a position with no finding, arrives here through
// a dump with fewer verdicts than it arrived with live. GroupDump reports that
// shortfall; nothing here can see it, because by then the discarded verdicts
// are gone.
func (a *Aggregate) AddVerdicts(verdicts []Verdict, expected int) []string {
	problems := verdictProblems(verdicts, expected)
	a.countVerdicts(verdicts)
	return problems
}

// verdictProblems reports what is wrong with a verdict list, separately from
// counting it.
//
// The two are split because the re-judge report has to do them to DIFFERENT
// lists: it counts a list reduced to one verdict per finding position, so that
// both judges are counted by one rule, but the complaint belongs to the list
// the judge actually returned. Validating the reduced list instead would report
// nothing at all — the reduction is what removed the duplicate and the
// out-of-range index, so the judge's malformed answer would be silently
// laundered into a well-formed one.
func verdictProblems(verdicts []Verdict, expected int) []string {
	var problems []string

	if len(verdicts) != expected {
		problems = append(problems, fmt.Sprintf(
			"judge returned %d verdicts for %d findings", len(verdicts), expected))
	}

	seen := map[int]bool{}
	for _, v := range verdicts {
		if v.Index < 0 || v.Index >= expected {
			problems = append(problems, fmt.Sprintf("judge verdict index %d is out of range", v.Index))
		}
		if seen[v.Index] {
			problems = append(problems, fmt.Sprintf("judge returned index %d twice", v.Index))
		}
		seen[v.Index] = true
	}

	return problems
}

// countVerdicts folds a verdict list into the totals.
func (a *Aggregate) countVerdicts(verdicts []Verdict) {
	for _, v := range verdicts {
		a.Findings++
		if v.Real {
			a.Real++
		}
		if v.WorthRaising {
			a.WorthRaising++
		}
		switch v.SeverityVerdict {
		case "inflated":
			a.Inflated++
		case "understated":
			a.Understated++
		}
		if v.ToneVerdict != "matches" {
			a.ToneOff++
		}
		if !v.ClassCorrect {
			a.Misclassed++
		}
	}
}

// AddSeverity folds one sample's objective severity comparison in.
//
// It is separate from Add because it needs the fixture and Add does not, but it
// must be called wherever Add is called and nowhere else: the report divides
// every count column by len(Grades), so scoring severity for a sample whose
// judgement failed would put the objective columns over a larger denominator
// than the judge's and make the two unreadable side by side.
// The fixture is taken rather than only the score so that O-COV has a
// denominator: how many defects were AVAILABLE to grade, not just how many the
// reviewer happened to reach. It supplies the vocabulary block's per-level
// denominators from the same pass, so the coverage cell and the description
// cannot be read against two different censuses of the same corpus.
func (a *Aggregate) AddSeverity(f Fixture, s SeverityScore) {
	a.SevAccurate += s.Accurate
	a.SevInflated += s.Inflated
	a.SevUnderstated += s.Understated
	a.SevPlanted += len(f.Defects)

	if a.SevUsage == nil {
		a.SevUsage = SeverityUsage{}
	}
	a.SevUsage.Merge(s.Usage())

	if a.SevPlantedLevels == nil {
		a.SevPlantedLevels = PlantedLevels{}
	}
	a.SevPlantedLevels.Add(f)
}

// DeclareScale folds one result's declared severity vocabulary into this row,
// withdrawing the row when two results disagree.
//
// It is the Aggregate-shaped twin of commonScale, and it exists because the two
// paths that build these rows had drifted apart. commonScale folds a Summary and
// withdraws on disagreement; the judged path built `&Aggregate{Scale:
// result.Scale}` on whichever goroutine reached the map first and never looked
// at another result's declaration again. First-declaration-wins is exactly the
// rule commonScale refuses: a row folding one adapter's runs together with
// another's is on no single scale, and publishing it at our resolution states
// more than anybody declared. One of the two judged call sites had grown its own
// copy of the check and the other had not, which is the shape of a rule that is
// written down twice.
//
// Withdrawal is sticky. Once two declarations have disagreed the row is on no
// scale, and a third result agreeing with one of them does not restore it.
// TestARowWithdrawsWhenItsAdaptersDisagree pins that.
func (a *Aggregate) DeclareScale(s SeverityScale) {
	if !a.scaleDeclared {
		a.Scale, a.scaleDeclared = s, true
		return
	}
	if a.Scale != s {
		a.Scale = UndeclaredSeverityScale
	}
}

// SevGraded is how many located defects the objective severity columns cover.
func (a Aggregate) SevGraded() int {
	return a.SevAccurate + a.SevInflated + a.SevUnderstated
}

// ObjectiveSeverityCells renders this contender's O-INFL/O-UNDER/O-ACC/O-COV,
// per sample for the first three and as a share of planted defects for O-COV.
//
// It returns "n/a" in all four for a row that has not declared our severity
// scale. That is the retraction, applied where the numbers are printed rather
// than only stated beneath them: filling the cells and adding a note saying not
// to compare them is the mitigation the previous retraction had already recorded
// as insufficient, and the row sits in a sorted ranking beside our models.
// SeverityScale carries the reasoning, including why the row declares it rather
// than being recognized by name.
//
// O-COV IS BLANKED HERE AND PRINTED BY ObjectiveSeverityCounts, and the two are
// not asserting opposite rules about one quantity. What O-COV measures — how
// many planted defects the reviewer LOCATED — is a detection fact in nobody's
// severity vocabulary, so it survives the gate as a NUMBER. What it does not
// survive is this position: a CELL in a sorted ranking, on a row whose other
// three severity cells read n/a, where a filled fourth invites reading the row
// as partly scored on severity after all. The same quantity is published as
// prose beneath the table, where it is not rankable and where RECALL already
// states it. TestSeverityCountsAreWithdrawnForAForeignVocabulary pins both
// halves.
func (a Aggregate) ObjectiveSeverityCells(samples int) (infl, under, acc, cov string) {
	if !a.Scale.PublishesOurLevels() {
		return "n/a", "n/a", "n/a", "n/a"
	}

	n := float64(max(samples, 1))
	rate := func(v int) string { return fmt.Sprintf("%.2f", float64(v)/n) }

	// A row with nothing planted behind it has no coverage to report; printing
	// 0.00 would read as "found none of them".
	cov = "n/a"
	if a.SevPlanted > 0 {
		cov = fmt.Sprintf("%.2f", float64(a.SevGraded())/float64(a.SevPlanted))
	}
	return rate(a.SevInflated), rate(a.SevUnderstated), rate(a.SevAccurate), cov
}

// ObjectiveSeverityCounts renders the COUNTS behind O-INFL/O-UNDER/O-ACC/O-COV,
// so a reader can see what resolution those four rates have.
//
// It passes through the same vocabulary gate as the cells, and that is not
// tidiness. The counts ARE the withdrawn thing: a foreign contender's severity
// triple is blanked in the table precisely because our five levels and its three
// are not commensurable, and printing "12 accurate of 14" underneath restores the
// comparison the cells refused, in a form that is easier to quote. Rendering
// these at the table rather than here is what
// TestNoReportFormatsSeverityCountersDirectly exists to catch, and it caught this
// function's first draft.
//
// O-COV's denominator survives the gate HERE while the O-COV CELL is blanked,
// and the difference between the two positions is the whole rule. How many
// planted defects a reviewer LOCATED is a statement about detection, in nobody's
// severity vocabulary, so nothing about the withdrawal argues for suppressing
// the number. What the withdrawal argues against is a filled cell sitting in a
// sorted ranking beside three cells reading n/a, which reads as a partial score.
// This line is prose under the table, it is not ranked, and it restates a
// detection fact RECALL already publishes. Aggregate.ObjectiveSeverityCells says
// the same thing from the other side, so the two stop appearing to disagree
// about one quantity.
func (a Aggregate) ObjectiveSeverityCounts(model string) string {
	located := fmt.Sprintf("%d located of %d planted", a.SevGraded(), a.SevPlanted)

	if !a.Scale.PublishesOurLevels() {
		return fmt.Sprintf("O-* withdrawn for %s (severity scale %q is not ours); %s",
			model, a.Scale.describe(), located)
	}
	return fmt.Sprintf("O-* %d accurate + %d inflated + %d understated over %s",
		a.SevAccurate, a.SevInflated, a.SevUnderstated, located)
}

// VocabularyBlockLimits is the list of things the vocabulary block cannot
// express, printed inside the block.
// TestTheVocabularyBlockStatesWhatItCannotSay checks that it reaches the page.
//
// It is printed rather than left in a doc comment because the block is what a
// reader is handed in place of a withdrawn number, and every limit here is one
// they would otherwise supply for themselves. The rule this repository keeps
// relearning is that a limitation recorded only in the source is a limitation
// nobody outside the source knows about.
//
// A const in non-test code, matching SeverityColumnLegend, so a guard in the
// default build sees it if it is edited out.
const VocabularyBlockLimits = "WHAT THIS BLOCK CANNOT SAY, so a reader does not read it in: " +
	"NOT DIRECTION — it shows which words landed on which plants, not whether the reviewer under- or " +
	"over-claims against our ladder, because ranking a word that has no rank in our ladder is the " +
	"reduction being refused. " +
	"NOT HEDGING — a reviewer answering all five severities renders exactly as one answering only the " +
	"loudest, because the loudest claim about a defect is the one credited (fail_on gates on the worst " +
	"thing said). " +
	"NOT PER-REVIEW STRUCTURE — the counts are pooled over the corpus, so a reviewer that split two " +
	"levels within one review and one that met them in different reviews render alike. " +
	"NOT AN ORDER BETWEEN REVIEWERS — two blocks are compared by eye, nothing here says which is " +
	"better, and no caller may compute one. " +
	"NOT THE DEFECTS NOBODY REPORTED, beyond the (0 of N located) denominators on each line."

// VocabularyRow is one contender's severity vocabulary, for the block printed
// under every table that compares reviewers.
type VocabularyRow struct {
	Name  string
	Usage SeverityUsage

	// Planted is what the corpus behind this row planted at each level — the
	// denominator every line is read against.
	//
	// A row that omits it renders UndeclaredPlantedTotal rather than a bare
	// count, because a count with no denominator is what made the most selective
	// reviewer's page look like a clean version of a calibrated one. See
	// SeverityUsage.Lines.
	Planted PlantedLevels
}

// SeverityVocabularyBlock renders what each contender CALLED the defects it
// located, against the level each was planted at.
//
// This is the description that replaced a withdrawn cross-tool accuracy score,
// and its shape is the point: there is no number in it. A reader comparing our
// five levels against a foreign reviewer's three can see for themselves that one
// answered "critical" to plants of critical AND of error while another split
// them, and can decide what that is worth. The figure that used to make that
// judgement for them was maximised by answering "critical" to everything. See
// NoCrossToolSeverityScore.
//
// The words are the reviewer's own. They used to be OURS: the block printed the
// level crSeverity had translated each foreign word to, captioned as what the
// contender called the defect, so a description offered in place of a score was
// itself a function of the free constant the withdrawal rested on. Where a word
// was translated the reading is now printed beside it and marked as ours; see
// SeverityUsage.
//
// WHAT THIS DESCRIPTION CANNOT SAY, stated here and printed in the block so a
// reader does not read it in:
//
//   - DIRECTION. It shows which words landed on which plants, not whether the
//     reviewer under- or over-claims against our ladder. On the shipped cache
//     the incumbent's "major" is credited on 8 plants, 4 of them blocking, and
//     our reading of it sits below every one of those 4. That is a real
//     one-directional pattern this block leaves the reader to see for
//     themselves, because ranking a word that has no rank in our ladder is the
//     reduction being refused.
//   - HEDGING. A reviewer answering all five severities renders exactly as one
//     answering only the loudest: reportingFinding credits the loudest claim,
//     and measured, "one comment per severity, on every plant" produces the same
//     page as "always critical". That is defensible — fail_on gates on the worst
//     thing said — and it is still a thing this page cannot show.
//   - PER-REVIEW STRUCTURE. The table is pooled over the corpus. Of the 12
//     cached reviews that locate anything, exactly one locates defects at two or
//     more distinct planted levels, and no line here says so.
//   - AN ORDER BETWEEN REVIEWERS. Two blocks are compared by eye. Nothing in
//     this artifact says which is better, and no caller may compute one.
//   - THE DEFECTS NOBODY REPORTED, beyond the (0 of N located) denominators.
//
// The printed version of that list names no reviewer's word, because the
// preamble beneath it names exactly the words THESE rows translated and a fixed
// sentence quoting one would be the hand-maintained claim this block already
// removed once. TestTheVocabularyBlockStatesWhatItCannotSay pins that it is
// printed.
func SeverityVocabularyBlock(rows []VocabularyRow) string {
	var b strings.Builder

	b.WriteString("SEVERITY VOCABULARY — the severity word each contender PRINTED for the defects it " +
		"located, against the level planted, with the number of defects PLANTED at that level beside " +
		"it. A DESCRIPTION, NOT A SCORE: the vocabularies differ in resolution, and every reduction " +
		"that makes them comparable is maximised by a reviewer that picks which defects to mention " +
		"and calls those blocking.\n" +
		"THE WORDS ARE VERBATIM AND THE READINGS ARE OURS: where this project translates a word into " +
		"its own five levels, the level follows it as [we read as X] and is our reading, not the " +
		"reviewer's claim. A word shown as " + UnrecordedWord + " was destroyed before it reached here, " +
		"or was never printed at all, and is reported missing rather than filled in from our reading.\n" +
		VocabularyBlockLimits + "\n" +
		translatedWordsNote(rows))

	for _, r := range rows {
		lines := r.Usage.Lines(r.Planted)
		if len(lines) == 0 {
			// Distinct from a reviewer that rated things badly: it located
			// nothing, so it said nothing about severity and has no vocabulary
			// to describe. RECALL is where that shows up.
			fmt.Fprintf(&b, "%s: no located defect to describe\n", r.Name)
			continue
		}

		fmt.Fprintf(&b, "%s\n%s\n", r.Name, strings.Join(lines, "\n"))
	}

	return b.String()
}

// translatedWordsNote names the words in THESE rows that this project had to
// translate, and what it read each of them as.
//
// It is derived rather than written down. The sentence it replaces was a
// hand-maintained claim about which words a particular reviewer prints —
// "incumbent prints 'critical' and 'major'" — sitting inside the block whose
// entire thesis is that a reviewer's vocabulary must be quoted rather than
// restated from memory. It was the same failure in miniature, and it was already
// drifting: it named one reviewer while the engine had begun translating our own
// models' words too, so the note described the corpus as it was two rounds ago.
//
// A word counts as translated when the reporter's spelling and our recorded
// level differ, CASE ASIDE. Identical spellings are left out: a reviewer that
// writes "error" and is recorded at error was not translated, and listing it
// would bury the words that were.
//
// TWO WAYS THIS NOTE CONTRADICTED THE ROWS IT INTRODUCES, both fixed here.
//
// A DESTROYED WORD IS NOT AN UNTRANSLATED ONE. Said == "" was skipped as though
// it were nothing to report, so a block in which every word had been destroyed
// printed "NO WORD IN THIS BLOCK WAS TRANSLATED ... each line quotes its
// reviewer directly" directly above rows reading "(word not recorded) x1 [we
// read as warning]" — the preamble asserting the exact opposite of every line
// under it, in the one published block whose entire purpose is to keep our
// substitutions distinguishable from a reviewer's own words. It is reachable
// with no cache at all: linters.normalize marks every analyzer finding
// translated and the ruff runner records no word, so a row of ruff findings
// produced it.
//
// CASE IS DECIDED IN ONE PLACE. The note compared with == while SeverityUsage
// .Lines suppresses the "[we read as X]" marker with EqualFold, so a model
// printing "Critical" was announced as a translated word above a row that quoted
// it unmarked. Lines' rule is the right one and its reason is written out there
// — a difference of case is not a difference of vocabulary — so this now asks
// the same question rather than a second, differently-spelled one.
func translatedWordsNote(rows []VocabularyRow) string {
	readings := map[string]map[string]bool{}
	destroyed := 0
	for _, r := range rows {
		for _, said := range r.Usage {
			for w, n := range said {
				if w.Said == "" {
					destroyed += n
					continue
				}
				if strings.EqualFold(w.Said, w.Recorded.String()) {
					continue
				}
				if readings[w.Said] == nil {
					readings[w.Said] = map[string]bool{}
				}
				readings[w.Said][w.Recorded.String()] = true
			}
		}
	}

	lostNote := ""
	if destroyed > 0 {
		lostNote = fmt.Sprintf(" %d GRADED FINDING(S) REACHED THIS BLOCK WITH NO WORD AT ALL, shown "+
			"as %s: something translated those and did not keep the original, so the level beside "+
			"them is ours and the reviewer's own word is unrecoverable.", destroyed, UnrecordedWord)
	}

	if len(readings) == 0 {
		if destroyed > 0 {
			return "NO SURVIVING WORD IN THIS BLOCK WAS TRANSLATED: every word that reached here is " +
				"one this project already uses, so those lines quote their reviewer directly." +
				lostNote + "\n"
		}
		return "NO WORD IN THIS BLOCK WAS TRANSLATED: every contender above printed a word this " +
			"project already uses, so each line quotes its reviewer directly.\n"
	}

	words := make([]string, 0, len(readings))
	for w := range readings {
		words = append(words, w)
	}
	sort.Strings(words)

	parts := make([]string, 0, len(words))
	for _, w := range words {
		levels := make([]string, 0, len(readings[w]))
		for l := range readings[w] {
			levels = append(levels, l)
		}
		sort.Strings(levels)
		parts = append(parts, fmt.Sprintf("%q read as %s", w, strings.Join(levels, "/")))
	}

	return "WORDS THIS PROJECT TRANSLATED, in this block: " + strings.Join(parts, "; ") +
		". Each reading is a choice we are free to change, and a word with no counterpart among our " +
		"five levels is a guess no corpus here can check — which is one of the reasons no cross-tool " +
		"severity score is offered." + lostNote + "\n"
}

// Precision is the share of findings a senior reviewer would actually raise.
//
// This, not recall, is what determines whether a review bot survives contact
// with a team: a bot that finds everything and says twenty things nobody needed
// gets switched off within a week.
//
// With no findings it is UNDEFINED, not perfect. Returning 1 made silence the
// global optimum of the tuning objective: a variant that reported nothing
// sorted to the top of the comparison table AND, because the suite's only
// assertion was guarded on the top row having findings, switched that
// assertion off entirely. Any prompt change that reduced output looked like an
// improvement.
func (a Aggregate) Precision() float64 {
	if a.Findings == 0 {
		return math.NaN()
	}
	return float64(a.WorthRaising) / float64(a.Findings)
}

// HasFindings reports whether precision is defined.
func (a Aggregate) HasFindings() bool { return a.Findings > 0 }

// MeanSignal is the average signal-to-noise score.
func (a Aggregate) MeanSignal() float64 { return mean(a.SignalToNoise) }

// MeanTone is the average tone-adherence score.
func (a Aggregate) MeanTone() float64 { return mean(a.ToneAdherence) }

func mean(xs []int) float64 {
	if len(xs) == 0 {
		return 0
	}
	sum := 0
	for _, x := range xs {
		sum += x
	}
	return float64(sum) / float64(len(xs))
}

// GradePoints converts a letter grade to a number so grades can be averaged.
func GradePoints(grade string) float64 {
	switch strings.TrimSpace(grade) {
	case "A+":
		return 4.3
	case "A":
		return 4.0
	case "A-":
		return 3.7
	case "B+":
		return 3.3
	case "B":
		return 3.0
	case "B-":
		return 2.7
	case "C+":
		return 2.3
	case "C":
		return 2.0
	case "C-":
		return 1.7
	case "D":
		return 1.0
	default:
		return 0
	}
}

// MeanGrade averages the letter grades.
func (a Aggregate) MeanGrade() float64 {
	if len(a.Grades) == 0 {
		return 0
	}
	sum := 0.0
	for _, g := range a.Grades {
		sum += GradePoints(g)
	}
	return sum / float64(len(a.Grades))
}

// GradeSpread is the range of the graded samples, worst to best.
//
// The samples are one per (fixture, run), so this is TOTAL dispersion: fixture
// difficulty and run-to-run variance together, not separated. That is the right
// quantity for the only question the table is asked — is this GRADE gap worth
// anything — because a mean over eight fixtures moves for either reason and the
// reader cannot act on the difference.
//
// It exists because this harness measured its own noise and the noise won: the
// run-to-run spread on a single model reached 0.49 while the whole distance
// from the best-ranked model to the twelfth was 0.28. A table of mean grades
// with no dispersion column invites the one reading it cannot support — that
// the order of the rows means something.
//
// Reported rather than turned into a confidence interval on purpose: eight
// fixtures is too few for the interval to be honest, and a number that looks
// like statistics gets quoted like statistics.
func (a Aggregate) GradeSpread() float64 {
	if len(a.Grades) < 2 {
		return 0
	}

	lo, hi := GradePoints(a.Grades[0]), GradePoints(a.Grades[0])
	for _, g := range a.Grades[1:] {
		p := GradePoints(g)
		lo = min(lo, p)
		hi = max(hi, p)
	}
	return hi - lo
}

// ---------------------------------------------------------------------------
// TWO JUDGES, AND THE DISAGREEMENT BETWEEN THEM.
//
// Everything below exists because a single number from a single judge is what
// this harness has been publishing and it is not defensible. The judge shares a
// vendor with three contenders it scores, and separately, the same cached
// findings scored 3.66, 3.90, 3.95 and 3.98 across four runs at temperature 0 —
// so a published GRADE carries both an untested preference and an unquantified
// instability, and nothing on the row said either.
//
// The fix is not a better single number. It is that a judged figure and the
// disagreement between the two judges who produced it are ONE VALUE, so a
// reader cannot receive half of it. See JudgedFigure.
// ---------------------------------------------------------------------------

// ContenderVendors is the set of vendors the battery ranks, derived from
// DefaultModels.
//
// Derived, never listed. The brief that commissioned the second judge named
// "openai, anthropic, z-ai, moonshotai and qwen" as the contender vendors and
// concluded that google, deepseek and minimax were therefore clean — while
// DefaultModels carries google/gemini-3.5-flash, google/gemini-3.1-pro-preview,
// deepseek/deepseek-v4-pro and minimax/minimax-m2.7. A judge picked from that
// list would have re-created the exact conflict it was chosen to remove, and
// nothing would have said so. A hand-written vendor list is wrong the first time
// somebody edits the battery, which is the only time it matters.
func ContenderVendors() []string {
	seen := map[string]bool{}
	for _, m := range DefaultModels() {
		seen[contenderVendor(m.ID)] = true
	}

	out := make([]string, 0, len(seen))
	for v := range seen {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// VendorConflicts returns the contenders a judge would be scoring its own
// vendor's work on.
//
// It answers the question the methodology gate has asked for three rounds, and
// it answers it about whichever judge it is handed rather than about the one
// that was current when someone wrote a comment. An empty result is the only
// state in which a judged column is that judge's opinion of somebody else's
// work.
func VendorConflicts(judge string) []string {
	vendor := contenderVendor(judge)

	var out []string
	for _, m := range DefaultModels() {
		if contenderVendor(m.ID) == vendor {
			out = append(out, m.ID)
		}
	}
	sort.Strings(out)
	return out
}

// JudgedFigure is a number an LLM judge produced, BOUND TO the disagreement
// between the judges who produced it.
//
// It is one value and not two columns, and that is the entire design. This
// package has twice shipped a metric published without the thing that gives it
// meaning — a severity triple with no coverage denominator, and a cross-tool
// band with no vocabulary caveat — and both times the missing half existed,
// correct, in a neighbouring function that the table did not call. A convention
// that says "always print the delta beside it" is exactly the convention that
// failed twice. So the delta is not beside the figure; it is INSIDE it, the
// fields are unexported, and the type implements fmt.Formatter so that EVERY
// verb — %v, %s, %f, %.2f — renders the pair. There is no formatting route to
// the bare number, from this package or any other.
//
// The four states it can be in are deliberately four, not two:
//
//	0.74-0.06   two judges, ONE QUESTION. 0.74 is the primary judge's figure;
//	            the second judge's is 0.68. |delta| is the reader's confidence
//	            interval.
//	0.74+?      ONE judge. The disagreement is UNMEASURED, which is not the
//	            same claim as +0.00 and must not be able to render as it.
//	0.74+NC     two judges, TWO DIFFERENT QUESTIONS. Both scored something; the
//	            difference between them is not a disagreement, so there is no
//	            delta to publish and the second judge's figure is not carried
//	            out of this type at all. See Stimulus.
//	n/a         undefined — no findings, no graded sample. There is no figure
//	            to disagree about, and printing 0.00 here is the bug
//	            Aggregate.Precision's doc comment already records shipping.
//
// The +NC state is the one this type was missing, and its absence is what let
// the nitpick axis publish a change of stimulus wearing the costume of a
// disagreement. It renders as neither zero nor blank for the reason the "+?"
// state does not: this package has shipped a zero that read as agreement and a
// blank that read as nothing-was-wrong, and the only rendering that can be
// quoted as neither is one that is not a number.
//
// The delta is SIGNED, so the pair is lossless: the second judge's figure is
// exactly primary+delta. An unsigned spread would hide direction, and direction
// is what says whether a vendor's own judge scores that vendor high.
type JudgedFigure struct {
	// primary is the first judge's value, second the corroborating judge's.
	// Unexported so that no caller outside this package can format either one
	// alone; the in-package equivalent is enforced by
	// TestNoReportFormatsAJudgedFigureDirectly, because a struct field is always
	// reachable from its own package.
	primary float64
	second  float64

	// defined separates "the judge scored this" from "there was nothing to
	// score". corroborated separates "both judges scored it" from "one did".
	// Two booleans rather than one tri-state because they are independent: a
	// figure can be defined and uncorroborated, and a run with no second judge
	// makes every figure that.
	defined      bool
	corroborated bool

	// crossStimulus marks a figure two judges scored FROM DIFFERENT FINDING
	// LISTS. It is mutually exclusive with corroborated by construction —
	// NotComparable is the only thing that sets it, and it never sets the other
	// — because a figure that reported itself as both would be one branch away
	// from rendering a delta again.
	//
	// When it is set, second is left at zero and never read. Storing the other
	// judge's number and merely declining to print it would leave the half-value
	// in the struct for the next accessor to reach; not storing it is the
	// version no future edit can undo.
	crossStimulus bool
}

// SingleJudged builds a figure only one judge scored.
//
// It renders with its disagreement marked UNMEASURED rather than omitted, which
// is the degradation this package wants: a run without a second judge still
// publishes, and still cannot be mistaken for a corroborated one.
func SingleJudged(v float64) JudgedFigure {
	if math.IsNaN(v) {
		return JudgedFigure{}
	}
	return JudgedFigure{primary: v, defined: true}
}

// Corroborated builds a figure two judges scored.
//
// A NaN on either side collapses to the state that is true: an undefined
// primary is an undefined figure, and an undefined second is a figure one judge
// scored. Substituting zero for either would publish a disagreement that was
// never measured, which is the failure this whole type exists to make
// impossible.
func Corroborated(primary, second float64) JudgedFigure {
	if math.IsNaN(primary) {
		return JudgedFigure{}
	}
	if math.IsNaN(second) {
		return SingleJudged(primary)
	}
	return JudgedFigure{primary: primary, second: second, defined: true, corroborated: true}
}

// NotComparable builds a figure BOTH judges scored, from different stimuli.
//
// It takes only the primary's value, and that is the point rather than an
// omission. The second judge's number is a correct measurement of a different
// question, and the one thing it must never be is the right-hand side of a
// subtraction; a constructor that accepted it would be a constructor some later
// edit could make render it.
//
// An undefined primary collapses to undefined, matching Corroborated: there is
// no figure here to be uncomparable about.
func NotComparable(primary float64) JudgedFigure {
	if math.IsNaN(primary) {
		return JudgedFigure{}
	}
	return JudgedFigure{primary: primary, defined: true, crossStimulus: true}
}

// String renders the figure and its cross-judge disagreement as one token.
func (f JudgedFigure) String() string {
	switch {
	case !f.defined:
		return "n/a"
	case f.crossStimulus:
		// "+NC" — NOT COMPARABLE. Two judges answered, about different finding
		// lists, so there is no disagreement to size. Spelled without digits
		// for the same reason "+?" is: nothing downstream can average it, and
		// nobody can quote it as a small delta.
		return fmt.Sprintf("%.2f+NC", f.primary)
	case !f.corroborated:
		// "+?" and not "+0.00". A second judge that was never asked agreed
		// about nothing, and the only rendering that cannot be quoted as
		// agreement is one that is not a number.
		return fmt.Sprintf("%.2f+?", f.primary)
	default:
		return fmt.Sprintf("%.2f%+.2f", f.primary, f.second-f.primary)
	}
}

// Format implements fmt.Formatter, and it IGNORES THE VERB ON PURPOSE.
//
// This is the lock. A judged figure reaches a report through fmt, and honouring
// %f or %.2f would hand back the bare primary — the exact half-value the type
// exists to prevent, obtainable by a format string nobody would look twice at.
// Every verb therefore renders the same token. Width and the '-' flag ARE
// honoured, because a table cell has to be padded and refusing that would push
// callers back to formatting the parts by hand.
func (f JudgedFigure) Format(s fmt.State, verb rune) {
	// verb is read and discarded so that the reader of this function sees the
	// discarding happen rather than inferring it from an unused parameter.
	_ = verb

	out := f.String()

	if w, ok := s.Width(); ok && len(out) < w {
		pad := strings.Repeat(" ", w-len(out))
		if s.Flag('-') {
			out += pad
		} else {
			out = pad + out
		}
	}

	_, _ = io.WriteString(s, out)
}

// Defined reports whether there is a figure at all.
//
// It returns a BOOL and not the number. Ranking needs to know that a contender
// with no judged finding sorts last — silence is not a perfect score — and that
// question can be answered without letting the value escape.
func (f JudgedFigure) Defined() bool { return f.defined }

// Corroborated reports whether a second judge scored THE SAME QUESTION.
//
// A cross-stimulus figure answers false. Callers use this to decide whether a
// derived comparison is available at all — the re-judge table's rank MOVE, the
// model ranking's second-judge order — and every one of those comparisons is
// exactly as invalid across two stimuli as it is with one judge.
func (f JudgedFigure) Corroborated() bool { return f.corroborated }

// NotComparable reports whether two judges scored this figure from different
// stimuli.
//
// It is not the negation of Corroborated: a single-judge figure is neither
// corroborated nor uncomparable, and a report that treated the two states as
// one would tell a reader their run had no second judge when it had two.
func (f JudgedFigure) NotComparable() bool { return f.crossStimulus }

// Compare orders two figures by the PRIMARY judge's value, undefined last.
//
// It exists so a report can sort without ever holding the number, and it
// compares primaries because the published ranking IS the primary judge's
// ranking. That is a real limitation and the reports say so: a gap between two
// rows that is smaller than either row's delta is not an ordering, and the
// second judge's own order is printed underneath so a reader can see whether it
// held.
func (f JudgedFigure) Compare(o JudgedFigure) int {
	switch {
	case f.defined && !o.defined:
		return -1
	case !f.defined && o.defined:
		return 1
	case !f.defined && !o.defined:
		return 0
	}
	// Descending: a higher judged figure sorts first, which is what every
	// caller of this wants for GRADE and PREC. A column where lower is better
	// is not ranked on in any published table.
	switch {
	case f.primary > o.primary:
		return -1
	case f.primary < o.primary:
		return 1
	default:
		return 0
	}
}

// SplitCells renders the figure across the three cells of the re-judge
// diagnostic table: the primary judge's value, the second judge's, and the
// signed delta between them.
//
// It is the ONE table whose subject is the two judges themselves, so showing
// both absolute values there is the point rather than a leak — a reader
// comparing PREC-A against the published number needs the number, not a
// difference. Three values from one call, matching
// Aggregate.ObjectiveSeverityCells, because that is what stops a row rendering
// two of them: you cannot ask for the primary here without also being handed
// the disagreement.
//
// An uncorroborated figure yields "n/a" for the second and the delta rather
// than a blank, so the diagnostic table cannot show a one-judge run as a
// zero-difference one.
func (f JudgedFigure) SplitCells() (primary, second, delta string) {
	if !f.defined {
		return "n/a", "n/a", "n/a"
	}
	// A cross-stimulus figure yields the primary and NOTHING ELSE. This is the
	// one table that prints both absolute values, and printing them here would
	// invite exactly the subtraction the figure exists to refuse — two numbers
	// side by side in a table whose subject is the two judges read as a
	// difference whatever the third cell says.
	if f.crossStimulus {
		return fmt.Sprintf("%.2f", f.primary), "NC", "NC"
	}
	if !f.corroborated {
		return fmt.Sprintf("%.2f", f.primary), "n/a", "n/a"
	}
	return fmt.Sprintf("%.2f", f.primary),
		fmt.Sprintf("%.2f", f.second),
		fmt.Sprintf("%+.2f", f.second-f.primary)
}

// CrossJudged is one contender's aggregate under each judge.
//
// Every accessor returns a JudgedFigure, so a report holding one of these has
// no route to a bare judged number. That is why the reports take this and not
// an Aggregate: Aggregate's own accessors return float64 and its counters are
// plain ints, and a table built from those is a table that can print half a
// result. TestNoReportFormatsAJudgedFigureDirectly is what keeps them out.
//
// The accessors are named with a Figure suffix and NOT after the Aggregate
// fields they derive from — GradeFigure, not Grade — so that a source scan for
// the Aggregate spellings cannot be confused by a same-named method on this
// type. The guard has no type information; the naming is what makes it exact.
type CrossJudged struct {
	// Primary is the judge that produced the published figure.
	Primary Aggregate

	// Second is the corroborating judge, zero and unused when HaveSecond is
	// false.
	Second Aggregate

	// HaveSecond distinguishes "the second judge scored nothing" from "there was
	// no second judge". Both leave Second empty and they are different claims:
	// the first is a second judge that failed on every sample, which is a result
	// worth failing a run over, and the second is an operator who did not set
	// NITPICK_EVAL_JUDGE2.
	HaveSecond bool
}

// primarySamples and secondSamples are the denominators each side's rates
// divide by: how many samples that judge actually graded.
//
// They are separate because they CAN differ — a second judge that errored on
// two fixtures graded fewer — and dividing both sides by one number would
// publish a delta between a rate and something that is not one. Where they
// differ, SamplesCell prints both.
func (c CrossJudged) primarySamples() int { return len(c.Primary.Grades) }
func (c CrossJudged) secondSamples() int  { return len(c.Second.Grades) }

// figure pairs a value computed from each judge's aggregate.
//
// Every accessor goes through here so that the "was there a second judge"
// question is answered once. Whether that judge produced anything is left to
// the VALUE: each accessor returns NaN where it has nothing to report, and
// Corroborated collapses a NaN second to an uncorroborated figure. Gating on a
// sample count here instead would have been wrong for the re-judge path, which
// carries verdicts and no grades at all — every figure would have declared
// itself uncorroborated in the one report whose entire subject is two judges.
func (c CrossJudged) figure(of func(Aggregate) float64) JudgedFigure {
	if !c.HaveSecond {
		return SingleJudged(of(c.Primary))
	}
	// THE STIMULUS GATE, and it is here — at the one function every accessor
	// goes through — rather than at each accessor, so that a figure added later
	// inherits it without anyone remembering to. A column that reached
	// Corroborated directly would be a column publishing a confidence interval
	// on a comparison nobody made, which is the defect this whole mechanism
	// closes.
	if !c.SameStimulus() {
		return NotComparable(of(c.Primary))
	}
	return Corroborated(of(c.Primary), of(c.Second))
}

// SameStimulus reports whether the two judges behind this row answered the same
// question.
//
// False has three causes and they are all the same defect: the two judges were
// shown different finding lists, one of them was folded in without recording
// what it was shown, or the second graded fewer samples than the primary. In
// every case the difference between the two figures mixes a disagreement with a
// change of question, and the legend beside them calls that difference a
// confidence interval.
//
// A row with no second judge answers false as well, which no caller can
// misread: HaveSecond is checked first everywhere it matters, and a figure with
// one judge already renders as unmeasured.
func (c CrossJudged) SameStimulus() bool {
	return c.HaveSecond && c.Primary.SameStimulusAs(c.Second)
}

// rateFigure pairs a per-sample rate, each side over its OWN sample count.
func (c CrossJudged) rateFigure(count func(Aggregate) int) JudgedFigure {
	rate := func(a Aggregate) float64 {
		n := len(a.Grades)
		if n == 0 {
			return math.NaN()
		}
		return float64(count(a)) / float64(n)
	}
	return c.figure(rate)
}

// GradeFigure is the judge's mean letter grade, with the disagreement.
func (c CrossJudged) GradeFigure() JudgedFigure {
	return c.figure(func(a Aggregate) float64 {
		if len(a.Grades) == 0 {
			// Undefined, not 0.00. A contender whose every review failed has no
			// grade, and 0.00 reads as "graded, and terrible" — the state the
			// benchmark reaches whenever the incumbent's every invocation errors.
			return math.NaN()
		}
		return a.MeanGrade()
	})
}

// SpreadFigure is the worst-to-best dispersion of the graded samples.
//
// Its delta answers a question this harness has never been able to ask: the
// spread is the judge's dispersion over fixtures and runs, and the delta beside
// it is how much the OTHER judge's dispersion differs. Two judges agreeing that
// a contender is unstable is a different finding from one judge being unstable
// about it.
func (c CrossJudged) SpreadFigure() JudgedFigure {
	return c.figure(func(a Aggregate) float64 {
		if len(a.Grades) < 2 {
			// Not 0.00, which would read as "perfectly stable" when it means
			// "not measured".
			return math.NaN()
		}
		return a.GradeSpread()
	})
}

// PrecisionFigure is the share of findings a senior reviewer would raise.
func (c CrossJudged) PrecisionFigure() JudgedFigure {
	return c.figure(func(a Aggregate) float64 { return a.Precision() })
}

// SignalFigure is the judge's signal-to-noise grade.
func (c CrossJudged) SignalFigure() JudgedFigure {
	return c.figure(func(a Aggregate) float64 {
		if len(a.SignalToNoise) == 0 {
			return math.NaN()
		}
		return a.MeanSignal()
	})
}

// ToneAdherenceFigure is the judge's tone-adherence grade.
func (c CrossJudged) ToneAdherenceFigure() JudgedFigure {
	return c.figure(func(a Aggregate) float64 {
		if len(a.ToneAdherence) == 0 {
			return math.NaN()
		}
		return a.MeanTone()
	})
}

// FindFigure is findings per sample.
//
// It carries a delta even though DescriptiveColumns classes FIND as a count
// rather than a score, because the number is COUNTED FROM VERDICTS: a judge that
// returns fewer verdicts than there were findings lowers it. Leaving the one
// judge-derived column in the table without a delta would be the same omission
// in miniature.
func (c CrossJudged) FindFigure() JudgedFigure {
	return c.rateFigure(func(a Aggregate) int { return a.Findings })
}

// RealFigure is technically-correct findings per sample.
func (c CrossJudged) RealFigure() JudgedFigure {
	return c.rateFigure(func(a Aggregate) int { return a.Real })
}

// WorthFigure is worth-raising findings per sample.
func (c CrossJudged) WorthFigure() JudgedFigure {
	return c.rateFigure(func(a Aggregate) int { return a.WorthRaising })
}

// InflatedFigure is J-INFL: findings the judge called over-severe, per sample.
func (c CrossJudged) InflatedFigure() JudgedFigure {
	return c.rateFigure(func(a Aggregate) int { return a.Inflated })
}

// UnderstatedFigure is J-UNDER, and is printed only ever beside InflatedFigure:
// counting over-claiming while ignoring under-claiming hands a free win to
// whichever reviewer is quieter about severity.
func (c CrossJudged) UnderstatedFigure() JudgedFigure {
	return c.rateFigure(func(a Aggregate) int { return a.Understated })
}

// MisclassedFigure is findings the judge said carried the wrong class.
func (c CrossJudged) MisclassedFigure() JudgedFigure {
	return c.rateFigure(func(a Aggregate) int { return a.Misclassed })
}

// ToneOffFigure is findings whose wording missed the configured voice.
func (c CrossJudged) ToneOffFigure() JudgedFigure {
	return c.rateFigure(func(a Aggregate) int { return a.ToneOff })
}

// MissedFigure is defects the judge would have raised and the review did not.
//
// This is the column the re-judge path has never been able to measure — the
// dump records no missed list — and it is also the column whose instability
// motivated that path: the same cached findings were scored 2 missed on one run
// and 5 on the next. Judging live with two judges is the first thing here that
// puts a number on it.
func (c CrossJudged) MissedFigure() JudgedFigure {
	return c.rateFigure(func(a Aggregate) int { return a.Missed })
}

// SamplesCell renders N: how many samples each judge graded.
//
// One number when they agree, "8/6" when the second judge graded fewer. A
// second judge that failed on two fixtures produces deltas computed from rates
// over different denominators, and that is legitimate — each rate is correct
// over its own sample — but it is not the same measurement, and a reader
// comparing a delta against a spread has to be able to see it.
func (c CrossJudged) SamplesCell() string {
	n := c.primarySamples()
	if !c.HaveSecond || c.secondSamples() == n {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%d/%d", n, c.secondSamples())
}

// VerdictCells renders how many verdicts each judge was COUNTED for.
//
// Two cells from one call, for the same reason SplitCells returns three: the
// two counts differing is itself a result — a judge that answered nine of
// twelve findings has a precision over a different population than one that
// answered all twelve — and a row that printed one of them would be claiming a
// shared denominator it does not have.
func (c CrossJudged) VerdictCells() (primary, second string) {
	if !c.HaveSecond {
		return fmt.Sprintf("%d", c.Primary.Findings), "n/a"
	}
	return fmt.Sprintf("%d", c.Primary.Findings), fmt.Sprintf("%d", c.Second.Findings)
}

// Denominators renders every rate above as the counts it came from, for BOTH
// judges.
//
// It lives here rather than at the table for the same reason the figures do.
// The counts are the resolution behind the rates — PREC 0.74 and PREC 0.67 read
// as a difference until you are told they are 17/23 and 2/3 — and a report that
// formatted them itself would be a report holding the raw judged counters,
// which is the state this type exists to keep it out of.
func (c CrossJudged) Denominators(label string) string {
	one := func(a Aggregate) string {
		return fmt.Sprintf("%d sample(s) over %d fixture(s) | PREC %d/%d worth raising | "+
			"%d inflated and %d understated of %d findings | MISCLASS %d | MISSED %d",
			len(a.Grades), a.Coverage(), a.WorthRaising, a.Findings,
			a.Inflated, a.Understated, a.Findings, a.Misclassed, a.Missed)
	}

	out := fmt.Sprintf("  %-36s primary judge: %s", truncate(label, 36), one(c.Primary))
	if !c.HaveSecond {
		return out + "\n" + fmt.Sprintf("  %-36s second judge:  NONE — every figure on this row is "+
			"one opinion, and its delta is unmeasured", "")
	}

	out += "\n" + fmt.Sprintf("  %-36s second judge:  %s", "", one(c.Second))

	// The reader who has come here to check a delta is told, in the same block,
	// whether there is a delta to check. Printing the two judges' counts and
	// leaving the comparability to be inferred from a "+NC" in a distant cell is
	// how the previous version of this report managed to be individually correct
	// everywhere and wrong as a whole.
	if !c.SameStimulus() {
		out += "\n" + fmt.Sprintf("  %-36s stimulus:      THE TWO JUDGES DID NOT SCORE THE SAME "+
			"FINDING LISTS, so no figure on this row carries a delta. Either they were shown "+
			"different lists, or one graded fewer samples than the other, or a judgement was folded "+
			"in without recording what produced it", "")
	}
	return out
}

// JudgePanel is the judges a report was scored by, and the second judge's
// aggregates keyed by contender.
//
// Reports take a panel rather than two maps so that "was there a second judge"
// is answered in one place and cannot be answered differently by the banner and
// by the cells. A report that decided per column would be one edit away from a
// table whose header claims corroboration and whose rows do not.
type JudgePanel struct {
	// Primary is the judge that produced the published figures. Second is the
	// corroborating judge, empty when there was none.
	Primary string
	Second  string

	// SecondAggregates is the corroborating judge's tally per contender, keyed
	// exactly as the primary map is. A contender missing from it was scored by
	// one judge, and Pair renders it as such rather than as agreement.
	SecondAggregates map[string]*Aggregate
}

// Corroborated reports whether a second judge was asked at all.
func (p JudgePanel) Corroborated() bool { return strings.TrimSpace(p.Second) != "" }

// Pair binds a contender's primary aggregate to the second judge's.
//
// A contender the second judge produced nothing for comes back UNCORROBORATED
// rather than paired against an empty Aggregate. The difference matters: an
// empty Aggregate has zero findings and zero grades, so pairing against it
// would publish a delta of "the other judge scored this at nothing", which is a
// measurement nobody made.
func (p JudgePanel) Pair(contender string, primary Aggregate) CrossJudged {
	if !p.Corroborated() {
		return CrossJudged{Primary: primary}
	}
	second, ok := p.SecondAggregates[contender]
	if !ok || second == nil {
		return CrossJudged{Primary: primary}
	}
	return CrossJudged{Primary: primary, Second: *second, HaveSecond: true}
}

// Unpaired returns the contenders the second judge scored that no row claimed.
//
// A second aggregate nobody looked up is the SILENT form of a keying bug, and
// this path has one available: Corroborate keys by contenderLabel — model and
// variant together — while the persona tables identify their rows by variant
// alone. Ask for "nitpick=off" when the aggregate is filed under
// "z-ai/glm-5.2 [nitpick=off]" and Pair returns an uncorroborated figure, so the
// judging is paid for, the answers exist, and every cell still prints "+?". The
// run looks exactly like one where no second judge was configured.
//
// Nothing in the type system can catch that, because both sides are strings. A
// report that states what it failed to claim can.
func (p JudgePanel) Unpaired(claimed []string) []string {
	seen := make(map[string]bool, len(claimed))
	for _, c := range claimed {
		seen[c] = true
	}

	var out []string
	for key := range p.SecondAggregates {
		if !seen[key] {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

// Banner states, above the table, how many judges are behind every figure in it
// and what each one's vendor conflict is.
//
// The single-judge case is the one this has to get right. It is the state every
// run before this one was in, it is the state a run falls back to when
// NITPICK_EVAL_JUDGE2 is unset, and a table that simply omitted the deltas
// there would be indistinguishable from the tables this work was commissioned
// to replace. So it prints, loudly, and names the command that fixes it.
func (p JudgePanel) Banner() string {
	conflict := func(judge string) string {
		c := VendorConflicts(judge)
		if len(c) == 0 {
			return fmt.Sprintf("%s — shares a vendor with NO contender", judge)
		}
		return fmt.Sprintf("%s — SHARES A VENDOR WITH %d CONTENDER(S) IT SCORES: %s",
			judge, len(c), strings.Join(c, ", "))
	}

	if !p.Corroborated() {
		return "SINGLE JUDGE, UNCORROBORATED. Every judged figure below is one model's opinion and " +
			"renders as `X+?`,\nwhere `?` is a cross-judge disagreement THAT WAS NOT MEASURED — not one " +
			"that was measured at zero.\n  primary judge: " + conflict(p.Primary) + "\n  second judge:  " +
			"none. Set " + EnvSecondJudge + "=default to score the same findings again with " +
			SecondJudgeModel + ",\n                 which re-judges cached findings and runs no review."
	}

	return "TWO JUDGES. Every judged figure below is printed with the disagreement between them, as one " +
		"value —\nor as `+NC` where the two judges did not score the same finding list, in which case " +
		"there is no\ndisagreement to compute and none is published.\n  primary judge: " +
		conflict(p.Primary) + "\n  second judge:  " + conflict(p.Second)
}

// judgedCellWidth is how wide a table cell must be to hold a corroborated
// figure.
//
// The widest rendering is a two-decimal value with a two-decimal signed delta.
// Eleven characters covers a rate that has gone into double digits against a
// delta of the same size — "15.00-15.00", which a FIND column reaches the first
// time a contender files fifteen findings a sample — and twelve leaves the
// column from touching its neighbour. It is a constant rather than a measured
// maximum because the header has to be built before any figure exists.
const judgedCellWidth = 12

// CorroboratedColumns are the columns whose value comes from a judge and must
// therefore be printed as a JudgedFigure.
//
// It is JudgeOpinionColumns — the package's own register of what an LLM judge
// supplies — plus the two counts that are TALLIED FROM VERDICTS. FIND and
// FINDINGS are classed descriptive because no reviewer is better for a larger
// one, and that classification is right about what they mean and wrong about
// where they come from: Aggregate.countVerdicts increments Findings once per
// verdict, so a judge that answers half the list halves the column.
//
// Derived from the register rather than listed, so a judged column added to
// JudgeOpinionColumns is one that must carry a delta from the day it is added.
func CorroboratedColumns() []string {
	out := append([]string(nil), JudgeOpinionColumns()...)
	out = append(out, "FIND", "FINDINGS")
	sort.Strings(out)
	return out
}

// tableColumn is one column of a header: its name and the field width the row
// under it must pad to.
type tableColumn struct {
	name  string
	width int
}

// tableColumns reads a header's columns and their widths.
//
// Columns are separated by runs of TWO OR MORE spaces, matching the rule the
// package's own header guards use, because a single space occurs inside a column
// name — "SEV A/I/U" is one column of SummaryTableHeader — and splitting on it
// shatters the header rather than reading it.
//
// The width of every column but the last is the distance to the next column's
// start, minus the one space that separates them; the last column is unbounded
// and reports the width of its own name. Reading the widths OUT of the header,
// rather than keeping a format string in step with it by hand, is what makes a
// row that cannot drift out of line with the header above it — a defect the
// judged tables have shipped once already.
func tableColumns(header string) []tableColumn {
	var (
		names  []string
		starts []int
	)

	for i := 0; i < len(header); {
		if header[i] == ' ' {
			i++
			continue
		}

		start := i
		for i < len(header) {
			// A single interior space continues the column name; two end it.
			if header[i] == ' ' && (i+1 >= len(header) || header[i+1] == ' ') {
				break
			}
			i++
		}
		names = append(names, strings.TrimRight(header[start:i], " "))
		starts = append(starts, start)
	}

	cols := make([]tableColumn, len(names))
	for i, name := range names {
		width := len(name)
		if i+1 < len(starts) {
			width = starts[i+1] - starts[i] - 1
		}
		cols[i] = tableColumn{name: name, width: width}
	}
	return cols
}

// WidenJudgedColumns rebuilds a header with every judge-supplied column wide
// enough to hold a figure AND its cross-judge delta.
//
// The published headers were sized for a bare number, and a corroborated figure
// does not fit: "3.66+0.24" in a six-character GRADE cell pushes every column
// after it out of line, which is the failure mode this package has already
// shipped once and describes as silent. Widening is done by DERIVING a new
// header from the declared one, so the two carry identical columns in identical
// order by construction — a hand-written second header is the maintained list
// that this package's own registry comment calls the way a bad column ships.
func WidenJudgedColumns(header string) string {
	judged := map[string]bool{}
	for _, c := range CorroboratedColumns() {
		judged[c] = true
	}

	cols := tableColumns(header)

	var b strings.Builder
	for i, c := range cols {
		width := c.width
		if judged[c.name] {
			width = max(width, judgedCellWidth)
		}

		if i == len(cols)-1 {
			b.WriteString(c.name)
			break
		}
		fmt.Fprintf(&b, "%-*s ", width, c.name)
	}
	return b.String()
}

// TableRow renders one row under a header, padding each cell to that header's
// own column width.
//
// The row and the header therefore cannot disagree about widths, because there
// is only one description of them. Both ways the pairing can still break are
// reported ON THE ROW rather than fixed silently: a row with the wrong number of
// cells, and a cell too wide for its column. Either one misaligns the table, and
// a misaligned table is read as data.
func TableRow(header string, cells []string) string {
	cols := tableColumns(header)

	var (
		b        strings.Builder
		overflow []string
	)

	for i, c := range cols {
		cell := ""
		if i < len(cells) {
			cell = cells[i]
		}
		if len(cell) > c.width && i != len(cols)-1 {
			overflow = append(overflow, c.name)
		}

		if i == len(cols)-1 {
			b.WriteString(cell)
			break
		}
		fmt.Fprintf(&b, "%-*s ", c.width, cell)
	}

	if len(cells) != len(cols) {
		fmt.Fprintf(&b, "   !! ROW HAS %d CELL(S) FOR A %d-COLUMN HEADER", len(cells), len(cols))
	}
	if len(overflow) > 0 {
		fmt.Fprintf(&b, "   !! CELL OVERFLOWS COLUMN: %s", strings.Join(overflow, ", "))
	}

	b.WriteString("\n")
	return b.String()
}

// CrossJudgedModelTableHeader is JudgedModelTableHeader with room for the
// disagreement, and is the header the judged model ranking is printed under.
//
// It is registered as a SCORED table, like the header it derives from, so every
// column guard in this package runs over it too: same column names, same order,
// wider cells. Deriving it is what guarantees that — a second hand-written
// header is how a column gets added to one table and not the other.
var CrossJudgedModelTableHeader = registerTableHeader(tableScored,
	WidenJudgedColumns(JudgedModelTableHeader))

// CrossJudgedVariantTableHeader is VariantTableHeader with the same treatment.
// The persona axis is judged by the same judge on the same terms, so its figures
// carry the same disagreement or the same admission that none was measured.
var CrossJudgedVariantTableHeader = registerTableHeader(tableScored,
	WidenJudgedColumns(VariantTableHeader))

// JudgedModelRow renders one contender's row of the judged model ranking.
//
// IT LIVES IN NON-TEST CODE ON PURPOSE, for the reason score.go gives for
// keeping the headers here: the reports are rendered from files behind the
// `eval` build tag, and a guard that only compiles under that tag cannot run in
// `go test ./...`. Putting the row here means the default build can render one
// and assert that every judged cell carries its disagreement — which is the
// claim, and which was previously unassertable without spending money.
//
// It takes a CrossJudged and not an Aggregate. That is the structural half: this
// function has no bare judged number available to print, because CrossJudged
// hands out nothing but JudgedFigures and JudgedFigure formats to a pair under
// every verb.
//
// The objective severity cells are computed here rather than passed in, so the
// gated renderer is always the one that produces them — a table that formatted
// SevAccurate itself is the defect TestNoReportFormatsSeverityCountersDirectly
// exists for, found the hard way.
func JudgedModelRow(model string, c CrossJudged, failed int) string {
	oInfl, oUnder, oAcc, oCov := c.Primary.ObjectiveSeverityCells(len(c.Primary.Grades))

	return TableRow(CrossJudgedModelTableHeader, []string{
		truncate(model, 36),
		c.GradeFigure().String(),
		c.SpreadFigure().String(),
		c.PrecisionFigure().String(),
		fmt.Sprintf("%d", c.Primary.Coverage()),
		c.SamplesCell(),
		fmt.Sprintf("%d", failed),
		c.FindFigure().String(),
		c.WorthFigure().String(),
		c.InflatedFigure().String(),
		c.UnderstatedFigure().String(),
		oInfl, oUnder, oAcc, oCov,
		c.MisclassedFigure().String(),
		c.MissedFigure().String(),
		c.SignalFigure().String(),
	})
}

// JudgedVariantRow renders one variant's row of the persona comparison, on the
// same terms and for the same reasons as JudgedModelRow.
func JudgedVariantRow(variant string, c CrossJudged, failures int) string {
	oInfl, oUnder, oAcc, oCov := c.Primary.ObjectiveSeverityCells(len(c.Primary.Grades))

	return TableRow(CrossJudgedVariantTableHeader, []string{
		truncate(variant, 23),
		c.SamplesCell(),
		fmt.Sprintf("%d", failures),
		c.FindFigure().String(),
		c.RealFigure().String(),
		c.WorthFigure().String(),
		c.PrecisionFigure().String(),
		c.InflatedFigure().String(),
		c.UnderstatedFigure().String(),
		oInfl, oUnder, oAcc, oCov,
		c.MisclassedFigure().String(),
		c.ToneOffFigure().String(),
		c.MissedFigure().String(),
		c.SignalFigure().String(),
		c.GradeFigure().String(),
	})
}

// CrossJudgeLegend is printed under every table carrying a JudgedFigure.
//
// A const in non-test code, matching SeverityColumnLegend, so that the
// admission cannot be edited out of a report without a guard in the default
// build seeing it go.
const CrossJudgeLegend = "EVERY JUDGED FIGURE IS PRINTED WITH ITS CROSS-JUDGE DISAGREEMENT, AS ONE VALUE. " +
	"`0.74-0.06` means BOTH JUDGES SCORED THE SAME FINDING LIST: the primary scored 0.74 and the second " +
	"0.68; the delta is SIGNED, so the second judge's figure is exactly the two added. ONLY IN THAT " +
	"FORM IS THE SIZE OF THE DELTA THE CONFIDENCE INTERVAL ON THE FIGURE BESIDE IT — there, a gap " +
	"between two rows smaller than either row's delta is not a ranking. `0.74+?` means ONE judge scored " +
	"it and the disagreement is UNMEASURED — which is not the same claim as +0.00, and is why it is not " +
	"printed as a number. `0.74+NC` means TWO judges scored it FROM DIFFERENT FINDING LISTS and the " +
	"comparison was NOT COMPARABLE, SO IT WAS NOT MADE: the difference between them would be a change " +
	"of question wearing the costume of a disagreement, there is no confidence interval on that figure, " +
	"and the second judge's number is not published at all. `n/a` means undefined: no finding, no graded " +
	"sample, nothing to disagree about. THE O-* COLUMNS CARRY NO DELTA AND NEED NONE: they compare each " +
	"located defect to the WantSeverity its fixture declares, with no model involved."
