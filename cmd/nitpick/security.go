package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/linters"
	"github.com/jdziat/open-nitpick/internal/review"
	"github.com/jdziat/open-nitpick/internal/security"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// runSecurity scans a tree for security issues: required deterministic
// scanners first, then an optional model pass filtered to class security.
func runSecurity(ctx context.Context, args []string) error {
	var (
		f          reviewFlags
		noModel    bool
		asJSON     bool
		failOnFlag string
		allowNone  bool
	)
	fs := flag.NewFlagSet("security", flag.ContinueOnError)
	fs.StringVar(&f.repo, "repo", ".", "repository root")
	fs.StringVar(&f.configPath, "config", "", "path to .nitpick.yaml (default: <repo>/.nitpick.yaml)")
	fs.StringVar(&f.instruction, "instruction", "", "extra instruction for the model's pass")
	fs.BoolVar(&noModel, "no-model", false, "deterministic scanners only: no model call")
	fs.BoolVar(&asJSON, "json", false, "print the result as JSON")
	fs.StringVar(&failOnFlag, "fail-on", "", "override security.fail_on (nit, info, warning, error, critical, none)")
	fs.BoolVar(&allowNone, "allow-clean-with-no-gate", false, "permit fail_on none to green a complete run (loud waiver)")
	fs.BoolVar(&f.verbose, "v", false, "verbose logging")
	fs.StringVar(&f.logFormat, "log-format", "text", "log format: text or json")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `Usage: nitpick security [flags] [path...]

Scans the working tree, or the paths given, for security issues.
Deterministic scanners always run (osv-scanner, gitleaks, and language-applicable
tools); the model pass runs unless -no-model or security.model is false.
There is no -budget and no -no-linters: completeness is not bargained away.

Flags:`)
		fs.PrintDefaults()
	}
	for _, a := range args {
		switch {
		case a == "-no-linters", a == "--no-linters", strings.HasPrefix(a, "-no-linters="):
			return fmt.Errorf("security rejects -no-linters: required scanners cannot be skipped")
		case a == "-budget", a == "--budget", strings.HasPrefix(a, "-budget="):
			return fmt.Errorf("security rejects -budget: completeness is not bargained away")
		}
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	log := newLogger(f.verbose, f.logFormat)
	result, err := securityScan(ctx, &f, fs.Args(), noModel, failOnFlag, allowNone, log)
	if err != nil {
		return err
	}
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(result); err != nil {
			return err
		}
	} else if _, err := fmt.Fprint(os.Stdout, result.Text()); err != nil {
		return fmt.Errorf("write security report: %w", err)
	}
	if !result.Complete {
		return errIncomplete
	}
	if result.Failed {
		return errFindings
	}
	return nil
}

// SecurityResult is what nitpick security and MCP security_scan return.
type SecurityResult struct {
	Findings     []Finding                `json:"findings"`
	Hidden       []Finding                `json:"hidden,omitempty" jsonschema:"findings outside the security class the same review made"`
	Scanners     []security.ScannerStatus `json:"scanners"`
	Model        security.ModelStatus     `json:"model"`
	Complete     bool                     `json:"complete"`
	FailedStages []string                 `json:"failed_stages,omitempty"`
	Failed       bool                     `json:"failed" jsonschema:"whether findings met the security fail_on gate"`
	FailOn       string                   `json:"fail_on"`
	GateWaived   bool                     `json:"gate_waived,omitempty"`
	CoverageNote string                   `json:"coverage_note,omitempty"`
	Skipped      []string                 `json:"skipped,omitempty"`
}

