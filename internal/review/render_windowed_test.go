package review

import (
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/bundle"
)

// TestWindowedFilesAreDisclosed is the regression test for a disclosure that
// went missing while the context handling got better.
//
// A file too large for its full content used to be refused outright, landing in
// Plan.Degraded, which the summary prints. Windowing it instead is an
// improvement — a window beats a bare diff — but it moved the file onto
// Plan.Windowed, which nothing rendered. The reader went from being told "this
// was reviewed from the diff alone" to being told nothing, on a file where most
// of the content had been elided.
func TestWindowedFilesAreDisclosed(t *testing.T) {
	report := &Report{
		Summary: "Walkthrough.",
		Plan: &bundle.Plan{
			Windowed: []bundle.Skip{{Path: "huge.go", Reason: "exceeded review.max_file_bytes"}},
		},
	}

	summary := renderSummary(report, nil)

	if !strings.Contains(summary, "huge.go") {
		t.Errorf("a windowed file must be named:\n%s", summary)
	}
	// The heading itself, because bundle.Plan.Windowed's doc now cites it by
	// name as the thing that closed the disclosure gap. Naming a heading in a
	// comment and asserting only that the path appears somewhere leaves the
	// comment able to go stale in the direction that matters: the file could be
	// listed under "Files not reviewed" and the two checks below would still be
	// the only thing standing between a reader and the wrong sentence.
	if !strings.Contains(summary, "Reviewed with reduced file context") {
		t.Errorf("the windowed section must carry the heading bundle.Plan.Windowed's doc cites:\n%s", summary)
	}
	// The wording has to distinguish it from both neighbours: this file WAS
	// reviewed, and it DID carry file context, just not all of it.
	if strings.Contains(summary, "Files not reviewed") {
		t.Errorf("a reviewed file must not be listed as unreviewed:\n%s", summary)
	}
	if strings.Contains(summary, "diff only") {
		t.Errorf("a windowed file had file context; it was not diff-only:\n%s", summary)
	}
}
