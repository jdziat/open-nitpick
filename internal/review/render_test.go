package review

import (
	"strings"
	"testing"
)

func TestPathSpanKeepsHostilePathInsideOneCell(t *testing.T) {
	got := PathSpan("a|b`c</td>\nd.go:7")
	for _, bad := range []string{"|", "\n", "</td>"} {
		if strings.Contains(got, bad) {
			t.Fatalf("path span %q still contains %q", got, bad)
		}
	}
	if strings.Count(got, "`") != 2 {
		t.Fatalf("path span %q must have exactly its own two backticks", got)
	}
}
