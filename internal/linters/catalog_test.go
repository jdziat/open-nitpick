package linters

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
)

// specByName finds a catalog entry.
func specByName(t *testing.T, name string) toolSpec {
	t.Helper()
	for _, s := range catalog() {
		if s.name == name {
			return s
		}
	}
	t.Fatalf("no catalog tool named %s", name)
	return toolSpec{}
}

// TestEveryCatalogToolIsWellFormed: a spec with no way to select files, no
// parser, or a shipped config that does not exist would fail at review time,
// on a pull request, for everybody.
func TestEveryCatalogToolIsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range catalog() {
		if seen[s.name] {
			t.Errorf("%s: listed twice", s.name)
		}
		seen[s.name] = true
		if len(s.exts)+len(s.names)+len(s.prefixes)+len(s.globs) == 0 {
			t.Errorf("%s: selects no files, so it can never run", s.name)
		}
		if s.args == nil || s.parse == nil {
			t.Errorf("%s: has no args or no parser", s.name)
		}
		if s.isolation == isolatedByShipped {
			if s.shipped == "" {
				t.Errorf("%s: isolatedByShipped with no shipped config named", s.name)
			} else if _, err := shippedConfigs.ReadFile("configs/" + s.shipped); err != nil {
				t.Errorf("%s: shipped config %s is not embedded: %v", s.name, s.shipped, err)
			}
		}
		if s.languages == "" {
			t.Errorf("%s: says nothing about what it covers", s.name)
		}
	}

	auto := 0
	for _, s := range catalog() {
		if s.auto {
			auto++
		}
		if s.auto && (s.trusted || s.isolation == operatorOnly) {
			t.Errorf("%s: auto-detected but executes tree code or needs an operator config", s.name)
		}
	}
	if auto < 15 {
		t.Errorf("only %d catalog tools are auto-detected", auto)
	}
	if !slices.Equal(config.DefaultLinters, []string{"golangci-lint", "ruff"}) {
		t.Errorf("DefaultLinters = %v; the catalog is auto-detected, not enabled, so that strict mode stays usable", config.DefaultLinters)
	}
}

func TestCatalogToolsSelectTheRightFiles(t *testing.T) {
	cases := map[string][]string{
		"shellcheck":       {"deploy.sh", "bin/run.bash"},
		"hadolint":         {"Dockerfile", "build/Dockerfile.ci", "images/api.dockerfile", "Containerfile"},
		"actionlint":       {".github/workflows/ci.yml"},
		"zizmor":           {".github/workflows/release.yaml"},
		"gitleaks":         {"config.yaml", ".env.production", "main.go"},
		"cppcheck":         {"src/a.c", "src/b.hpp"},
		"checkmake":        {"Makefile", "build/rules.mk"},
		"rubocop":          {"app/models/user.rb", "Gemfile", "Rakefile"},
		"checkov":          {"infra/main.tf", "k8s/deploy.yaml", "docker-compose.yml"},
		"osv-scanner":      {"package-lock.json", "go.mod", "Cargo.lock"},
		"dotenv-linter":    {".env", ".env.local"},
		"clippy":           {"src/lib.rs"},
		"psscriptanalyzer": {"scripts/Deploy.ps1"},
	}
	for name, want := range cases {
		s := specByName(t, name)
		for _, f := range want {
			if !s.matches(f) {
				t.Errorf("%s does not select %s", name, f)
			}
		}
		for _, f := range []string{"photo.png", "notes/todo"} {
			if s.matches(f) {
				t.Errorf("%s selects %s", name, f)
			}
		}
	}
	if specByName(t, "actionlint").matches("workflows/ci.yml") {
		t.Error("actionlint selected a workflow outside .github/workflows")
	}
	if specByName(t, "checkov").matches("docs/config.yaml") {
		t.Error("checkov selected a YAML file with no infrastructure path")
	}
}

