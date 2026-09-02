package evals

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/review"
)

// ContenderModel is the pseudo-model id used for Contender in reports.
const ContenderModel = "contender/cli"

// contenderCacheDir holds one cached Contender review per fixture, beside the
// Incumbent cache and in the same shape, so the two incumbents are read by
// one loader.
const contenderCacheDir = "testdata/contender"

// ContenderSeverityScale declares that the Contender adapter translates a
// foreign severity vocabulary onto ours, for the same reason
// IncumbentSeverityScale does: its findings' O-* cells are withdrawn rather
// than scored at our resolution.
const ContenderSeverityScale = ForeignSeverityScale

// ContenderAvailable reports whether the CLI is installed and signed in.
func ContenderAvailable(ctx context.Context) bool {
	if _, err := exec.LookPath("contender"); err != nil {
		return false
	}
	checkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	out, err := exec.CommandContext(checkCtx, "contender", "whoami").CombinedOutput()
	if err != nil {
		return false
	}
	return !strings.Contains(strings.ToLower(string(out)), "not signed in")
}

// contenderOutput is the shape `contender review --json` prints, read from the
// CLI bundle's own serialiser: a summary and a list of comments, each carrying
// a path, a start and end line, a severity word and prose.
//
// Fields not named here are ignored rather than rejected, because the CLI is
// not ours and adds keys between releases. The ones that matter to a score
// are path, the lines, and the text a keyword can be found in.
type contenderOutput struct {
	Summary  string            `json:"summary"`
	Comments []contenderComment `json:"comments"`
}

