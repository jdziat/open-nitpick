package review

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	llms "github.com/nocturnium/llm-go-sdk/v6"

	"github.com/nitpick-oss/nitpick/internal/config"
	"github.com/nitpick-oss/nitpick/internal/diff"
	"github.com/nitpick-oss/nitpick/internal/llm"
	"github.com/nitpick-oss/nitpick/internal/vcs"
)

func twoFindings() []Finding {
	return []Finding{
		{Path: "app.go", Line: 4, Severity: "error", Category: "correctness", Class: "correctness",
			Title: "Ignored error from http.Get", Rationale: "resp may be nil, so the deferred Close panics."},
		{Path: "app.go", Line: 5, Severity: "info", Category: "correctness", Class: "correctness",
			Title: "Close error is dropped", Rationale: "Body.Close returns an error nobody reads."},
	}
}

// toolLLM is a checker that answers by script: for the finding whose title is
// in the first user message, step N of its script is the Nth assistant turn.
type toolLLM struct {
	mu     sync.Mutex
	script map[string][]llms.Response
	err    error
	calls  int
	tools  []string // tool results handed back, in order
}

func (t *toolLLM) GenerateContent(_ context.Context, msgs []llms.Message, _ ...llms.CallOption) (*llms.Response, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.calls++
	if t.err != nil {
		return nil, t.err
	}
	step := 0
	for _, m := range msgs {
		switch m.Role {
		case llms.RoleAssistant:
			step++
		case llms.RoleTool:
			t.tools = append(t.tools, m.Content)
		}
	}
	for title, steps := range t.script {
		if strings.Contains(msgs[1].Content, "Title: "+title) {
			if step >= len(steps) {
				return &llms.Response{}, nil
			}
			r := steps[step]
			return &r, nil
		}
	}
	return &llms.Response{}, nil
}
func (t *toolLLM) Stream(context.Context, []llms.Message, ...llms.CallOption) (<-chan llms.StreamChunk, error) {
	return nil, errors.New("not supported")
}
func (t *toolLLM) Provider() llms.Provider { return "tool" }
func (t *toolLLM) Model() string           { return "tool" }

func call(id, name string, args map[string]any) llms.ToolCall {
	b, _ := json.Marshal(args)
	return llms.ToolCall{ID: id, Type: llms.ToolTypeFunction, Function: &llms.FunctionCall{Name: name, Arguments: string(b)}}
}

func verdict(v, reason string) llms.Response {
	return llms.Response{ToolCalls: []llms.ToolCall{call("v", "submit_verdict", map[string]any{"verdict": v, "reason": reason})}}
}

func lookup(path string) llms.Response {
	return llms.Response{ToolCalls: []llms.ToolCall{call("r", "read_file", map[string]any{"path": path})}}
}

func verifyEngine(t *testing.T, checker *toolLLM, mode config.VerifyMode) (*Engine, *scriptedLLM) {
	t.Helper()
	fs := twoFindings()
	model := &scriptedLLM{byPrompt: map[string]string{
		"Review the following changes": mustJSON(t, Result{Findings: fs}),
		"triaging findings":            mustJSON(t, TriageResult{Summary: "s", Verdicts: verdictsFor(fs)}),
	}}
	e := newEngine(t, model, &stubProvider{diff: engineDiff, content: map[string]string{"app.go": "package app\n"}}, func(c *config.Config) {
		c.Review.Verify = mode
	})
	e.Roles.Validate = llm.NewClientForTest(checker, e.Config.Models.Default)
	return e, model
}

func TestTheVerifyKeepRuleConfirmedKeepsOnlyConfirmedAndNotRefutedDropsOnlyRefuted(t *testing.T) {
	cases := []struct {
		mode    config.VerifyMode
		verdict string
		want    bool
	}{
		{config.VerifyConfirmed, verdictConfirmed, true},
		{config.VerifyConfirmed, verdictRefuted, false},
		{config.VerifyConfirmed, verdictUncertain, false},
		{config.VerifyNotRefuted, verdictConfirmed, true},
		{config.VerifyNotRefuted, verdictRefuted, false},
		{config.VerifyNotRefuted, verdictUncertain, true},
		// A verdict outside the vocabulary checked nothing.
		{config.VerifyConfirmed, "", true},
		{config.VerifyConfirmed, "probably", true},
	}
	for _, c := range cases {
		if got := keepAfterVerify(c.mode, c.verdict); got != c.want {
			t.Errorf("keepAfterVerify(%q, %q) = %v, want %v", c.mode, c.verdict, got, c.want)
		}
	}
}