func TestCatalogDetectRefusesWhatItMust(t *testing.T) {
	ctx := context.Background()

	// A trusted tool without linters.trusted.
	clippy := &catalogTool{spec: ptr(specByName(t, "clippy"))}
	if err := clippy.Detect(ctx, t.TempDir(), []string{"src/main.rs"}); err == nil || !strings.Contains(err.Error(), "linters.trusted") {
		t.Errorf("clippy must be refused without linters.trusted, got %v", err)
	}

	// An operator-only tool without a config.
	stylelint := &catalogTool{spec: ptr(specByName(t, "stylelint"))}
	if err := stylelint.Detect(ctx, t.TempDir(), []string{"a.css"}); err == nil || !strings.Contains(err.Error(), "linters.configs.stylelint") {
		t.Errorf("stylelint must be refused without a config, got %v", err)
	}

	// No targets is not a refusal.
	sc := &catalogTool{spec: ptr(specByName(t, "shellcheck"))}
	if err := sc.Detect(ctx, t.TempDir(), []string{"main.go"}); err != errNoTargets {
		t.Errorf("shellcheck on a Go change: %v, want errNoTargets", err)
	}

	// A refused config refuses the tool, and does not fall back to isolated.
	repo := t.TempDir()
	inside := filepath.Join(repo, ".hadolint.yaml")
	_ = os.WriteFile(inside, []byte("ignored: []\n"), 0o644)
	had := &catalogTool{spec: ptr(specByName(t, "hadolint")), cfg: fileConfig(repo, inside)}
	if err := had.Detect(ctx, repo, []string{"Dockerfile"}); err == nil || !strings.Contains(err.Error(), "inside the repository") {
		t.Errorf("a config inside the repository must refuse the tool, got %v", err)
	}
	if !strings.HasPrefix(had.State(), "did not run") {
		t.Errorf("state = %q", had.State())
	}
}

func ptr(s toolSpec) *toolSpec { return &s }

// stubTool puts a script named bin on PATH that records its argv and prints
// out on the given stream, and returns the argv file.
func stubTool(t *testing.T, bin, out string, toStderr bool, exit int) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("stub scripts need a POSIX shell")
	}
	dir := t.TempDir()
	argv := filepath.Join(dir, "argv")
	stream := ""
	if toStderr {
		stream = " >&2"
	}
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argv + "\ncat <<'REPORT'" + stream + "\n" + out + "\nREPORT\nexit " + itoa(exit) + "\n"
	if err := os.WriteFile(filepath.Join(dir, bin), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return argv
}

func itoa(n int) string { return strconv.Itoa(n) }

