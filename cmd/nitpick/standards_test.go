package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/standards"
	"github.com/jdziat/open-nitpick/internal/vcs"
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
	// No .nitpick.yaml here. An earlier version of this test wrote one and
	// commented that the change "says in its own config that wrapping is not a
	// standard": measureStandards never loads config, so replacing that file
	// with one disabling the probe left the test green. Configuration is
	// covered by TestTheConfiguredFloorReachesTheMeasurement, through the
	// entry point that reads it.

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

// The base's file list comes from the base, not from the working tree.
//
// Walking the working tree and reading those paths at the base makes the base
// measurement a function of the change. A branch that DELETES the
// counterevidence for a convention was never asked about the deleted files, so
// the tool reported the convention as a standard the base never held, and then
// issued a finding against the author under it. A rename vanished the same
// way, and an untracked scratch file was asked for at a revision that never
// had it, failing the command outright for anybody who had one.
func TestTheBaseFileListComesFromTheBase(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")

	// A base divided about marking its helpers: 20 marked, 20 not, which is
	// 50% and contested.
	var marked, unmarked strings.Builder
	marked.WriteString("package p\n\nimport \"testing\"\n")
	unmarked.WriteString("package p\n\nimport \"testing\"\n")
	for i := range 20 {
		fmt.Fprintf(&marked, "\nfunc good%d(t *testing.T) { t.Helper() }\n", i)
		fmt.Fprintf(&unmarked, "\nfunc bad%d(t *testing.T) { t.Log(\"x\") }\n", i)
	}
	write(t, dir, "good_test.go", marked.String())
	write(t, dir, "bad_test.go", unmarked.String())
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "base")

	baseline := helperResult(t, dir, "main")
	if baseline.Total != 40 || baseline.Standing != standards.StandingContested {
		t.Fatalf("the base reads %d/%d %q, want 40 sites and contested",
			baseline.Conforming, baseline.Total, baseline.Standing)
	}

	// The change deletes the counterevidence and adds one unmarked helper.
	git(t, dir, "rm", "-q", "bad_test.go")
	write(t, dir, "new_test.go", "package p\n\nimport \"testing\"\n\nfunc added(t *testing.T) { _ = t }\n")
	git(t, dir, "add", ".")

	after := helperResult(t, dir, "main")
	if after.Total != baseline.Total || after.Standing != standards.StandingContested {
		t.Errorf("after deleting the counterevidence the base reads %d/%d %q; "+
			"the change manufactured a standard the base never held",
			after.Conforming, after.Total, after.Standing)
	}

	// A rename keeps its file in the base list under the old path.
	git(t, dir, "mv", "good_test.go", "renamed_test.go")
	git(t, dir, "add", "-A")
	if renamed := helperResult(t, dir, "main"); renamed.Total != baseline.Total {
		t.Errorf("after a rename the base reads %d sites, want %d",
			renamed.Total, baseline.Total)
	}
}

// An untracked file in the working tree does not stop the command.
//
// git diff never mentions it and the base never had it, and asking the base
// for it failed the whole run. Anybody with a scratch .go file could not use
// -base at all.
func TestAnUntrackedFileDoesNotBreakTheBaseRead(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "a.go", "package p\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "base")

	write(t, dir, "scratch.go", "package p\n\nfunc scratch() {}\n")

	if _, err := measureStandards(context.Background(), dir, "main", standards.Options{}); err != nil {
		t.Fatalf("an untracked file failed the run: %v", err)
	}
}

// helperResult measures dir against base and returns the helper probe's row.
func helperResult(t *testing.T, dir, base string) standards.Result {
	t.Helper()
	res, err := measureStandards(context.Background(), dir, base, standards.Options{})
	if err != nil {
		t.Fatalf("measure against %s: %v", base, err)
	}
	for _, r := range res.Report.Results {
		if r.ID == "go-test-helper-marks" {
			return r
		}
	}
	t.Fatalf("no go-test-helper-marks row in %+v", res.Report.Results)
	return standards.Result{}
}

// failingBase lists one file and refuses to read it.
type failingBase struct{ paths []string }

func (f failingBase) Tree(context.Context, string) ([]string, error) { return f.paths, nil }

func (f failingBase) FileContent(context.Context, vcs.Ref, string) ([]byte, error) {
	return nil, errors.New("object store is having a day")
}

// A file the base holds and cannot return stops the command.
//
// Every path readAtBase asks for is one git said the base has, so a failed
// read is a failure and not an absence. Dropping it would compute the base's
// share over a subset, with nothing in the output saying which subset, and a
// share whose denominator moved for a reason nobody can see is the partial
// measurement this command exists to refuse.
func TestAFailedBaseReadStopsTheCommand(t *testing.T) {
	_, err := readAtBase(context.Background(), failingBase{paths: []string{"a.go"}}, "main")
	if err == nil {
		t.Fatal("a failed base read was skipped, so the base share is silently over a subset")
	}
	if !strings.Contains(err.Error(), "a.go") {
		t.Errorf("the error does not name the file: %v", err)
	}
}

// A base of files no probe reads is a census, not a failure.
//
// This test expected a refusal and was wrong. A markdown-only base has nothing
// to measure and something to report: the report names the language it did not
// read, which is the whole point of carrying the census. The refusal is for a
// base holding no file at all.
func TestABaseOfUnprobedFilesIsACensusNotAFailure(t *testing.T) {
	got, err := readAtBase(context.Background(), failingBase{paths: []string{"README.md"}}, "main")
	if err != nil {
		t.Fatalf("a markdown-only base failed: %v", err)
	}
	if len(got) != 1 || got[0].Src != nil {
		t.Fatalf("base files = %+v, want README.md named and unread", got)
	}
	if rep := standards.Measure(got, standards.Options{}); !slices.Contains(rep.Unprobed, "markdown") {
		t.Errorf("unprobed = %v, want markdown named", rep.Unprobed)
	}

	// And a base holding nothing at all is refused.
	if _, err := readAtBase(context.Background(), failingBase{}, "main"); err == nil {
		t.Error("an empty base was measured rather than refused")
	}
}

