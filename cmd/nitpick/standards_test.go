package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/standards"
)

// A disabled entry that names no probe is refused.
//
// Silently disabling nothing produces the report the author asked for minus
// the part they meant to remove, and it looks correct. A typo in a config key
// is the one failure this whole feature would otherwise have no way to
// surface, since the output of a probe that ran and a probe that was never
// switched off are the same output.
func TestAnUnknownDisabledProbeIsRefused(t *testing.T) {
	err := checkDisabled([]string{"go-error-wrap", "go-error-wrapp"})
	if err == nil {
		t.Fatal("a misspelled probe ID was accepted, and disables nothing")
	}
	if !strings.Contains(err.Error(), "go-error-wrapp") {
		t.Errorf("error does not name the unknown ID: %v", err)
	}
	if !strings.Contains(err.Error(), "go-ctx-first-arg") {
		t.Errorf("error does not list the probes that do exist: %v", err)
	}
	if err := checkDisabled([]string{"go-error-wrap"}); err != nil {
		t.Errorf("a real probe ID was refused: %v", err)
	}
}

// git runs a command in the fixture repository.
func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The standard a change is measured against comes from the base revision.
//
// This is the same seam config.BasePolicy holds for policy, applied to a
// measurement. Without it a branch that rewrites a package's style is scored
// as though the repository had always written it that way, and the tighter the
// tighter the convention the base holds, the more a change gains by rewriting
// it in the same diff.
func TestAChangeIsMeasuredAgainstTheBasesStandardNotItsOwn(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")

	// A base that wraps its errors everywhere, over enough sites to clear the
	// floor.
	var base strings.Builder
	base.WriteString("package p\n\nimport \"fmt\"\n")
	for i := range 20 {
		base.WriteString("\nfunc F")
		base.WriteString(string(rune('a' + i)))
		base.WriteString("(err error) error { return fmt.Errorf(\"read: %w\", err) }\n")
	}
	write(t, dir, "base.go", base.String())
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "base")

	// A change that does not wrap, and says in its own config that wrapping is
	// not a standard here.
	write(t, dir, "new.go", "package p\n\nimport \"fmt\"\n\nfunc G(err error) error { return fmt.Errorf(\"read: %v\", err) }\n")
	write(t, dir, ".nitpick.yaml", "standards:\n  min_share: 0.99\n  min_sites: 500\n")

	// Tracked, because git diff does not show an untracked file and this
	// command reads the change the same way `nitpick review -base` does.
	git(t, dir, "add", "new.go")

	res, err := measureStandards(context.Background(), dir, "main", standards.Options{})
	if err != nil {
		t.Fatal(err)
	}

	a, ok := res.Adherence["go-error-wrap"]
	if !ok {
		t.Fatalf("the change was not scored against the base's standard; adherence = %v", res.Adherence)
	}
	if a.Total != 1 || a.Conforming != 0 {
		t.Errorf("adherence = %d/%d, want 0/1", a.Conforming, a.Total)
	}

	// And the base's own reading is of the base, not of the working tree: 20
	// wrapping sites, not 21 with the change's counted in.
	for _, r := range res.Report.Results {
		if r.ID == "go-error-wrap" && r.Total != 20 {
			t.Errorf("the base measured %d sites, want 20: the change voted on its own standard", r.Total)
		}
	}
}

// A change that touches no site says so.
//
// docs/measurement.md Rule 10: an empty adherence map and a change that
// conformed everywhere render as the same silence unless one of them speaks.
func TestAChangeWithNoSitesSaysSoRatherThanScoringFull(t *testing.T) {
	got := standards.AdherenceText(map[string]standards.Adherence{})
	if !strings.Contains(got, "no site") {
		t.Errorf("empty adherence rendered as %q; it must not read as full marks", got)
	}
	if strings.Contains(got, "100") {
		t.Errorf("empty adherence rendered a percentage: %q", got)
	}
}
