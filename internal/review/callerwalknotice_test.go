package review

import (
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
)

// The caller walk's ceiling was a silent truncation once: a file with no
// callers attached read the same as a file with no callers. The plan flag is
// tested in internal/bundle; this is the sentence reaching the reader, and
// reaching them with review.summary off, since it describes what the review
// did not see.
func TestCallerWalkTruncationIsPublished(t *testing.T) {
	const sentence = "stopped at its ceiling of candidate files"

	report := &Report{Plan: &bundle.Plan{CallerWalkTruncated: true}}
	if got := Render(report, diff.Files{}, config.Defaults()).Summary; !strings.Contains(got, sentence) {
		t.Fatalf("summary lacks the truncation notice:\n%s", got)
	}

	quiet := config.Defaults()
	quiet.Review.Summary = false
	if got := Render(report, diff.Files{}, quiet).Summary; !strings.Contains(got, sentence) {
		t.Fatalf("summary with review.summary off lacks the truncation notice:\n%s", got)
	}

	report = &Report{Plan: &bundle.Plan{}}
	if got := Render(report, diff.Files{}, config.Defaults()).Summary; strings.Contains(got, sentence) {
		t.Fatalf("summary carries the notice for a walk that finished:\n%s", got)
	}
}
