package review

import (
	"strings"
	"testing"
	"time"

	"github.com/jdziat/open-nitpick/internal/knowledge"
)

func shapedHit(id, path string, score float64) knowledge.Hit {
	return knowledge.Hit{
		Entry: knowledge.Entry{
			ID: id, Title: "title " + id, Body: strings.Repeat("body sentence. ", 40),
			Source: "https://example.invalid/" + id, Checked: time.Now(),
		},
		Score: score,
		Path:  path,
	}
}

// The budget counts the framing, not just the entries. The heading and the
// disclaimer are most of a one-entry section, and ignoring them would let a
// section overrun the number an operator wrote by the size of the words that
// make it safe to read.
func TestTheKnowledgeBudgetCountsItsOwnFraming(t *testing.T) {
	hits := []knowledge.Hit{shapedHit("a", "", 0.9), shapedHit("b", "", 0.8), shapedHit("c", "", 0.7)}

	full := knowledgeTokens(knowledgeSection(hits))
	if full == 0 {
		t.Fatal("a rendered section costs no tokens")
	}

	// Zero is unbounded, which is what shipped and what was measured.
	if got := fitKnowledge(hits, 0); len(got) != 3 {
		t.Errorf("an unbounded budget kept %d of 3 entries", len(got))
	}

	fitted := fitKnowledge(hits, full/2)
	if len(fitted) == 0 || len(fitted) >= 3 {
		t.Fatalf("half the budget kept %d of 3 entries, want some but not all", len(fitted))
	}
	if got := knowledgeTokens(knowledgeSection(fitted)); got > full/2 {
		t.Errorf("the fitted section costs %d tokens against a budget of %d", got, full/2)
	}

	// Best first in, least relevant dropped.
	if fitted[0].Entry.ID != "a" {
		t.Errorf("fitting dropped the closest entry, kept %q first", fitted[0].Entry.ID)
	}
}

// A budget too small for one entry yields no section: a disclaimer about
// reference material with no reference material under it is tokens spent on
// nothing.
func TestATinyBudgetYieldsNoSectionRatherThanAnEmptyHeading(t *testing.T) {
	got := fitKnowledge([]knowledge.Hit{shapedHit("a", "", 0.9)}, 5)
	if len(got) != 0 {
		t.Fatalf("a 5-token budget kept %d entries", len(got))
	}
	if s := knowledgeSection(got); s != "" {
		t.Errorf("an empty hit list still rendered a section:\n%s", s)
	}
}

// The section still says three times over that none of this was written about
// the change. That sentence is what keeps a reviewer from reporting the
// reference, and no shaping here may cost it.
func TestTheSectionStillRefusesToBeEvidence(t *testing.T) {
	s := knowledgeSection([]knowledge.Hit{shapedHit("a", "", 0.9)})
	for _, want := range []string{"not findings", "None of this was written about this", "report nothing on the strength of this"} {
		if !strings.Contains(s, want) {
			t.Errorf("the section no longer says %q:\n%s", want, s)
		}
	}
}

// Applicability is rendered rather than filtered on: nothing here knows the
// versions a change runs under, and the model is already asked to judge.
func TestApplicabilityReachesThePrompt(t *testing.T) {
	h := shapedHit("a", "", 0.9)
	h.Entry.Versions = "go >= 1.23"
	h.Entry.Frameworks = []string{"gin", "echo"}

	s := knowledgeSection([]knowledge.Hit{h})
	if !strings.Contains(s, "go >= 1.23") {
		t.Errorf("the version boundary did not reach the prompt:\n%s", s)
	}
	if !strings.Contains(s, "gin, echo") {
		t.Errorf("the frameworks did not reach the prompt:\n%s", s)
	}
}
