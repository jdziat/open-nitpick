package review

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	llms "github.com/nocturnium/llm-go-sdk/v6"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/llm"
	"github.com/jdziat/open-nitpick/internal/prompt"
)

// The critique stage grades how well the diff supports each model finding and
// withholds the ones it does not. It exists because severity and triage do not
// rank findings well enough to cut on (docs/bakeoff-mira.md), and because the
// expert validation pass, which asks "is this claim wrong", cost more recall
// than it bought precision. Grading evidence is a different question: it never
// asks the grader to build a case against the claim.

// Evidence grades, the closed set the schema offers.
const (
	evidenceProven      = "proven"
	evidencePlausible   = "plausible"
	evidenceUnsupported = "unsupported"
)

const (
	critiqueSchemaName = "finding_critique"

	// critiqueBatch bounds findings per call, so one pull request with dozens
	// of findings does not become one prompt the grader cannot hold.
	critiqueBatch = 12

	// critiqueWindow is how many diff lines either side of the cited one the
	// grader is shown.
	critiqueWindow = 30

	// critiqueExpert is how a withheld finding is attributed to a reader.
	critiqueExpert = "an evidence critic"
)

type critiqueGrade struct {
	Number   int    `json:"number"`
	Evidence string `json:"evidence"`
	Reason   string `json:"reason"`
}

type critiqueResult struct {
	Grades []critiqueGrade `json:"grades"`
}

func critiqueSchema() (json.RawMessage, error) {
	return json.Marshal(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"grades": map[string]any{
				"type":        "array",
				"description": "One grade for every finding number you were given, exactly once.",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"number":   map[string]any{"type": "integer", "description": "The finding's number in the list you were given."},
						"evidence": map[string]any{"type": "string", "enum": []string{evidenceProven, evidencePlausible, evidenceUnsupported}},
						"reason":   map[string]any{"type": "string", "description": "One sentence naming the line or fact you graded on."},
					},
					"required":             []string{"number", "evidence", "reason"},
					"additionalProperties": false,
				},
			},
		},
		"required":             []string{"grades"},
		"additionalProperties": false,
	})
}

// keepAfterCritique is the rule that turns a grade into a decision. It is the
// whole policy, and it lives here rather than in the prompt so that moving the
// dial is a code change a test can see.
//
// An unrecognised grade keeps the finding: a grader that answers outside its
// vocabulary has not graded.
func keepAfterCritique(evidence string, severity config.Severity) bool {
	switch evidence {
	case evidenceProven:
		return true
	case evidencePlausible:
		return severity.AtLeast(config.SeverityWarning)
	case evidenceUnsupported:
		return false
	}
	return true
}