// Off is the default and off means no checker call: a pass costing about eight
// model calls a finding must not run on a repository that did not ask for it.
func TestVerificationNeverRunsUnlessAskedFor(t *testing.T) {
	checker := &toolLLM{}
	e, _ := verifyEngine(t, checker, "")
	report, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if checker.calls != 0 {
		t.Errorf("the checker was called %d times with review.verify off", checker.calls)
	}
	if len(report.Findings) != 2 || len(report.Overruled) != 0 {
		t.Errorf("findings %d, withheld %d, want both published untouched", len(report.Findings), len(report.Overruled))
	}
}

func TestVerificationWithholdsARefutedFindingAndSaysWhyAfterReadingTheFile(t *testing.T) {
	checker := &toolLLM{script: map[string][]llms.Response{
		"Ignored error from http.Get": {lookup("app.go"), verdict(verdictConfirmed, "app.go:4 discards the error")},
		"Close error is dropped":      {verdict(verdictRefuted, "app.go:5 is wrapped by a deferred check")},
	}}
	e, _ := verifyEngine(t, checker, config.VerifyConfirmed)
	report, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Findings) != 1 || report.Findings[0].Title != "Ignored error from http.Get" {
		t.Fatalf("findings = %+v, want only the confirmed one", report.Findings)
	}
	if len(report.Overruled) != 1 {
		t.Fatalf("withheld = %d, want the refuted one", len(report.Overruled))
	}
	got := report.Overruled[0]
	if got.Expert != verifyExpert || got.Finding.Title != "Close error is dropped" || !strings.Contains(got.Reason, "deferred check") {
		t.Errorf("withheld record = %+v, want the refuted finding with the checker's reason", got)
	}
	if len(checker.tools) == 0 || !strings.Contains(checker.tools[0], "package app") {
		t.Errorf("tool results = %q, want the file the checker asked for handed back", checker.tools)
	}
}

// A finding the check could not confirm was not disagreed with, so the summary
// must not say an expert disagreed; a validation-only report keeps that wording.
func TestAFindingWithheldByVerificationIsNotBlamedOnADisagreeingExpert(t *testing.T) {
	finding := Finding{Path: "app.go", Line: 5, Title: "Close error is dropped", Severity: "warning"}
	verified := &Report{Overruled: []Overruled{{Finding: finding, Expert: verifyExpert, Reason: "could not be confirmed against the repository: no caller found"}}}
	summary := renderSummary(verified, nil)
	if strings.Contains(summary, "domain expert disagreed") || !strings.Contains(summary, "withheld by a second check") {
		t.Errorf("verification-withheld summary misdescribes the cause:\n%s", summary)
	}

	validated := &Report{Overruled: []Overruled{{Finding: finding, Expert: "a Go expert", Reason: "the error is checked by the deferred call"}}}
	if got := validated.WithheldCause(); got != "after a domain expert disagreed" {
		t.Errorf("validation-only cause = %q, want the expert wording kept", got)
	}
}

func TestAnUncertainVerdictIsWithheldUnderConfirmedAndKeptUnderNotRefuted(t *testing.T) {
	script := map[string][]llms.Response{
		"Ignored error from http.Get": {verdict(verdictConfirmed, "ok")},
		"Close error is dropped":      {verdict(verdictUncertain, "depends on the Body type")},
	}
	for mode, want := range map[config.VerifyMode]int{config.VerifyConfirmed: 1, config.VerifyNotRefuted: 2} {
		e, _ := verifyEngine(t, &toolLLM{script: script}, mode)
		report, err := e.Review(context.Background(), vcs.Ref{})
		if err != nil {
			t.Fatal(err)
		}
		if len(report.Findings) != want {
			t.Errorf("%s: findings = %d, want %d", mode, len(report.Findings), want)
		}
		if mode == config.VerifyConfirmed && !strings.Contains(report.Overruled[0].Reason, "could not be confirmed") {
			t.Errorf("reason = %q, want it to say the finding could not be confirmed", report.Overruled[0].Reason)
		}
	}
}