// Text renders the result for a terminal.
func (r *SecurityResult) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Security scan: complete=%t fail_on=%s failed=%t\n", r.Complete, r.FailOn, r.Failed)
	if r.CoverageNote != "" {
		fmt.Fprintf(&b, "%s\n", r.CoverageNote)
	}
	fmt.Fprintf(&b, "\nScanners:\n")
	for _, s := range r.Scanners {
		if s.Reason != "" {
			fmt.Fprintf(&b, "  %-16s %s (%s)\n", s.ID, s.Status, s.Reason)
		} else {
			fmt.Fprintf(&b, "  %-16s %s\n", s.ID, s.Status)
		}
	}
	fmt.Fprintf(&b, "Model: %s", r.Model.Status)
	if r.Model.Reason != "" {
		fmt.Fprintf(&b, " (%s)", r.Model.Reason)
	}
	b.WriteByte('\n')
	if len(r.FailedStages) > 0 {
		fmt.Fprintf(&b, "\nIncomplete: %s\n", strings.Join(r.FailedStages, "; "))
	}
	fmt.Fprintf(&b, "\nFindings (%d):\n", len(r.Findings))
	if len(r.Findings) == 0 {
		b.WriteString("  none\n")
	}
	for _, fd := range r.Findings {
		fmt.Fprintf(&b, "  [%s] %s:%d  %s\n    %s\n", fd.Severity, bundle.PromptSafe(fd.Path), fd.Line,
			bundle.PromptSafe(fd.Title), bundle.PromptSafe(strings.TrimSpace(fd.Rationale)))
	}
	if len(r.Hidden) > 0 {
		fmt.Fprintf(&b, "\n%d finding(s) outside the security class (hidden):\n", len(r.Hidden))
		for _, fd := range r.Hidden {
			fmt.Fprintf(&b, "  [%s/%s] %s:%d  %s\n", fd.Severity, fd.Class, bundle.PromptSafe(fd.Path), fd.Line, bundle.PromptSafe(fd.Title))
		}
	}
	if len(r.Skipped) > 0 {
		fmt.Fprintf(&b, "\nLeft out (%d): %s\n", len(r.Skipped), strings.Join(r.Skipped, "; "))
	}
	return b.String()
}

// securityScan runs the security instruments over the tree.
func securityScan(ctx context.Context, f *reviewFlags, paths []string, noModel bool, failOnFlag string, allowNone bool, log *slog.Logger) (*SecurityResult, error) {
	repo, err := filepath.Abs(f.repo)
	if err != nil {
		return nil, fmt.Errorf("resolve repo path: %w", err)
	}
	cfg, err := loadConfig(repo, f.configPath)
	if err != nil {
		return nil, err
	}
	if err := security.ValidateAnalyzersExtends(cfg.Security.Analyzers); err != nil {
		return nil, err
	}

	failOn, err := resolveSecurityFailOn(cfg, failOnFlag, allowNone)
	if err != nil {
		return nil, err
	}
	// Working-tree config cannot weaken the shipped gate; an explicit -fail-on
	// is the operator's own choice and is not subject to this clamp.
	if failOnFlag == "" {
		if err := refuseWeakerSecurityGate(failOn); err != nil {
			return nil, err
		}
	}
	if err := refuseSeverityMute(cfg, failOn); err != nil {
		return nil, err
	}

	applySecurityOverlay(cfg)
	gateWaived := failOn == config.SeverityNone && allowNone

	var (
		report *review.Report
		tree   *vcs.Tree
		model  security.ModelStatus
	)

	switch {
	case noModel:
		model = security.ModelStatus{Status: security.ModelSkippedByFlag, Reason: "-no-model"}
		report, tree, err = securityAnalyzersOnly(ctx, repo, cfg, paths, log)
	case !cfg.Security.ModelOn():
		model = security.ModelStatus{Status: security.ModelSkippedByConfig, Reason: "security.model=false"}
		report, tree, err = securityAnalyzersOnly(ctx, repo, cfg, paths, log)
	default:
		report, tree, err = securityTreeReview(ctx, f, cfg, paths, log)
		if err != nil {
			return nil, err
		}
		model = modelStatusFromReport(report)
	}
	if err != nil {
		return nil, err
	}
	if tree == nil {
		return nil, errors.New("security scan produced no tree")
	}
	if report == nil {
		return nil, errors.New("security scan produced no report")
	}

	required := securityRequiredIDs(cfg)
	roster := security.BuildRoster(report.Linters, required, model, gateWaived)

	out := &SecurityResult{
		Scanners:     roster.Scanners,
		Model:        roster.Model,
		Complete:     roster.Complete,
		FailedStages: append([]string(nil), roster.FailedStages...),
		FailOn:       string(failOn),
		GateWaived:   gateWaived,
		CoverageNote: "complete means required instruments finished, not that every language had a SAST; Python/JS need Semgrep configured for required SAST coverage",
	}
	for _, s := range tree.Skipped {
		out.Skipped = append(out.Skipped, bundle.PromptSafe(s.Path)+": "+s.Reason)
		// Oversized files never reach scanners; treating the run as complete
		// would greenwash a secret or vuln that lived only in the omitted file.
		if strings.Contains(s.Reason, "larger than") {
			out.Complete = false
			out.FailedStages = append(out.FailedStages, bundle.PromptSafe(s.Path)+": "+s.Reason)
		}
	}

	for i := range report.Findings {
		security.RedactFinding(&report.Findings[i])
	}
	kept, hidden := partitionSecurityFindings(report.Findings)
	for _, fd := range kept {
		out.Findings = append(out.Findings, findingFromReview(fd))
	}
	for _, fd := range hidden {
		out.Hidden = append(out.Hidden, findingFromReview(fd))
	}

	// Failed is independent of Complete. An incomplete roster used to leave
	// Failed false, so a JSON consumer read a clean gate beside a critical finding.
	if failOn != config.SeverityNone {
		out.Failed = securityFailed(failOn, out.Findings)
	}
	return out, nil
}

