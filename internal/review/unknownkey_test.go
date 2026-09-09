package review

import (
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/config"
)

// Keys this build does not have are named on the pull request, not only in the
// CI log.
//
// The two audiences are different people. The operator who set the opt-in
// reads the log; the contributor who wrote the key reads the pull request, and
// a setting they believe is in force and is not changes how every finding
// below should be read.
func TestTheUnknownKeyNoticeIsPublished(t *testing.T) {
	cfg := config.Defaults()
	cfg.Unknown = []string{"max_fils (line 3)"}

	summary := renderSummary(&Report{Plan: &bundle.Plan{}, Policy: Policy{Config: cfg}}, nil)

	for _, want := range []string{"max_fils (line 3)", config.EnvIgnoreUnknownKeys, "typo"} {
		if !strings.Contains(summary, want) {
			t.Errorf("the notice never says %q:\n%s", want, summary)
		}
	}

	// Every line of it inside the blockquote, for the reason
	// TestTheNoticeContainsEveryLineItPrints records: text that escapes the
	// quote renders at top level of a comment posted under this tool's name.
	notice := unknownKeyNotice(&Report{Plan: &bundle.Plan{}, Policy: Policy{Config: cfg}})
	for _, line := range strings.Split(strings.TrimRight(notice, "\n"), "\n") {
		if line != "" && !strings.HasPrefix(line, ">") {
			t.Errorf("this line escaped the blockquote:\n%q", line)
		}
	}
}

// A run with nothing ignored publishes nothing.
//
// The empty case is what makes the notice evidence rather than boilerplate a
// reader learns to skip.
func TestNoUnknownKeysPublishesNoNotice(t *testing.T) {
	cfg := config.Defaults()
	if got := unknownKeyNotice(&Report{Plan: &bundle.Plan{}, Policy: Policy{Config: cfg}}); got != "" {
		t.Errorf("notice = %q, want none", got)
	}
	if got := unknownKeyNotice(&Report{Plan: &bundle.Plan{}}); got != "" {
		t.Errorf("notice = %q for a report with no config, want none", got)
	}
}

// A key carrying markup or a backtick cannot break out of the span that holds
// it.
//
// The key is bytes the change under review chose, and it reaches a comment
// posted under this tool's name. inline escapes HTML and does not neutralise
// markdown, so the backtick half is this call site's to close: a key that ends
// the code span renders an attacker's link as the bot's own prose.
func TestAnUnknownKeyCannotEscapeItsNotice(t *testing.T) {
	cfg := config.Defaults()
	cfg.Unknown = []string{
		"</blockquote><img src=x onerror=alert(1)> (line 3)",
		"a`[CLICK ME](https://evil.example)`b (line 4)",
	}

	notice := unknownKeyNotice(&Report{Plan: &bundle.Plan{}, Policy: Policy{Config: cfg}})

	if strings.Contains(notice, "<img") {
		t.Errorf("the key opened a tag:\n%s", notice)
	}
	if !strings.Contains(notice, "&lt;img") {
		t.Errorf("the markup was not escaped:\n%s", notice)
	}
	// The link text may survive; inside a closed span it renders as text. What
	// must not survive is the backtick that would close the span around it and
	// leave the link live.
	if strings.Contains(notice, "a`[CLICK ME]") {
		t.Errorf("the key's backtick closed its code span:\n%s", notice)
	}

	// Every backtick in the rendered notice is one this function wrote, so the
	// spans it opens are the spans it closes.
	if n := strings.Count(notice, "`"); n%2 != 0 {
		t.Errorf("odd number of backticks (%d), so a span is left open:\n%s", n, notice)
	}
}
