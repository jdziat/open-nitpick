package linters

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jdziat/open-nitpick/internal/config"
)

// catalog lists every analyzer beyond the hand-written four.
//
// Order is not significant; CatalogNames sorts. Each entry says which files
// it reads, how its configuration is kept out of the tree, and how the report
// is read. The report formats are the tools' own documented machine formats,
// and every parser returns an error rather than an empty list for output it
// cannot read, so a format change on the tool's side surfaces as "did not
// run" on the pull request rather than as a clean review.
func catalog() []toolSpec {
	return []toolSpec{
		shellcheckSpec(), hadolintSpec(), yamllintSpec(), actionlintSpec(), zizmorSpec(),
		gitleaksSpec(), cppcheckSpec(), luacheckSpec(), dotenvLinterSpec(), checkmakeSpec(),
		sqlfluffSpec(), biomeSpec(), oxlintSpec(), rubocopSpec(), detektSpec(), swiftlintSpec(),
		pmdSpec(), checkovSpec(), tflintSpec(), htmlhintSpec(), bufSpec(), psScriptAnalyzerSpec(),
		markdownlintSpec(), stylelintSpec(), phpstanSpec(), clippySpec(), osvScannerSpec(),
		pylintSpec(), brakemanSpec(),
	}
}

// --- Python and Ruby, second analyzers ------------------------------------------

func pylintSpec() toolSpec {
	type msg struct {
		Type      string `json:"type"`
		Path      string `json:"path"`
		Line      int    `json:"line"`
		Symbol    string `json:"symbol"`
		MessageID string `json:"message-id"`
		Message   string `json:"message"`
	}
	return toolSpec{
		name: "pylint", auto: true, languages: "Python (a second opinion beside ruff)",
		exts:      []string{".py"},
		isolation: isolatedByShipped, shipped: "pylintrc",
		args: func(inv invocation) []string {
			// --rcfile is the isolation: no pylintrc or pyproject from the
			// tree; --disable=all --enable=E,W keeps it to errors and warnings
			// so it does not duplicate ruff's style opinions.
			return append([]string{"--rcfile", inv.config, "--output-format=json", "--score=n", "--reports=n",
				"--disable=all", "--enable=E,W", "--"}, inv.files...)
		},
		parse: func(inv invocation, report []byte, exit int) ([]Finding, error) {
			if strings.TrimSpace(string(report)) == "" {
				return nil, nil
			}
			var msgs []msg
			if err := decodeJSON(report, &msgs); err != nil {
				return nil, fmt.Errorf("parse pylint output: %w", err)
			}
			var findings []Finding
			for _, m := range msgs {
				rule := m.Symbol
				if rule == "" {
					rule = m.MessageID
				}
				sev := config.SeverityWarning
				switch m.Type {
				case "error", "fatal":
					sev = config.SeverityError
				case "convention", "refactor":
					sev = config.SeverityNit
				case "info":
					sev = config.SeverityInfo
				}
				findings = append(findings, Finding{Path: m.Path, Line: m.Line, Rule: rule, Message: m.Message, Severity: sev, RawSeverity: m.Type})
			}
			return findings, nil
		},
	}
}

func brakemanSpec() toolSpec {
	type warning struct {
		WarningType string `json:"warning_type"`
		Code        int    `json:"warning_code"`
		Message     string `json:"message"`
		File        string `json:"file"`
		Line        int    `json:"line"`
		Confidence  string `json:"confidence"`
	}
	return toolSpec{
		name: "brakeman", auto: true, languages: "Ruby on Rails (security)",
		exts: []string{".rb", ".erb", ".haml", ".slim"},
		// Brakeman scans an application, not a file list; it is asked for
		// the whole app and the diff's own filter keeps what landed on a
		// changed line. --config-file and --ignore-config point at shipped
		// files so neither config/brakeman.yml nor config/brakeman.ignore
		// from the tree is read.
		isolation: isolatedByShipped, shipped: "brakeman.yml",
		args: func(inv invocation) []string {
			return []string{"--quiet", "--no-pager", "--no-color", "--no-exit-on-warn", "--no-exit-on-error",
				"--format", "json", "--config-file", inv.config, "--ignore-config", inv.report("brakeman.ignore"),
				"--only-files", strings.Join(inv.files, ","), "--path", inv.repoRoot}
		},
		parse: func(inv invocation, report []byte, exit int) ([]Finding, error) {
			var out struct {
				Warnings []warning `json:"warnings"`
			}
			if err := decodeJSON(report, &out); err != nil {
				return nil, fmt.Errorf("parse brakeman output: %w", err)
			}
			var findings []Finding
			for _, w := range out.Warnings {
				if w.Line <= 0 {
					continue
				}
				sev := config.SeverityWarning
				switch w.Confidence {
				case "High":
					sev = config.SeverityError
				case "Weak":
					sev = config.SeverityInfo
				}
				findings = append(findings, Finding{Path: w.File, Line: w.Line, Rule: w.WarningType, Message: w.Message, Severity: sev, RawSeverity: w.Confidence})
			}
			return findings, nil
		},
	}
}

// --- Shell, containers, configuration, CI ------------------------------------

