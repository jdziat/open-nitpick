package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/review"
	"github.com/jdziat/open-nitpick/internal/security"
)

func TestSecurityRejectsNoLintersFlag(t *testing.T) {
	err := runSecurity(context.Background(), []string{"-no-linters"})
	if err == nil || !strings.Contains(err.Error(), "no-linters") {
		t.Fatalf("want rejection of -no-linters, got %v", err)
	}
}

func TestSecurityRejectsBudgetFlag(t *testing.T) {
	err := runSecurity(context.Background(), []string{"-budget", "1000"})
	if err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatalf("want rejection of -budget, got %v", err)
	}
}

func TestSecurityFailOnNoneRequiresLoudWaiver(t *testing.T) {
	cfg := config.Defaults()
	cfg.Security.FailOn = config.SeverityNone
	_, err := resolveSecurityFailOn(cfg, "", false)
	if err == nil || !strings.Contains(err.Error(), "allow-clean-with-no-gate") {
		t.Fatalf("want waiver required, got %v", err)
	}
	got, err := resolveSecurityFailOn(cfg, "", true)
	if err != nil || got != config.SeverityNone {
		t.Fatalf("waiver should allow none: got %v err %v", got, err)
	}
}

func TestSecurityRefuseSeverityMuteBelowGate(t *testing.T) {
	cfg := config.Defaults()
	cfg.Linters.MaxSeverity = config.SeverityInfo
	err := refuseSeverityMute(cfg, config.SeverityWarning)
	if err == nil || !strings.Contains(err.Error(), "max_severity") {
		t.Fatalf("want mute refusal, got %v", err)
	}
}

func TestSecurityOverlayNamesRequiredScannersAndGosec(t *testing.T) {
	cfg := config.Defaults()
	cfg.Linters.Enabled = nil
	applySecurityOverlay(cfg)
	if !cfg.Linters.ForceGosec {
		t.Fatal("ForceGosec must be set")
	}
	for _, id := range []string{"osv-scanner", "gitleaks", "golangci-lint"} {
		if !containsString(cfg.Linters.Enabled, id) {
			t.Fatalf("enabled missing %s: %v", id, cfg.Linters.Enabled)
		}
	}
}

func TestSecurityAnalyzersOnlyRosterIncludesRequiredIds(t *testing.T) {
	root := t.TempDir()
	run := exec.Command("git", "init")
	run.Dir = root
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/x\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	applySecurityOverlay(cfg)
	report, _, err := securityAnalyzersOnly(context.Background(), root, cfg, nil, newLogger(false, "text"))
	if err != nil {
		t.Fatal(err)
	}
	roster := security.BuildRoster(report.Linters, security.FrozenRequired(),
		security.ModelStatus{Status: security.ModelSkippedByFlag, Reason: "-no-model"}, false)
	var sawOSV, sawGitleaks bool
	for _, s := range roster.Scanners {
		if s.ID == "osv-scanner" {
			sawOSV = true
		}
		if s.ID == "gitleaks" {
			sawGitleaks = true
		}
	}
	if !sawOSV || !sawGitleaks {
		t.Fatalf("roster missing required ids: %+v", roster.Scanners)
	}
}

func TestSecurityRedactsFindingBeforeEmit(t *testing.T) {
	f := review.Finding{
		Title: "leak",
		// Built in pieces so gosec does not treat the fixture as a live Stripe key.
		Rationale:    "token " + "ghp_" + strings.Repeat("A", 36),
		Class:        string(config.ClassSecurity),
		Severity:     string(config.SeverityWarning),
		Path:         "a.go",
		Line:         1,
		FromAnalyzer: true,
	}
	security.RedactFinding(&f)
	if strings.Contains(f.Rationale, "ghp_") {
		t.Fatalf("raw secret survived redaction: %q", f.Rationale)
	}
}

func TestSecurityModelFailureIsIncomplete(t *testing.T) {
	report := &review.Report{
		Incomplete: []string{"a.go"},
		Stages:     []review.StageStatus{{Stage: "review", Reason: "provider unavailable"}},
	}
	st := modelStatusFromReport(report)
	if st.Status != security.ModelFailed {
		t.Fatalf("status = %q, want failed", st.Status)
	}
	roster := security.BuildRoster(nil, security.RequiredAlways, st, false)
	if roster.Complete {
		t.Fatal("model failure must not be complete")
	}
}

func TestSecurityFindingsMeetGate(t *testing.T) {
	findings := []Finding{{Severity: string(config.SeverityWarning)}}
	if !findingsMeetGate(findings, config.SeverityWarning) {
		t.Fatal("warning should meet warning gate")
	}
	if findingsMeetGate(findings, config.SeverityError) {
		t.Fatal("warning should not meet error gate")
	}
}

func TestSecurityScanToolDocumentsTrustAndCoverage(t *testing.T) {
	session := mcpSession(t, t.TempDir())
	res, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var desc string
	var schema []byte
	for _, tool := range res.Tools {
		if tool.Name != "security_scan" {
			continue
		}
		desc = tool.Description
		schema, _ = json.Marshal(tool.InputSchema)
	}
	if desc == "" {
		t.Fatal("security_scan not registered")
	}
	for _, want := range []string{"trusted-operator-only", "required instruments finished", "no_linters", "allow_clean_with_no_gate"} {
		if !strings.Contains(desc, want) {
			t.Errorf("security_scan description missing %q", want)
		}
	}
	if strings.Contains(string(schema), "no_linters") {
		t.Fatalf("security_scan must not expose no_linters: %s", schema)
	}
	if strings.Contains(string(schema), `"budget"`) {
		t.Fatalf("security_scan must not expose budget: %s", schema)
	}
}

func TestSecurityOversizedSkipMakesIncomplete(t *testing.T) {
	t.Setenv("NITPICK_NO_USER_CONFIG", "1")
	root := t.TempDir()
	run := exec.Command("git", "init")
	run.Dir = root
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	cfgYAML := "models:\n  default:\n    provider: openai\n    model: test\n" +
		"review:\n  max_file_bytes: 16\n" +
		"security:\n  fail_on: warning\n"
	if err := os.WriteFile(filepath.Join(root, ".nitpick.yaml"), []byte(cfgYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(root, "big.env")
	if err := os.WriteFile(big, bytes.Repeat([]byte("A"), 64), 0o600); err != nil {
		t.Fatal(err)
	}
	f := &reviewFlags{repo: root}
	res, err := securityScan(context.Background(), f, nil, true, "", false, newLogger(false, "text"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Complete {
		t.Fatalf("oversized skip must be incomplete; skipped=%v stages=%v", res.Skipped, res.FailedStages)
	}
	if !strings.Contains(strings.Join(res.FailedStages, " "), "larger than") {
		t.Fatalf("failed_stages %v must name the size skip", res.FailedStages)
	}
}

func TestSecurityRunOnFixtureDoesNotPanic(t *testing.T) {
	root := t.TempDir()
	run := exec.Command("git", "init")
	run.Dir = root
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// May be incomplete (missing binaries) or clean under a complete roster;
	// either is fine. A panic or config theater error is not.
	_ = runSecurity(context.Background(), []string{"-repo", root, "-no-model", "-json"})
}

func containsString(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
