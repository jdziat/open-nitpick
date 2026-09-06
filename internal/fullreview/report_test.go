package fullreview

import (
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/review"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

func TestFullReviewSectionsGroupByWhatAReaderDoes(t *testing.T) {
	report := &review.Report{
		Findings: []review.Finding{
			{Path: "app/db.py", Line: 11, Severity: "error", Class: "correctness", Title: "page size rejected"},
			{Path: "go.mod", Line: 1, Severity: "warning", Class: "security", Title: "vulnerable dependency", FromAnalyzer: true, Source: "GHSA-xxxx-yyyy-zzzz"},
			{Path: "web/auth.go", Line: 40, Severity: "critical", Class: "security", Title: "token accepted after expiry"},
			{Path: "web/auth.go", Line: 12, Severity: "nit", Class: "style", Title: "naming"},
		},
		Discarded: []review.LinterDiscard{{Rule: "CVE-2026-0001", Path: "package-lock.json", Line: 1, Reason: "duplicate"}},
	}
	out := Sections(report)
	for _, want := range []string{
		"Known advisories", "GHSA-xxxx-yyyy-zzzz  go.mod:1", "CVE-2026-0001  package-lock.json:1  (reported by the scanner, set aside by the review: duplicate)",
		"Security risks:\n  [critical/security] web/auth.go:40", "Bugs:\n  [error/correctness] app/db.py:11", "[nit/style] web/auth.go:12",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("sections lack %q:\n%s", want, out)
		}
	}
	// The advisory is not also a security risk, and the bug is not an advisory.
	if strings.Contains(out, "[warning/security] go.mod") {
		t.Errorf("an advisory was listed twice:\n%s", out)
	}
	empty := Sections(&review.Report{})
	for _, want := range []string{"Known advisories", "none reported. If osv-scanner is not installed", "Security risks:\n  none reported.", "Bugs:\n  none reported."} {
		if !strings.Contains(empty, want) {
			t.Errorf("empty sections lack %q:\n%s", want, empty)
		}
	}
}

func TestRemediationPlanOrdersBySeverityAndGroupsSharedFixes(t *testing.T) {
	plan := RemediationPlan([]review.Finding{
		{Path: "b.go", Line: 3, Severity: "warning", Class: "resource", Title: "Response body not closed"},
		{Path: "a.go", Line: 9, Severity: "warning", Class: "resource", Title: "response body not closed"},
		{Path: "c.go", Line: 1, Severity: "critical", Class: "security", Title: "Secret logged"},
		{Path: "d.go", Line: 2, Severity: "nit", Class: "style", Title: "Naming"},
	})
	lines := strings.Split(strings.TrimSpace(plan), "\n")
	if !strings.Contains(lines[1], "[critical/security] Secret logged") {
		t.Errorf("first item is not the critical one:\n%s", plan)
	}
	if !strings.Contains(plan, "2 finding(s) in 2 file(s): a.go, b.go") {
		t.Errorf("the two body-not-closed findings were not grouped:\n%s", plan)
	}
	if !strings.HasSuffix(strings.TrimSpace(plan), "1 finding(s) in 1 file(s): d.go") {
		t.Errorf("the nit is not last:\n%s", plan)
	}
	if RemediationPlan(nil) != "\nRemediation plan: nothing to remediate.\n" {
		t.Errorf("empty plan = %q", RemediationPlan(nil))
	}
}

// A committed credential the model graded warning still leads an error-grade
// crash: the incident before the bug. A security nit does not.
func TestRemediationPlanPutsASecurityWarningWithTheErrors(t *testing.T) {
	plan := RemediationPlan([]review.Finding{
		{Path: "b.go", Line: 3, Severity: "error", Class: "correctness", Title: "Deferred close panics on a request error"},
		{Path: "c.go", Line: 1, Severity: "warning", Class: "security", Title: "AWS credentials hardcoded"},
		{Path: "a.go", Line: 1, Severity: "critical", Class: "correctness", Title: "Data loss on restart"},
		{Path: "d.go", Line: 2, Severity: "nit", Class: "security", Title: "Comment names an internal host"},
		{Path: "e.go", Line: 2, Severity: "info", Class: "style", Title: "Naming"},
	})
	var items []string
	for _, l := range strings.Split(plan, "\n") {
		if strings.Contains(l, ". [") {
			items = append(items, l)
		}
	}
	want := []string{"[critical/correctness] Data loss", "[warning/security] AWS credentials", "[error/correctness] Deferred close", "[info/style] Naming", "[nit/security] Comment names"}
	if len(items) != len(want) {
		t.Fatalf("items = %q", items)
	}
	for i, w := range want {
		if !strings.Contains(items[i], w) {
			t.Errorf("item %d = %q, want %q\n%s", i+1, items[i], w, plan)
		}
	}
}

func TestCoverageNoticeNamesWhatWasLeftOut(t *testing.T) {
	tree := &vcs.Tree{Covered: []string{"a.go"}, Unbudgeted: []string{"z.go"}, Skipped: []vcs.TreeSkip{{Path: "img.png", Reason: "binary"}}}
	out := CoverageNotice(tree)
	for _, want := range []string{"Covered 1 file(s).", "budget ran out first (1 file(s)): z.go", "img.png: binary"} {
		if !strings.Contains(out, want) {
			t.Errorf("notice lacks %q:\n%s", want, out)
		}
	}
}
