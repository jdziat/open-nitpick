package review

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/nitpick-oss/nitpick/internal/bundle"
	"github.com/nitpick-oss/nitpick/internal/diff"
	"github.com/nitpick-oss/nitpick/internal/jev"
	"github.com/nitpick-oss/nitpick/internal/vcs"
)

// The Jev stage is an optional second opinion on each model finding. A decision
// model is shown the claim with the facts a reviewer would look up, and says
// how likely the finding is to be a defect that breaks something. Findings
// under review.jev.threshold are withheld, with the reason.
//
// It follows the repository check on purpose. Measured on 100 findings that
// agents graded, the check and Jev together withheld more findings that were
// not real than either did alone, at the same cost in real ones, and the
// check's lookups are the evidence Jev is shown.

const (
	// jevExpert is how a finding this stage withholds is attributed to a reader.
	jevExpert = "a decision model"

	jevStage = "jev"

	// envOpenRouterKey is the key read when review.jev.api_key_env is empty.
	envOpenRouterKey = "OPENROUTER_API_KEY"

	// The bounds on each part of what Jev is shown, in characters. They are the
	// ones the measurement ran under: a state larger than this was never scored.
	jevMaxDescription = 2500
	jevMaxFileList    = 3000
	jevMaxEvidence    = 14000
	jevMaxDiff        = 15000
	jevMaxFile        = 30000

	// jevDiffRadius is how far from the cited line the file's diff is shown. It
	// is wide enough to be the whole diff of an ordinary file.
	jevDiffRadius = 400
)

// Decider is what the Jev stage asks. The production implementation is
// jev.Client; a test supplies its own.
type Decider interface {
	Classify(ctx context.Context, state string) (jev.Scores, error)
}

// keepAfterJev is the rule that turns scores into a decision. It lives here, in
// code, so moving the dial is a change a test can see. The boundary is
// inclusive: a finding exactly at the threshold is kept.
func keepAfterJev(threshold float64, s jev.Scores) bool { return s.Defect() >= threshold }

// decider returns the engine's own Decider, or builds a client from
// review.jev. The error says what is missing and is for the log, not the report.
func (e *Engine) decider() (Decider, error) {
	if e.Decider != nil {
		return e.Decider, nil
	}
	cfg := e.Config.Review.Jev
	name := cfg.APIKeyEnv
	if name == "" {
		name = envOpenRouterKey
	}
	client, err := jev.NewClient("", cfg.Model, os.Getenv(name))
	if err != nil {
		return nil, fmt.Errorf("%w (set %s)", err, name)
	}
	client.ObserveCalls(e.ObserveCall)
	return client, nil
}

type jevOutcome struct {
	scores jev.Scores
	failed string // set when the request could not be made; the finding is kept
}

// jevFilter scores the model findings in the list and returns what is kept,
// what was withheld with its reason, and a stage status when a request could
// not be made.
//
// A request that fails keeps its finding. An outage costs precision, which the
// stage status shows, where dropping the finding would cost recall nobody is
// told about. With no key at all every finding is kept and the status says the
// stage did not run, so a clean report is not read as a filtered one.
func (e *Engine) jevFilter(ctx context.Context, ref vcs.Ref, pr *vcs.PullRequest, findings []Finding, plan *bundle.Plan, evidence map[string]string) ([]Finding, []Overruled, []StageStatus) {
	cfg := e.Config.Review.Jev
	if !cfg.Enabled() || len(findings) == 0 {
		return findings, nil, nil
	}
	decider, err := e.decider()
	if err != nil {
		e.log().Error("jev unavailable; publishing unscored", "error", err)
		return findings, nil, []StageStatus{{Stage: jevStage, Reason: "unavailable"}}
	}
	tools := newRepoTools(e.RepoRoot, e.Provider, ref)
	files := filesByPath(plan)
	changed := changedPaths(files)

	var todo []int
	for i, f := range findings {
		if !f.FromAnalyzer {
			todo = append(todo, i)
		}
	}
	e.log().Info("asking the decision model", "findings", len(todo), "of", len(findings))

	outcomes := make([]jevOutcome, len(findings))
	var wg sync.WaitGroup
	sem := make(chan struct{}, max(1, e.Config.Review.Concurrency))
	for _, i := range todo {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				outcomes[i] = jevOutcome{failed: errorKind(ctx.Err())}
				return
			}
			f := findings[i]
			state := jevState(ctx, pr, changed, f, files[f.Path], evidence[findingKey(f)], tools)
			scores, err := decider.Classify(ctx, state)
			if err != nil {
				e.log().Warn("jev request failed; keeping the finding", "path", f.Path, "line", f.Line, "error", err)
				outcomes[i] = jevOutcome{failed: errorKind(err)}
				return
			}
			// Logged for every finding, not only the withheld ones: a threshold can
			// then be moved afterwards from the stored run, in either direction.
			e.log().Info("decision model scored", "path", f.Path, "line", f.Line, "title", f.Title,
				"defect", fmt.Sprintf("%.3f", scores.Defect()), "leaning", scores.Leaning())
			outcomes[i] = jevOutcome{scores: scores}
		}(i)
	}
	wg.Wait()

	var (
		kept     []Finding
		withheld []Overruled
		failed   int
		failure  string
	)
	for i, f := range findings {
		o := outcomes[i]
		switch {
		case f.FromAnalyzer:
			kept = append(kept, f)
		case o.failed != "":
			failed++
			failure = o.failed
			kept = append(kept, f)
		case keepAfterJev(cfg.Threshold, o.scores):
			kept = append(kept, f)
		default:
			withheld = append(withheld, Overruled{Finding: f, Expert: jevExpert, Reason: jevReason(cfg.Threshold, o.scores)})
		}
	}
	e.log().Info("decision model finished", "scored", len(todo)-failed, "failed", failed, "withheld", len(withheld))

	var stages []StageStatus
	if failure != "" {
		stages = append(stages, StageStatus{Stage: jevStage, Reason: failure})
	}
	return kept, withheld, stages
}