// partitionSecurityFindings keeps class-security findings and advisories;
// everything else is hidden so the gate cannot fail on a race the persona
// was told not to report.
func partitionSecurityFindings(in []review.Finding) (kept, hidden []review.Finding) {
	for _, fd := range in {
		if fd.Class == string(config.ClassSecurity) || fd.IsAdvisory() {
			kept = append(kept, fd)
		} else {
			hidden = append(hidden, fd)
		}
	}
	return kept, hidden
}

func findingFromReview(fd review.Finding) Finding {
	return Finding{
		Path: fd.Path, Line: fd.Line, Severity: fd.Severity, Class: fd.Class,
		Title: fd.Title, Rationale: fd.Rationale, Suggestion: fd.Suggestion,
		Source: analyzerSource(fd),
	}
}

func resolveSecurityFailOn(cfg *config.Config, flag string, allowNone bool) (config.Severity, error) {
	failOn := cfg.Security.FailOn
	if flag != "" {
		failOn = config.Severity(flag)
		if !failOn.Valid() {
			return "", fmt.Errorf("invalid -fail-on %q", flag)
		}
	}
	if failOn == config.SeverityNone && !allowNone {
		return "", fmt.Errorf("security.fail_on none requires -allow-clean-with-no-gate (or MCP allow_clean_with_no_gate)")
	}
	return failOn, nil
}

// refuseWeakerSecurityGate rejects a FailOn threshold above the shipped
// default. fail_on: critical (or none without a loud waiver) greens findings
// that warning would fail; a pull request must not supply that weaker gate.
func refuseWeakerSecurityGate(failOn config.Severity) error {
	def := config.Defaults().Security.FailOn
	if failOn == config.SeverityNone {
		return nil // loud waiver path already gated
	}
	if failOn.Rank() > def.Rank() {
		return fmt.Errorf("security.fail_on %q is weaker than the shipped default %q; refuse rather than greenwash", failOn, def)
	}
	return nil
}

// refuseSeverityMute rejects configs that would silence findings at or above
// the security gate via linters.max_severity.
func refuseSeverityMute(cfg *config.Config, gate config.Severity) error {
	if gate == config.SeverityNone {
		return nil
	}
	max := cfg.Linters.MaxSeverity
	if max == "" {
		return nil
	}
	if !max.AtLeast(gate) {
		return fmt.Errorf("linters.max_severity %q would mute security findings at gate %q; refuse rather than greenwash", max, gate)
	}
	return nil
}

func applySecurityOverlay(cfg *config.Config) {
	cfg.Linters.ForceGosec = true
	cfg.Review.RelatedContext = true
	cfg.Review.RelatedContextCallers = true
	cfg.Review.MaxFiles = 1 << 30
	cfg.Review.Incremental = false
	// Advisories must count toward the security gate, not be capped away.
	if !cfg.Linters.MaxSeverity.AtLeast(config.SeverityCritical) {
		cfg.Linters.MaxSeverity = config.SeverityCritical
	}
	for _, id := range security.FrozenRequired() {
		if !slices.Contains(cfg.Linters.Enabled, id) {
			cfg.Linters.Enabled = append(cfg.Linters.Enabled, id)
		}
	}
	for _, id := range cfg.Security.Analyzers {
		id = strings.TrimSpace(id)
		if id != "" && !slices.Contains(cfg.Linters.Enabled, id) {
			cfg.Linters.Enabled = append(cfg.Linters.Enabled, id)
		}
	}
	if strings.TrimSpace(cfg.Linters.SemgrepConfig) != "" && !slices.Contains(cfg.Linters.Enabled, "semgrep") {
		cfg.Linters.Enabled = append(cfg.Linters.Enabled, "semgrep")
	}
	enableAdvisoryScanner(cfg)
}

