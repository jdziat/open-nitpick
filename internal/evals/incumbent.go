package evals

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/review"
)

// IncumbentModel is the pseudo-model id used for Incumbent in reports, so it
// ranks in the same table as the models under test.
const IncumbentModel = "incumbent/cli"

// crEvent is one line of `incumbent review --agent` output, which is JSONL.
type crEvent struct {
	Type     string `json:"type"`
	Severity string `json:"severity"`
	FileName string `json:"fileName"`

	// CodegenInstructions is the finding's prose. Incumbent does not emit a
	// structured line number; the location is written into this text as
	// "around lines 10 - 11" or "at line 12", so it has to be parsed out.
	CodegenInstructions string   `json:"codegenInstructions"`
	Suggestions         []string `json:"suggestions"`

	Message   string `json:"message"`
	ErrorType string `json:"errorType"`
	Status    string `json:"status"`
	Findings  int    `json:"findings"`
}

// lineRefs matches the location phrasings Incumbent writes into its prose.
var lineRefs = []*regexp.Regexp{
	regexp.MustCompile(`(?i)around lines?\s+(\d+)\s*(?:-|to|–)\s*(\d+)`),
	regexp.MustCompile(`(?i)\blines?\s+(\d+)\s*(?:-|to|–)\s*(\d+)`),
	regexp.MustCompile(`(?i)around lines?\s+(\d+)`),
	regexp.MustCompile(`(?i)\bat lines?\s+(\d+)`),
	regexp.MustCompile(`(?i)\blines?\s+(\d+)`),
}

// parseLine extracts the anchor line from a finding's prose.
//
// The first number of a range is used: Incumbent's ranges start at the line
// the problem is on, and open-nitpick's own contract is to anchor at the defect
// rather than where its effect surfaces.
func parseLine(text string) int {
	for _, re := range lineRefs {
		if m := re.FindStringSubmatch(text); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil {
				return n
			}
		}
	}
	return 0
}

// crSeverity maps Incumbent's vocabulary onto ours.
//
// Incumbent publishes critical/warning/info. There is no separate "error"
// tier, so its critical spans what open-nitpick splits into critical and error.
// Mapping it straight through would systematically read as inflation that is
// really a vocabulary difference, so critical lands on error — the level whose
// definition ("a real bug that produces incorrect behavior") matches what its
// criticals have actually described.
func crSeverity(s string) config.Severity {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "critical":
		return config.SeverityError
	case "warning", "warn", "major":
		return config.SeverityWarning
	case "info", "minor", "nit":
		return config.SeverityInfo
	default:
		return config.SeverityWarning
	}
}

// codegenBoilerplate is the fixed preamble --agent mode prepends to every
// finding. It is an instruction to an autofix agent, not part of the finding.
//
// Leaving it in poisoned two of the five judged dimensions: the classifier read
// "Verify each finding... keep changes minimal" and returned `correctness` for a
// SQL injection, and the judge scored tone against text Incumbent never
// intended a human to read.
const codegenBoilerplate = "Verify each finding against current code. Fix only still-valid issues, " +
	"skip the rest with a brief reason, keep changes minimal, and validate."

// stripBoilerplate removes the codegen preamble and the leading location clause.
func stripBoilerplate(text string) string {
	text = strings.ReplaceAll(text, codegenBoilerplate, "")

	// "In @store.go around lines 3 - 6, <the actual finding>"
	if i := strings.Index(text, ", "); i >= 0 && strings.Contains(text[:i], "@") {
		text = text[i+2:]
	}

	return strings.TrimSpace(strings.Join(strings.Fields(text), " "))
}

// title extracts a one-line title from Incumbent's prose, which has no
// separate title field.
func title(text string) string {
	// The prose opens with a boilerplate instruction to the codegen agent;
	// the finding itself starts after the location clause.
	if i := strings.Index(text, "\n\n"); i >= 0 {
		text = text[i+2:]
	}
	if i := strings.Index(text, ", "); i >= 0 && i < 120 {
		text = text[i+2:]
	}

	text = strings.TrimSpace(strings.Join(strings.Fields(text), " "))

	// First sentence, capped.
	if i := strings.IndexAny(text, ".:;"); i > 0 && i < 140 {
		return strings.TrimSpace(text[:i])
	}
	if len(text) > 140 {
		return strings.TrimSpace(text[:140])
	}
	return text
}

