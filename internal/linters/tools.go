package linters

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/jdziat/open-nitpick/internal/config"
)

// The analyzer catalog: every deterministic tool after the first four,
// described as data rather than as a type each.
//
// golangci-lint, ruff, eslint and semgrep are hand-written because each one
// turned out to have a channel through which the tree under review could
// silence or forge its report, and closing those took code specific to the
// tool (see runners.go). The tools here are simpler in that respect, most
// take a config path on the command line and read nothing else from the
// working directory, so what each needs is a description: which files it
// reads, how it is invoked with its configuration held outside the tree, and
// how its report is read back. That description is a toolSpec, and one
// runner type (catalogTool) executes all of them.
//
// The trust model is the same for every one of them and is enforced in the
// shared runner rather than per tool:
//
//   - the binary is resolved from PATH and refused inside the repository
//     (runCommand does this for every analyzer);
//   - configuration comes from linters.configs.<name>, which must resolve
//     outside the repository, or from a file this package ships and writes
//     outside the repository; a tool with neither useful default nor
//     operator config does not run and says so;
//   - a tool that EXECUTES the tree's own code to analyze it, cargo clippy
//     runs build scripts and proc macros, phpstan loads the project's
//     autoloader, is refused unless the operator names it in
//     linters.trusted, because no configuration outside the tree makes that
//     safe on a pull request from a stranger.
//
// What this does not close, for any of them, is in-source suppression: a
// `# noqa`, `// NOLINT`, `# rubocop:disable`. That channel is documented in
// the README and is the same for the hand-written four.

// isolation says where a catalog tool's configuration comes from when the
// operator supplies none.
type isolation int

const (
	// isolatedByFlag: the tool has a switch that stops it reading
	// configuration from the working directory (shellcheck --norc, luacheck
	// --no-config, sqlfluff --ignore-local-config).
	isolatedByFlag isolation = iota

	// isolatedByShipped: the tool reads exactly the config file it is handed,
	// and this package ships one and writes it outside the repository.
	isolatedByShipped

	// noConfig: the tool reads no configuration at all.
	noConfig

	// operatorOnly: the tool has no useful default and does not run until an
	// operator names a configuration outside the repository.
	operatorOnly
)

// toolSpec describes one analyzer.
type toolSpec struct {
	// name is the key in linters.enabled and linters.configs.
	name string

	// binary is the executable, when it differs from name.
	binary string

	// languages is what the tool covers, for documentation and the roster.
	languages string

	// Which changed files the tool reads: by extension, exact basename,
	// basename prefix, or repository-relative glob. A file matching any is a
	// target.
	exts     []string
	names    []string
	prefixes []string
	globs    []string

	isolation isolation

	// shipped names the file under configs/ this package writes outside the
	// repository when isolation is isolatedByShipped.
	shipped string

	// configAsDir hands the tool the DIRECTORY the config was written into
	// rather than the file, for tools that take a config directory (biome).
	configAsDir bool

	// trusted marks a tool that executes code from the tree under review.
	trusted bool

	// auto marks a tool that runs without being named in linters.enabled
	// when it is installed: isolated, executing nothing from the tree, and
	// worth running on every repository that has files of its kind.
	auto bool

	// perFile invokes the tool once per target rather than once with all.
	perFile bool

	// reportOnStderr is set for a tool that writes its findings to stderr,
	// which cppcheck does.
	reportOnStderr bool

	// args builds the command line.
	args func(inv invocation) []string

	// parse reads the report. exit is the process's exit status; a parser
	// must return an error, not an empty list, for output it cannot read.
	parse func(inv invocation, report []byte, exit int) ([]Finding, error)

	// env, when set, adds environment variables for the run.
	env map[string]string

	// unsetEnv names variables cleared for the run, for tools whose
	// environment can redirect their output.
	unsetEnv []string
}

// invocation is one run's inputs.
type invocation struct {
	repoRoot string

	// config is the absolute path handed to the tool, or empty.
	config string

	// tmpDir is a scratch directory outside the repository, for tools that
	// write their report to a file.
	tmpDir string

	// files are the repository-relative targets.
	files []string
}

// report is the path of a file the tool is asked to write its report to.
func (inv invocation) report(name string) string { return filepath.Join(inv.tmpDir, name) }

