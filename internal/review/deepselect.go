package review

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	llms "github.com/nocturnium/llm-go-sdk/v6"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/prompt"
)

// Deep review posts fewer findings than it drafts. Mira's drafts on the tuning
// set were 50.6% precise and its posted comments 73.0%, at about the same F1,
// and the cut that does it is this one: a confidence floor, a cap on
// low-priority findings, then a critique that grades each survivor against the
// code it cites. Analyzer findings carry no reviewer confidence and pass
// untouched.

const (
	deepFilterExpert   = "the deep-review filter"
	deepCritiqueExpert = "the deep-review critique"

	// deepPlausibleConfidence is the confidence a warning-or-worse finding the
	// critique graded plausible needs to be kept: above the posting floor, so
	// a finding the code does not prove is kept only when its author was sure.
	deepPlausibleConfidence = 0.75

	// deepEvidenceLines and deepEvidenceChars bound the diff the critique is
	// shown around each finding's cited line.
	deepEvidenceLines = 20
	deepEvidenceChars = 2400

	submitGrades = "submit_grades"
)

// deepSelect replaces triage in a deep review. Every finding it withholds is
// returned with the reason, so the report says what was held back and by what.
func (e *Engine) deepSelect(ctx context.Context, findings []Finding, plan *bundle.Plan) ([]Finding, []Overruled, []StageStatus) {
	findings = dedupe(findings)
	var model, analyzer []Finding
	for _, f := range findings {
		if f.FromAnalyzer {
			analyzer = append(analyzer, f)
		} else {
			model = append(model, f)
		}
	}
	deep := e.Config.Review.Deep
	kept, withheld := deepFilter(model, deep.ConfidenceFloor, deep.LowPriorityCap)
	afterFilter := len(kept)
	var stages []StageStatus
	if deep.Critique && len(kept) > 0 {
		var dropped []Overruled
		var failed *StageStatus
		kept, dropped, failed = e.deepCritique(ctx, kept, plan)
		withheld = append(withheld, dropped...)
		if failed != nil {
			stages = append(stages, *failed)
		}
	}
	e.log().Info("deep-review selected findings", "drafted", len(model), "after_filter", afterFilter, "posted", len(kept))
	return append(analyzer, kept...), withheld, stages
}

// confidenceOf is a finding's confidence, zero when it gave none.
func confidenceOf(f Finding) float64 {
	if f.Confidence == nil {
		return 0
	}
	return *f.Confidence
}

// deepFilter withholds findings below the confidence floor, orders the rest by
// severity and then confidence, withholds a finding that repeats one ranked
// above it, and keeps at most lowCap info and nit findings. Warnings and above
// are never capped: the cap controls noise, and a real defect is not noise
// however many there are.
func deepFilter(findings []Finding, floor float64, lowCap int) ([]Finding, []Overruled) {
	var kept []Finding
	var withheld []Overruled
	for _, f := range findings {
		if c := confidenceOf(f); c < floor {
			withheld = append(withheld, Overruled{Finding: f, Expert: deepFilterExpert,
				Reason: fmt.Sprintf("its confidence of %.2f is below the floor of %.2f", c, floor)})
			continue
		}
		kept = append(kept, f)
	}
	sort.SliceStable(kept, func(i, j int) bool {
		if ri, rj := kept[i].Sev().Rank(), kept[j].Sev().Rank(); ri != rj {
			return ri > rj
		}
		return confidenceOf(kept[i]) > confidenceOf(kept[j])
	})
	unique := kept[:0:0]
	for _, f := range kept {
		if i := slices.IndexFunc(unique, func(u Finding) bool { return deepDuplicate(f, u) }); i >= 0 {
			withheld = append(withheld, Overruled{Finding: f, Expert: deepFilterExpert,
				Reason: fmt.Sprintf("it repeats %q at %s:%d", oneLine(unique[i].Title), unique[i].Path, unique[i].Line)})
			continue
		}
		unique = append(unique, f)
	}
	kept = unique
	if lowCap == 0 {
		return kept, withheld
	}
	out := kept[:0:0]
	low := 0
	for _, f := range kept {
		if !f.Sev().AtLeast(config.SeverityWarning) {
			low++
			if low > lowCap {
				withheld = append(withheld, Overruled{Finding: f, Expert: deepFilterExpert,
					Reason: fmt.Sprintf("it is over the cap of %d low-priority findings", lowCap)})
				continue
			}
		}
		out = append(out, f)
	}
	return out, withheld
}