// An outage costs precision, which the stage status shows. Dropping the
// findings would cost recall nobody is told about.
func TestAFailedCheckKeepsTheFindingAndSaysVerificationDidNotRun(t *testing.T) {
	e, _ := verifyEngine(t, &toolLLM{err: errors.New("provider down")}, config.VerifyConfirmed)
	report, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Findings) != 2 || len(report.Overruled) != 0 {
		t.Errorf("findings %d, withheld %d, want both kept", len(report.Findings), len(report.Overruled))
	}
	found := false
	for _, s := range report.Stages {
		found = found || s.Stage == "verify"
	}
	if !found {
		t.Errorf("stages = %v, want a verify entry so the run does not read as clean", report.Stages)
	}
}

// stallLLM never answers its first stalls calls, the way a request the provider
// has acknowledged with a keepalive never does: it returns only when its context
// ends. Later calls answer with a confirming verdict.
type stallLLM struct {
	mu     sync.Mutex
	stalls int
	calls  int
}

func (s *stallLLM) GenerateContent(ctx context.Context, _ []llms.Message, _ ...llms.CallOption) (*llms.Response, error) {
	s.mu.Lock()
	s.calls++
	stall := s.calls <= s.stalls
	s.mu.Unlock()
	if stall {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	r := verdict(verdictConfirmed, "app.go:4 discards the error")
	return &r, nil
}
func (s *stallLLM) Stream(context.Context, []llms.Message, ...llms.CallOption) (<-chan llms.StreamChunk, error) {
	return nil, errors.New("not supported")
}
func (s *stallLLM) Provider() llms.Provider { return "stall" }
func (s *stallLLM) Model() string           { return "stall" }

func shortCallTimeout(t *testing.T) {
	t.Helper()
	old := verifyCallTimeout
	verifyCallTimeout = 40 * time.Millisecond
	t.Cleanup(func() { verifyCallTimeout = old })
}

// One stalled turn costs one resend, not the finding: the check still returns
// its verdict, and it does so on the call timeout rather than the five minutes.
func TestAStalledCheckerTurnIsSentAgainAndTheFindingStillGetsAVerdict(t *testing.T) {
	shortCallTimeout(t)
	checker := &stallLLM{stalls: 1}
	e, _ := verifyEngine(t, &toolLLM{}, config.VerifyConfirmed)
	e.Roles.Validate = llm.NewClientForTest(checker, e.Config.Models.Default)
	f := twoFindings()[0]
	got := e.verifyOne(context.Background(), "system", newRepoTools("", e.Provider, vcs.Ref{}), f, nil)
	if got.failed != "" || got.verdict != verdictConfirmed {
		t.Errorf("outcome = %+v, want the verdict from the second send", got)
	}
	if checker.calls != 2 {
		t.Errorf("calls = %d, want the stalled one and one resend", checker.calls)
	}
}

// Two stalls in a row fail the finding, which the stage reports, and the second
// ends on the call deadline: without it the loop would wait out verifyTimeout.
func TestACheckerThatStallsTwiceFailsTheFindingOnTheCallDeadline(t *testing.T) {
	shortCallTimeout(t)
	checker := &stallLLM{stalls: 100}
	e, _ := verifyEngine(t, &toolLLM{}, config.VerifyConfirmed)
	e.Roles.Validate = llm.NewClientForTest(checker, e.Config.Models.Default)
	f := twoFindings()[0]
	start := time.Now()
	got := e.verifyOne(context.Background(), "system", newRepoTools("", e.Provider, vcs.Ref{}), f, nil)
	if got.failed != "timeout" {
		t.Errorf("outcome = %+v, want a failed check of kind timeout", got)
	}
	if checker.calls != 2 || time.Since(start) > 5*time.Second {
		t.Errorf("calls = %d after %s, want two sends ended by the call deadline", checker.calls, time.Since(start))
	}
}

// A cancelled review is not a stalled call: resending would keep working for a
// caller that has gone.
func TestACancelledReviewDoesNotResendACheckerTurn(t *testing.T) {
	shortCallTimeout(t)
	checker := &stallLLM{stalls: 100}
	e, _ := verifyEngine(t, &toolLLM{}, config.VerifyConfirmed)
	e.Roles.Validate = llm.NewClientForTest(checker, e.Config.Models.Default)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := twoFindings()[0]
	got := e.verifyOne(ctx, "system", newRepoTools("", e.Provider, vcs.Ref{}), f, nil)
	if got.failed != "cancelled" || checker.calls != 1 {
		t.Errorf("outcome = %+v after %d calls, want one call and a cancelled check", got, checker.calls)
	}
}

// failingLLM fails its first calls with the errors it was given, in order, and
// answers every call after them.
type failingLLM struct {
	errs  []error
	calls int
}

func (f *failingLLM) GenerateContent(context.Context, []llms.Message, ...llms.CallOption) (*llms.Response, error) {
	f.calls++
	if f.calls <= len(f.errs) {
		return nil, f.errs[f.calls-1]
	}
	r := verdict(verdictConfirmed, "app.go:4 discards the error")
	return &r, nil
}

// sdkLookupFailure wraps a DNS failure the way the SDK reported one on a
// held-out deep review, so the test reads the error chain a live run produced.
func sdkLookupFailure(dns *net.DNSError) error {
	return fmt.Errorf("openai: generate content: request failed after 0 retries: %w",
		&url.Error{Op: "Post", URL: "https://openrouter.ai/api/v1/chat/completions",
			Err: &net.OpError{Op: "dial", Net: "tcp", Err: dns}})
}

// A DNS lookup that timed out is a blip the SDK does not resend. Left to it,
// one ended both agents of a held-out deep review on their first turn; here it
// costs one resend.
func TestATurnLostToADNSTimeoutIsSentAgain(t *testing.T) {
	model := &failingLLM{errs: []error{sdkLookupFailure(&net.DNSError{
		Err: "read udp 127.0.0.1:35468->127.0.0.53:53: i/o timeout", Name: "openrouter.ai", IsTimeout: true})}}
	resp, err := generateBounded(context.Background(), model, nil, nil, time.Minute)
	if err != nil || resp == nil {
		t.Fatalf("generateBounded = %v, %v; want the answer to the resend", resp, err)
	}
	if model.calls != 2 {
		t.Errorf("calls = %d, want the timed-out one and one resend", model.calls)
	}
}

// A host that does not resolve is configuration, not a blip, so the lookup
// failure comes back after one call instead of costing a second.
func TestAHostThatDoesNotResolveIsNotSentAgain(t *testing.T) {
	model := &failingLLM{errs: []error{sdkLookupFailure(&net.DNSError{
		Err: "no such host", Name: "openrouter.example", IsNotFound: true})}}
	_, err := generateBounded(context.Background(), model, nil, nil, time.Minute)
	var dns *net.DNSError
	if !errors.As(err, &dns) || model.calls != 1 {
		t.Errorf("err = %v after %d calls; want the lookup failure after one call", err, model.calls)
	}
}

// A checker that never stops looking is cut off: the lookups past the budget
// get a refusal, and the loop ends rather than running until the timeout.
func TestACheckerThatKeepsLookingIsCutOffAtTheLookupBudget(t *testing.T) {
	endless := make([]llms.Response, 40)
	for i := range endless {
		endless[i] = lookup("app.go")
	}
	checker := &toolLLM{script: map[string][]llms.Response{"Ignored error from http.Get": endless}}
	e, _ := verifyEngine(t, checker, config.VerifyConfirmed)
	f := twoFindings()[0]
	got := e.verifyOne(context.Background(), "system", newRepoTools("", e.Provider, vcs.Ref{}), f, nil)
	if got.verdict != verdictUncertain {
		t.Errorf("verdict = %q, want uncertain for a checker that never submitted", got.verdict)
	}
	if checker.calls > verifyMaxLookups+3 {
		t.Errorf("the checker was called %d times, want at most %d", checker.calls, verifyMaxLookups+3)
	}
	spent := 0
	for _, r := range checker.tools {
		if strings.Contains(r, "lookup budget is spent") {
			spent++
		}
	}
	if spent == 0 {
		t.Error("no lookup past the budget was refused")
	}
}

func TestACheckerThatStopsWithoutAVerdictIsUncertain(t *testing.T) {
	e, _ := verifyEngine(t, &toolLLM{script: map[string][]llms.Response{"Ignored error from http.Get": {{Content: "looks fine to me"}}}}, config.VerifyConfirmed)
	got := e.verifyOne(context.Background(), "system", newRepoTools("", e.Provider, vcs.Ref{}), twoFindings()[0], nil)
	if got.verdict != verdictUncertain || got.failed != "" {
		t.Errorf("outcome = %+v, want uncertain and not a failure", got)
	}
}

// Deterministic findings are not a model's claim and have nothing to check.
func TestAnalyzerFindingsAreNeverChecked(t *testing.T) {
	checker := &toolLLM{}
	e, _ := verifyEngine(t, checker, config.VerifyConfirmed)
	fs := []Finding{{Path: "app.go", Line: 4, Severity: "error", Title: "gosec", FromAnalyzer: true}}
	kept, withheld, _, stages := e.verifyFindings(context.Background(), vcs.Ref{}, fs, nil)
	if checker.calls != 0 || len(kept) != 1 || len(withheld) != 0 || len(stages) != 0 {
		t.Errorf("calls %d kept %d withheld %d stages %v, want the analyzer finding kept without a call", checker.calls, len(kept), len(withheld), stages)
	}
}

func TestADiffWindowShowsTheCitedLineWithItsMarginNumberAndRemovedLines(t *testing.T) {
	files, err := parseDiff(engineDiff)
	if err != nil {
		t.Fatal(err)
	}
	var file *diff.File
	for _, f := range files {
		if f.Path == "app.go" {
			file = f
		}
	}
	got := diffWindow(file, 4, 3)
	for _, want := range []string{"    4 +\tresp, _ := http.Get", "-func Get() error {", "    5 +\tdefer resp.Body.Close()"} {
		if !strings.Contains(got, want) {
			t.Errorf("window lacks %q:\n%s", want, got)
		}
	}
	if diffWindow(nil, 4, 2) != "" || diffWindow(&diff.File{}, 4, 2) != "" {
		t.Error("a missing or empty file produced a window")
	}
}

func gitRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for path, body := range files {
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git unavailable: %v %s", err, out)
		}
	}
	return dir
}

