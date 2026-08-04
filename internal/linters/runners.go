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

// mapSeverity translates an analyzer's severity vocabulary to ours. Analyzers
// disagree wildly here, and an unknown value becoming "critical" would poison
// the gate, so anything unrecognized becomes a warning.
//
// It DESTROYS the analyzer's word, which is why every caller records the
// original in Finding.RawSeverity beside the result. Four analyzers' spellings
// collapse onto three levels here — "HIGH", "ERROR" and "CRITICAL" all land on
// error — so the mapped value cannot be un-mapped, and a report that quotes it
// as the analyzer's own has invented a quotation.
func mapSeverity(s string) config.Severity {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "ERROR", "HIGH", "CRITICAL":
		return config.SeverityError
	case "WARNING", "WARN", "MEDIUM":
		return config.SeverityWarning
	case "INFO", "INFORMATION", "LOW", "NOTE":
		return config.SeverityInfo
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
