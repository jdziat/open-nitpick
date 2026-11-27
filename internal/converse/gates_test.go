package converse

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/nitpick-oss/nitpick/internal/config"
)

// gateIf reads the if: of one job of one workflow in this repository.
func gateIf(t *testing.T, file, job string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", file))
	if err != nil {
		t.Fatal(err)
	}
	var wf struct {
		Jobs map[string]struct {
			If string `yaml:"if"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	expr := wf.Jobs[job].If
	if expr == "" {
		t.Fatalf("%s has no jobs.%s.if", file, job)
	}
	return expr
}

// comment is the event context a gate sees for a comment on a pull request.
func comment(body, userType, association string, inReplyTo any) map[string]any {
	return map[string]any{
		"github": map[string]any{
			"event_name": "issue_comment",
			"event": map[string]any{
				"issue": map[string]any{"pull_request": map[string]any{}},
				"comment": map[string]any{
					"body":               body,
					"user":               map[string]any{"type": userType},
					"author_association": association,
					"in_reply_to_id":     inReplyTo,
				},
			},
		},
	}
}

func gateRuns(t *testing.T, expr string, ctx map[string]any) bool {
	t.Helper()
	v, err := gateEval(expr, ctx)
	if err != nil {
		t.Fatal(err)
	}
	return gateTruthy(v)
}

// The gates are prefilters over the parser. Every comment the parser reads as a
// command must start the workflow that answers it, and a fix must start the fix
// workflow and not the respond one, or both would try to create one branch. A
// trigger added to one side and not the other is the drift this fails on.
func TestTheWorkflowGatesStartTheRunnerForEveryCommandTheParserReads(t *testing.T) {
	respond := gateIf(t, "nitpick-respond.yml", "respond")
	fix := gateIf(t, "nitpick-fix.yml", "fix")

	triggers := []string{config.DefaultMention, config.LegacyMention, SlashCommand, "@NITPICK-REVIEW"}
	verbs := []string{
		"review", "restart-review", "rereview", "resolve", "fixed in the last push",
		"fix", "fix all", "apply this", "improve", "polish this",
		"why does this fail?", "what does this do",
	}
	checked := 0
	for _, trigger := range triggers {
		for _, verb := range verbs {
			for _, body := range []string{trigger + " " + verb, "thanks\n" + trigger + " " + verb, "please " + trigger + " " + verb} {
				kind, _, ok := Command(body, config.DefaultMention)
				if !ok {
					continue // a body the parser refuses owes the gate nothing
				}
				checked++
				ctx := comment(body, "User", "MEMBER", nil)
				gotRespond, gotFix := gateRuns(t, respond, ctx), gateRuns(t, fix, ctx)
				switch {
				case kind == KindFix && (!gotFix || gotRespond):
					t.Errorf("%q is a fix: fix workflow runs=%v respond workflow runs=%v, want true and false", body, gotFix, gotRespond)
				case kind != KindFix && (!gotRespond || gotFix):
					t.Errorf("%q is %s: respond workflow runs=%v fix workflow runs=%v, want true and false", body, kind, gotRespond, gotFix)
				}
			}
		}
	}
	// Rule 10: the loop above is silent when it reads nothing. It read a lot.
	if checked < 60 {
		t.Fatalf("only %d comments were checked; the parser read almost none of them", checked)
	}
}

// Rule 10, and proof the evaluator evaluates: the same command is refused for
// a bot, for an outsider, and when it is not a command at all, each for its own
// reason. A gate that said yes to everything would pass the test above.
func TestTheWorkflowGatesRefuseWhatTheyAreMeantToRefuse(t *testing.T) {
	respond := gateIf(t, "nitpick-respond.yml", "respond")
	fix := gateIf(t, "nitpick-fix.yml", "fix")
	for name, tc := range map[string]struct {
		expr string
		ctx  map[string]any
	}{
		"respond, a bot wrote it":       {respond, comment("/nitpick review", "Bot", "MEMBER", nil)},
		"respond, a stranger wrote it":  {respond, comment("/nitpick review", "User", "NONE", nil)},
		"respond, a contributor":        {respond, comment("@nitpick-review review", "User", "CONTRIBUTOR", nil)},
		"respond, nobody was addressed": {respond, comment("looks good to me", "User", "MEMBER", nil)},
		"fix, a bot wrote it":           {fix, comment("/nitpick fix", "Bot", "OWNER", nil)},
		"fix, a stranger wrote it":      {fix, comment("/nitpick fix", "User", "NONE", nil)},
		"fix, only a review was asked":  {fix, comment("/nitpick review", "User", "OWNER", nil)},
	} {
		if gateRuns(t, tc.expr, tc.ctx) {
			t.Errorf("%s: the gate started a runner", name)
		}
	}
	// And the other side of each, so the refusals above are not a gate that is
	// shut for everyone.
	if !gateRuns(t, respond, comment("/nitpick review", "User", "MEMBER", nil)) {
		t.Error("respond: a member's command was refused")
	}
	if !gateRuns(t, fix, comment("/nitpick fix", "User", "OWNER", nil)) {
		t.Error("fix: an owner's fix was refused")
	}
}

// A reply inside a thread starts the respond workflow without a trigger, so the
// reviewer can read an answer to its own finding.
func TestAnInlineReplyStartsTheRespondWorkflowWithoutATrigger(t *testing.T) {
	respond := gateIf(t, "nitpick-respond.yml", "respond")
	if !gateRuns(t, respond, comment("I disagree", "User", "MEMBER", float64(42))) {
		t.Error("a reply in a thread was refused")
	}
	if gateRuns(t, respond, comment("I disagree", "User", "MEMBER", nil)) {
		t.Error("a plain comment with no trigger started a runner")
	}
}

// Every trigger the parser accepts appears in the gate as a literal, so adding
// one to the parser without the gate is a failure here and not a silent no-op.
func TestEveryTriggerTheParserAcceptsAppearsInTheRespondGate(t *testing.T) {
	respond := gateIf(t, "nitpick-respond.yml", "respond")
	for _, trigger := range []string{config.DefaultMention, config.LegacyMention, SlashCommand} {
		if !gateRuns(t, respond, comment(trigger+" review", "User", "MEMBER", nil)) {
			t.Errorf("the respond gate does not start for %s", trigger)
		}
	}
}

// The evaluator itself, against GitHub's documented behaviour, so the tests
// above rest on something checked.
func TestTheGateEvaluatorFollowsGitHubExpressionRules(t *testing.T) {
	ctx := map[string]any{"a": map[string]any{"s": "Hello World", "n": nil}}
	for expr, want := range map[string]any{
		"contains(a.s, 'WORLD')":                    true,
		"contains(a.s, 'xyz')":                      false,
		"contains(fromJSON('[\"A\", \"B\"]'), 'b')": true,
		"a.n != null":                               false,
		"a.s == 'hello world'":                      true,
		"!contains(a.s, 'xyz') && a.s":              "Hello World",
		"false || a.s":                              "Hello World",
		"true || false && false":                    true,
		"(true || false) && false":                  false,
		"contains(a.s, 'World') && 'yes' || 'no'":   "yes",
		"contains(a.s, 'xyz') && 'yes' || 'no'":     "no",
		"missing.path == null":                      true,
		"contains(a.s, 'it''s')":                    false,
	} {
		got, err := gateEval(expr, ctx)
		if err != nil || got != want {
			t.Errorf("%s = (%v, %v), want %v", expr, got, err, want)
		}
	}
	if _, err := gateEval("startsWith(a.s, 'H')", ctx); err == nil {
		t.Error("an unsupported function was evaluated instead of refused")
	}
}

// Whitespace is the one thing a workflow expression cannot match the way the
// parser does: "contains(body, '/nitpick fix')" sees one space and nothing else.
// The workflow allowed to write is chosen by that match, so the parser reads fix
// and apply as a fix only after one space. After anything else they are a
// question, which the read-only workflow answers. This holds, for every trigger and
// every gap, that the two never disagree: a body the parser reads as a fix starts
// the fix workflow and not the respond one.
func TestTheParserAndTheGatesNeverDisagreeAboutAFixWhateverTheWhitespace(t *testing.T) {
	respond := gateIf(t, "nitpick-respond.yml", "respond")
	fix := gateIf(t, "nitpick-fix.yml", "fix")
	fixes, questions := 0, 0
	for _, trigger := range []string{config.DefaultMention, config.LegacyMention, SlashCommand, "please " + config.DefaultMention} {
		for _, gap := range []string{" ", "  ", "   ", "\t", " \t ", "\n", "\r\n", " \n", "\n\n", ": ", ":  ", ",\n", ":\t"} {
			for _, verb := range []string{"fix", "fix all", "apply this", "Fix.", "FIX"} {
				body := trigger + gap + verb
				kind, _, ok := Command(body, config.DefaultMention)
				if !ok {
					continue
				}
				ctx := comment(body, "User", "MEMBER", nil)
				gotFix, gotRespond := gateRuns(t, fix, ctx), gateRuns(t, respond, ctx)
				if kind == KindFix {
					fixes++
					if !gotFix || gotRespond {
						t.Errorf("%q is a fix: fix workflow runs=%v respond workflow runs=%v, want true and false", body, gotFix, gotRespond)
					}
					continue
				}
				questions++
				if kind != KindAsk {
					t.Errorf("%q = %q, want a fix or a question", body, kind)
				}
				// A question is answered by the respond workflow. The fix gate is a
				// looser prefilter, so it may also start for these, and then the
				// binary it runs reads the comment again and writes nothing.
				if !gotRespond && !gotFix {
					t.Errorf("%q is a question and neither workflow starts for it", body)
				}
			}
		}
	}
	if fixes < 20 || questions < 40 {
		t.Fatalf("checked %d fixes and %d questions; the sweep is not reading enough of either", fixes, questions)
	}
}

// Rule 9 for the gap: the parser's own rule, stated directly. After exactly one
// space a fix is a fix; after any other gap it is a question and carries its text.
func TestAFixCountsOnlyAfterASingleSpace(t *testing.T) {
	for _, trigger := range []string{config.DefaultMention, SlashCommand} {
		for gap, want := range map[string]Kind{
			" ": KindFix, "  ": KindAsk, "\t": KindAsk, "\n": KindAsk, "\r\n": KindAsk, " \n": KindAsk, ": ": KindAsk,
		} {
			// The colon is punctuation after a mention. After the slash command it is
			// part of the next word, "/nitpick:", which is not the command.
			if trigger == SlashCommand && gap == ": " {
				continue
			}
			for _, verb := range []string{"fix", "apply"} {
				got, text, ok := Command(trigger+gap+verb+" this", config.DefaultMention)
				if !ok || got != want {
					t.Errorf("%q = (%q, %v), want %q", trigger+gap+verb, got, ok, want)
				}
				if want == KindAsk && !strings.Contains(text, verb) {
					t.Errorf("%q: the question lost its text %q", trigger+gap+verb, text)
				}
			}
		}
	}
	// The other verbs are unaffected by the gap: a resolve is a resolve.
	for _, gap := range []string{" ", "  ", "\t", "\n"} {
		if kind, _, ok := Command(SlashCommand+gap+"resolve", config.DefaultMention); !ok || kind != KindResolve {
			t.Errorf("resolve after %q = (%q, %v)", gap, kind, ok)
		}
	}
	if kind, _, ok := Command(config.DefaultMention+": resolve", config.DefaultMention); !ok || kind != KindResolve {
		t.Errorf("resolve after a colon = (%q, %v)", kind, ok)
	}
}

// The same bodies, as non-fixes: whatever the whitespace, a resolve or a question
// still goes to the respond workflow and not to the fix one.
func TestOddWhitespaceInANonFixStillGoesToTheRespondWorkflow(t *testing.T) {
	respond := gateIf(t, "nitpick-respond.yml", "respond")
	fix := gateIf(t, "nitpick-fix.yml", "fix")
	checked := 0
	for _, trigger := range []string{config.DefaultMention, SlashCommand} {
		for _, gap := range []string{" ", "  ", "\t", "\n"} {
			for _, verb := range []string{"review", "resolve", "fixed in the last push", "why does this fail?"} {
				body := trigger + gap + verb
				kind, _, ok := Command(body, config.DefaultMention)
				if !ok || kind == KindFix {
					continue
				}
				checked++
				ctx := comment(body, "User", "MEMBER", nil)
				if !gateRuns(t, respond, ctx) || gateRuns(t, fix, ctx) {
					t.Errorf("%q: respond runs=%v fix runs=%v, want true and false", body, gateRuns(t, respond, ctx), gateRuns(t, fix, ctx))
				}
			}
		}
	}
	if checked < 16 {
		t.Fatalf("only %d non-fixes were checked", checked)
	}
}

// The gap rule also holds when the slash command is read after a mention that sat
// inside a code fence, which is a separate path through Command.
func TestAFixAfterAFencedMentionObeysTheSameGapRule(t *testing.T) {
	fenced := "```\n" + config.DefaultMention + " review\n```\n"
	for gap, want := range map[string]Kind{" ": KindFix, "  ": KindAsk, "\t": KindAsk, "\n": KindAsk} {
		got, _, ok := Command(fenced+SlashCommand+gap+"fix", config.DefaultMention)
		if !ok || got != want {
			t.Errorf("a fix after a fenced mention with gap %q = (%q, %v), want %q", gap, got, ok, want)
		}
	}
}
