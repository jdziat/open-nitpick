package config

import (
	"os"
	"strings"
	"testing"
)

// docs/ci.md shows a respond workflow for people to copy. The mention default
// changed and the old handle is accepted for one release, so a copy that gates on
// only the new handle would stop answering the old one without saying so. The
// example must name every handle the reviewer answers to.
func TestTheDocumentedRespondGateNamesEveryHandleTheReviewerAnswersTo(t *testing.T) {
	raw, err := os.ReadFile("../../docs/ci.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	start := strings.Index(text, "  respond:\n    if: >-")
	if start < 0 {
		t.Fatal("the documented respond job is not in docs/ci.md, so this test reads nothing")
	}
	gate := text[start : start+strings.Index(text[start:], "runs-on:")]
	for _, handle := range []string{DefaultMention, LegacyMention} {
		if !strings.Contains(gate, "'"+handle+"'") {
			t.Errorf("the documented respond gate does not name %s:\n%s", handle, gate)
		}
	}
}