func securityRequiredIDs(cfg *config.Config) []string {
	ids := security.FrozenRequired()
	for _, id := range cfg.Security.Analyzers {
		id = strings.TrimSpace(id)
		if id != "" && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	if strings.TrimSpace(cfg.Linters.SemgrepConfig) != "" && !slices.Contains(ids, "semgrep") {
		ids = append(ids, "semgrep")
	}
	return ids
}

func securityTreeReview(ctx context.Context, f *reviewFlags, cfg *config.Config, paths []string, log *slog.Logger) (*review.Report, *vcs.Tree, error) {
	repo, err := filepath.Abs(f.repo)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve repo path: %w", err)
	}
	if len(cfg.Dropped) > 0 {
		log.Warn("ignored settings an untrusted config file may not supply", "keys", strings.Join(cfg.Dropped, ", "))
	}
	instruction := security.Instruction
	if strings.TrimSpace(f.instruction) != "" {
		instruction = instruction + "\n" + f.instruction
	}
	f.instruction = instruction
	f.noLinters = false

	tree := vcs.NewTree(vcs.NewLocal(repo, io.Discard), paths)
	tree.MaxBytes = cfg.Review.MaxFileBytes
	ref := vcs.Ref{Head: vcs.Worktree}

	log.Info("security scanning the tree", "repo", repo, "paths", strings.Join(paths, ","))
	// ResolveSecurity falls back to review/default when models.security is
	// unset. Pin it onto Review for this run so BuildRoles uses those weights
	// without a parallel Roles.Security field.
	runCfg := *cfg
	sec := cfg.Models.ResolveSecurity()
	runCfg.Models.Review = &sec
	engine, err := newEngine(ctx, f, repo, &runCfg, tree, ref, log)
	if err != nil {
		return nil, nil, err
	}
	// A tree scan "changes" every file, .nitpick.yaml included. Re-reading
	// policy from a base revision would drop the operator's loaded config.
	// The person running this on their checkout is the policy's author.
	engine.Policy = nil
	report, err := engine.Review(ctx, ref)
	if err != nil && (!errors.Is(err, review.ErrPublish) || report == nil) {
		return nil, nil, err
	}
	return report, tree, nil
}

func securityAnalyzersOnly(ctx context.Context, repo string, cfg *config.Config, paths []string, log *slog.Logger) (*review.Report, *vcs.Tree, error) {
	tree := vcs.NewTree(vcs.NewLocal(repo, io.Discard), paths)
	tree.MaxBytes = cfg.Review.MaxFileBytes
	raw, err := tree.Diff(ctx, vcs.Ref{Head: vcs.Worktree})
	if err != nil {
		return nil, nil, err
	}
	files, err := diff.Parse(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("parse tree diff for analyzers: %w", err)
	}
	set := linters.New(repo, cfg, log)
	found, runErr := set.Run(ctx, files)
	report := &review.Report{
		Findings:         found,
		AnalyzerFindings: append([]review.Finding(nil), found...),
		Linters:          set.Statuses(),
	}
	if runErr != nil {
		log.Warn("security analyzers reported an error", "error", runErr)
	}
	return report, tree, nil
}

func modelStatusFromReport(report *review.Report) security.ModelStatus {
	if report == nil {
		return security.ModelStatus{Status: security.ModelFailed, Reason: "no report"}
	}
	if !report.PipelineComplete() {
		stages := report.FailedStages()
		reason := "pipeline incomplete"
		if len(stages) > 0 {
			reason = strings.Join(stages, "; ")
		}
		if len(report.Incomplete) > 0 {
			reason = fmt.Sprintf("%s; %d file(s) unreviewed", reason, len(report.Incomplete))
		}
		return security.ModelStatus{Status: security.ModelFailed, Reason: reason}
	}
	return security.ModelStatus{Status: security.ModelRan}
}

func findingsMeetGate(findings []Finding, gate config.Severity) bool {
	for _, f := range findings {
		if config.Severity(f.Severity).AtLeast(gate) {
			return true
		}
	}
	return false
}

// securityFailed reports whether findings meet the gate. Completeness is a
// separate field: an incomplete roster must not hide a finding that already
// meets fail_on.
func securityFailed(failOn config.Severity, findings []Finding) bool {
	if failOn == config.SeverityNone {
		return false
	}
	return findingsMeetGate(findings, failOn)
}