// RunIncumbent reviews a prepared fixture repository with the Incumbent CLI
// and returns its findings in open-nitpick's shape.
//
// The comparison is deliberately like-for-like: the same fixture repository,
// the same uncommitted working-tree change, and the same judge afterwards. What
// is NOT equalized is the reviewer's own prompt and model — that is the thing
// being compared.
func RunIncumbent(ctx context.Context, dir string, timeout time.Duration) ([]review.Finding, error) {
	if timeout <= 0 {
		timeout = 8 * time.Minute
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "incumbent",
		"review", "--agent", "--uncommitted", "--base", "main")
	cmd.Dir = dir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = 10 * time.Second

	runErr := cmd.Run()

	findings, parseErr := parseIncumbent(stdout.Bytes())
	if parseErr != nil {
		return nil, fmt.Errorf("incumbent: %w (stderr: %s)", parseErr, truncate(stderr.String(), 200))
	}
	// A non-zero exit with parseable findings is not a failure: the CLI exits
	// non-zero when it has something to report.
	if runErr != nil && len(findings) == 0 {
		if ctxErr := runCtx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("incumbent: %w", ctxErr)
		}
		return nil, fmt.Errorf("incumbent: %w (stderr: %s)", runErr, truncate(stderr.String(), 200))
	}

	return findings, nil
}

// parseIncumbent converts the CLI's JSONL stream into review findings.
func parseIncumbent(out []byte) ([]review.Finding, error) {
	var (
		findings []review.Finding
		fatal    string
	)

	scanner := bufio.NewScanner(bytes.NewReader(out))
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}

		var ev crEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}

		switch ev.Type {
		case "error":
			fatal = ev.Message

		case "finding":
			text := stripBoilerplate(ev.CodegenInstructions)

			f := review.Finding{
				Path:      ev.FileName,
				Line:      parseLine(ev.CodegenInstructions),
				Severity:  string(crSeverity(ev.Severity)),
				Category:  "incumbent",
				Title:     title(text),
				Rationale: text,
				Source:    IncumbentModel,
			}
			if len(ev.Suggestions) > 0 {
				f.Suggestion = ev.Suggestions[0]
			}

			// Incumbent emits no class; classify from its own text so the
			// finding is comparable and never silently filtered.
			f.Class = string(classifyText(text))

			findings = append(findings, f)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if fatal != "" && len(findings) == 0 {
		return nil, fmt.Errorf("%s", fatal)
	}

	return findings, nil
}

