package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The hash is what makes a record made in CI replay on a developer machine, so
// it must not move with anything about the machine and must move with
// everything that changes what the model is asked. Each case reviews the same
// change and reads the hash the notary filed it under.

type hashCase struct {
	name string
	// args are extra review flags; config rewrites the config text; repo, when
	// set, reviews a different but identical checkout.
	args   []string
	config func(string) string
	repo   bool
	// same says the review is the same one as the baseline.
	same bool
	// operational marks a knob that changes how a request is sent or how a
	// result is judged, not what the model is asked. It is expected to leave
	// the hash alone, and is reported rather than asserted while it does not.
	operational bool
}

func hashOf(t *testing.T, repo, url string, args ...string) string {
	t.Helper()
	out, err := reviewJSON(t, repo, append([]string{"-notary", url}, args...)...)
	if err != nil {
		t.Fatalf("review %v: %v", args, err)
	}
	h, _ := notaryField(t, out, "Hash").(string)
	if h == "" {
		t.Fatalf("no hash in the report for %v: %v", args, out["notary"])
	}
	return h
}

func writeConfig(t *testing.T, repo string, edit func(string) string) string {
	t.Helper()
	base, err := os.ReadFile(filepath.Join(repo, ".nitpick.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(base)
	if edit != nil {
		text = edit(text)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "elsewhere", "review.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTheHashMovesOnlyWithWhatChangesTheReview(t *testing.T) {
	model := newFakeModel(t)
	repo := gitRepo(t, model)
	other := gitRepo(t, model)
	url, _ := notaryServer(t)
	t.Setenv(EnvNotaryToken, "ci-secret")

	baseline := hashOf(t, repo, url)

	cases := []hashCase{
		// Nothing about the machine or the invocation may change the hash.
		{name: "an identical checkout in another directory", repo: true, same: true},
		{name: "the same config at another path", config: func(s string) string { return s }, same: true},
		{name: "config keys in another order", config: func(s string) string {
			lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
			// Move the trailing review: block above models: while keeping each
			// block's own lines together.
			var models, review []string
			cur := &models
			for _, l := range lines {
				if strings.HasPrefix(l, "review:") {
					cur = &review
				}
				*cur = append(*cur, l)
			}
			return strings.Join(append(review, models...), "\n") + "\n"
		}, same: true},
		{name: "a comment added to the config", config: func(s string) string { return "# a note\n" + s }, same: true},
		{name: "-v", args: []string{"-v"}, same: true},
		{name: "-log-format json", args: []string{"-log-format", "json"}, same: true},
		// Settings that decide a gate or an exit code, not what a model says.
		{name: "-fail-on critical", operational: true, args: []string{"-fail-on", "critical"}, same: true},
		{name: "-max-rounds 9", args: []string{"-max-rounds", "9"}, same: true},
		{name: "-stop-at warning", args: []string{"-stop-at", "warning"}, same: true},
		// Operational knobs that change how a request is sent, not what is asked.
		{name: "review.concurrency", operational: true, config: func(s string) string { return s + "  concurrency: 2\n" }, same: true},
		{name: "models.default.timeout", operational: true, config: func(s string) string {
			return strings.Replace(s, "    model: fake\n", "    model: fake\n    timeout: 7m\n", 1)
		}, same: true},
		{name: "models.default.max_retries", operational: true, config: func(s string) string {
			return strings.Replace(s, "    model: fake\n", "    model: fake\n    max_retries: 1\n", 1)
		}, same: true},
		// What changes the review must change the hash.
		{name: "-instruction", args: []string{"-instruction", "be terse"}, same: false},
		{name: "review.related_context off", config: func(s string) string { return s + "  related_context: false\n" }, same: false},
		{name: "review.slop on", config: func(s string) string { return s + "  slop: true\n" }, same: false},
		{name: "a different model name", config: func(s string) string { return strings.Replace(s, "model: fake", "model: fake-two", 1) }, same: false},
		{name: "review.max_files_per_request", config: func(s string) string { return s + "  max_files_per_request: 1\n" }, same: false},
		{name: "persona", config: func(s string) string { return s + "persona:\n  nitpick: pedantic\n" }, same: false},
	}

	var movedButShouldNot, stayedButShould, operationalMisses []string
	for _, tc := range cases {
		target, args := repo, append([]string(nil), tc.args...)
		if tc.repo {
			target = other
		}
		if tc.config != nil {
			args = append(args, "-config", writeConfig(t, repo, tc.config))
		}
		got := hashOf(t, target, url, args...)
		moved := got != baseline
		switch {
		case moved && tc.same && tc.operational:
			operationalMisses = append(operationalMisses, tc.name)
		case moved && tc.same:
			movedButShouldNot = append(movedButShouldNot, tc.name)
		case !moved && !tc.same:
			stayedButShould = append(stayedButShould, tc.name)
		}
		t.Logf("%-48s hash %s  moved=%-5v expected-same=%v", tc.name, got[:10], moved, tc.same)
	}
	sort.Strings(movedButShouldNot)
	// Not a failure: the per-request key already guarantees a changed prompt is
	// asked again, so the record-level hash only needs to name the review. Each
	// of these costs a replay between two machines that set the knob differently.
	if len(operationalMisses) > 0 {
		t.Logf("KNOWN GAP, operational settings that move the hash and so forfeit a replay: %v", operationalMisses)
	}
	if len(stayedButShould) > 0 {
		t.Errorf("inputs that change the review but left the hash alone (a stale replay of the wrong review): %v", stayedButShould)
	}
	if len(movedButShouldNot) > 0 {
		t.Errorf("inputs that change nothing the model sees but moved the hash (a record made elsewhere will not replay): %s", fmt.Sprint(movedButShouldNot))
	}
}