func TestReadFileNumbersALineWindowAndTurnsBadPathsIntoText(t *testing.T) {
	root := gitRepo(t, map[string]string{"a/b.go": "one\ntwo\nthree\nfour\n"})
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link.txt")); err != nil {
		t.Skip("symlinks unavailable")
	}
	tools := newRepoTools(root, nil, vcs.Ref{})
	ctx := context.Background()

	if got := tools.read(ctx, "a/b.go", 2, 3); got != "    2 two\n    3 three\n" {
		t.Errorf("window = %q", got)
	}
	for _, path := range []string{"../x", "/etc/passwd", "a/../../x", "missing.go", "link.txt", ""} {
		if got := tools.read(ctx, path, 1, 5); !strings.HasPrefix(got, "no such file") || strings.Contains(got, "outside") {
			t.Errorf("read(%q) = %q, want a no-such-file message that leaks nothing", path, got)
		}
	}
	if got := tools.read(ctx, "a/b.go", 99, 120); !strings.Contains(got, "has 4 lines") {
		t.Errorf("past the end = %q, want the line count", got)
	}
}

func TestGrepSearchesTrackedFilesAndCannotBeGivenAnOption(t *testing.T) {
	root := gitRepo(t, map[string]string{"x/a.go": "func Alpha() {}\n", "y/b.go": "func Beta() {}\n"})
	tools := newRepoTools(root, nil, vcs.Ref{})
	ctx := context.Background()

	if got := tools.grep(ctx, `func (Alpha|Beta)`, ""); !strings.Contains(got, "x/a.go:1:") || !strings.Contains(got, "y/b.go:1:") {
		t.Errorf("grep = %q, want both files", got)
	}
	if got := tools.grep(ctx, "Beta", "x/"); got != "(no matches)" {
		t.Errorf("grep under x/ = %q, want no matches", got)
	}
	for _, hostile := range []string{"--version", "--open-files-in-pager=touch pwned", "-h"} {
		if got := tools.grep(ctx, hostile, ""); strings.Contains(got, "git version") || strings.Contains(got, "usage") {
			t.Errorf("grep(%q) was read by git as an option: %q", hostile, got)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "pwned")); err == nil {
		t.Error("a pager command ran")
	}
	if got := newRepoTools("", nil, vcs.Ref{}).grep(ctx, "x", ""); !strings.Contains(got, "unavailable") {
		t.Errorf("grep without a checkout = %q, want it to say search is unavailable", got)
	}
}