// Two agents reading one pull request file one defect twice, under different
// titles and often on adjacent lines, and the exact match dedupe makes keeps
// both. deepDuplicate is Mira's composite rule (core/noise_filter.py).
const (
	deepNearLines         = 3
	deepNearTextThreshold = 0.25
	deepMinTextTokens     = 8
	deepTitleThreshold    = 0.6
)

// deepDuplicate reports whether two findings are one defect: in different
// files, near-identical titles and rationales; in one file, the same lines and
// class, or the same class within three lines with similar text, or overlapping
// lines with titles that share words, or near-identical titles anywhere. Two
// classes on one line stay apart, since one call can leak and inject at once.
func deepDuplicate(a, b Finding) bool {
	title := wordOverlap(a.Title, b.Title)
	if a.Path != b.Path {
		return title >= 0.8 && wordOverlap(a.Rationale, b.Rationale) >= 0.5
	}
	endA, endB := lastLine(a), lastLine(b)
	if a.Line == b.Line && endA == endB && a.Class == b.Class {
		return true
	}
	if a.Class == b.Class && abs(a.Line-b.Line) <= deepNearLines &&
		tokenOverlap(a.Title+" "+a.Rationale, b.Title+" "+b.Rationale) >= deepNearTextThreshold {
		return true
	}
	if a.Line <= endB && b.Line <= endA && title >= 0.2 {
		return true
	}
	return title >= deepTitleThreshold
}

func lastLine(f Finding) int {
	if f.EndLine > f.Line {
		return f.EndLine
	}
	return f.Line
}

// wordOverlap is the Jaccard overlap of two texts' lowercased words.
func wordOverlap(a, b string) float64 {
	return jaccard(strings.Fields(strings.ToLower(a)), strings.Fields(strings.ToLower(b)))
}

var identifierToken = regexp.MustCompile(`[a-z0-9_]+`)

// tokenOverlap is the Jaccard overlap of two texts' identifier-like tokens, or
// zero when either is too short for an overlap to mean anything.
func tokenOverlap(a, b string) float64 {
	ta := identifierToken.FindAllString(strings.ToLower(a), -1)
	tb := identifierToken.FindAllString(strings.ToLower(b), -1)
	if len(set(ta)) < deepMinTextTokens || len(set(tb)) < deepMinTextTokens {
		return 0
	}
	return jaccard(ta, tb)
}

func set(words []string) map[string]bool {
	out := make(map[string]bool, len(words))
	for _, w := range words {
		out[w] = true
	}
	return out
}

func jaccard(a, b []string) float64 {
	sa, sb := set(a), set(b)
	if len(sa) == 0 || len(sb) == 0 {
		return 0
	}
	shared := 0
	for w := range sa {
		if sb[w] {
			shared++
		}
	}
	return float64(shared) / float64(len(sa)+len(sb)-shared)
}

// critiqueGrade is one grade from the critique.
type critiqueGrade struct {
	Number   int    `json:"number"`
	Evidence string `json:"evidence"`
	Reason   string `json:"reason"`
}

// deepCritiqueKeeps is the rule that turns a grade into a decision. It lives in
// code, where a test can see the dial move, rather than in the prompt's wording.
func deepCritiqueKeeps(evidence string, f Finding) bool {
	switch evidence {
	case "proven":
		return true
	case "plausible":
		return f.Sev().AtLeast(config.SeverityWarning) && confidenceOf(f) >= deepPlausibleConfidence
	}
	return false
}