// abs makes the targets absolute, for tools that resolve paths against
// their own working directory rather than ours.
func (inv invocation) abs() []string {
	out := make([]string, 0, len(inv.files))
	for _, f := range inv.files {
		out = append(out, filepath.Join(inv.repoRoot, filepath.FromSlash(f)))
	}
	return out
}

//go:embed all:configs
var shippedConfigs embed.FS

// targets returns the changed files this tool reads.
func (s toolSpec) targets(files []string) []string {
	var out []string
	for _, f := range files {
		if s.matches(f) {
			out = append(out, f)
		}
	}
	return out
}

func (s toolSpec) matches(f string) bool {
	base := path.Base(f)
	ext := strings.ToLower(path.Ext(f))
	for _, e := range s.exts {
		if ext == e {
			return true
		}
	}
	for _, n := range s.names {
		if base == n {
			return true
		}
	}
	for _, p := range s.prefixes {
		if strings.HasPrefix(base, p) {
			return true
		}
	}
	for _, g := range s.globs {
		if ok, _ := doublestar.Match(g, f); ok {
			return true
		}
	}
	return false
}

// catalogTool runs one toolSpec under the shared trust rules.
type catalogTool struct {
	spec    *toolSpec
	cfg     analyzerConfig
	trusted bool

	// detected is true when the tool was added by auto-detection rather
	// than named by the operator, in which case its binary being absent is
	// a skip and not a failure.
	detected bool
}

// errNotInstalled is Detect's answer for an auto-detected tool whose binary is
// absent: nothing the operator asked for went missing.
var errNotInstalled = errors.New("not installed")

func (t *catalogTool) Name() string { return t.spec.name }

func (t *catalogTool) bin() string {
	if t.spec.binary != "" {
		return t.spec.binary
	}
	return t.spec.name
}

// errUntrusted is Detect's answer for a tool that would execute the tree's
// code and has not been trusted to.
func errUntrusted(name string) error {
	return fmt.Errorf("%s executes code from the tree under review to analyze it; "+
		"add it to linters.trusted to run it on repositories whose changes you trust", name)
}

// errUnconfigured is Detect's answer for an operatorOnly tool with no config.
func errUnconfigured(name string) error {
	return fmt.Errorf("%s is not configured; set linters.configs.%s to a configuration outside the repository", name, name)
}

func (t *catalogTool) Detect(_ context.Context, _ string, files []string) error {
	if t.cfg.Err != nil {
		return t.cfg.Err
	}
	if len(t.spec.targets(files)) == 0 {
		return errNoTargets
	}
	if t.spec.trusted && !t.trusted {
		return errUntrusted(t.spec.name)
	}
	if t.spec.isolation == operatorOnly && t.cfg.Ref == "" {
		return errUnconfigured(t.spec.name)
	}
	if !available(t.bin()) {
		if t.detected {
			return fmt.Errorf("%w (auto-detected; %s is not on PATH)", errNotInstalled, t.bin())
		}
		return notOnPath(t.bin())
	}
	return nil
}

func (t *catalogTool) State() string {
	var isolated string
	switch t.spec.isolation {
	case isolatedByShipped:
		isolated = "isolated: open-nitpick's own analyzer config"
	case isolatedByFlag:
		isolated = "isolated"
	case noConfig:
		isolated = "no configuration"
	default:
		isolated = "not configured"
	}
	return t.cfg.state(isolated)
}