func shellcheckSpec() toolSpec {
	type comment struct {
		File    string `json:"file"`
		Line    int    `json:"line"`
		Level   string `json:"level"`
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	return toolSpec{
		name: "shellcheck", auto: true, languages: "shell (sh, bash, ksh, dash)",
		exts:      []string{".sh", ".bash", ".ksh", ".dash"},
		isolation: isolatedByFlag,
		args: func(inv invocation) []string {
			// --norc is the isolation: no .shellcheckrc from the tree.
			return append([]string{"--norc", "-f", "json1", "--"}, inv.files...)
		},
		parse: func(inv invocation, report []byte, exit int) ([]Finding, error) {
			var out struct {
				Comments []comment `json:"comments"`
			}
			if err := decodeJSON(report, &out); err != nil {
				return nil, fmt.Errorf("parse shellcheck output: %w", err)
			}
			var findings []Finding
			for _, c := range out.Comments {
				findings = append(findings, Finding{
					Path: c.File, Line: c.Line, Rule: fmt.Sprintf("SC%d", c.Code), Message: c.Message,
					Severity: shellcheckLevel(c.Level), RawSeverity: c.Level,
				})
			}
			return findings, nil
		},
	}
}

func shellcheckLevel(level string) config.Severity {
	switch level {
	case "error":
		return config.SeverityError
	case "warning":
		return config.SeverityWarning
	case "info":
		return config.SeverityInfo
	case "style":
		return config.SeverityNit
	default:
		return config.SeverityWarning
	}
}

func hadolintSpec() toolSpec {
	type issue struct {
		File    string `json:"file"`
		Line    int    `json:"line"`
		Level   string `json:"level"`
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	return toolSpec{
		name: "hadolint", auto: true, languages: "Dockerfile",
		names: []string{"Dockerfile", "Containerfile"}, prefixes: []string{"Dockerfile."}, exts: []string{".dockerfile"},
		isolation: isolatedByShipped, shipped: "hadolint.yaml",
		args: func(inv invocation) []string {
			return append([]string{"--no-color", "-f", "json", "--config", inv.config, "--"}, inv.files...)
		},
		parse: func(inv invocation, report []byte, exit int) ([]Finding, error) {
			var issues []issue
			if err := decodeJSON(report, &issues); err != nil {
				return nil, fmt.Errorf("parse hadolint output: %w", err)
			}
			var findings []Finding
			for _, i := range issues {
				findings = append(findings, Finding{
					Path: i.File, Line: i.Line, Rule: i.Code, Message: i.Message,
					Severity: shellcheckLevel(i.Level), RawSeverity: i.Level,
				})
			}
			return findings, nil
		},
	}
}

func yamllintSpec() toolSpec {
	// path:line:col: [level] message (rule)
	re := regexp.MustCompile(`^(?P<file>[^:]+):(?P<line>\d+):\d+:\s*\[(?P<sev>\w+)\]\s*(?P<msg>.*?)(?:\s*\((?P<rule>[\w-]+)\))?$`)
	return toolSpec{
		name: "yamllint", auto: true, languages: "YAML",
		exts:      []string{".yml", ".yaml"},
		isolation: isolatedByShipped, shipped: "yamllint.yaml",
		args: func(inv invocation) []string {
			return append([]string{"-f", "parsable", "-c", inv.config, "--"}, inv.files...)
		},
		parse: func(inv invocation, report []byte, exit int) ([]Finding, error) {
			f, _ := lineReport{re: re, fallback: config.SeverityWarning}.parse(report)
			return requireReport(f, report, exit, 1)
		},
	}
}

func actionlintSpec() toolSpec {
	type issue struct {
		Message  string `json:"message"`
		Filepath string `json:"filepath"`
		Line     int    `json:"line"`
		Kind     string `json:"kind"`
	}
	return toolSpec{
		name: "actionlint", auto: true, languages: "GitHub Actions workflows",
		globs:     []string{".github/workflows/*.yml", ".github/workflows/*.yaml"},
		isolation: isolatedByShipped, shipped: "actionlint.yaml",
		args: func(inv invocation) []string {
			return append([]string{"-no-color", "-format", "{{json .}}", "-config-file", inv.config, "--"}, inv.files...)
		},
		parse: func(inv invocation, report []byte, exit int) ([]Finding, error) {
			var issues []issue
			if err := decodeJSON(report, &issues); err != nil {
				return nil, fmt.Errorf("parse actionlint output: %w", err)
			}
			var findings []Finding
			for _, i := range issues {
				// actionlint publishes no severity; every report is a
				// workflow that will misbehave, which is a warning on our
				// scale and nothing it said.
				findings = append(findings, Finding{Path: i.Filepath, Line: i.Line, Rule: i.Kind, Message: i.Message, Severity: config.SeverityWarning})
			}
			return findings, nil
		},
	}
}

func zizmorSpec() toolSpec {
	type finding struct {
		Ident          string `json:"ident"`
		Desc           string `json:"desc"`
		Determinations struct {
			Severity string `json:"severity"`
		} `json:"determinations"`
		Locations []struct {
			Symbolic struct {
				Key struct {
					Path string `json:"path"`
				} `json:"key"`
			} `json:"symbolic"`
			Concrete struct {
				Location struct {
					StartPoint struct {
						Row int `json:"row"`
					} `json:"start_point"`
				} `json:"location"`
			} `json:"concrete"`
		} `json:"locations"`
	}
	return toolSpec{
		name: "zizmor", auto: true, languages: "GitHub Actions workflows (security)",
		globs:     []string{".github/workflows/*.yml", ".github/workflows/*.yaml"},
		isolation: isolatedByShipped, shipped: "zizmor.yml",
		args: func(inv invocation) []string {
			return append([]string{"--format", "json", "--no-online-audits", "--config", inv.config, "--"}, inv.files...)
		},
		parse: func(inv invocation, report []byte, exit int) ([]Finding, error) {
			var items []finding
			if err := decodeJSON(report, &items); err != nil {
				return nil, fmt.Errorf("parse zizmor output: %w", err)
			}
			var findings []Finding
			for _, i := range items {
				if len(i.Locations) == 0 {
					continue
				}
				loc := i.Locations[0]
				sev := i.Determinations.Severity
				findings = append(findings, Finding{
					Path: loc.Symbolic.Key.Path, Line: loc.Concrete.Location.StartPoint.Row + 1,
					Rule: i.Ident, Message: i.Desc, Severity: zizmorSeverity(sev), RawSeverity: sev,
				})
			}
			return findings, nil
		},
	}
}

func zizmorSeverity(s string) config.Severity {
	switch strings.ToLower(s) {
	case "high":
		return config.SeverityError
	case "medium":
		return config.SeverityWarning
	case "low":
		return config.SeverityInfo
	case "informational", "unknown":
		return config.SeverityNit
	default:
		return config.SeverityWarning
	}
}

func gitleaksSpec() toolSpec {
	type leak struct {
		Description string `json:"Description"`
		StartLine   int    `json:"StartLine"`
		File        string `json:"File"`
		RuleID      string `json:"RuleID"`
	}
	return toolSpec{
		name: "gitleaks", auto: true, languages: "secrets in any file",
		// Everything text-like; the secret is what matters, not the language.
		exts: []string{".go", ".py", ".js", ".ts", ".tsx", ".jsx", ".rb", ".php", ".java", ".kt", ".rs", ".cs", ".swift", ".scala",
			".yml", ".yaml", ".json", ".toml", ".ini", ".cfg", ".conf", ".env", ".sh", ".bash", ".ps1", ".tf", ".tfvars", ".xml",
			".properties", ".txt", ".md", ".sql", ".c", ".cc", ".cpp", ".h", ".hpp", ".lua", ".pl", ".ex", ".exs", ".dart", ".m", ".mm"},
		names: []string{"Dockerfile", "Makefile", "Jenkinsfile"}, prefixes: []string{".env"},
		isolation: isolatedByShipped, shipped: "gitleaks.toml",
		perFile: true,
		args: func(inv invocation) []string {
			return []string{"detect", "--no-git", "--no-banner", "--exit-code", "0", "--redact",
				"--config", inv.config, "--report-format", "json", "--report-path", inv.report("gitleaks.json"),
				"--source", inv.abs()[0]}
		},
		parse: func(inv invocation, report []byte, exit int) ([]Finding, error) {
			data, err := readJSONReport(inv, "gitleaks.json")
			if err != nil {
				return nil, err
			}
			var leaks []leak
			if strings.TrimSpace(string(data)) == "" {
				return nil, nil
			}
			if err := decodeJSON(data, &leaks); err != nil {
				return nil, fmt.Errorf("parse gitleaks report: %w", err)
			}
			var findings []Finding
			for _, l := range leaks {
				// Never the secret itself: the finding is posted on the pull
				// request, and --redact keeps it out of the report too.
				findings = append(findings, Finding{
					Path: inv.files[0], Line: l.StartLine, Rule: l.RuleID,
					Message:  fmt.Sprintf("%s (a credential appears to be committed here; rotate it and load it from the environment)", l.Description),
					Severity: config.SeverityError,
				})
			}
			return findings, nil
		},
	}
}

// --- C, Lua, env, Makefiles, SQL ---------------------------------------------

func cppcheckSpec() toolSpec {
	re := regexp.MustCompile(`^(?P<file>[^\t]+)\t(?P<line>\d+)\t(?P<sev>\w+)\t(?P<rule>[\w-]+)\t(?P<msg>.*)$`)
	return toolSpec{
		name: "cppcheck", auto: true, languages: "C, C++",
		exts:           []string{".c", ".cc", ".cpp", ".cxx", ".c++", ".h", ".hh", ".hpp", ".hxx"},
		isolation:      noConfig,
		reportOnStderr: true,
		args: func(inv invocation) []string {
			// No --inline-suppr: an inline suppression is the tree's policy.
			// No --project: a project file is the tree's configuration.
			return append([]string{"--quiet", "--enable=warning,performance,portability",
				"--template={file}\t{line}\t{severity}\t{id}\t{message}", "--"}, inv.files...)
		},
		parse: func(inv invocation, report []byte, exit int) ([]Finding, error) {
			return lineReport{re: re, severity: cppcheckSeverity, fallback: config.SeverityWarning}.parse(report)
		},
	}
}

func cppcheckSeverity(s string) config.Severity {
	switch s {
	case "error":
		return config.SeverityError
	case "warning", "portability":
		return config.SeverityWarning
	case "performance", "information":
		return config.SeverityInfo
	case "style":
		return config.SeverityNit
	default:
		return config.SeverityWarning
	}
}

func luacheckSpec() toolSpec {
	// path:line:col: (W211) message
	re := regexp.MustCompile(`^(?P<file>[^:]+):(?P<line>\d+):\d+:\s*\((?P<sev>[WE])(?P<rule>\d+)\)\s*(?P<msg>.*)$`)
	return toolSpec{
		name: "luacheck", auto: true, languages: "Lua",
		exts:      []string{".lua"},
		isolation: isolatedByFlag,
		args: func(inv invocation) []string {
			return append([]string{"--no-config", "--codes", "--no-color", "--formatter", "plain", "--"}, inv.files...)
		},
		parse: func(inv invocation, report []byte, exit int) ([]Finding, error) {
			f, _ := lineReport{re: re, severity: func(w string) config.Severity {
				if w == "E" {
					return config.SeverityError
				}
				return config.SeverityWarning
			}}.parse(report)
			for i := range f {
				f[i].Rule = f[i].RawSeverity + f[i].Rule
			}
			return requireReport(f, report, exit, 1, 2)
		},
	}
}

func dotenvLinterSpec() toolSpec {
	// path:line Code: message
	re := regexp.MustCompile(`^(?P<file>[^:]+):(?P<line>\d+)\s+(?P<rule>\w+):\s*(?P<msg>.*)$`)
	return toolSpec{
		name: "dotenv-linter", auto: true, languages: ".env files",
		prefixes:  []string{".env"},
		isolation: noConfig,
		args: func(inv invocation) []string {
			return append([]string{"--no-color", "--not-check-updates", "--"}, inv.files...)
		},
		parse: func(inv invocation, report []byte, exit int) ([]Finding, error) {
			f, _ := lineReport{re: re, fallback: config.SeverityNit}.parse(report)
			return requireReport(f, report, exit, 1)
		},
	}
}

func checkmakeSpec() toolSpec {
	re := regexp.MustCompile(`^(?P<file>[^\t]+)\t(?P<line>\d+)\t(?P<rule>[\w-]+)\t(?P<msg>.*)$`)
	return toolSpec{
		name: "checkmake", auto: true, languages: "Makefile",
		names: []string{"Makefile", "GNUmakefile"}, exts: []string{".mk"},
		isolation: isolatedByShipped, shipped: "checkmake.ini",
		perFile: true,
		args: func(inv invocation) []string {
			return []string{"--config", inv.config, "--format", "{{.FileName}}\t{{.LineNumber}}\t{{.Rule}}\t{{.Violation}}\n", inv.files[0]}
		},
		parse: func(inv invocation, report []byte, exit int) ([]Finding, error) {
			// Output the parser cannot read is an error, as it is for every
			// other parser here: dropping it published a failed run as a
			// clean Makefile.
			f, err := lineReport{re: re, fallback: config.SeverityNit}.parse(report)
			if err != nil {
				return nil, err
			}
			for i := range f {
				if f[i].Path == "" {
					f[i].Path = inv.files[0]
				}
			}
			return f, nil
		},
	}
}

func sqlfluffSpec() toolSpec {
	type violation struct {
		Line        int    `json:"start_line_no"`
		LegacyLine  int    `json:"line_no"`
		Code        string `json:"code"`
		Description string `json:"description"`
		Name        string `json:"name"`
	}
	type file struct {
		Filepath   string      `json:"filepath"`
		Violations []violation `json:"violations"`
	}
	return toolSpec{
		name: "sqlfluff", auto: true, languages: "SQL",
		exts:      []string{".sql"},
		isolation: isolatedByShipped, shipped: "sqlfluff.cfg",
		args: func(inv invocation) []string {
			return append([]string{"lint", "--format", "json", "--nocolor", "--disable-progress-bar",
				"--ignore-local-config", "--config", inv.config, "--"}, inv.files...)
		},
		parse: func(inv invocation, report []byte, exit int) ([]Finding, error) {
			var files []file
			if err := decodeJSON(report, &files); err != nil {
				return nil, fmt.Errorf("parse sqlfluff output: %w", err)
			}
			var findings []Finding
			for _, f := range files {
				for _, v := range f.Violations {
					// PRS and TMP are sqlfluff saying it could not parse or
					// template the file, almost always the wrong dialect for
					// this repository, which is a fact about the analyzer's
					// configuration, not a finding about the change. Publishing
					// "unparsable SQL" on a valid migration is noise the
					// operator fixes with linters.configs.sqlfluff.
					if strings.HasPrefix(v.Code, "PRS") || strings.HasPrefix(v.Code, "TMP") {
						continue
					}
					line := v.Line
					if line == 0 {
						line = v.LegacyLine
					}
					findings = append(findings, Finding{Path: f.Filepath, Line: line, Rule: v.Code, Message: v.Description, Severity: config.SeverityNit})
				}
			}
			return findings, nil
		},
	}
}

// --- JavaScript and friends ---------------------------------------------------

func biomeSpec() toolSpec {
	// ::error title=lint/suspicious/noDoubleEquals,file=src/a.ts,line=3,endLine=3,col=5,endColumn=7::message
	re := regexp.MustCompile(`^::(?P<sev>\w+) title=(?P<rule>[^,]+),file=(?P<file>[^,]+),line=(?P<line>\d+)[^:]*::(?P<msg>.*)$`)
	return toolSpec{
		name: "biome", auto: true, languages: "JavaScript, TypeScript, JSX, TSX, JSON, CSS",
		exts:      []string{".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs", ".json", ".jsonc", ".css"},
		isolation: isolatedByShipped, shipped: "biome.json", configAsDir: true,
		args: func(inv invocation) []string {
			return append([]string{"lint", "--reporter=github", "--colors=off", "--config-path", inv.config, "--"}, inv.files...)
		},
		parse: func(inv invocation, report []byte, exit int) ([]Finding, error) {
			f, _ := lineReport{re: re, severity: func(w string) config.Severity {
				switch w {
				case "error":
					return config.SeverityWarning // a lint error is a warning on our scale; biome's own bar is lower than a compile error
				case "notice":
					return config.SeverityInfo
				default:
					return config.SeverityWarning
				}
			}}.parse(report)
			for i := range f {
				f[i].Message = strings.ReplaceAll(f[i].Message, "%0A", " ")
			}
			return requireReport(f, report, exit, 1)
		},
	}
}

func oxlintSpec() toolSpec {
	// path:line:col: message [severity/rule]   or   path:line:col: message [rule]
	re := regexp.MustCompile(`^(?P<file>[^:]+):(?P<line>\d+):\d+:\s*(?P<msg>.*?)\s*\[(?:(?P<sev>\w+)/)?(?P<rule>[^\]]+)\]$`)
	return toolSpec{
		name: "oxlint", auto: true, languages: "JavaScript, TypeScript, JSX, TSX",
		exts:      []string{".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs"},
		isolation: isolatedByShipped, shipped: ".oxlintrc.json",
		args: func(inv invocation) []string {
			return append([]string{"-f", "unix", "-c", inv.config, "--"}, inv.files...)
		},
		parse: func(inv invocation, report []byte, exit int) ([]Finding, error) {
			f, _ := lineReport{re: re, fallback: config.SeverityWarning}.parse(report)
			return requireReport(f, report, exit, 1)
		},
	}
}

func stylelintSpec() toolSpec {
	type warning struct {
		Line     int    `json:"line"`
		Rule     string `json:"rule"`
		Severity string `json:"severity"`
		Text     string `json:"text"`
	}
	type result struct {
		Source   string    `json:"source"`
		Warnings []warning `json:"warnings"`
	}
	return toolSpec{
		name: "stylelint", languages: "CSS, SCSS, Less",
		exts: []string{".css", ".scss", ".less", ".sass"},
		// operatorOnly: a stylelint config may be JavaScript, and the tool
		// loads plugins the config names, so a config from the tree is code
		// from the tree.
		isolation: operatorOnly,
		args: func(inv invocation) []string {
			return append([]string{"--formatter", "json", "--no-color", "--config", inv.config, "--"}, inv.files...)
		},
		parse: func(inv invocation, report []byte, exit int) ([]Finding, error) {
			var results []result
			if err := decodeJSON(report, &results); err != nil {
				return nil, fmt.Errorf("parse stylelint output: %w", err)
			}
			var findings []Finding
			for _, r := range results {
				for _, w := range r.Warnings {
					findings = append(findings, Finding{Path: r.Source, Line: w.Line, Rule: w.Rule, Message: w.Text, Severity: mapSeverity(w.Severity), RawSeverity: w.Severity})
				}
			}
			return findings, nil
		},
	}
}

func htmlhintSpec() toolSpec {
	type message struct {
		Line    int    `json:"line"`
		Type    string `json:"type"`
		Message string `json:"message"`
		Rule    struct {
			ID string `json:"id"`
		} `json:"rule"`
	}
	type file struct {
		File     string    `json:"file"`
		Messages []message `json:"messages"`
	}
	return toolSpec{
		name: "htmlhint", auto: true, languages: "HTML",
		exts:      []string{".html", ".htm"},
		isolation: isolatedByShipped, shipped: ".htmlhintrc",
		args: func(inv invocation) []string {
			return append([]string{"--format", "json", "--nocolor", "--config", inv.config}, inv.files...)
		},
		parse: func(inv invocation, report []byte, exit int) ([]Finding, error) {
			var files []file
			if err := decodeJSON(report, &files); err != nil {
				return nil, fmt.Errorf("parse htmlhint output: %w", err)
			}
			var findings []Finding
			for _, f := range files {
				for _, m := range f.Messages {
					findings = append(findings, Finding{Path: f.File, Line: m.Line, Rule: m.Rule.ID, Message: m.Message, Severity: mapSeverity(m.Type), RawSeverity: m.Type})
				}
			}
			return findings, nil
		},
	}
}

func markdownlintSpec() toolSpec {
	type issue struct {
		FileName        string   `json:"fileName"`
		LineNumber      int      `json:"lineNumber"`
		RuleNames       []string `json:"ruleNames"`
		RuleDescription string   `json:"ruleDescription"`
		ErrorDetail     string   `json:"errorDetail"`
	}
	return toolSpec{
		name: "markdownlint", languages: "Markdown",
		exts:      []string{".md", ".markdown"},
		isolation: isolatedByShipped, shipped: ".markdownlint.json",
		args: func(inv invocation) []string {
			return append([]string{"--json", "--config", inv.config, "--"}, inv.files...)
		},
		reportOnStderr: true, // markdownlint-cli prints --json results to stderr
		parse: func(inv invocation, report []byte, exit int) ([]Finding, error) {
			if strings.TrimSpace(string(report)) == "" {
				return nil, nil
			}
			var issues []issue
			if err := decodeJSON(report, &issues); err != nil {
				return nil, fmt.Errorf("parse markdownlint output: %w", err)
			}
			var findings []Finding
			for _, i := range issues {
				rule := ""
				if len(i.RuleNames) > 0 {
					rule = i.RuleNames[0]
				}
				msg := i.RuleDescription
				if i.ErrorDetail != "" {
					msg += ": " + i.ErrorDetail
				}
				findings = append(findings, Finding{Path: i.FileName, Line: i.LineNumber, Rule: rule, Message: msg, Severity: config.SeverityNit})
			}
			return findings, nil
		},
	}
}

// --- Ruby, Kotlin, Swift, Java, PHP, Rust --------------------------------------

func rubocopSpec() toolSpec {
	type offense struct {
		Severity string `json:"severity"`
		Message  string `json:"message"`
		CopName  string `json:"cop_name"`
		Location struct {
			Line int `json:"line"`
		} `json:"location"`
	}
	type file struct {
		Path     string    `json:"path"`
		Offenses []offense `json:"offenses"`
	}
	return toolSpec{
		name: "rubocop", auto: true, languages: "Ruby",
		exts: []string{".rb", ".rake", ".gemspec"}, names: []string{"Gemfile", "Rakefile"},
		isolation: isolatedByShipped, shipped: ".rubocop.yml",
		args: func(inv invocation) []string {
			return append([]string{"--format", "json", "--no-color", "--config", inv.config, "--disable-pending-cops", "--"}, inv.files...)
		},
		parse: func(inv invocation, report []byte, exit int) ([]Finding, error) {
			var out struct {
				Files []file `json:"files"`
			}
			if err := decodeJSON(report, &out); err != nil {
				return nil, fmt.Errorf("parse rubocop output: %w", err)
			}
			var findings []Finding
			for _, f := range out.Files {
				for _, o := range f.Offenses {
					findings = append(findings, Finding{Path: f.Path, Line: o.Location.Line, Rule: o.CopName, Message: o.Message, Severity: rubocopSeverity(o.Severity), RawSeverity: o.Severity})
				}
			}
			return findings, nil
		},
	}
}

func rubocopSeverity(s string) config.Severity {
	switch s {
	case "fatal", "error":
		return config.SeverityError
	case "warning":
		return config.SeverityWarning
	case "info":
		return config.SeverityInfo
	case "convention", "refactor":
		return config.SeverityNit
	default:
		return config.SeverityWarning
	}
}

func detektSpec() toolSpec {
	return toolSpec{
		name: "detekt", auto: true, languages: "Kotlin",
		exts:      []string{".kt", ".kts"},
		isolation: isolatedByShipped, shipped: "detekt.yml",
		args: func(inv invocation) []string {
			return []string{"--input", strings.Join(inv.abs(), ","), "--config", inv.config, "--build-upon-default-config",
				"--report", "sarif:" + inv.report("detekt.sarif")}
		},
		parse: func(inv invocation, report []byte, exit int) ([]Finding, error) {
			data, err := readJSONReport(inv, "detekt.sarif")
			if err != nil {
				return nil, err
			}
			return parseSARIF(data, config.SeverityWarning)
		},
	}
}

func swiftlintSpec() toolSpec {
	type violation struct {
		File           string `json:"file"`
		Line           int    `json:"line"`
		Severity       string `json:"severity"`
		RuleID         string `json:"rule_id"`
		Reason         string `json:"reason"`
		RuleIdentifier string `json:"rule_identifier"`
	}
	return toolSpec{
		name: "swiftlint", auto: true, languages: "Swift",
		exts:      []string{".swift"},
		isolation: isolatedByShipped, shipped: ".swiftlint.yml",
		args: func(inv invocation) []string {
			return append([]string{"lint", "--quiet", "--reporter", "json", "--no-cache", "--config", inv.config, "--"}, inv.files...)
		},
		parse: func(inv invocation, report []byte, exit int) ([]Finding, error) {
			var vs []violation
			if err := decodeJSON(report, &vs); err != nil {
				return nil, fmt.Errorf("parse swiftlint output: %w", err)
			}
			var findings []Finding
			for _, v := range vs {
				rule := v.RuleID
				if rule == "" {
					rule = v.RuleIdentifier
				}
				findings = append(findings, Finding{Path: v.File, Line: v.Line, Rule: rule, Message: v.Reason, Severity: mapSeverity(v.Severity), RawSeverity: v.Severity})
			}
			return findings, nil
		},
	}
}

func pmdSpec() toolSpec {
	return toolSpec{
		name: "pmd", auto: true, languages: "Java",
		exts:      []string{".java"},
		isolation: isolatedByShipped, shipped: "pmd-ruleset.xml",
		args: func(inv invocation) []string {
			return []string{"check", "--no-cache", "--no-progress", "-f", "sarif", "-R", inv.config, "-r", inv.report("pmd.sarif"),
				"-d", strings.Join(inv.abs(), ",")}
		},
		parse: func(inv invocation, report []byte, exit int) ([]Finding, error) {
			data, err := readJSONReport(inv, "pmd.sarif")
			if err != nil {
				return nil, err
			}
			findings, err := parseSARIF(data, config.SeverityWarning)
			if err != nil {
				return nil, err
			}
			for i := range findings {
				// PMD's SARIF level is "warning" for every priority; the
				// priority is in the rule's properties, which parseSARIF
				// does not carry. Everything PMD says is a warning at most.
				if findings[i].Severity.Rank() > config.SeverityWarning.Rank() {
					findings[i].Severity = config.SeverityWarning
				}
			}
			return findings, nil
		},
	}
}

func phpstanSpec() toolSpec {
	return toolSpec{
		name: "phpstan", languages: "PHP",
		exts: []string{".php"},
		// operatorOnly AND trusted: phpstan needs a configuration naming the
		// project's autoloader, and running it loads that autoloader.
		isolation: operatorOnly, trusted: true,
		args: func(inv invocation) []string {
			return append([]string{"analyse", "--error-format", "json", "--no-progress", "--no-ansi", "--no-interaction", "-c", inv.config, "--"}, inv.files...)
		},
		parse: func(inv invocation, report []byte, exit int) ([]Finding, error) {
			var out struct {
				Files map[string]struct {
					Messages []struct {
						Message    string `json:"message"`
						Line       int    `json:"line"`
						Identifier string `json:"identifier"`
					} `json:"messages"`
				} `json:"files"`
			}
			if err := decodeJSON(report, &out); err != nil {
				return nil, fmt.Errorf("parse phpstan output: %w", err)
			}
			var findings []Finding
			for path, f := range out.Files {
				for _, m := range f.Messages {
					findings = append(findings, Finding{Path: path, Line: m.Line, Rule: m.Identifier, Message: m.Message, Severity: config.SeverityWarning})
				}
			}
			return findings, nil
		},
	}
}

func clippySpec() toolSpec {
	return toolSpec{
		name: "clippy", binary: "cargo", languages: "Rust",
		exts: []string{".rs"},
		// trusted: cargo runs build scripts and procedural macros from the
		// tree. A config outside the tree does not change that.
		isolation: noConfig, trusted: true,
		args: func(inv invocation) []string {
			return []string{"clippy", "--quiet", "--message-format", "json", "--", "-W", "clippy::all"}
		},
		parse: func(inv invocation, report []byte, exit int) ([]Finding, error) {
			var findings []Finding
			err := jsonLines(report, func(raw json.RawMessage) {
				var rec struct {
					Reason  string `json:"reason"`
					Message struct {
						Level string `json:"level"`
						Code  *struct {
							Code string `json:"code"`
						} `json:"code"`
						Message string `json:"message"`
						Spans   []struct {
							FileName  string `json:"file_name"`
							LineStart int    `json:"line_start"`
							IsPrimary bool   `json:"is_primary"`
						} `json:"spans"`
					} `json:"message"`
				}
				if json.Unmarshal(raw, &rec) != nil || rec.Reason != "compiler-message" {
					return
				}
				for _, s := range rec.Message.Spans {
					if !s.IsPrimary {
						continue
					}
					rule := ""
					if rec.Message.Code != nil {
						rule = rec.Message.Code.Code
					}
					findings = append(findings, Finding{Path: s.FileName, Line: s.LineStart, Rule: rule, Message: rec.Message.Message, Severity: mapSeverity(rec.Message.Level), RawSeverity: rec.Message.Level})
					break
				}
			})
			return findings, err
		},
	}
}

// --- Infrastructure -------------------------------------------------------------

func checkovSpec() toolSpec {
	return toolSpec{
		name: "checkov", auto: true, languages: "Terraform, CloudFormation, Kubernetes, Helm, Docker Compose, Dockerfile, ARM, Bicep",
		exts: []string{".tf", ".tfvars", ".bicep"}, names: []string{"Dockerfile"}, prefixes: []string{"Dockerfile.", "docker-compose"},
		globs:     []string{"**/k8s/**/*.y{,a}ml", "**/kubernetes/**/*.y{,a}ml", "**/manifests/**/*.y{,a}ml", "**/helm/**/*.y{,a}ml", "**/charts/**/*.y{,a}ml", "**/templates/**/*.y{,a}ml", "**/cloudformation/**/*.{yml,yaml,json}"},
		isolation: isolatedByShipped, shipped: "checkov.yaml",
		args: func(inv invocation) []string {
			_ = os.MkdirAll(inv.report("checkov"), 0o700)
			args := []string{"--quiet", "--skip-download", "--config-file", inv.config, "--output", "sarif", "--output-file-path", inv.report("checkov")}
			for _, f := range inv.abs() {
				args = append(args, "-f", f)
			}
			return args
		},
		parse: func(inv invocation, report []byte, exit int) ([]Finding, error) {
			data, err := readReportDir(inv, "checkov")
			if err != nil {
				return nil, err
			}
			return parseSARIF(data, config.SeverityWarning)
		},
	}
}

func tflintSpec() toolSpec {
	return toolSpec{
		name: "tflint", auto: true, languages: "Terraform",
		exts:      []string{".tf", ".tfvars"},
		isolation: isolatedByShipped, shipped: ".tflint.hcl",
		args: func(inv invocation) []string {
			// The filter is matched against the path tflint reports, which
			// is relative to the directory it runs in (the repository root
			// here), so the repository-relative path is what matches; a
			// basename matched nothing below the root and collided across
			// directories. Modules below the root still need tflint's own
			// --recursive or --chdir to be inspected at all, which this
			// spec does not add: unverified against a real run.
			args := []string{"--format", "sarif", "--no-color", "--config", inv.config}
			for _, f := range inv.files {
				args = append(args, "--filter", filepath.ToSlash(f))
			}
			return args
		},
		parse: func(inv invocation, report []byte, exit int) ([]Finding, error) {
			return parseSARIF(report, config.SeverityWarning)
		},
	}
}

func osvScannerSpec() toolSpec {
	return toolSpec{
		name: "osv-scanner", languages: "dependency lockfiles (vulnerabilities; queries osv.dev)",
		names: []string{"package-lock.json", "yarn.lock", "pnpm-lock.yaml", "go.mod", "Cargo.lock", "poetry.lock", "Pipfile.lock",
			"requirements.txt", "Gemfile.lock", "composer.lock", "pubspec.lock", "packages.lock.json", "gradle.lockfile", "pom.xml"},
		isolation: isolatedByShipped, shipped: "osv-scanner.toml",
		perFile: true,
		args: func(inv invocation) []string {
			return []string{"scan", "source", "--format", "sarif", "--config", inv.config, "--lockfile", inv.abs()[0]}
		},
		parse: func(inv invocation, report []byte, exit int) ([]Finding, error) {
			findings, err := parseSARIF(report, config.SeverityWarning)
			if err != nil {
				return nil, err
			}
			// The scanner repeats an advisory once per path by which the
			// package is reachable (measured: 7 results for 4 advisories on
			// a go.mod with one require), and a report that lists the same
			// CVE twice reads as two. One per advisory and message.
			seen := map[string]bool{}
			out := findings[:0]
			for _, f := range findings {
				// A vulnerable dependency is reported against the lockfile,
				// on line 1 when the scanner gives none, so the finding is
				// placeable on a lockfile bump.
				if f.Line <= 0 {
					f.Line = 1
				}
				f.Path = inv.files[0]
				key := f.Rule + "\x00" + f.Message
				if seen[key] {
					continue
				}
				seen[key] = true
				out = append(out, f)
			}
			return out, nil
		},
	}
}

func bufSpec() toolSpec {
	return toolSpec{
		name: "buf", auto: true, languages: "Protocol Buffers",
		exts:      []string{".proto"},
		isolation: isolatedByShipped, shipped: "buf.yaml",
		args: func(inv invocation) []string {
			args := []string{"lint", "--error-format", "json", "--config", inv.config}
			for _, f := range inv.files {
				args = append(args, "--path", f)
			}
			return args
		},
		parse: func(inv invocation, report []byte, exit int) ([]Finding, error) {
			var findings []Finding
			err := jsonLines(report, func(raw json.RawMessage) {
				var rec struct {
					Path      string `json:"path"`
					StartLine int    `json:"start_line"`
					Type      string `json:"type"`
					Message   string `json:"message"`
				}
				if json.Unmarshal(raw, &rec) != nil || rec.Path == "" {
					return
				}
				findings = append(findings, Finding{Path: rec.Path, Line: rec.StartLine, Rule: rec.Type, Message: rec.Message, Severity: config.SeverityNit})
			})
			return findings, err
		},
	}
}

func psScriptAnalyzerSpec() toolSpec {
	return toolSpec{
		name: "psscriptanalyzer", auto: true, binary: "pwsh", languages: "PowerShell",
		exts:      []string{".ps1", ".psm1", ".psd1"},
		isolation: noConfig,
		perFile:   true,
		args: func(inv invocation) []string {
			return []string{"-NoProfile", "-NonInteractive", "-Command",
				"Invoke-ScriptAnalyzer -Path '" + strings.ReplaceAll(inv.abs()[0], "'", "''") + "' | Select-Object RuleName,Severity,Line,Message | ConvertTo-Json -AsArray -Depth 2"}
		},
		parse: func(inv invocation, report []byte, exit int) ([]Finding, error) {
			if strings.TrimSpace(string(report)) == "" {
				return nil, nil
			}
			var recs []struct {
				RuleName string `json:"RuleName"`
				Severity any    `json:"Severity"`
				Line     int    `json:"Line"`
				Message  string `json:"Message"`
			}
			if err := decodeJSON(report, &recs); err != nil {
				return nil, fmt.Errorf("parse PSScriptAnalyzer output: %w", err)
			}
			var findings []Finding
			for _, r := range recs {
				word := fmt.Sprint(r.Severity)
				sev := config.SeverityWarning
				switch strings.ToLower(word) {
				case "error", "2":
					sev = config.SeverityError
				case "information", "0":
					sev = config.SeverityInfo
				}
				findings = append(findings, Finding{Path: inv.files[0], Line: r.Line, Rule: r.RuleName, Message: r.Message, Severity: sev, RawSeverity: word})
			}
			return findings, nil
		},
	}
}
