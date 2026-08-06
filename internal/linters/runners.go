package linters

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jdziat/open-nitpick/internal/config"
)

// golangciLint runs golangci-lint over the changed Go packages.
type golangciLint struct{}

func (g *golangciLint) Name() string { return "golangci-lint" }

func (g *golangciLint) Detect(_ context.Context, repoRoot string) bool {
	return available("golangci-lint") && exists(repoRoot, "go.mod")
}

// golangciOutput is the subset of golangci-lint's JSON report that matters.
type golangciOutput struct {
	Issues []struct {
		FromLinter string `json:"FromLinter"`
		Text       string `json:"Text"`
		Severity   string `json:"Severity"`
		Pos        struct {
			Filename string `json:"Filename"`
			Line     int    `json:"Line"`
		} `json:"Pos"`
	} `json:"Issues"`
}

func (g *golangciLint) Run(ctx context.Context, repoRoot string, files []string) ([]Finding, error) {
	dirs := goPackageDirs(files)
	if len(dirs) == 0 {
		return nil, nil
	}

	args := append([]string{"run", "--output.json.path", "stdout", "--issues-exit-code", "0", "--"}, dirs...)

	out, err := runCommand(ctx, repoRoot, "golangci-lint", args...)
	if err != nil {
		return nil, err
	}

	return g.findings(out)
}

// findings converts one golangci-lint report into findings.
//
// Split from Run for the reason semgrep's is: what this repository does with a
// severity is decided from the analyzer's real output, and a test that
// hand-builds the Finding has already made the decision under test. It matters
// more here than anywhere else, because golangci-lint's Severity is not a
// vocabulary at all — it is whatever text the operator wrote in .golangci.yml.
func (g *golangciLint) findings(out []byte) ([]Finding, error) {
	var parsed golangciOutput
	if err := decodeJSON(out, &parsed); err != nil {
		return nil, fmt.Errorf("parse golangci-lint output: %w", err)
	}

	findings := make([]Finding, 0, len(parsed.Issues))
	for _, issue := range parsed.Issues {
		findings = append(findings, Finding{
			Path:        filepath.ToSlash(issue.Pos.Filename),
			Line:        issue.Pos.Line,
			Rule:        issue.FromLinter,
			Message:     issue.Text,
			Severity:    mapSeverity(issue.Severity),
			RawSeverity: strings.TrimSpace(issue.Severity),
		})
	}
	return findings, nil
}

// goPackageDirs reduces changed Go files to the package directories to lint.
// golangci-lint works on packages, not files.
func goPackageDirs(files []string) []string {
	seen := map[string]struct{}{}
	var dirs []string

	for _, f := range files {
		if filepath.Ext(f) != ".go" {
			continue
		}

		dir := "./" + filepath.ToSlash(filepath.Dir(f))
		if dir == "./." {
			dir = "./"
		}
		if _, ok := seen[dir]; ok {
			continue
		}
		seen[dir] = struct{}{}
		dirs = append(dirs, dir)
	}

	return dirs
}

// ruff runs the Python linter.
type ruff struct{}

func (r *ruff) Name() string { return "ruff" }

func (r *ruff) Detect(_ context.Context, repoRoot string) bool {
	return available("ruff") &&
		(exists(repoRoot, "pyproject.toml") || exists(repoRoot, "ruff.toml") ||
			exists(repoRoot, "setup.py") || exists(repoRoot, "requirements.txt"))
}

// ruffIssue is one entry of ruff's JSON output.
type ruffIssue struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	Filename string `json:"filename"`
	Location struct {
		Row int `json:"row"`
	} `json:"location"`
}

func (r *ruff) Run(ctx context.Context, repoRoot string, files []string) ([]Finding, error) {
	targets := filterExt(files, ".py", ".pyi")
	if len(targets) == 0 {
		return nil, nil
	}

	args := append([]string{"check", "--output-format", "json", "--no-fix", "--quiet", "--"}, targets...)

	out, err := runCommand(ctx, repoRoot, "ruff", args...)
	if err != nil {
		return nil, err
	}

	var issues []ruffIssue
	if err := decodeJSON(out, &issues); err != nil {
		return nil, fmt.Errorf("parse ruff output: %w", err)
	}

	findings := make([]Finding, 0, len(issues))
	for _, issue := range issues {
		// RawSeverity stays empty because ruff publishes no severity at all:
		// the warning below is entirely this project's choice, and there is no
		// word of ruff's to quote. Downstream that reads as "not recorded",
		// which is the truth, rather than as ruff having said "warning".
		findings = append(findings, Finding{
			Path:     relative(repoRoot, issue.Filename),
			Line:     issue.Location.Row,
			Rule:     issue.Code,
			Message:  issue.Message,
			Severity: config.SeverityWarning,
		})
	}
	return findings, nil
}

// eslint runs the JavaScript and TypeScript linter.
type eslint struct{}

func (e *eslint) Name() string { return "eslint" }

