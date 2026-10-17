package review

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/bundle"
)

func TestAHugeSkipListIsCountedNotListed(t *testing.T) {
	var skips []bundle.Skip
	for i := 0; i < 447; i++ {
		skips = append(skips, bundle.Skip{Path: fmt.Sprintf("declaration:pkg/f%d.go:%d", i, i), Reason: "design task exceeds max_files_per_request"})
	}
	got := groupByReason(skips, "")
	if n := len(got); n > 2000 {
		t.Fatalf("447 skipped paths rendered %d bytes; the list must be bounded", n)
	}
	if !strings.Contains(got, "and 439 more (447 in all)") {
		t.Errorf("the omitted paths are not counted:\n%s", got)
	}
	if strings.Count(got, "declaration:") != maxPathsPerReason {
		t.Errorf("listed %d paths, want %d", strings.Count(got, "declaration:"), maxPathsPerReason)
	}
}

func TestAShortSkipListIsListedInFull(t *testing.T) {
	skips := []bundle.Skip{{Path: "a.go", Reason: "binary"}, {Path: "b.go", Reason: "binary"}}
	got := groupByReason(skips, "")
	if got != "- binary: a.go, b.go\n" {
		t.Errorf("got %q", got)
	}
}