// TestShippedConfigIsWrittenOutsideTheRepository: the whole point of the
// catalog's isolation is that the config a tool reads is one the change under
// review cannot have written.
func TestShippedConfigIsWrittenOutsideTheRepository(t *testing.T) {
	argv := stubTool(t, "hadolint", `[{"file":"Dockerfile","line":3,"level":"warning","code":"DL3008","message":"Pin versions in apt get install."}]`, false, 1)
	repo := t.TempDir()
	tool := &catalogTool{spec: ptr(specByName(t, "hadolint"))}

	findings, err := tool.Run(context.Background(), repo, []string{"Dockerfile", "main.go"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(findings) != 1 || findings[0].Path != "Dockerfile" || findings[0].Line != 3 || findings[0].Rule != "DL3008" || findings[0].Severity != config.SeverityWarning || findings[0].RawSeverity != "warning" {
		t.Errorf("findings = %+v", findings)
	}

	args, _ := os.ReadFile(argv)
	lines := strings.Split(strings.TrimSpace(string(args)), "\n")
	i := slices.Index(lines, "--config")
	if i < 0 || i+1 >= len(lines) {
		t.Fatalf("hadolint was not handed a config: %v", lines)
	}
	cfg := lines[i+1]
	if !filepath.IsAbs(cfg) || strings.HasPrefix(cfg, repo) {
		t.Errorf("config %q must be absolute and outside %s", cfg, repo)
	}
	if lines[len(lines)-1] != "Dockerfile" || slices.Contains(lines, "main.go") {
		t.Errorf("targets = %v; only the Dockerfile is hadolint's", lines)
	}
	if tool.State() != "isolated: open-nitpick's own analyzer config" {
		t.Errorf("state = %q", tool.State())
	}
}

func TestReportOnStderrIsRead(t *testing.T) {
	stubTool(t, "cppcheck", "src/a.c\t12\terror\tnullPointer\tNull pointer dereference: p", true, 0)
	tool := &catalogTool{spec: ptr(specByName(t, "cppcheck"))}
	findings, err := tool.Run(context.Background(), t.TempDir(), []string{"src/a.c"})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Line != 12 || findings[0].Severity != config.SeverityError || findings[0].Rule != "nullPointer" {
		t.Errorf("findings = %+v", findings)
	}
}

func TestUnreadableReportIsAnErrorNotACleanRun(t *testing.T) {
	stubTool(t, "shellcheck", "shellcheck: unexpected flag --json1", false, 2)
	tool := &catalogTool{spec: ptr(specByName(t, "shellcheck"))}
	if _, err := tool.Run(context.Background(), t.TempDir(), []string{"a.sh"}); err == nil {
		t.Fatal("output the parser cannot read must be an error, or a broken tool reads as a clean review")
	}

	// A line-format tool that exits "found something" and prints nothing
	// readable is the same case.
	stubTool(t, "yamllint", "yamllint: error: unrecognized arguments", false, 1)
	tool = &catalogTool{spec: ptr(specByName(t, "yamllint"))}
	if _, err := tool.Run(context.Background(), t.TempDir(), []string{"a.yml"}); err == nil {
		t.Fatal("exit 1 with no parseable findings must be an error")
	}
}

// Parser samples, in each tool's documented machine format.
func TestCatalogParsers(t *testing.T) {
	inv := invocation{repoRoot: "/repo", files: []string{"x"}}
	cases := []struct {
		tool     string
		report   string
		exit     int
		wantPath string
		wantLine int
		wantSev  config.Severity
		wantRaw  string
	}{
		{"shellcheck", `{"comments":[{"file":"run.sh","line":7,"endLine":7,"column":5,"level":"style","code":2086,"message":"Double quote to prevent globbing."}]}`, 1, "run.sh", 7, config.SeverityNit, "style"},
		{"yamllint", "ci.yml:4:1: [error] too many spaces inside brackets (brackets)\nci.yml:9:81: [warning] line too long (81 > 80 characters) (line-length)", 1, "ci.yml", 4, config.SeverityError, "error"},
		{"actionlint", `[{"message":"property \"foo\" is not defined","filepath":".github/workflows/ci.yml","line":12,"column":9,"kind":"expression"}]`, 1, ".github/workflows/ci.yml", 12, config.SeverityWarning, ""},
		{"zizmor", `[{"ident":"template-injection","desc":"code injection via template expansion","determinations":{"confidence":"High","severity":"High"},"locations":[{"symbolic":{"key":{"path":".github/workflows/ci.yml"}},"concrete":{"location":{"start_point":{"row":9,"column":6}}}}]}]`, 14, ".github/workflows/ci.yml", 10, config.SeverityError, "High"},
		{"luacheck", "src/a.lua:3:7: (W211) unused variable 'x'\nsrc/a.lua:9:1: (E011) expected expression", 2, "src/a.lua", 3, config.SeverityWarning, "W"},
		{"dotenv-linter", ".env:2 UnorderedKey: The FOO key should go before the BAR key", 1, ".env", 2, config.SeverityNit, ""},
		{"sqlfluff", `[{"filepath":"q.sql","violations":[{"start_line_no":2,"start_line_pos":1,"code":"LT02","description":"Expected indent","name":"layout.indent","warning":false}]}]`, 1, "q.sql", 2, config.SeverityNit, ""},
		{"biome", "::error title=lint/suspicious/noDoubleEquals,file=src/a.ts,line=3,endLine=3,col=5,endColumn=7::Use === instead of ==.%0AThis is unsafe.", 1, "src/a.ts", 3, config.SeverityWarning, "error"},
		{"oxlint", "src/a.ts:5:3: Unexpected debugger statement [warning/eslint(no-debugger)]", 1, "src/a.ts", 5, config.SeverityWarning, "warning"},
		{"stylelint", `[{"source":"a.css","warnings":[{"line":2,"column":3,"rule":"block-no-empty","severity":"error","text":"Unexpected empty block (block-no-empty)"}]}]`, 2, "a.css", 2, config.SeverityError, "error"},
		{"htmlhint", `[{"file":"index.html","messages":[{"type":"error","message":"Doctype must be declared first.","raw":"<html>","evidence":"<html>","line":1,"col":1,"rule":{"id":"doctype-first"}}]}]`, 1, "index.html", 1, config.SeverityError, "error"},
		{"markdownlint", `[{"fileName":"README.md","lineNumber":5,"ruleNames":["MD022","blanks-around-headings"],"ruleDescription":"Headings should be surrounded by blank lines","errorDetail":"Expected: 1; Actual: 0"}]`, 1, "README.md", 5, config.SeverityNit, ""},
		{"rubocop", `{"files":[{"path":"app/x.rb","offenses":[{"severity":"convention","message":"Style/StringLiterals: Prefer single-quoted strings","cop_name":"Style/StringLiterals","location":{"line":4}}]}]}`, 1, "app/x.rb", 4, config.SeverityNit, "convention"},
		{"swiftlint", `[{"character":1,"file":"/repo/Sources/A.swift","line":8,"reason":"Force casts should be avoided","rule_id":"force_cast","severity":"Error","type":"Force Cast"}]`, 2, "Sources/A.swift", 8, config.SeverityError, "Error"},
		{"phpstan", `{"totals":{"errors":0,"file_errors":1},"files":{"src/A.php":{"errors":1,"messages":[{"message":"Call to undefined method foo()","line":14,"ignorable":true,"identifier":"method.notFound"}]}},"errors":[]}`, 1, "src/A.php", 14, config.SeverityWarning, ""},
		{"clippy", `{"reason":"compiler-artifact","target":{}}` + "\n" + `{"reason":"compiler-message","message":{"level":"warning","code":{"code":"clippy::needless_return"},"message":"unneeded return statement","spans":[{"file_name":"src/lib.rs","line_start":4,"is_primary":true}]}}`, 0, "src/lib.rs", 4, config.SeverityWarning, "warning"},
		{"buf", `{"path":"api/v1/a.proto","start_line":3,"start_column":1,"end_line":3,"end_column":10,"type":"PACKAGE_DIRECTORY_MATCH","message":"Files with package \"a\" must be within a directory \"a\"."}`, 100, "api/v1/a.proto", 3, config.SeverityNit, ""},
		{"psscriptanalyzer", `[{"RuleName":"PSAvoidUsingCmdletAliases","Severity":1,"Line":2,"Message":"'gci' is an alias of 'Get-ChildItem'."}]`, 0, "x", 2, config.SeverityWarning, "1"},
		{"tflint", `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"tflint","rules":[{"id":"terraform_deprecated_syntax","defaultConfiguration":{"level":"warning"}}]}},"results":[{"ruleId":"terraform_deprecated_syntax","level":"warning","message":{"text":"Deprecated syntax"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"main.tf"},"region":{"startLine":6}}}]}]}]}`, 2, "main.tf", 6, config.SeverityWarning, "warning"},
	}
	for _, c := range cases {
		t.Run(c.tool, func(t *testing.T) {
			s := specByName(t, c.tool)
			findings, err := s.parse(inv, []byte(c.report), c.exit)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if len(findings) == 0 {
				t.Fatal("no findings parsed")
			}
			f := findings[0]
			f.Path = normalizePath(inv.repoRoot, f.Path)
			if f.Path != c.wantPath || f.Line != c.wantLine || f.Severity != c.wantSev || f.RawSeverity != c.wantRaw {
				t.Errorf("first finding = %+v; want %s:%d %s raw=%q", f, c.wantPath, c.wantLine, c.wantSev, c.wantRaw)
			}
			if f.Message == "" {
				t.Error("no message")
			}
		})
	}
}

func TestSARIFParserReadsLevelsAndRefusesAnEmptyLog(t *testing.T) {
	sarif := `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"detekt","rules":[{"id":"detekt.style.MagicNumber","properties":{"problem.severity":"warning"}}]}},"results":[
	  {"ruleId":"detekt.style.MagicNumber","message":{"text":"magic number"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"file:///repo/src/A.kt"},"region":{"startLine":9}}}]},
	  {"ruleId":"x","level":"error","message":{"text":"bad"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"src/B.kt"},"region":{"startLine":2}}}]},
	  {"ruleId":"noloc","level":"error","message":{"text":"nowhere"}}]}]}`
	findings, err := parseSARIF([]byte(sarif), config.SeverityWarning)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 2 {
		t.Fatalf("findings = %+v", findings)
	}
	if normalizePath("/repo", findings[0].Path) != "src/A.kt" || findings[0].Severity != config.SeverityWarning || findings[0].RawSeverity != "warning" {
		t.Errorf("rule-level severity not applied: %+v", findings[0])
	}
	if findings[1].Severity != config.SeverityError {
		t.Errorf("result level not applied: %+v", findings[1])
	}
	if _, err := parseSARIF([]byte(`{"version":"2.1.0"}`), config.SeverityWarning); err == nil {
		t.Error("a log with no runs must be an error")
	}
}

func TestGitleaksNeverReportsTheSecret(t *testing.T) {
	inv := invocation{repoRoot: "/repo", tmpDir: t.TempDir(), files: []string{"config.yaml"}}
	report := `[{"Description":"AWS Access Key","StartLine":4,"Secret":"AKIAIOSFODNN7EXAMPLE","Match":"AKIAIOSFODNN7EXAMPLE","File":"config.yaml","RuleID":"aws-access-token"}]`
	if err := os.WriteFile(inv.report("gitleaks.json"), []byte(report), 0o600); err != nil {
		t.Fatal(err)
	}
	findings, err := specByName(t, "gitleaks").parse(inv, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Line != 4 || findings[0].Severity != config.SeverityError {
		t.Fatalf("findings = %+v", findings)
	}
	if strings.Contains(findings[0].Message, "AKIA") {
		t.Error("the finding quotes the secret; it would be posted on the pull request")
	}
	args := specByName(t, "gitleaks").args(inv)
	if !slices.Contains(args, "--redact") || !slices.Contains(args, "--no-git") {
		t.Errorf("gitleaks args %v lack --redact or --no-git", args)
	}
}

func TestSetRunsCatalogToolsFromConfig(t *testing.T) {
	stubTool(t, "shellcheck", `{"comments":[{"file":"run.sh","line":2,"level":"warning","code":2086,"message":"Double quote."}]}`, false, 1)
	repo := t.TempDir()
	cfg := baseConfig()
	cfg.Linters.Enabled = []string{"shellcheck", "clippy"}
	off := false
	cfg.Linters.AutoDetect = &off
	set := New(repo, cfg, nil)

	files := parse(t, "diff --git a/run.sh b/run.sh\n--- a/run.sh\n+++ b/run.sh\n@@ -1,2 +1,2 @@\n #!/bin/sh\n-echo $1\n+echo $1 $2\n")
	found, err := set.Run(context.Background(), files)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(found) != 1 || found[0].Source != "shellcheck(SC2086)" {
		t.Errorf("findings = %+v", found)
	}
	for _, s := range set.Statuses() {
		switch s.Linter {
		case "shellcheck":
			if s.Outcome != "ran" {
				t.Errorf("shellcheck: %+v", s)
			}
		case "clippy":
			// No Rust in the change: skipped, not refused, so strict mode
			// does not fail over a tool that had nothing to read.
			if s.Outcome != "skipped" {
				t.Errorf("clippy: %+v", s)
			}
		}
	}
}

// TestAutoDetectedToolsSkipWhenAbsentAndRunWhenPresent: the catalog runs
// without being named, an absent binary is a skip even under strict mode,
// and naming the tool turns that skip into a failure.
func TestAutoDetectedToolsSkipWhenAbsentAndRunWhenPresent(t *testing.T) {
	repo := t.TempDir()
	files := parse(t, "diff --git a/run.sh b/run.sh\n--- a/run.sh\n+++ b/run.sh\n@@ -1,2 +1,2 @@\n #!/bin/sh\n-echo $1\n+echo $1 $2\n")

	cfg := baseConfig()
	cfg.Linters.Mode = config.LinterStrict
	t.Setenv("PATH", t.TempDir()+string(os.PathListSeparator)+"/usr/bin:/bin") // no analyzers installed
	set := New(repo, cfg, nil)
	if _, err := set.Run(context.Background(), files); err != nil {
		t.Fatalf("strict mode must not fail over an auto-detected tool that is not installed: %v", err)
	}
	for _, s := range set.Statuses() {
		if s.Linter == "shellcheck" || s.Linter == "gitleaks" {
			t.Errorf("an absent auto-detected tool made a roster entry: %+v", s)
		}
	}

	cfg.Linters.Enabled = append(cfg.Linters.Enabled, "shellcheck")
	set = New(repo, cfg, nil)
	if _, err := set.Run(context.Background(), files); err == nil {
		t.Fatal("a NAMED tool that is not installed must fail strict mode")
	}

	stubTool(t, "shellcheck", `{"comments":[]}`, false, 0)
	cfg = baseConfig()
	set = New(repo, cfg, nil)
	if _, err := set.Run(context.Background(), files); err != nil {
		t.Fatal(err)
	}
	for _, s := range set.Statuses() {
		if s.Linter == "shellcheck" && s.Outcome != "ran" {
			t.Errorf("installed auto-detected shellcheck: %+v", s)
		}
	}
}
