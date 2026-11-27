package converse

import (
	"strings"
	"testing"

	"github.com/nitpick-oss/nitpick/internal/config"
)

// A slash command does what the mention does, kind for kind.
func TestASlashCommandMeansWhatTheMentionMeans(t *testing.T) {
	for _, tc := range []struct {
		verb string
		kind Kind
	}{
		{"review", KindReview},
		{"restart-review", KindRestartReview},
		{"resolve", KindResolve},
		{"fix", KindFix},
		{"fix all", KindFix},
		{"improve", KindImprove},
		{"why does this fail?", KindAsk},
	} {
		slashKind, slashText, slashOK := Command("/nitpick "+tc.verb, config.DefaultMention)
		mentionKind, mentionText, mentionOK := Command(config.DefaultMention+" "+tc.verb, config.DefaultMention)
		if !slashOK || slashKind != tc.kind {
			t.Errorf("/nitpick %s = (%q, %v), want %q", tc.verb, slashKind, slashOK, tc.kind)
		}
		if slashKind != mentionKind || slashText != mentionText || slashOK != mentionOK {
			t.Errorf("/nitpick %s = (%q, %q, %v) but the mention gives (%q, %q, %v)",
				tc.verb, slashKind, slashText, slashOK, mentionKind, mentionText, mentionOK)
		}
	}
}

// The slash command is read on a later line of a longer comment, and it keeps
// what the person wrote after it, fenced logs included.
func TestASlashCommandOnALaterLineKeepsTheTextAfterIt(t *testing.T) {
	body := "Thanks for the review.\r\n/nitpick why does this fail?\r\n```\r\npanic: nil\r\n```\r\n"
	kind, text, ok := Command(body, config.DefaultMention)
	if !ok || kind != KindAsk {
		t.Fatalf("= (%q, %v), want an ask", kind, ok)
	}
	if !strings.Contains(text, "why does this fail?") || !strings.Contains(text, "panic: nil") {
		t.Errorf("text = %q, want the question and the log under it", text)
	}
}

// Rule 10, quote: a reply that quotes "/nitpick fix" is not a command, and it
// is silent because the line is quoted. The same line unquoted is a command.
func TestAQuotedSlashCommandIsNotACommand(t *testing.T) {
	if kind, _, ok := Command("/nitpick fix", config.DefaultMention); !ok || kind != KindFix {
		t.Fatalf("the unquoted line = (%q, %v), want a fix", kind, ok)
	}
	for _, body := range []string{"> /nitpick fix", ">/nitpick fix", "> > /nitpick fix", "> quoted\n> /nitpick fix\n\nthanks"} {
		if _, _, ok := Command(body, config.DefaultMention); ok {
			t.Errorf("%q started a run", body)
		}
	}
}

// Rule 10, fence: the same command inside a fenced block is not a command, and
// it is silent because of the fence. The same line after the fence closes is.
func TestASlashCommandInsideAFencedBlockIsNotACommand(t *testing.T) {
	if _, _, ok := Command("```\n/nitpick fix\n```\n/nitpick review", config.DefaultMention); !ok {
		t.Fatal("the command after the closed fence was not read")
	}
	for name, body := range map[string]string{
		"backticks":      "```\n/nitpick fix\n```",
		"tildes":         "~~~\n/nitpick fix\n~~~",
		"language tag":   "```sh\n/nitpick fix\n```",
		"longer fence":   "````\n```\n/nitpick fix\n```\n````",
		"indented fence": "   ```\n/nitpick fix\n   ```",
		"never closed":   "```\n/nitpick fix",
		"pasted log":     "```\n$ git log\n/nitpick fix\n```\nsee above",
	} {
		if _, _, ok := Command(body, config.DefaultMention); ok {
			t.Errorf("%s: %q started a run", name, body)
		}
	}
}