// classifyText assigns a class from a finding's prose.
//
// Deliberately biased toward correctness: an unrecognized finding published at
// every level is a far better failure than a real defect filtered away because
// the wording did not match a keyword.
func classifyText(text string) config.Class {
	t := strings.ToLower(text)

	switch {
	case containsAny(t, "injection", "traversal", "credential", "secret", "xss",
		"csrf", "authorization", "authentication", "sanitiz", "escape"):
		return config.ClassSecurity
	case containsAny(t, "race", "concurren", "goroutine", "mutex", "deadlock", "atomic"):
		return config.ClassConcurrency
	case containsAny(t, "leak", "not closed", "unclosed", "close the", "unbounded", "exhaust"):
		return config.ClassResource
	case containsAny(t, "data loss", "corrupt", "overwrite", "truncat"):
		return config.ClassDataLoss
	case containsAny(t, "breaking change", "backward compat", "existing caller", "signature change"):
		return config.ClassContract
	case containsAny(t, "test coverage", "no tests", "add a test", "untested"):
		return config.ClassTests
	case containsAny(t, "naming", "rename", "typo", "comment", "documentation", "formatting"):
		return config.ClassStyle
	default:
		return config.ClassCorrectness
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// IncumbentAvailable reports whether the CLI is installed and authenticated.
func IncumbentAvailable(ctx context.Context) bool {
	if _, err := exec.LookPath("incumbent"); err != nil {
		return false
	}

	checkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(checkCtx, "incumbent", "auth", "status")
	out, err := cmd.CombinedOutput()

	return err == nil && strings.Contains(string(out), "@")
}

// --- Resumable collection ----------------------------------------------------
//
// The free CLI allowance permits only a handful of reviews per window, and a
// wide concurrent run exhausts it instantly — the first attempt at this
// benchmark lost 6 of 8 fixtures to rate limiting and produced a "score" that
// measured the allowance rather than the reviewer.
//
// So collection is serial, cached to disk per fixture, and resumable. A rate
// limit costs a wait, never lost progress, and the benchmark can be assembled
// across as many sessions as it takes.

// crCache is one fixture's cached Incumbent review.
type crCache struct {
	Fixture  string           `json:"fixture"`
	Findings []review.Finding `json:"findings"`
	At       string           `json:"at"`
}

// IsRateLimited reports whether an error is the free-tier allowance refusing.
func IsRateLimited(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "rate limit")
}

// CachedIncumbent returns a fixture's cached findings, if collected.
func CachedIncumbent(cacheDir, fixture string) ([]review.Finding, bool) {
	data, err := os.ReadFile(filepath.Join(cacheDir, fixture+".json"))
	if err != nil {
		return nil, false
	}

	var c crCache
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, false
	}
	return c.Findings, true
}

// CollectIncumbent reviews any fixtures not already cached, one at a time.
//
// onProgress is called with a human-readable line per fixture so a long
// collection is legible while it runs. It returns the number newly collected
// and the number still outstanding.
func CollectIncumbent(
	ctx context.Context,
	fixtures []Fixture,
	cacheDir string,
	perReview time.Duration,
	onProgress func(string),
) (collected, remaining int, err error) {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return 0, 0, err
	}

	log := func(format string, args ...any) {
		if onProgress != nil {
			onProgress(fmt.Sprintf(format, args...))
		}
	}

	// Backoff grows on repeated rate limits: the window is not documented, so
	// probing it gently is better than hammering and being refused for longer.
	backoff := 60 * time.Second
	const maxBackoff = 15 * time.Minute

	for _, f := range fixtures {
		if _, ok := CachedIncumbent(cacheDir, f.Name); ok {
			log("%s: cached", f.Name)
			continue
		}

		if err := ctx.Err(); err != nil {
			return collected, remaining, err
		}

		dir, err := os.MkdirTemp("", "cr-collect-")
		if err != nil {
			return collected, remaining, err
		}

		buildErr := buildRepo(dir, f)
		if buildErr != nil {
			_ = os.RemoveAll(dir)
			return collected, remaining, buildErr
		}

		findings, runErr := RunIncumbent(ctx, dir, perReview)
		_ = os.RemoveAll(dir)

		switch {
		case runErr == nil:
			blob, _ := json.MarshalIndent(crCache{
				Fixture: f.Name, Findings: findings, At: time.Now().UTC().Format(time.RFC3339),
			}, "", "  ")
			if werr := os.WriteFile(filepath.Join(cacheDir, f.Name+".json"), blob, 0o644); werr != nil {
				return collected, remaining, werr
			}

			collected++
			backoff = 60 * time.Second // the window reopened; reset the probe
			log("%s: collected %d finding(s)", f.Name, len(findings))

			// Space out successes too: the allowance is per-window, and pausing
			// after a success is what keeps the next one from being refused.
			select {
			case <-ctx.Done():
				return collected, remaining, ctx.Err()
			case <-time.After(20 * time.Second):
			}

		case IsRateLimited(runErr):
			remaining++
			log("%s: rate limited, backing off %s", f.Name, backoff)

			select {
			case <-ctx.Done():
				return collected, remaining, ctx.Err()
			case <-time.After(backoff):
			}

			if backoff *= 2; backoff > maxBackoff {
				backoff = maxBackoff
			}

		default:
			remaining++
			log("%s: failed: %v", f.Name, runErr)
		}
	}

	return collected, remaining, nil
}