func (t *catalogTool) Run(ctx context.Context, repoRoot string, files []string) ([]Finding, error) {
	if err := t.cfg.Err; err != nil {
		return nil, err
	}
	targets, rejected := safePaths(t.spec.targets(files))
	if len(rejected) > 0 {
		// A path that would be read as a flag is refused rather than quoted
		// into argv; the review's own skip list carries it.
		targets = targets[:len(targets):len(targets)]
	}
	if len(targets) == 0 {
		return nil, nil
	}

	tmp, err := os.MkdirTemp("", "open-nitpick-"+t.spec.name+"-")
	if err != nil {
		return nil, fmt.Errorf("%s: scratch directory: %w", t.spec.name, err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	// The scratch directory is subject to the same containment rule as a
	// config: a TMPDIR inside the checkout would have this tool writing its
	// own policy into the tree and reading it back.
	if _, err := resolveConfig(tmp, repoRoot); err != nil {
		return nil, fmt.Errorf("%s: scratch directory is not usable here: %w", t.spec.name, err)
	}

	configPath := t.cfg.Ref
	if configPath == "" && t.spec.isolation == isolatedByShipped {
		configPath, err = writeShipped(tmp, t.spec.shipped)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", t.spec.name, err)
		}
		if t.spec.configAsDir {
			configPath = filepath.Dir(configPath)
		}
	}

	inv := invocation{repoRoot: repoRoot, config: configPath, tmpDir: tmp}
	env := analyzerEnv(t.spec.env, t.spec.unsetEnv...)

	run := func(batch []string) ([]Finding, error) {
		inv.files = batch
		stdout, stderr, exit, err := runTool(ctx, repoRoot, repoRoot, t.bin(), env, t.spec.args(inv)...)
		if err != nil {
			return nil, err
		}
		report := stdout
		if t.spec.reportOnStderr {
			report = stderr
		}
		findings, err := t.spec.parse(inv, report, exit)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", t.spec.name, err)
		}
		for i := range findings {
			findings[i].Path = normalizePath(repoRoot, findings[i].Path)
		}
		return findings, nil
	}

	if !t.spec.perFile {
		return run(targets)
	}
	var all []Finding
	for _, f := range targets {
		found, err := run([]string{f})
		if err != nil {
			return all, err
		}
		all = append(all, found...)
	}
	return all, nil
}

// writeShipped materializes an embedded config outside the repository and
// returns its path, for writeGolangciDefaults's reasons.
func writeShipped(dir, name string) (string, error) {
	content, err := shippedConfigs.ReadFile("configs/" + name)
	if err != nil {
		return "", fmt.Errorf("open-nitpick ships no analyzer config named %s: %w", name, err)
	}
	file := filepath.Join(dir, name)
	if err := os.WriteFile(file, content, 0o600); err != nil {
		return "", fmt.Errorf("write open-nitpick's analyzer config: %w", err)
	}
	return file, nil
}

// runTool is runCommand for tools whose report may be on either stream and
// whose exit status carries meaning: it fails only when the process could not
// be run or the context ended, and otherwise returns both streams.
func runTool(ctx context.Context, repoRoot, workDir, name string, env []string, args ...string) (stdout, stderr []byte, exit int, err error) {
	bin, err := resolveBinary(name, repoRoot)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("%s: %w", name, err)
	}

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = workDir
	cmd.Env = env
	cmd.WaitDelay = 5 * time.Second

	var out, errs strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errs

	runErr := cmd.Run()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, nil, 0, fmt.Errorf("%s: %w", name, ctxErr)
	}
	if runErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(runErr, &exitErr) {
			return nil, nil, 0, fmt.Errorf("%s: %s", name, truncate(runErr.Error(), 400))
		}
	}
	return []byte(out.String()), []byte(errs.String()), exitCode(runErr), nil
}

// normalizePath turns whatever path a tool printed into a repository-relative
// one: absolute paths are made relative, "./" prefixes dropped, separators
// normalized.
func normalizePath(repoRoot, p string) string {
	p = strings.TrimPrefix(p, "file://")
	if filepath.IsAbs(p) {
		return relative(repoRoot, p)
	}
	p = filepath.ToSlash(filepath.Clean(p))
	return strings.TrimPrefix(p, "./")
}

// --- Shared parsers -----------------------------------------------------------