func (e *eslint) Detect(_ context.Context, repoRoot string) bool {
	// Deliberately does NOT consider node_modules/.bin/eslint. That binary
	// comes from the tree under review, and running it would execute
	// attacker-supplied code with the review's credentials in the environment.
	// See resolveBinary.
	return exists(repoRoot, "package.json") && available("eslint")
}

// eslintFile is one file's results in eslint's JSON output.
type eslintFile struct {
	FilePath string `json:"filePath"`
	Messages []struct {
		RuleID   string `json:"ruleId"`
		Severity int    `json:"severity"`
		Message  string `json:"message"`
		Line     int    `json:"line"`
	} `json:"messages"`
}

func (e *eslint) Run(ctx context.Context, repoRoot string, files []string) ([]Finding, error) {
	targets := filterExt(files, ".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs")
	if len(targets) == 0 {
		return nil, nil
	}

	args := append([]string{"--format", "json", "--no-color", "--"}, targets...)

	out, err := runCommand(ctx, repoRoot, "eslint", args...)
	if err != nil {
		return nil, err
	}

	var results []eslintFile
	if err := decodeJSON(out, &results); err != nil {
		return nil, fmt.Errorf("parse eslint output: %w", err)
	}

	var findings []Finding
	for _, file := range results {
		for _, m := range file.Messages {
			severity := config.SeverityWarning
			if m.Severity >= 2 {
				severity = config.SeverityError
			}

			findings = append(findings, Finding{
				Path:        relative(repoRoot, file.FilePath),
				Line:        m.Line,
				Rule:        m.RuleID,
				Message:     m.Message,
				Severity:    severity,
				RawSeverity: strconv.Itoa(m.Severity),
			})
		}
	}
	return findings, nil
}

// semgrep runs the multi-language static analyzer.
type semgrep struct{}

func (s *semgrep) Name() string { return "semgrep" }

func (s *semgrep) Detect(_ context.Context, repoRoot string) bool {
	// Unlike the others, semgrep applies to any language, so its presence on
	// PATH plus a config is enough.
	return available("semgrep") &&
		(exists(repoRoot, ".semgrep.yml") || exists(repoRoot, ".semgrep.yaml") ||
			exists(repoRoot, ".semgrepignore") || exists(repoRoot, ".semgrep"))
}

// semgrepOutput is the subset of semgrep's JSON report that matters.
type semgrepOutput struct {
	Results []struct {
		CheckID string `json:"check_id"`
		Path    string `json:"path"`
		Start   struct {
			Line int `json:"line"`
		} `json:"start"`
		Extra struct {
			Message  string `json:"message"`
			Severity string `json:"severity"`
		} `json:"extra"`
	} `json:"results"`
}

func (s *semgrep) Run(ctx context.Context, repoRoot string, files []string) ([]Finding, error) {
	if len(files) == 0 {
		return nil, nil
	}

	args := append([]string{"--json", "--quiet", "--config", "auto", "--metrics", "off", "--"}, files...)

	out, err := runCommand(ctx, repoRoot, "semgrep", args...)
	if err != nil {
		return nil, err
	}

	return s.findings(out)
}

// findings converts one semgrep report into findings.
//
// Split from Run so that the step which decides what this repository does with
// a severity semgrep printed can be exercised from the analyzer's real output
// rather than from a hand-built Finding. A test that assembles the Finding
// itself has already made the decision under test.
func (s *semgrep) findings(out []byte) ([]Finding, error) {
	var parsed semgrepOutput
	if err := decodeJSON(out, &parsed); err != nil {
		return nil, fmt.Errorf("parse semgrep output: %w", err)
	}

	findings := make([]Finding, 0, len(parsed.Results))
	for _, r := range parsed.Results {
		findings = append(findings, Finding{
			Path:        filepath.ToSlash(r.Path),
			Line:        r.Start.Line,
			Rule:        r.CheckID,
			Message:     r.Extra.Message,
			Severity:    mapSeverity(r.Extra.Severity),
			RawSeverity: strings.TrimSpace(r.Extra.Severity),
		})
	}
	return findings, nil
}

