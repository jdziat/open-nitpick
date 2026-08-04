package evals

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math"
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
const DefaultJudgeModel = "openai/gpt-5.6-terra"

// EnvJudgeModel overrides the judge.
const EnvJudgeModel = "NITPICK_EVAL_JUDGE"

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
	// stamped one blocking word on every finding, and it could not see the parser
	// bug it was written in response to. See NoCrossToolSeverityScore.
	SevUsage SeverityUsage

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
}

// Saw records that this contender was judged on a fixture.
func (a *Aggregate) Saw(fixture string) {
	if a.Fixtures == nil {
		a.Fixtures = map[string]bool{}
	}
	a.Fixtures[fixture] = true
}

// Coverage is how many distinct fixtures this contender was judged on.
func (a Aggregate) Coverage() int { return len(a.Fixtures) }

// Add folds one judgement in.
//
// expected is how many findings were submitted. A judge that returns a
// different number of verdicts, or repeats an index, would otherwise compute
// precision over an arbitrary subset with no signal that it happened.
func (a *Aggregate) Add(r *JudgeResult, expected int) []string {
	if r == nil {
		return []string{"judge returned no result"}
	}

	problems := a.AddVerdicts(r.Verdicts, expected)

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
// reviewer happened to reach.
func (a *Aggregate) AddSeverity(f Fixture, s SeverityScore) {
	a.SevAccurate += s.Accurate
	a.SevInflated += s.Inflated
	a.SevUnderstated += s.Understated
	a.SevPlanted += len(f.Defects)

	if a.SevUsage == nil {
		a.SevUsage = SeverityUsage{}
	}
	a.SevUsage.Merge(s.Usage())
}

// SevGraded is how many located defects the objective severity columns cover.
func (a Aggregate) SevGraded() int {
	return a.SevAccurate + a.SevInflated + a.SevUnderstated
}

// ObjectiveSeverityCells renders this contender's O-INFL/O-UNDER/O-ACC/O-COV,
// per sample for the first three and as a share of planted defects for O-COV.
//
// It returns "n/a" in all four for a contender whose severity vocabulary is not
// ours. That is the retraction, applied where the numbers are printed rather
// than only stated beneath them: filling the cells and adding a note saying not
// to compare them is the mitigation the previous retraction had already recorded
// as insufficient, and the row sits in a sorted ranking beside our models.
// PublishesOurSeverityLevels carries the reasoning.
func (a Aggregate) ObjectiveSeverityCells(model string, samples int) (infl, under, acc, cov string) {
	if !PublishesOurSeverityLevels(model) {
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
// O-COV's denominator survives the gate for a foreign row because it is not a
// severity claim: how many planted defects a reviewer LOCATED is a statement
// about detection, in nobody's severity vocabulary.
func (a Aggregate) ObjectiveSeverityCounts(model string) string {
	located := fmt.Sprintf("%d located of %d planted", a.SevGraded(), a.SevPlanted)

	if !PublishesOurSeverityLevels(model) {
		return "O-* withdrawn (severity vocabulary is not ours); " + located
	}
	return fmt.Sprintf("O-* %d accurate + %d inflated + %d understated over %s",
		a.SevAccurate, a.SevInflated, a.SevUnderstated, located)
}

// VocabularyRow is one contender's severity vocabulary, for the block printed
// under every table that compares reviewers.
type VocabularyRow struct {
	Name  string
	Usage SeverityUsage
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
func SeverityVocabularyBlock(rows []VocabularyRow) string {
	var b strings.Builder

	b.WriteString("SEVERITY VOCABULARY — the severity word each contender PRINTED for the defects it " +
		"located, against the level planted. A DESCRIPTION, NOT A SCORE: the vocabularies differ in " +
		"resolution, and every reduction that makes them comparable is maximised by rating everything " +
		"blocking.\n" +
		"THE WORDS ARE VERBATIM AND THE READINGS ARE OURS: where this project translates a word into " +
		"its own five levels, the level follows it as [we read as X] and is our reading, not the " +
		"reviewer's claim. A word shown as " + UnrecordedWord + " was destroyed before it reached here, " +
		"or was never printed at all, and is reported missing rather than filled in from our reading.\n" +
		translatedWordsNote(rows))

	for _, r := range rows {
		lines := r.Usage.Lines()
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