// deepCritique grades every finding in one call and withholds those its rule
// drops. A critique that fails, or returns no grades at all, has not judged
// anything: every finding is kept and the stage is reported as not run. Once it
// returns grades, a finding it left ungraded is withheld, as Mira does.
func (e *Engine) deepCritique(ctx context.Context, findings []Finding, plan *bundle.Plan) ([]Finding, []Overruled, *StageStatus) {
	system, err := prompt.DeepCritique()
	if err != nil {
		return findings, nil, &StageStatus{Stage: "critique", Reason: "unavailable"}
	}
	client := e.Roles.Review
	zero := 0.0
	tool := llms.WithTools([]llms.Tool{toolDef(submitGrades, "Submit one grade for every draft comment.", critiqueSchema)})
	choice := llms.WithToolChoiceTool(submitGrades)
	opts := append(client.CallOptionsWith(&zero, e.Config.Review.Deep.Reasoning), tool, choice)
	msgs := []llms.Message{
		{Role: llms.RoleSystem, Content: system},
		{Role: llms.RoleUser, Content: critiqueRequest(findings, filesByPath(plan))},
	}

	resp, err := e.generateDeep(ctx, client, msgs, opts, deepCallTimeout)
	var grades []critiqueGrade
	var ok bool
	if err == nil {
		grades, ok = parseGrades(resp)
	}
	if err == nil && (!ok || len(grades) == 0) {
		// A critique that gave no usable grades is asked once more without
		// reasoning. It can be cut at the output cap, empty or part way through
		// its grades, or it can spend its whole output thinking and end without
		// calling the tool it was told to call, as one did on discourse-4 with
		// 4,167 reasoning tokens and no call. None of those graded anything.
		e.log().Warn("the critique gave no usable grades; asking again without reasoning",
			"finish", resp.FinishReason, "tool_calls", len(resp.ToolCalls), "reasoning_tokens", resp.Usage.ReasoningTokens)
		opts = append(client.CallOptionsWith(&zero, config.ReasoningOff), tool, choice)
		resp, err = e.generateDeep(ctx, client, msgs, opts, deepCallTimeout)
		if err == nil {
			grades, ok = parseGrades(resp)
		}
	}
	if err != nil {
		e.log().Warn("the critique failed; keeping every finding", "error", err)
		return findings, nil, &StageStatus{Stage: "critique", Reason: errorKind(err)}
	}
	e.log().Info("deep-review critique answered", "findings", len(findings),
		"prompt_tokens", resp.Usage.PromptTokens, "completion_tokens", resp.Usage.CompletionTokens,
		"reasoning_tokens", resp.Usage.ReasoningTokens)
	if !ok || len(grades) == 0 {
		e.log().Warn("the critique returned no grades; keeping every finding", "findings", len(findings))
		return findings, nil, &StageStatus{Stage: "critique", Reason: "no grades"}
	}

	byNumber := make(map[int]critiqueGrade, len(grades))
	for _, g := range grades {
		if g.Number >= 0 && g.Number < len(findings) {
			byNumber[g.Number] = g
		}
	}
	var kept []Finding
	var withheld []Overruled
	for i, f := range findings {
		g, graded := byNumber[i]
		switch {
		case !graded:
			withheld = append(withheld, Overruled{Finding: f, Expert: deepCritiqueExpert, Reason: "the critique returned no grade for it"})
		case deepCritiqueKeeps(g.Evidence, f):
			kept = append(kept, f)
		default:
			withheld = append(withheld, Overruled{Finding: f, Expert: deepCritiqueExpert,
				Reason: fmt.Sprintf("graded %s (%s)", g.Evidence, oneLine(g.Reason))})
		}
	}
	return kept, withheld, nil
}

const critiqueSchema = `{"type":"object","properties":{"grades":{"type":"array","items":{"type":"object","properties":{` +
	`"number":{"type":"integer","description":"The draft comment's number."},` +
	`"evidence":{"type":"string","enum":["proven","plausible","unsupported"]},` +
	`"reason":{"type":"string","description":"One short sentence explaining the grade."}},` +
	`"required":["number","evidence","reason"]}}},"required":["grades"]}`

// parseGrades reads the submit_grades call, if the reply made one.
func parseGrades(resp *llms.Response) ([]critiqueGrade, bool) {
	for _, call := range resp.ToolCalls {
		if call.Function == nil || call.Function.Name != submitGrades {
			continue
		}
		var body struct {
			Grades []critiqueGrade `json:"grades"`
		}
		if err := json.Unmarshal([]byte(call.Function.Arguments), &body); err != nil {
			return nil, false
		}
		return body.Grades, true
	}
	return nil, false
}

// critiqueRequest lists the findings the critique grades, each with the diff
// around the line it cites, so the critique grades what the code shows and
// not only the finding's own quotation of it.
func critiqueRequest(findings []Finding, files map[string]*diff.File) string {
	var b strings.Builder
	b.WriteString("## Draft comments\n\n")
	for i, f := range findings {
		cites := strings.TrimSpace(f.Citation)
		if cites == "" {
			cites = "(no quotation)"
		}
		fmt.Fprintf(&b, "[%d] %s:%d, %s, %s\n    Title: %s\n    Rationale: %s\n    Cites: %s\n",
			i, f.Path, f.Line, f.Severity, f.Class, oneLine(f.Title), clip(oneLine(f.Rationale), 500), clip(cites, 400))
		if file := files[f.Path]; file != nil {
			if window := diffWindow(file, f.Line, deepEvidenceLines); window != "" {
				fmt.Fprintf(&b, "    Diff around the cited lines:\n%s\n", clip(window, deepEvidenceChars))
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

// clip shortens s to at most n bytes, marking the cut.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