// mapSeverity translates an analyzer's severity vocabulary to ours.
//
// EVERY word here is foreign, including the ones spelled like our levels. There
// is no branch for "the analyzer used our own word, so nothing needs
// translating": semgrep's documented vocabulary is LOW/MEDIUM/HIGH/CRITICAL with
// "the older levels ERROR, WARNING and INFO match HIGH, MEDIUM and LOW", so
// semgrep's ERROR *is* semgrep's HIGH — one level, two spellings, both inside
// semgrep's scale and neither one a statement about ours. Sorting the two into
// "translated" and "untranslated" branches described a distinction that does not
// exist. It is a translation table, so it DESTROYS the analyzer's word: ERROR
// and HIGH both land on error and the result cannot be un-mapped, which is why
// every caller records the original in Finding.RawSeverity and why
// review.Finding.SeverityTranslated is set for all of them.
//
// THE BUG: "CRITICAL" folded onto error, so the codomain excluded critical and
// no analyzer finding could ever BE critical. review.fail_on accepts "critical",
// so a repository configured that way got zero gating from semgrep — the only
// one of the four analyzers whose scale HAS a critical — and from any
// golangci-lint whose severity settings name one. The build went green on a
// finding the analyzer itself called critical, and nothing said so. The
// justification given was that "an unknown value becoming critical would poison
// the gate", which is an argument about UNRECOGNIZED input. An analyzer that
// printed the word critical is not an analyzer that printed something we could
// not parse, and treating the two as one case is what opened the hole.
//
// THE RULE NOW: translate for fidelity, reduce at policy time. The codomain
// covers all five levels review.fail_on accepts, so no gate threshold is
// unreachable by construction. Whether a deterministic tool may fail a build is
// the operator's question and config.Linters.MaxSeverity is where they answer
// it; answering it here answered it for every repository at once and
// permanently. That cuts both ways and the wide side is golangci-lint, whose
// Severity field is whatever text an operator put in .golangci.yml: with
// `severity.default: CRITICAL` a misspell or a typecheck compile error arrives
// as a critical, and only max_severity stops it reaching a critical gate.
//
// NIT is reachable from the literal word and nothing else. golangci-lint emits
// operator text, so "nit" genuinely arrives, and folding it up to info would be
// the same unjustified reduction pointed the other way. It carries a cost the
// operator should know about, because nit is the one level BELOW the default
// review.min_severity of info: a repository with `severity.default: nit` in
// .golangci.yml publishes no Go analyzer findings at all under the default gate.
// That is both configurations doing what they say, and it is why nothing FOREIGN
// is folded onto nit — LOW, INFO and NOTE are the weakest thing these analyzers
// can say and still mean "a rule fired", where nit means "take it or leave it",
// so inventing nits from an analyzer's floor would silence findings nobody asked
// to silence.
//
// AN UNREADABLE WORD is warning, the same as no word at all, because they are
// the same state: this project has no usable severity from the analyzer and
// picks one. Semgrep prints values outside its own documented four (EXPERIMENT),
// and golangci-lint prints anything. Flooring those at info instead — to match
// config.Severity.Normalize, which is where a MODEL's unrecognized word lands —
// looks symmetric and is not: a model was handed our enum and writing outside it
// is that reporter misbehaving, whereas an analyzer was never given our
// vocabulary and an unreadable word is our translation failing. Making our
// failure quieter deletes real findings under any min_severity above info, and
// it would rank "the tool said something we could not read" BELOW "the tool said
// nothing", which is incoherent.
func mapSeverity(s string) config.Severity {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "CRITICAL":
		return config.SeverityCritical
	// Synonyms within one analyzer's scale, not one foreign word and one of
	// ours: semgrep documents ERROR/WARNING/INFO as the older spellings of
	// HIGH/MEDIUM/LOW.
	case "ERROR", "HIGH":
		return config.SeverityError
	case "WARNING", "WARN", "MEDIUM":
		return config.SeverityWarning
	case "INFO", "INFORMATION", "LOW", "NOTE":
		return config.SeverityInfo
	case "NIT":
		return config.SeverityNit
	default:
		return config.SeverityWarning
	}
}

// filterExt keeps files with one of the given extensions.
func filterExt(files []string, exts ...string) []string {
	out := make([]string, 0, len(files))

	for _, f := range files {
		ext := strings.ToLower(filepath.Ext(f))
		for _, want := range exts {
			if ext == want {
				out = append(out, f)
				break
			}
		}
	}
	return out
}

// exists reports whether a path exists inside the repository.
func exists(repoRoot, name string) bool {
	_, err := os.Stat(filepath.Join(repoRoot, name))
	return err == nil
}

// relative converts an absolute analyzer path to a repository-relative one.
func relative(repoRoot, path string) string {
	if rel, err := filepath.Rel(repoRoot, path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(path)
}

// decodeJSON decodes an analyzer's JSON payload out of its stdout.
//
// Analyzers surround their JSON with human-readable noise on both sides:
// deprecation notices before it, and — golangci-lint does this — a "1 issues:"
// summary after it. A streaming decoder reads exactly the first JSON value and
// ignores whatever trails it, which a substring trim cannot do.
func decodeJSON(out []byte, target any) error {
	trimmed := trimToJSON(out)
	if len(trimmed) == 0 {
		return nil
	}

	if err := json.NewDecoder(bytes.NewReader(trimmed)).Decode(target); err != nil {
		return fmt.Errorf("%w (got: %s)", err, snippet(trimmed))
	}
	return nil
}

// trimToJSON drops anything printed before the JSON payload, returning nil when
// there is no JSON at all — which is the normal "analyzer found nothing and
// said so in prose" case rather than an error.
func trimToJSON(out []byte) []byte {
	s := strings.TrimSpace(string(out))
	if s == "" {
		return nil
	}

	start := strings.IndexAny(s, "[{")
	if start < 0 {
		return nil
	}
	return []byte(s[start:])
}

// snippet truncates output for an error message.
func snippet(b []byte) string {
	const max = 200

	s := strings.Join(strings.Fields(string(b)), " ")
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