type contenderComment struct {
	ID        any    `json:"id"`
	Path      string `json:"path"`
	StartLine int    `json:"startLine"`
	EndLine   int    `json:"endLine"`
	Line      int    `json:"line"`
	Severity  string `json:"severity"`
	Category  string `json:"category"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Comment   string `json:"comment"`
}

// RunContender reviews the fixture repository in dir with the Contender CLI and
// returns its findings and the raw JSON it printed.
//
// Contender reviews COMMITTED work on a branch against a base, not a working
// tree, so the fixture's head state is committed onto a `review` branch first.
// buildRepo leaves it uncommitted on purpose — that is the path our own
// engine is measured on — and committing here changes nothing the reviewer
// would score differently: the diff between main and the branch is the same
// change.
func RunContender(ctx context.Context, dir string, timeout time.Duration) ([]review.Finding, string, error) {
	if timeout <= 0 {
		timeout = 8 * time.Minute
	}

	git := func(args ...string) error {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=eval", "GIT_AUTHOR_EMAIL=eval@example.com",
			"GIT_COMMITTER_NAME=eval", "GIT_COMMITTER_EMAIL=eval@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, out)
		}
		return nil
	}
	if err := git("checkout", "-q", "-b", "review"); err != nil {
		return nil, "", err
	}
	if err := git("add", "-A"); err != nil {
		return nil, "", err
	}
	if err := git("commit", "-qm", "change under review"); err != nil {
		return nil, "", err
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "contender", "review", "-b", "main", "--json", "--no-color")
	cmd.Dir = dir
	// The CLI switches to agent-friendly output when it sees an agent's
	// environment; --json already asks for that, and the variable makes the
	// welcome screen stay out of stdout.
	cmd.Env = append(os.Environ(), "CI=1")

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = 10 * time.Second

	runErr := cmd.Run()
	raw := crEscape.ReplaceAllString(stdout.String(), "")

	findings, parseErr := parseContender([]byte(raw))
	if parseErr == nil {
		return findings, raw, nil
	}

	switch {
	case runCtx.Err() != nil:
		return nil, raw, fmt.Errorf("contender: %w (output did not parse: %w)", runCtx.Err(), parseErr)
	case runErr != nil:
		return nil, raw, fmt.Errorf("contender: %w (stderr: %s)", runErr, truncate(stderr.String(), 300))
	default:
		return nil, raw, fmt.Errorf("contender: %w (stderr: %s)", parseErr, truncate(stderr.String(), 300))
	}
}

// parseContender converts the CLI's JSON review into findings.
//
// The JSON object may be preceded by log lines on stdout, so the parse starts
// at the first `{`. A document with no comments array at all is an error
// rather than a clean review: the difference between "Contender found nothing"
// and "Contender printed something this parser does not read" is the whole
// benchmark, and the second must never score as the first.
func parseContender(out []byte) ([]review.Finding, error) {
	start := bytes.IndexByte(out, '{')
	if start < 0 {
		return nil, errors.New("no JSON object in output")
	}

	var doc contenderOutput
	dec := json.NewDecoder(bytes.NewReader(out[start:]))
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("decode review: %w", err)
	}
	if doc.Comments == nil {
		return nil, errors.New(`output carries no "comments" array; the JSON format may have changed`)
	}

	findings := make([]review.Finding, 0, len(doc.Comments))
	for _, c := range doc.Comments {
		line := c.StartLine
		if line == 0 {
			line = c.Line
		}
		if c.Path == "" || line <= 0 {
			continue
		}
		body := c.Body
		if body == "" {
			body = c.Comment
		}
		title := c.Title
		if title == "" {
			title = firstSentence(body)
		}

		sev, translated := contenderSeverity(c.Severity)
		f := review.Finding{
			Path:      filepath.ToSlash(c.Path),
			Line:      line,
			Severity:  string(sev),
			Category:  c.Category,
			Title:     title,
			Rationale: body,
			Source:    ContenderModel,
		}
		if c.EndLine > line {
			f.EndLine = c.EndLine
		}
		if translated {
			f.SeverityTranslated = true
			f.RawSeverity = c.Severity
		}
		findings = append(findings, f)
	}
	return findings, nil
}

// contenderSeverity maps Contender's severity word onto our five levels. The
// word is kept beside the result; see review.Finding.RawSeverity.
func contenderSeverity(word string) (level config.Severity, translated bool) {
	switch strings.ToLower(strings.TrimSpace(word)) {
	case "critical", "blocker":
		return config.SeverityCritical, true
	case "high", "error", "major":
		return config.SeverityError, true
	case "medium", "warning", "moderate":
		return config.SeverityWarning, true
	case "low", "info", "minor", "suggestion":
		return config.SeverityInfo, true
	case "nit", "nitpick", "style":
		return config.SeverityNit, true
	default:
		// No word, or one this adapter has not met: warning, the same level
		// an analyzer's unrecognised word lands on, and recorded as translated
		// so the vocabulary block never quotes it as Contender's own.
		return config.SeverityWarning, true
	}
}

// CachedContender returns a fixture's cached findings, re-parsed from the raw
// JSON the collection retained, for CachedIncumbent's reasons.
func CachedContender(cacheDir string, f Fixture) ([]review.Finding, bool) {
	c, ok := readIncumbentCache(cacheDir, f, contenderReviewMode)
	if !ok || c.Raw == "" {
		return nil, false
	}
	findings, err := parseContender([]byte(c.Raw))
	if err != nil {
		return nil, false
	}
	return findings, true
}

// contenderReviewMode names the CLI mode a cache entry was collected in.
const contenderReviewMode = "json"

// readIncumbentCache loads a cache entry in the shape both incumbents share,
// if it matches the fixture's exact content and the named mode.
func readIncumbentCache(cacheDir string, f Fixture, mode string) (crCache, bool) {
	data, err := os.ReadFile(filepath.Join(cacheDir, f.Name+".json"))
	if err != nil {
		return crCache{}, false
	}
	var c crCache
	if err := json.Unmarshal(data, &c); err != nil {
		return crCache{}, false
	}
	if c.Mode != mode || c.Fingerprint != fixtureFingerprint(f) {
		return crCache{}, false
	}
	return c, true
}

// CollectContender reviews any fixtures not already cached, one at a time,
// retaining the raw JSON beside the parsed findings.
func CollectContender(ctx context.Context, fixtures []Fixture, cacheDir string, perReview time.Duration, onProgress func(string)) (collected, remaining int, err error) {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return 0, 0, err
	}
	log := func(format string, args ...any) {
		if onProgress != nil {
			onProgress(fmt.Sprintf(format, args...))
		}
	}

	for _, f := range fixtures {
		if _, ok := CachedContender(cacheDir, f); ok {
			log("%s: cached", f.Name)
			continue
		}
		if err := ctx.Err(); err != nil {
			return collected, remaining, err
		}

		dir, err := os.MkdirTemp("", "contender-collect-")
		if err != nil {
			return collected, remaining, err
		}
		if err := buildRepo(dir, f); err != nil {
			_ = os.RemoveAll(dir)
			return collected, remaining, err
		}

		findings, raw, runErr := RunContender(ctx, dir, perReview)
		_ = os.RemoveAll(dir)
		if runErr != nil {
			remaining++
			log("%s: failed: %v", f.Name, runErr)
			continue
		}

		blob, _ := json.MarshalIndent(crCache{
			Fixture:     f.Name,
			Findings:    findings,
			At:          time.Now().UTC().Format(time.RFC3339),
			Mode:        contenderReviewMode,
			Fingerprint: fixtureFingerprint(f),
			Raw:         raw,
		}, "", "  ")
		if werr := os.WriteFile(filepath.Join(cacheDir, f.Name+".json"), blob, 0o644); werr != nil {
			return collected, remaining, werr
		}
		collected++
		log("%s: collected %d finding(s)", f.Name, len(findings))
	}
	return collected, remaining, nil
}

// firstSentence takes a title out of prose that has none.
func firstSentence(s string) string {
	s = strings.TrimSpace(strings.SplitN(s, "\n", 2)[0])
	if i := strings.Index(s, ". "); i > 0 {
		s = s[:i+1]
	}
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}