// critiqueFindings grades the model findings in the list and returns what is
// kept, what was withheld with its reason, and a stage status for a call that
// failed.
//
// A failed call keeps its findings: an outage costs precision, which a reader
// can see in the stage status, where dropping the findings would cost recall
// nobody is told about. A finding the grader did not number, graded with an
// unknown word, or whose file has no diff to show, is kept and counted in the
// log, so that "nothing was withheld" can be told from "nothing was graded".
func (e *Engine) critiqueFindings(ctx context.Context, findings []Finding, plan *bundle.Plan) ([]Finding, []Overruled, []StageStatus) {
	if !e.Config.Review.Critique || len(findings) == 0 {
		return findings, nil, nil
	}
	system, err := prompt.Critique()
	if err != nil {
		e.log().Error("critique prompt unavailable; publishing ungraded", "error", err)
		return findings, nil, []StageStatus{{Stage: "critique", Reason: "prompt unavailable"}}
	}
	schema, err := schemaOption(critiqueSchemaName, critiqueSchema)
	if err != nil {
		e.log().Error("critique schema unavailable; publishing ungraded", "error", err)
		return findings, nil, []StageStatus{{Stage: "critique", Reason: "schema unavailable"}}
	}

	files := filesByPath(plan)
	var gradable []int
	for i, f := range findings {
		if f.FromAnalyzer || files[f.Path] == nil {
			continue
		}
		gradable = append(gradable, i)
	}
	if len(gradable) == 0 {
		e.log().Info("critique had nothing to grade", "findings", len(findings))
		return findings, nil, nil
	}
	e.log().Info("grading evidence", "findings", len(gradable), "of", len(findings))

	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		grades   = map[int]critiqueGrade{}
		failure  string
		sem      = make(chan struct{}, max(1, e.Config.Review.Concurrency))
		chunks   [][]int
		ungraded int
	)
	for start := 0; start < len(gradable); start += critiqueBatch {
		chunks = append(chunks, gradable[start:min(start+critiqueBatch, len(gradable))])
	}
	for _, chunk := range chunks {
		wg.Add(1)
		go func(chunk []int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				mu.Lock()
				failure = errorKind(ctx.Err())
				mu.Unlock()
				return
			}
			picked := make([]Finding, len(chunk))
			for k, idx := range chunk {
				picked[k] = findings[idx]
			}
			msgs := []llms.Message{
				{Role: llms.RoleSystem, Content: system},
				{Role: llms.RoleUser, Content: critiqueRequest(picked, files)},
			}
			result, err := llm.Extract[critiqueResult](ctx, e.Roles.Validator(), msgs, schema)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				e.log().Warn("evidence critique failed; keeping the findings", "findings", len(chunk), "error", err)
				failure = errorKind(err)
				return
			}
			for _, g := range result.Grades {
				if g.Number < 1 || g.Number > len(chunk) {
					continue
				}
				if _, dup := grades[chunk[g.Number-1]]; dup {
					continue
				}
				grades[chunk[g.Number-1]] = g
			}
		}(chunk)
	}
	wg.Wait()

	var (
		kept      []Finding
		overruled []Overruled
	)
	for i, f := range findings {
		g, graded := grades[i]
		if !graded {
			if !f.FromAnalyzer && files[f.Path] != nil {
				ungraded++
			}
			kept = append(kept, f)
			continue
		}
		if keepAfterCritique(g.Evidence, f.Sev()) {
			kept = append(kept, f)
			continue
		}
		reason := strings.TrimSpace(g.Reason)
		if reason == "" {
			reason = "the shown code does not support the claim"
		}
		if g.Evidence == evidencePlausible {
			reason = "depends on code not shown, and below warning: " + reason
		}
		overruled = append(overruled, Overruled{Finding: f, Expert: critiqueExpert, Reason: reason})
	}
	e.log().Info("evidence graded", "kept", len(kept), "withheld", len(overruled), "ungraded", ungraded)

	var stages []StageStatus
	if failure != "" {
		stages = append(stages, StageStatus{Stage: "critique", Reason: failure})
	}
	return kept, overruled, stages
}

// filesByPath indexes the plan's changed files for window lookup.
func filesByPath(plan *bundle.Plan) map[string]*diff.File {
	out := map[string]*diff.File{}
	if plan == nil {
		return out
	}
	for _, b := range plan.Batches {
		for _, entry := range b.Entries {
			if entry.File != nil {
				out[entry.File.Path] = entry.File
			}
		}
	}
	return out
}

// critiqueRequest numbers the findings and shows each with the diff around the
// line it cites.
func critiqueRequest(findings []Finding, files map[string]*diff.File) string {
	var b strings.Builder
	b.WriteString("Grade each draft comment against the diff shown under it.\n\n")
	for i, f := range findings {
		fmt.Fprintf(&b, "[%d] %s:%d (%s)\n", i+1, f.Path, f.Line, f.Sev())
		fmt.Fprintf(&b, "Title: %s\nClaim: %s\n", oneLineTitle(f.Title), defang(strings.TrimSpace(f.Rationale)))
		if c := strings.TrimSpace(f.Citation); c != "" {
			fmt.Fprintf(&b, "Cites: %s\n", defang(c))
		}
		fmt.Fprintf(&b, "Diff around the cited line, new-file line numbers in the margin:\n```\n%s```\n\n", defang(diffWindow(files[f.Path], f.Line, critiqueWindow)))
	}
	return b.String()
}

// diffWindow renders the lines of a file's diff within radius lines of the one
// cited, removed lines included, numbered as the new file numbers them.
func diffWindow(file *diff.File, line, radius int) string {
	if file == nil {
		return ""
	}
	type row struct {
		new  int
		text string
	}
	var rows []row
	nearest, best := -1, int(^uint(0)>>1)
	for _, h := range file.Hunks {
		for _, l := range h.Lines {
			mark := " "
			switch l.Kind {
			case diff.LineAdded:
				mark = "+"
			case diff.LineRemoved:
				mark = "-"
			}
			rows = append(rows, row{new: l.NewLine, text: mark + l.Content})
			if l.NewLine > 0 {
				if d := abs(l.NewLine - line); d < best {
					best, nearest = d, len(rows)-1
				}
			}
		}
	}
	if nearest < 0 {
		return ""
	}
	lo, hi := max(0, nearest-radius), min(len(rows), nearest+radius+1)
	var b strings.Builder
	for _, r := range rows[lo:hi] {
		if r.new > 0 {
			fmt.Fprintf(&b, "%5d %s\n", r.new, r.text)
		} else {
			fmt.Fprintf(&b, "      %s\n", r.text)
		}
	}
	return b.String()
}