// jevReason says why a finding was withheld, in numbers a reader can check
// against the threshold they set.
func jevReason(threshold float64, s jev.Scores) string {
	return fmt.Sprintf("a decision model rated it %.0f%% likely to be a defect, under the %.0f%% this repository requires, and leaned %s",
		100*s.Defect(), 100*threshold, s.Leaning())
}

// changedPaths lists the pull request's changed files, in order.
func changedPaths(files map[string]*diff.File) []string {
	out := make([]string, 0, len(files))
	for p := range files {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// jevState is what the decision model is shown about one finding: the pull
// request's intent, the claim and the reviewer's own facts, what the checker
// read, the file's diff and the whole file. Each part is bounded, and a part
// that is not available says so rather than being left out, because a model
// that is not told the file could not be read would take its absence for a
// clean file.
func jevState(ctx context.Context, pr *vcs.PullRequest, changed []string, f Finding, file *diff.File, evidence string, tools *repoTools) string {
	var b strings.Builder
	if pr != nil {
		if t := strings.TrimSpace(pr.Title); t != "" {
			fmt.Fprintf(&b, "Pull request: %s\n", defang(oneLineTitle(t)))
		}
		if body := strings.TrimSpace(pr.Body); body != "" {
			fmt.Fprintf(&b, "Description: %s\n", defang(truncate(body, jevMaxDescription)))
		}
	}
	safeNames := make([]string, 0, len(changed))
	for _, name := range changed {
		safeNames = append(safeNames, defang(bundle.PromptSafe(name)))
	}
	fmt.Fprintf(&b, "Files changed in the pull request:\n%s\n\n", truncate("  "+strings.Join(safeNames, "\n  "), jevMaxFileList))

	fmt.Fprintf(&b, "Review comment to check.\nFile: %s:%d\nTitle: %s\nClaim: %s\n", defang(bundle.PromptSafe(f.Path)), f.Line,
		defang(oneLineTitle(f.Title)), defang(strings.TrimSpace(f.Rationale)))
	fmt.Fprintf(&b, "Reviewer's severity: %s, class: %s, confidence %s\n", orUnknown(f.Severity), orUnknown(f.Class), confidenceText(f.Confidence))
	fmt.Fprintf(&b, "Reviewer's cited code:\n%s\n\n", orNone(defang(strings.TrimSpace(f.Citation))))

	fmt.Fprintf(&b, "What an investigator found by reading and searching the repository:\n%s\n\n",
		defang(truncate(orText(evidence, "(the check made no lookups)"), jevMaxEvidence)))

	diffText := diffWindow(file, f.Line, jevDiffRadius)
	fmt.Fprintf(&b, "Diff of the cited file:\n%s\n\n", defang(truncate(orText(diffText, "(file not in the diff)"), jevMaxDiff)))

	fmt.Fprintf(&b, "Whole file at the head commit:\n%s", defang(truncate(wholeFile(ctx, tools, f.Path), jevMaxFile)))
	return b.String()
}

// wholeFile numbers every line of a file, or says it could not be read. A file
// the review's tools refuse, such as one the repository ignores, is not sent.
func wholeFile(ctx context.Context, tools *repoTools, path string) string {
	data, err := tools.fileBytes(ctx, path)
	if err != nil {
		return "(file not readable)"
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	var b strings.Builder
	for i, l := range lines {
		fmt.Fprintf(&b, "%5d  %s\n", i+1, l)
	}
	return b.String()
}

// truncate cuts s to at most n bytes without splitting a character.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "")
}

func orText(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

func orUnknown(s string) string { return orText(s, "unknown") }

func orNone(s string) string { return orText(s, "(none)") }

func confidenceText(c *float64) string {
	if c == nil {
		return "not given"
	}
	return fmt.Sprintf("%.2f", *c)
}