// Rule 10: a comment that does not start the line with the command, or has
// nothing after it, returns no command.
func TestOnlyALineThatStartsWithTheCommandIsACommand(t *testing.T) {
	for _, body := range []string{
		"run /nitpick fix please",
		"Thanks!! /nitpick fix",
		"see https://github.com/nitpick-oss/nitpick/issues/1",
		"/nitpick-review fix",
		"/nitpicker fix",
		"    /nitpick fix",
		"/nitpick",
		"/nitpick   \n",
		"",
	} {
		if _, _, ok := Command(body, config.DefaultMention); ok {
			t.Errorf("%q started a run", body)
		}
	}
}

// The command is case-insensitive, as the workflow gate is.
func TestTheSlashCommandIgnoresCase(t *testing.T) {
	if kind, _, ok := Command("/NitPick fix", config.DefaultMention); !ok || kind != KindFix {
		t.Errorf("/NitPick fix = (%q, %v), want a fix", kind, ok)
	}
}

// With both triggers, the one written first decides.
func TestWhicheverTriggerComesFirstDecides(t *testing.T) {
	if kind, _, _ := Command("/nitpick fix\n"+config.DefaultMention+" resolve", config.DefaultMention); kind != KindFix {
		t.Errorf("slash first = %q, want fix", kind)
	}
	if kind, _, _ := Command(config.DefaultMention+" resolve\n/nitpick fix", config.DefaultMention); kind != KindResolve {
		t.Errorf("mention first = %q, want resolve", kind)
	}
}

// The slash command is not the mention's setting: an operator's own handle
// leaves it working.
func TestTheSlashCommandWorksWhateverTheMentionIs(t *testing.T) {
	if kind, _, ok := Command("/nitpick review", "@bot"); !ok || kind != KindReview {
		t.Errorf("= (%q, %v), want a review", kind, ok)
	}
}

// A mention pasted inside a fenced block (a log, a quoted workflow) must not
// shadow the request that follows it. The same mention outside the fence is a
// request and comes first, as before.
func TestAFencedMentionDoesNotShadowASlashCommandAfterIt(t *testing.T) {
	fenced := "```\n" + config.DefaultMention + " fix\n```\n/nitpick review"
	if kind, _, ok := Command(fenced, config.DefaultMention); !ok || kind != KindReview {
		t.Errorf("a fenced mention before a slash command = (%q, %v), want the slash command's review", kind, ok)
	}

	// Rule 10: the same text with the mention outside the fence is the mention's
	// fix. So the result above is the fence's doing, not the parser ignoring it.
	open := config.DefaultMention + " fix\n```\nlog\n```\n/nitpick review"
	if kind, _, ok := Command(open, config.DefaultMention); !ok || kind != KindFix {
		t.Errorf("an unfenced mention before a slash command = (%q, %v), want the mention's fix", kind, ok)
	}
}

// A mention that is alone in a fenced block still counts: this change adds a
// trigger and does not take one away.
func TestAFencedMentionWithNoSlashCommandStillCounts(t *testing.T) {
	for _, body := range []string{
		"```\n" + config.DefaultMention + " fix\n```",
		"```\n" + config.DefaultMention + " fix",
		"~~~\n" + config.LegacyMention + " fix\n~~~",
	} {
		if kind, _, ok := Command(body, config.DefaultMention); !ok || kind != KindFix {
			t.Errorf("%q = (%q, %v), want the fenced mention read as it always was", body, kind, ok)
		}
	}
}

// The slash command inside a fence, after a mention inside the same fence, is
// still nothing: both are pasted text, and the mention keeps its old reading.
func TestASlashCommandInsideTheSameFenceAsAMentionDoesNotWin(t *testing.T) {
	body := "```\n" + config.DefaultMention + " resolve\n/nitpick fix\n```"
	if kind, _, ok := Command(body, config.DefaultMention); !ok || kind != KindResolve {
		t.Errorf("= (%q, %v), want the mention's resolve", kind, ok)
	}
}