// A checkout's own configuration holds the token a CI job pushes with, and the
// tools are driven by text a pull request's author wrote, so a file the tools can
// read is a file a finding can publish. Nothing under .git is returned, by its
// own name or through a link inside the tree.
func TestTheToolsNeverReturnAnythingUnderTheGitDirectory(t *testing.T) {
	root := gitRepo(t, map[string]string{"app.go": "package app\n"})
	secret := "AUTHORIZATION: basic c2VjcmV0LXRva2Vu"
	cfg := filepath.Join(root, ".git", "config")
	f, err := os.OpenFile(cfg, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("[http \"https://github.com/\"]\n\textraheader = " + secret + "\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if err := os.Symlink(cfg, filepath.Join(root, "innocent.txt")); err != nil {
		t.Skip("symlinks unavailable")
	}
	tools := newRepoTools(root, nil, vcs.Ref{})
	for _, path := range []string{".git/config", "./.git/config", ".GIT/config", "innocent.txt"} {
		if got := tools.read(context.Background(), path, 1, 50); strings.Contains(got, secret) || !strings.HasPrefix(got, "no such file") {
			t.Errorf("read(%q) = %q, want the file refused", path, got)
		}
	}
	if got := tools.read(context.Background(), "app.go", 1, 5); !strings.Contains(got, "package app") {
		t.Errorf("a tracked file was refused: %q", got)
	}
}

// A working tree keeps its secrets in files git ignores, so those are refused
// too, while a file that is tracked, or new and not ignored, is still read.
func TestTheToolsRefuseIgnoredFilesAndReadTheRest(t *testing.T) {
	root := gitRepo(t, map[string]string{"app.go": "package app\n", ".gitignore": ".env\nsecrets/\n"})
	for path, body := range map[string]string{".env": "API_KEY=hunter2\n", "secrets/key.pem": "PRIVATE KEY\n", "new.go": "package new\n"} {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tools := newRepoTools(root, nil, vcs.Ref{})
	for _, path := range []string{".env", "secrets/key.pem"} {
		if got := tools.read(context.Background(), path, 1, 5); !strings.HasPrefix(got, "no such file") {
			t.Errorf("read(%q) = %q, want the ignored file refused", path, got)
		}
	}
	for path, want := range map[string]string{"app.go": "package app", "new.go": "package new"} {
		if got := tools.read(context.Background(), path, 1, 5); !strings.Contains(got, want) {
			t.Errorf("read(%q) = %q, want it read", path, got)
		}
	}
}

// When git cannot say whether a file is ignored, the file is refused. A checkout
// whose .git is not a repository is that case, and a tool that read the file
// anyway would be reading it unchecked.
func TestTheToolsRefuseAFileWhenGitCannotSayWhetherItIsIgnored(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("visible\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tools := newRepoTools(root, nil, vcs.Ref{})
	if !tools.canSearch() {
		t.Fatal("the test checkout was not taken for one")
	}
	if got := tools.read(context.Background(), "a.txt", 1, 5); !strings.HasPrefix(got, "no such file") {
		t.Errorf("read = %q, want a file refused when git cannot answer", got)
	}
}