// parseSARIF reads a SARIF 2.1 log, which several tools can write and which
// is the one report format worth reading once.
//
// A log with no runs at all is an error rather than a clean result: every
// tool that writes SARIF writes at least one run, so none is the shape of a
// report that was not produced.
func parseSARIF(report []byte, defaultSeverity config.Severity) ([]Finding, error) {
	var log struct {
		Runs []struct {
			Tool struct {
				Driver struct {
					Rules []struct {
						ID                   string `json:"id"`
						DefaultConfiguration struct {
							Level string `json:"level"`
						} `json:"defaultConfiguration"`
						Properties map[string]any `json:"properties"`
					} `json:"rules"`
				} `json:"driver"`
			} `json:"tool"`
			Results []struct {
				RuleID  string `json:"ruleId"`
				Level   string `json:"level"`
				Message struct {
					Text string `json:"text"`
				} `json:"message"`
				Properties map[string]any `json:"properties"`
				Locations  []struct {
					PhysicalLocation struct {
						ArtifactLocation struct {
							URI string `json:"uri"`
						} `json:"artifactLocation"`
						Region struct {
							StartLine int `json:"startLine"`
						} `json:"region"`
					} `json:"physicalLocation"`
				} `json:"locations"`
			} `json:"results"`
		} `json:"runs"`
	}
	if err := decodeJSON(report, &log); err != nil {
		return nil, fmt.Errorf("parse SARIF: %w", err)
	}
	if log.Runs == nil {
		return nil, errors.New("parse SARIF: no runs in the log")
	}

	var findings []Finding
	for _, run := range log.Runs {
		ruleLevel := map[string]string{}
		for _, r := range run.Tool.Driver.Rules {
			if r.DefaultConfiguration.Level != "" {
				ruleLevel[r.ID] = r.DefaultConfiguration.Level
			}
			if sev, ok := r.Properties["problem.severity"].(string); ok && ruleLevel[r.ID] == "" {
				ruleLevel[r.ID] = sev
			}
		}
		for _, res := range run.Results {
			if len(res.Locations) == 0 {
				continue
			}
			loc := res.Locations[0].PhysicalLocation
			if loc.ArtifactLocation.URI == "" {
				continue
			}
			// A result with a file but no region is kept with Line 0: a
			// dependency scanner reports against the lockfile as a whole,
			// and the caller that knows the tool places it (osv-scanner puts
			// it on line 1). A caller that does not is protected downstream,
			// where a line the file does not have is dropped.
			word := res.Level
			if word == "" {
				word = ruleLevel[res.RuleID]
			}
			if word == "" {
				if sev, ok := res.Properties["severity"].(string); ok {
					word = sev
				}
			}
			sev := defaultSeverity
			if word != "" {
				sev = sarifLevel(word)
			}
			findings = append(findings, Finding{
				Path:        loc.ArtifactLocation.URI,
				Line:        loc.Region.StartLine,
				Rule:        res.RuleID,
				Message:     strings.TrimSpace(res.Message.Text),
				Severity:    sev,
				RawSeverity: word,
			})
		}
	}
	return findings, nil
}

// sarifLevel maps a SARIF level (or a tool's severity word that leaked into
// one) onto our scale.
func sarifLevel(level string) config.Severity {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "error":
		return config.SeverityError
	case "warning":
		return config.SeverityWarning
	case "note", "none":
		return config.SeverityInfo
	default:
		return mapSeverity(level)
	}
}

// lineReport parses reports of the form the regexp names: it must define
// the groups file and line, and may define sev, rule and msg.
type lineReport struct {
	re       *regexp.Regexp
	severity func(word string) config.Severity
	fallback config.Severity
}

func (l lineReport) parse(report []byte) ([]Finding, error) {
	var findings []Finding
	names := l.re.SubexpNames()
	idx := map[string]int{}
	for i, n := range names {
		if n != "" {
			idx[n] = i
		}
	}
	for line := range strings.SplitSeq(string(report), "\n") {
		m := l.re.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if m == nil {
			continue
		}
		get := func(n string) string {
			if i, ok := idx[n]; ok {
				return strings.TrimSpace(m[i])
			}
			return ""
		}
		ln, err := strconv.Atoi(get("line"))
		if err != nil || ln <= 0 {
			continue
		}
		f := Finding{Path: get("file"), Line: ln, Rule: get("rule"), Message: get("msg"), Severity: l.fallback}
		if word := get("sev"); word != "" {
			f.RawSeverity = word
			if l.severity != nil {
				f.Severity = l.severity(word)
			} else {
				f.Severity = mapSeverity(word)
			}
		}
		findings = append(findings, f)
	}
	return findings, nil
}

// requireReport fails a parse that found nothing in output the tool's exit
// status says should have carried findings. exit codes are per tool: the
// caller passes the codes that mean "ran and found something".
func requireReport(findings []Finding, report []byte, exit int, foundExits ...int) ([]Finding, error) {
	for _, code := range foundExits {
		if exit == code && len(findings) == 0 && len(strings.TrimSpace(string(report))) > 0 {
			return nil, fmt.Errorf("exit %d says findings exist and none could be read from the report (got: %q)", exit, snippet(report))
		}
	}
	return findings, nil
}

// readJSONReport reads a report a tool wrote to a file in the scratch
// directory, for tools that cannot print one to stdout.
func readJSONReport(inv invocation, name string) ([]byte, error) {
	data, err := os.ReadFile(inv.report(name))
	if err != nil {
		return nil, fmt.Errorf("read the report the tool was asked to write: %w", err)
	}
	return data, nil
}

