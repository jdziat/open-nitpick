package practices

import (
	"strings"
	"testing"
	"time"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/standards"
)

func TestExceptionsExpireAndInvalidateOnEvidenceOrRuleChanges(t *testing.T) {
	now := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	files := []standards.File{{Path: "app.go", Src: []byte("package app\n")}}
	r := fixtureReport(Check{ID: "rules", Version: "1", Findings: []Finding{{Rule: "rule", Target: Target{Kind: FileTarget, ID: "app.go", Line: 1}, Title: "cost", Blocking: true}}})
	ApplyExceptions(&r, nil, files, now)
	fingerprint := r.Checks[0].Findings[0].Fingerprint
	if len(fingerprint) != 64 {
		t.Fatal("evidence was not fingerprinted")
	}
	exceptions := []config.PracticeException{{Rule: "rule", Target: "app.go", Fingerprint: fingerprint, Reason: "accepted transition", Expires: "2026-09-12"}}
	ApplyExceptions(&r, exceptions, files, now)
	if r.Checks[0].Findings[0].Exception == "" {
		t.Fatal("valid exception not applied")
	}
	ApplyExceptions(&r, exceptions, files, now.Add(24*time.Hour))
	if r.Checks[0].Findings[0].Exception != "" {
		t.Fatal("expired exception applied")
	}
	files[0].Src = []byte("package changed\n")
	ApplyExceptions(&r, exceptions, files, now)
	if r.Checks[0].Findings[0].Exception != "" {
		t.Fatal("changed source kept exception")
	}
	files[0].Src = []byte("package app\n")
	r.Checks[0].Version = "2"
	ApplyExceptions(&r, exceptions, files, now)
	if r.Checks[0].Findings[0].Exception != "" {
		t.Fatal("changed rule version kept exception")
	}
}

func TestExceptionsCannotMatchMissingEvidenceOrNeighboringFindings(t *testing.T) {
	now := time.Now()
	r := fixtureReport(Check{ID: "rules", Version: "1", Findings: []Finding{{Rule: "rule", Target: Target{Kind: FileTarget, ID: "app.go", Line: 1}, Title: "cost", Blocking: true}}})
	ApplyExceptions(&r, []config.PracticeException{{Rule: "rule", Target: "app.go", Fingerprint: strings.Repeat("0", 64), Reason: "no evidence"}}, nil, now)
	if r.Checks[0].Findings[0].Exception != "" || r.Checks[0].Findings[0].Fingerprint != "" {
		t.Fatal("missing evidence accepted")
	}
	files := []standards.File{{Path: "app.go", Src: []byte("package app\n// next\n")}}
	ApplyExceptions(&r, nil, files, now)
	exception := config.PracticeException{Rule: "rule", Target: "app.go", Fingerprint: r.Checks[0].Findings[0].Fingerprint, Reason: "first only"}
	r.Checks[0].Findings[0].Target.Line = 2
	ApplyExceptions(&r, []config.PracticeException{exception}, files, now)
	if r.Checks[0].Findings[0].Exception != "" {
		t.Fatal("exception suppressed a different site")
	}
}

func TestEmptyFileEvidenceRemainsDistinctFromMissingSource(t *testing.T) {
	r := fixtureReport(Check{ID: "linters", Version: "1", Findings: []Finding{{Rule: "empty-file", Target: Target{Kind: FileTarget, ID: "a.go"}, Title: "missing package declaration"}}})
	ApplyExceptions(&r, nil, []standards.File{{Path: "a.go", Src: []byte{}}}, time.Now())
	if r.Checks[0].Findings[0].Fingerprint == "" {
		t.Fatal("available empty file was treated as absent evidence")
	}
	ApplyExceptions(&r, nil, nil, time.Now())
	if r.Checks[0].Findings[0].Fingerprint != "" {
		t.Fatal("missing source acquired a fingerprint")
	}
}