// The configured floor reaches the measurement, through the entry point.
//
// loadStandardsConfig and runStandards had no coverage at all, so the fix that
// made this command read the standards block alone (rather than validating a
// models section it never touches) was guarded by nothing. Reverting it left
// the suite green.
func TestTheConfiguredFloorReachesTheMeasurement(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")

	var src strings.Builder
	src.WriteString("package p\n\nimport \"testing\"\n")
	for i := range 20 {
		fmt.Fprintf(&src, "\nfunc h%d(t *testing.T) { t.Helper() }\n", i)
	}
	write(t, dir, "a_test.go", src.String())

	// A models block this command must not read, alongside a floor it must.
	write(t, dir, ".nitpick.yaml", "models:\n  default:\n    provider: nonsense\n    providers: [also-nonsense]\n"+
		"standards:\n  min_sites: 500\n")

	cfg, err := loadStandardsConfig(dir, "")
	if err != nil {
		t.Fatalf("an unrelated models block stopped a command that calls no model: %v", err)
	}
	if cfg.MinSites != 500 {
		t.Fatalf("min_sites = %d, want 500", cfg.MinSites)
	}

	// And it changes the answer: 20 sites is a standard by default and
	// contested under a floor of 500.
	res, err := measureStandards(context.Background(), dir, "",
		standards.Options{Floor: standards.Floor{MinSites: cfg.MinSites}})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res.Report.Results {
		if r.ID == "go-test-helper-marks" && r.Standing != standards.StandingContested {
			t.Errorf("standing = %q under min_sites=500 over 20 sites, want %q",
				r.Standing, standards.StandingContested)
		}
	}

	// A malformed standards block is still refused.
	write(t, dir, ".nitpick.yaml", "standards:\n  min_share: 85\n")
	if _, err := loadStandardsConfig(dir, ""); err == nil {
		t.Error("min_share: 85 was accepted; it is a fraction, and 85 is a floor no count can clear")
	}
}

// writeAgents preserves what it did not write, and refuses a broken pair.
//
// This is what CI's `make agents` executes, and it had no coverage: the
// unchanged-file short circuit and the refusal both lived only in a function
// nothing called from a test.
func TestWriteAgentsRewritesOnlyItsOwnBlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENTS.md")
	files := []standards.File{{Path: "a_test.go", Src: []byte(helperSource(20))}}
	measured := standards.Measure(files, standards.Options{Floor: standards.Floor{MinSites: 5}})

	// A file that does not exist yet.
	if err := writeAgents(path, measured); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(first), standards.BeginMarker) {
		t.Fatal("no block was written")
	}

	// Writing the same measurement again changes no bytes.
	if err := writeAgents(path, measured); err != nil {
		t.Fatal(err)
	}
	again, _ := os.ReadFile(path)
	if string(again) != string(first) {
		t.Error("a second write changed the file, so the drift gate would never pass")
	}

	// Hand-written text survives.
	withProse := strings.Replace(string(first), "# Working in this repository",
		"# Working in this repository\n\nRun make check. This line is mine.", 1)
	if err := os.WriteFile(path, []byte(withProse), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeAgents(path, measured); err != nil {
		t.Fatal(err)
	}
	kept, _ := os.ReadFile(path)
	if !strings.Contains(string(kept), "This line is mine.") {
		t.Error("hand-written prose was lost")
	}

	// A half-written marker pair is refused rather than guessed at.
	if err := os.WriteFile(path, []byte("# T\n"+standards.BeginMarker+"\nrules\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeAgents(path, measured); err == nil {
		t.Error("a broken marker pair was accepted, and the file would have been eaten")
	}
}

// helperSource builds n marked test helpers.
func helperSource(n int) string {
	var b strings.Builder
	b.WriteString("package p\n\nimport \"testing\"\n")
	for i := range n {
		fmt.Fprintf(&b, "\nfunc h%d(t *testing.T) { t.Helper() }\n", i)
	}
	return b.String()
}

// The command runs, and refuses the flag pairs that would publish the wrong
// measurement.
//
// -agents writes what it measured. With -base it measured the base revision,
// so it would write yesterday's conventions into today's AGENTS.md and pass
// the drift gate while doing it. runStandards had no coverage at all until
// this, so both the flags and the wiring under them were unguarded.
func TestRunStandardsWritesAndRefusesTheWrongPairs(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "a_test.go", helperSource(20))
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "base")

	agents := filepath.Join(dir, "AGENTS.md")
	ctx := context.Background()

	if err := runStandards(ctx, []string{"-repo", dir, "-agents", agents}); err != nil {
		t.Fatalf("plain run: %v", err)
	}
	body, err := os.ReadFile(agents)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "t.Helper()") {
		t.Errorf("the measured rule is not in the file:\n%s", body)
	}

	for name, args := range map[string][]string{
		"agents with base": {"-repo", dir, "-agents", agents, "-base", "main"},
		"agents with json": {"-repo", dir, "-agents", agents, "-json"},
	} {
		if err := runStandards(ctx, args); err == nil {
			t.Errorf("%s: accepted, and would publish a measurement of something else", name)
		}
	}

	// And the plain report path runs.
	if err := runStandards(ctx, []string{"-repo", dir}); err != nil {
		t.Errorf("report: %v", err)
	}
	if err := runStandards(ctx, []string{"-repo", dir, "-json"}); err != nil {
		t.Errorf("json: %v", err)
	}
}