// readReportDir returns the first file in a directory the tool wrote its
// report into, for tools that choose their own report filename.
func readReportDir(inv invocation, dir string) ([]byte, error) {
	entries, err := os.ReadDir(filepath.Join(inv.tmpDir, dir))
	if err != nil {
		return nil, fmt.Errorf("read the report directory: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return nil, errors.New("the tool wrote no report")
	}
	sort.Strings(names)
	return os.ReadFile(filepath.Join(inv.tmpDir, dir, names[0]))
}

// jsonLines decodes newline-delimited JSON objects, skipping lines that are
// not objects (progress output tools print between records).
func jsonLines(report []byte, each func(json.RawMessage)) error {
	saw := false
	for line := range strings.SplitSeq(string(report), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var raw json.RawMessage
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			return fmt.Errorf("parse JSON line: %w", err)
		}
		saw = true
		each(raw)
	}
	if !saw && len(strings.TrimSpace(string(report))) > 0 {
		return fmt.Errorf("no JSON records in the report (got: %q)", snippet(report))
	}
	return nil
}

// catalogRunners builds a runner for every catalog tool, with its operator
// configuration resolved once.
func catalogRunners(repoRoot string, cfg *config.Config) []Runner {
	trusted := map[string]bool{}
	for _, name := range cfg.Linters.Trusted {
		trusted[strings.TrimSpace(name)] = true
	}
	var out []Runner
	for _, spec := range catalog() {
		spec := spec
		out = append(out, &catalogTool{
			spec:    &spec,
			cfg:     fileConfig(repoRoot, cfg.Linters.Configs[spec.name]),
			trusted: trusted[spec.name],
		})
	}
	return out
}

// autoDetected returns the catalog tools that run without being named, each
// marked so that an absent binary is a skip.
func autoDetected(repoRoot string, cfg *config.Config) []Runner {
	var out []Runner
	for _, r := range catalogRunners(repoRoot, cfg) {
		t := r.(*catalogTool)
		if t.spec.auto {
			t.detected = true
			out = append(out, t)
		}
	}
	return out
}

// CatalogNames lists every catalog tool, for validation and documentation.
func CatalogNames() []string {
	var names []string
	for _, s := range catalog() {
		names = append(names, s.name)
	}
	sort.Strings(names)
	return names
}

// CatalogEntry describes one tool for documentation.
type CatalogEntry struct {
	Name, Languages, Configuration string
	Default, Auto, Trusted         bool

	// NeedsConfig marks a tool that does nothing until an operator points it
	// at a configuration outside the repository. `nitpick init` writes these
	// commented out: naming one in linters.enabled without the config it
	// needs buys a roster line that reports "not configured" forever.
	NeedsConfig bool

	// Exts, Names and Prefixes are the inputs the tool reads: file
	// extensions, exact basenames, and basename prefixes. Suggest matches a
	// checkout against them.
	Exts, Names, Prefixes, Globs []string
}

// Reads reports whether the analyzer accepts this repository-relative path.
func (e CatalogEntry) Reads(path string) bool {
	return (toolSpec{exts: e.Exts, names: e.Names, prefixes: e.Prefixes, globs: e.Globs}).matches(path)
}

// Catalog describes every tool, for `nitpick linters` and the README.
func Catalog() []CatalogEntry {
	defaults := map[string]bool{}
	for _, n := range config.DefaultLinters {
		defaults[n] = true
	}
	var out []CatalogEntry
	for _, s := range catalog() {
		var conf string
		switch s.isolation {
		case isolatedByFlag:
			conf = "isolated by flag; linters.configs." + s.name + " overrides"
		case isolatedByShipped:
			conf = "open-nitpick's own config; linters.configs." + s.name + " overrides"
		case noConfig:
			conf = "none"
		default:
			conf = "linters.configs." + s.name + " required"
		}
		out = append(out, CatalogEntry{
			Name: s.name, Languages: s.languages, Configuration: conf,
			Default: defaults[s.name], Auto: s.auto, Trusted: s.trusted,
			NeedsConfig: s.isolation == operatorOnly,
			Exts:        s.exts, Names: s.names, Prefixes: s.prefixes, Globs: s.globs,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
