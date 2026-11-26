package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
)

// reviewVia runs one review command and returns its JSON report. Stdout is
// swapped for a pipe, as reviewJSON does, but the command is a parameter so the
// other review modes can be driven too.
func reviewVia(t *testing.T, run func(context.Context, []string) error, repo string, args ...string) (map[string]any, error) {
	t.Helper()
	t.Setenv("NITPICK_TRUST_CONFIG_ENDPOINTS", "1")
	for _, name := range []string{"GITHUB_ACTIONS", "GITHUB_REPOSITORY", "GITHUB_REF", "PR_NUMBER", "GITHUB_EVENT_PATH", "GITHUB_TOKEN", "NITPICK_GITHUB_TOKEN"} {
		t.Setenv(name, "")
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	runErr := run(context.Background(), append([]string{"-repo", repo, "-dry-run", "-format", "json", "-no-linters", "-fail-on", "none"}, args...))
	_ = w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	var out map[string]any
	if buf.Len() > 0 {
		raw := buf.Bytes()
		if i := bytes.LastIndex(raw, []byte("\n{")); i >= 0 {
			raw = raw[i+1:]
		}
		_ = json.Unmarshal(raw, &out)
	}
	return out, runErr
}

func findingTitles(out map[string]any) string {
	var titles []string
	for _, f := range out["findings"].([]any) {
		titles = append(titles, f.(map[string]any)["title"].(string))
	}
	return strings.Join(titles, "|")
}

// TestEveryReviewModeReplaysWithNoModelRequest reviews one change in each mode
// the command line offers, then reviews it again, and counts the requests the
// model server received. A mode whose model calls bypass the notary shows up as
// a replay that still asks.
func TestEveryReviewModeReplaysWithNoModelRequest(t *testing.T) {
	cases := []struct {
		name   string
		run    func(context.Context, []string) error
		config func(string) string
		// severity is what the fixture rates its finding, "error" when empty.
		severity string
		// skipReason, when set, says the fixture cannot drive this mode.
		skipReason string
	}{
		{name: "review", run: runReview},
		{name: "review.votes 3", run: runReview, config: func(s string) string { return s + "  votes: 3\n" }},
		{name: "pedantic persona (a second, style pass)", run: runReview, config: func(s string) string { return s + "persona:\n  nitpick: pedantic\n" }},
		{name: "residual approve judge", run: runReview, severity: "info", config: func(s string) string {
			return s + "  min_severity: info\n  approve:\n    enabled: true\n    residual:\n      enabled: true\n"
		}},
		{name: "fast-review", run: runFastReview},
		{name: "deep-review", run: runDeepReview, skipReason: "its agents read the repository through tool calls, which the fixture model cannot make"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.skipReason != "" {
				t.Skip(tc.skipReason)
			}
			model := newFakeModel(t)
			if tc.severity != "" {
				model.severity.Store(&tc.severity)
			}
			repo := gitRepo(t, model)
			url, _ := notaryServer(t)
			t.Setenv(EnvNotaryToken, "ci-secret")
			args := []string{"-notary", url}
			if tc.config != nil {
				args = append(args, "-config", writeConfig(t, repo, tc.config))
			}

			live, err := reviewVia(t, tc.run, repo, args...)
			if err != nil {
				t.Fatalf("live review: %v", err)
			}
			liveCalls := model.calls.Load()
			if liveCalls == 0 {
				t.Fatal("the live review asked the model nothing, so the replay below proves nothing")
			}
			if n := live["findings"].([]any); len(n) == 0 {
				t.Fatalf("the live review found nothing: a replay of nothing agrees with anything")
			}
			if mode := notaryField(t, live, "Mode"); mode != "live" {
				t.Fatalf("live review notary mode = %v: %v", mode, live["notary"])
			}
			if tc.name == "residual approve judge" {
				// The judge is a model call after triage. Without it in the live
				// run this case would only repeat the plain review.
				if got := model.callsMatching("You decide whether a pull request review"); got == 0 {
					t.Fatal("setup: the residual approve judge never ran, so this case tests nothing about it")
				}
			}

			replay, err := reviewVia(t, tc.run, repo, args...)
			if err != nil {
				t.Fatalf("replay: %v", err)
			}
			if extra := model.calls.Load() - liveCalls; extra != 0 {
				t.Errorf("the replay asked the model %d times (the live review asked %d): some call in this mode bypasses the notary", extra, liveCalls)
			}
			if mode := notaryField(t, replay, "Mode"); mode != "replayed" {
				t.Errorf("replay notary mode = %v: %v", mode, replay["notary"])
			}
			if findingTitles(live) != findingTitles(replay) {
				t.Errorf("the replay said something else:\n live   %s\n replay %s", findingTitles(live), findingTitles(replay))
			}
		})
	}
}
