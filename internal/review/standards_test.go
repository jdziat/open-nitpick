package review

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/standards"
)

func quietLog() *slog.Logger { return slog.New(slog.DiscardHandler) }

func gitIn(t *testing.T, dir string, args ...string) {
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

func writeIn(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// helpers builds n test helpers, marked or not.
func helpers(n int, marked bool) string {
	var b strings.Builder
	b.WriteString("package p\n\nimport \"testing\"\n")
	body := "t.Log(\"x\")"
	if marked {
		body = "t.Helper()"
	}
	for i := range n {
		b.WriteString("\nfunc h")
		b.WriteString(string(rune('a' + i%26)))
		b.WriteString(string(rune('a' + i/26)))
		b.WriteString("(t *testing.T) { " + body + " }\n")
	}
	return b.String()
}

func standardsOn() *config.Config {
	cfg := config.Defaults()
	cfg.Review.Standards = true
	return cfg
}

// The measurement reads the base revision, not the working tree.
//
// A change cannot supply the convention it is reviewed against, which is the
// seam config.BasePolicy holds for policy applied to a measurement. Reading
// the working tree would let a branch that rewrites a package's style be told
// the repository has always written it that way, and reference material is the
// one place that lands in front of the model rather than in a report.
func TestTheMeasurementReadsTheBaseRevision(t *testing.T) {
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	writeIn(t, dir, "a_test.go", helpers(20, true))
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "-qm", "base")
	base := "main"

	// The working tree now says the opposite of the base.
	writeIn(t, dir, "a_test.go", helpers(20, false))

	ref, status := BuildStandards(context.Background(), standardsOn(), dir, base, quietLog())
	if status.State != StandardsActive {
		t.Fatalf("state = %q (%s), want active", status.State, status.Reason)
	}

	var helper standards.Result
	for _, r := range ref.Report().Results {
		if r.ID == "go-test-helper-marks" {
			helper = r
		}
	}
	if helper.Conforming != 20 || helper.Total != 20 {
		t.Errorf("helper probe = %d/%d, want 20/20: the working tree was measured instead of %s",
			helper.Conforming, helper.Total, base)
	}
}

// A run that could not measure says why, and says it on the report.
//
// docs/measurement.md Rule 10. Off, skipped and active-with-nothing-found are
// three facts that render as the same absent section, and a reviewer given no
// conventions because there was no checkout is not a repository without
// conventions.
func TestAMeasurementThatDidNotRunSaysWhy(t *testing.T) {
	ctx := context.Background()

	if _, st := BuildStandards(ctx, config.Defaults(), "somewhere", "main", quietLog()); st.State != StandardsOff {
		t.Errorf("state = %q, want off when review.standards is not set", st.State)
	}

	for name, tc := range map[string]struct{ checkout, base, want string }{
		"no checkout": {"", "main", "checkout"},
		"no base":     {"somewhere", "", "base revision"},
	} {
		_, st := BuildStandards(ctx, standardsOn(), tc.checkout, tc.base, quietLog())
		if st.State != StandardsSkipped {
			t.Errorf("%s: state = %q, want skipped", name, st.State)
		}
		if !strings.Contains(st.Reason, tc.want) {
			t.Errorf("%s: reason = %q, want it to mention %q", name, st.Reason, tc.want)
		}
	}

	// A checkout that is not a repository is a skip, not a failed review.
	if _, st := BuildStandards(ctx, standardsOn(), t.TempDir(), "main", quietLog()); st.State != StandardsSkipped {
		t.Errorf("state = %q, want skipped when git cannot read the revision", st.State)
	}
}

// Only what cleared the floor reaches the prompt, and only for its own pass.
//
// A contested probe is a proposal. Putting a proposal in front of a reviewer
// as a measured convention is how a style disagreement becomes a finding, and
// a style rule in front of the defect pass is the dilution the generation
// scope exists to prevent.
func TestOnlyStandardsReachThePromptAndOnlyTheirOwnPass(t *testing.T) {
	ref := &StandardsRef{report: standards.Report{Results: []standards.Result{
		{ID: "style-one", Rule: "Do the style thing.", Class: config.ClassStyle,
			Conforming: 100, Total: 100, Standing: standards.StandingStandard},
		{ID: "correct-one", Rule: "Do the correct thing.", Class: config.ClassCorrectness,
			Conforming: 100, Total: 100, Standing: standards.StandingStandard},
		{ID: "contested-one", Rule: "Do the contested thing.", Class: config.ClassCorrectness,
			Conforming: 60, Total: 100, Standing: standards.StandingContested},
	}}}
	e := &Engine{Config: config.Defaults()}

	defect := ref.forClasses(e.standardsClasses(false))
	style := ref.forClasses(e.standardsClasses(true))

	if len(defect) != 1 || defect[0].ID != "correct-one" {
		t.Errorf("the defect pass sees %v, want the correctness rule alone", ruleIDs(defect))
	}
	if len(style) != 1 || style[0].ID != "style-one" {
		t.Errorf("the style pass sees %v, want the style rule alone", ruleIDs(style))
	}

	section := standardsSection(defect)
	if strings.Contains(section, "contested") {
		t.Error("a contested probe reached the prompt as a measured convention")
	}
	if !strings.Contains(section, "Do the correct thing.") {
		t.Errorf("the rule is not in the section:\n%s", section)
	}
	// The count travels with the rule, so the model can weigh it rather than
	// obey it, and the wording says a departure is not automatically a finding.
	if !strings.Contains(section, "every site of 100+ places") {
		t.Errorf("the section carries no evidence:\n%s", section)
	}
	if !strings.Contains(section, "Reference material, not findings") {
		t.Errorf("the section does not say what it is:\n%s", section)
	}
}

// ruleIDs names the results in a slice, for a failure message.
func ruleIDs(rs []standards.Result) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.ID)
	}
	return out
}

// Nothing to say renders nothing, rather than an empty heading.
func TestAnEmptySectionIsNotRendered(t *testing.T) {
	if got := standardsSection(nil); got != "" {
		t.Errorf("standardsSection(nil) = %q", got)
	}
}
