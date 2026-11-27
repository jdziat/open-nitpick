package converse

import (
	"testing"

	"github.com/nitpick-oss/nitpick/internal/config"
)

// The default handle is the App's name, and the old one still works beside it
// for a release.
func TestTheDefaultMentionAcceptsTheNewHandleAndTheLegacyOne(t *testing.T) {
	for _, handle := range []string{config.DefaultMention, config.LegacyMention, "@NITPICK-REVIEW"} {
		kind, _, ok := Command(handle+" review", config.DefaultMention)
		if !ok || kind != KindReview {
			t.Errorf("%s review = (%q, %v), want a review command", handle, kind, ok)
		}
	}
}

// Rule 10: the legacy handle is silent because the operator chose another one,
// not because the parser read nothing.
func TestALegacyHandleIsIgnoredWhenTheOperatorChoseTheirOwn(t *testing.T) {
	if kind, _, ok := Command("@bot review", "@bot"); !ok || kind != KindReview {
		t.Fatalf("the chosen handle itself = (%q, %v), want a review command", kind, ok)
	}
	if _, _, ok := Command(config.LegacyMention+" review", "@bot"); ok {
		t.Error("the legacy handle answered although the operator set review.mention")
	}
}

// A bare @nitpick is another person's account. It must never address the
// reviewer under the default, or a stray mention would run one.
func TestTheBareNitpickHandleDoesNotAddressTheReviewer(t *testing.T) {
	if _, _, ok := Command("@nitpick review", config.DefaultMention); ok {
		t.Error("@nitpick addressed the reviewer")
	}
}

// The new handle is a prefix of nothing else: "@nitpick-reviewer" is not it.
func TestAHandleThatMerelyStartsWithTheMentionIsNotTheMention(t *testing.T) {
	if _, _, ok := Command("@nitpick-reviewer review", config.DefaultMention); ok {
		t.Error("@nitpick-reviewer addressed the reviewer")
	}
}
