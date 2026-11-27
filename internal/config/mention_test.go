package config

import "testing"

// The handle is the App's name. A bare @nitpick is another person's account,
// so a default that drifted back to it would notify them on every mention.
func TestTheDefaultMentionIsTheAppNameAndNotAnotherPersonsAccount(t *testing.T) {
	if got := Defaults().Review.Mention; got != "@nitpick-review" {
		t.Errorf("default mention = %q, want @nitpick-review", got)
	}
	if DefaultMention == "@nitpick" {
		t.Error("the default mention is a bare @nitpick")
	}
	if LegacyMention != "@open-nitpick" {
		t.Errorf("legacy mention = %q, want the old default kept for one release", LegacyMention)
	}
}
