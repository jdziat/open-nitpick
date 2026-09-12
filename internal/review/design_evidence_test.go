package review

import (
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

func TestDesignEvidenceOutsideDiffPublishesOnlyInSummary(t *testing.T) {
	finding := Finding{Path: "caller.go", Line: 2, Title: "Caller violates the new contract", Rationale: "The caller retains the old argument convention.", Class: "correctness", Severity: "warning", TaskContext: &TaskContext{ID: "package:api", Text: "full source", Lines: map[string]int{"caller.go": 3}}}
	kept, discarded, unpublished := (&Engine{Config: config.Defaults()}).filterTaskAnchors([]Finding{finding}, nil)
	if len(kept) != 1 || !kept[0].SummaryOnly || kept[0].Line != 2 || len(discarded) != 0 || len(unpublished) != 0 {
		t.Fatalf("valid task source lost or moved: kept=%+v dropped=%+v unpublished=%+v", kept, discarded, unpublished)
	}
	report := &Report{Findings: kept, Counts: counts(kept), PullRequest: &vcs.PullRequest{SourceBaseURL: "https://forge.example/fork/repo/blob/head-sha"}}
	rendered := Render(report, nil, config.Defaults())
	if len(rendered.Comments) != 0 || !strings.Contains(rendered.Summary, "caller.go:2") || !strings.Contains(rendered.Summary, finding.Title) || !strings.Contains(rendered.Summary, `href="https://forge.example/fork/repo/blob/head-sha/caller.go#L2"`) {
		t.Fatalf("summary evidence became an inline anchor or disappeared: %+v", rendered)
	}
}

func TestUnsupportedTaskLocationRetainsUnpublishedClaim(t *testing.T) {
	finding := Finding{Path: "missing.go", Line: 9, Title: "Unsupported claim", Class: "correctness", Severity: "warning", TaskContext: &TaskContext{ID: "package:api", Lines: map[string]int{"caller.go": 3}}}
	kept, _, unpublished := (&Engine{Config: config.Defaults()}).filterTaskAnchors([]Finding{finding}, nil)
	if len(kept) != 0 || len(unpublished) != 1 || unpublished[0].Unresolved == "" {
		t.Fatalf("unsupported evidence silently disappeared: kept=%+v unpublished=%+v", kept, unpublished)
	}
	rendered := Render(&Report{UnpublishedModelFindings: unpublished}, nil, config.Defaults())
	if len(rendered.Comments) != 0 || !strings.Contains(rendered.Summary, "Unsupported claim") || !strings.Contains(rendered.Summary, "unsupported source locations") {
		t.Fatalf("raw evidence was lost or invented an inline location: %+v", rendered)
	}
}

func TestDesignEvidenceLinksEscapePathsAndRejectUnsafeMetadata(t *testing.T) {
	finding := Finding{Path: "src/a #?.go", Line: 7}
	report := &Report{PullRequest: &vcs.PullRequest{SourceBaseURL: "https://forge.example/fork/repo/blob/revision"}}
	location := designEvidenceLocation(report, finding)
	if !strings.Contains(location, `href="https://forge.example/fork/repo/blob/revision/src/a%20%23%3F.go#L7"`) {
		t.Fatalf("source link corrupted path: %s", location)
	}
	for _, base := range []string{"", "javascript:alert(1)", "https://user:secret@forge.example/repo"} {
		report.PullRequest.SourceBaseURL = base
		if location := designEvidenceLocation(report, finding); strings.Contains(location, "href=") || !strings.Contains(location, "src/a #?.go:7") {
			t.Fatalf("unsafe metadata produced a link or hid location: %s", location)
		}
	}
}

func TestDesignReceiptCountsOnlySuccessfulChangedSources(t *testing.T) {
	file := &diff.File{Path: "a.go"}
	report := &Report{Files: diff.Files{file}, DesignExecution: &DesignExecution{}, Plan: &bundle.Plan{Batches: []bundle.Batch{{DesignTask: "a", Entries: []bundle.Entry{{File: file}, {File: &diff.File{Path: "caller.go"}, SourceOnly: true}}}}}}
	if text := receipt(report); !strings.Contains(text, "Read 0 of 1 changed file") {
		t.Fatalf("planned task counted as read: %s", text)
	}
	report.AssessedDesignTasks = []string{"a"}
	if text := receipt(report); !strings.Contains(text, "Read 1 changed file.") {
		t.Fatalf("successful task scope was miscounted: %s", text)
	}
}
