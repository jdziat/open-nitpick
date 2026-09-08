//go:build eval

package evals

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/fullreview"
	"github.com/jdziat/open-nitpick/internal/linters"
	"github.com/jdziat/open-nitpick/internal/llm"
	"github.com/jdziat/open-nitpick/internal/review"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// TestFullReviewFixture is the acceptance test for `nitpick full-review`,
// section 1 of notes/plan-full-review.md: the fixture repository reviewed
// whole finds the planted bug and the planted secret, says nothing about the
// clean file, lists the known advisory when osv-scanner is installed, and puts
// the secret first in the remediation plan.
//
// The note behind it is in docs/harness-notes.md#advisoryid.
var advisoryID = regexp.MustCompile(`(?m)^\s+osv-scanner\((GHSA-|CVE-|GO-20)[^)]*\)\s+go\.mod:\d+ `)

func TestFullReviewFixture(t *testing.T) {
	report, tree, model := reviewTree(t, FullReviewFixture)
	out := fullreview.Sections(report) + fullreview.RemediationPlan(report.Findings) + fullreview.CoverageNotice(report, tree)
	t.Logf("model %s reviewed %d file(s), %d finding(s):\n%s", model.ID, len(tree.Covered), len(report.Findings), out)

	for _, plant := range FullReviewPlants {
		if !located(report.Findings, plant) {
			t.Errorf("planted %s at %s:%d not located (%s)", plant.Class, plant.Path, plant.Line, plant.Why)
		}
	}
	for _, f := range report.Findings {
		if f.Path == FullReviewClean {
			t.Errorf("finding on the clean control %s:%d: %s", f.Path, f.Line, f.Title)
		}
	}

	if _, err := exec.LookPath("osv-scanner"); err == nil {
		// The scanner's section, not the model's wording: the model names
		// the pin on its own, and that must not stand in for the advisory.
		if !strings.Contains(out, "\nKnown advisories") || strings.Contains(out, "none reported. If osv-scanner") || !advisoryID.MatchString(out) {
			t.Errorf("osv-scanner is installed but the known-advisories section lists no advisory id for golang.org/x/text v0.3.0:\n%s", out)
		}
	} else {
		t.Logf("osv-scanner not installed; the advisories section is unverified")
	}

	plan := fullreview.RemediationPlan(report.Findings)
	first := strings.SplitN(strings.TrimSpace(strings.TrimPrefix(plan, "\nRemediation plan, most severe first; findings that share a fix are grouped:\n")), "\n", 2)[0]
	if !strings.Contains(first, "/security]") {
		t.Errorf("the remediation plan does not put the secret first:\n%s", plan)
	}
}

// TestRepoScoreFixture is the acceptance for `nitpick repo-score`, section 3
// of the plan: the fixture with its slop file scores above SlopThreshold,
// and the same fixture without it scores below.
func TestRepoScoreFixture(t *testing.T) {
	with, tree, model := reviewTree(t, FullReviewFixture)
	withCard := fullreview.Score(with, tree)
	t.Logf("model %s, with the slop file:%s", model.ID, withCard)

	without := map[string]string{}
	for p, c := range FullReviewFixture {
		if !strings.HasPrefix(p, "internal/slop/") {
			without[p] = c
		}
	}
	report, tree, _ := reviewTree(t, without)
	withoutCard := fullreview.Score(report, tree)
	t.Logf("without the slop file:%s", withoutCard)

	if got := withCard.Total.PerKLOC(withCard.Total.SlopWeighted); got <= fullreview.SlopThreshold {
		t.Errorf("with the slop file, slop per KLOC = %.2f, want above %.1f", got, fullreview.SlopThreshold)
	}
	if got := withoutCard.Total.PerKLOC(withoutCard.Total.SlopWeighted); got >= fullreview.SlopThreshold {
		t.Errorf("without the slop file, slop per KLOC = %.2f, want below %.1f", got, fullreview.SlopThreshold)
	}
}

// reviewTree materialises files as a git repository and reviews it whole,
// the way runTreeReview does, under the eval model.
func reviewTree(t *testing.T, files map[string]string) (*review.Report, *vcs.Tree, Model) {
	t.Helper()
	opts, err := OptionsFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	model := opts.Models[0]

	dir := t.TempDir()
	for p, content := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, p), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "-A"}, {"-c", "user.email=e@x", "-c", "user.name=e", "commit", "-qm", "tree"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	cfg := evalConfig(model)
	cfg.Review.RelatedContext = true
	cfg.Review.RelatedContextCallers = true
	cfg.Review.Slop = true
	cfg.Review.MaxFiles = 1 << 30
	cfg.Review.Incremental = false
	cfg.Linters.Mode = config.LinterAuto
	auto := true
	cfg.Linters.AutoDetect = &auto
	cfg.Linters.Enabled = append(cfg.Linters.Enabled, "osv-scanner") // as the command does; a skip when absent

	client, err := llm.Build(cfg.Models.Default)
	if err != nil {
		t.Fatalf("build model %s: %v", model.ID, err)
	}
	tree := vcs.NewTree(vcs.NewLocal(dir, io.Discard), nil)
	tree.MaxBytes = cfg.Review.MaxFileBytes
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	engine := &review.Engine{
		Config:   cfg,
		Roles:    &llm.Roles{Review: client, Triage: client},
		Provider: tree,
		Log:      log,
		Linters: func(policy *config.Config) review.LinterRunner {
			return linters.New(dir, policy, log)
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	report, err := engine.Review(ctx, vcs.Ref{Head: vcs.Worktree})
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	return report, tree, model
}

// located reports whether a finding on the plant's file, within the anchor
// tolerance of its line, uses one of its keywords.
func located(findings []review.Finding, d Defect) bool {
	for _, f := range findings {
		if f.Path != d.Path || anchorDistance(f, d.Line) > anchorTolerance {
			continue
		}
		if mentionsAny(f, d.Keywords) {
			return true
		}
	}
	return false
}
