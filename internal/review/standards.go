package review

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/standards"
)

// What this repository has decided for itself, handed to the reviewer.
//
// internal/knowledge carries facts a model was not taught and cites a source
// outside the tree for each. This carries the conventions the tree arrived at
// on its own, and its citation is a count over the tree. A reviewer told
// "wrap an in-scope error with %w, 98%+ of 200+ places here" is being told
// something no amount of Go knowledge would have supplied.
//
// Measured at the base revision, never at the head. A change cannot supply the
// convention it is reviewed against, which is the seam config.BasePolicy holds
// for policy, applied to a measurement.

// StandardsState is what happened to the measurement on this run.
type StandardsState string

const (
	// StandardsOff means the operator did not ask for it.
	StandardsOff StandardsState = "off"

	// StandardsActive means a measurement ran and the reviewer was given it.
	StandardsActive StandardsState = "active"

	// StandardsSkipped means it was asked for and did not run, with a reason.
	// Distinct from off, and distinct from active with nothing found: three
	// outcomes that render as the same silence unless they are named. See
	// docs/measurement.md Rule 10.
	StandardsSkipped StandardsState = "skipped"
)

// StandardsStatus is the run's record of the measurement, for the report.
type StandardsStatus struct {
	State  StandardsState `json:"state"`
	Reason string         `json:"reason,omitempty"`

	// Standards is how many rules cleared the floor, and Contested how many
	// probes had sites and did not. Both, because "no standards" and "no probe
	// found anything to look at" are different facts about a repository.
	Standards int `json:"standards"`
	Contested int `json:"contested"`
}

// StandardsRef is the measurement a review reads from.
type StandardsRef struct {
	report standards.Report
}

// Report exposes the measurement for a caller that wants the counts.
func (s *StandardsRef) Report() standards.Report {
	if s == nil {
		return standards.Report{}
	}
	return s.report
}

// forClasses returns the rules a pass should see, or nothing.
func (s *StandardsRef) forClasses(allowed map[config.Class]bool) []standards.Result {
	if s == nil {
		return nil
	}
	return s.report.ForClasses(allowed)
}

// gitTree reads a revision through the git binary in a checkout.
//
// The provider interfaces have no notion of listing a revision's files, and
// adding one to every provider to serve this would be a large change for a
// measurement that already needs a clone to be affordable: reading a whole
// tree over an API is thousands of requests. The Action always has a checkout,
// and without one the measurement is skipped with a reason rather than run on
// something else.
type gitTree struct{ dir string }

func (g gitTree) Tree(ctx context.Context, rev string) ([]string, error) {
	out, err := g.git(ctx, "ls-tree", "-r", "--name-only", "-z", rev)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths, nil
}

func (g gitTree) Read(ctx context.Context, rev, path string) ([]byte, error) {
	return g.git(ctx, "show", rev+":"+path)
}

func (g gitTree) git(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = g.dir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

// BuildStandards measures the base revision's conventions, or says why it did
// not.
//
// Every failure here is a skip rather than an error, matching BuildKnowledge:
// this is an addition to a review that worked without it, so a shallow clone
// or a missing checkout costs the run its extra context and not the review.
// The reason reaches the report either way.
func BuildStandards(ctx context.Context, cfg *config.Config, checkout, base string,
	log *slog.Logger) (*StandardsRef, StandardsStatus) {

	if cfg == nil || !cfg.Review.Standards {
		return nil, StandardsStatus{State: StandardsOff}
	}

	skip := func(reason string) (*StandardsRef, StandardsStatus) {
		log.Warn("review.standards is on and no measurement ran; reviewing without it", "reason", reason)
		return nil, StandardsStatus{State: StandardsSkipped, Reason: reason}
	}

	switch {
	case checkout == "":
		return skip("no local checkout, and reading a whole tree over an API is thousands of requests")
	case base == "":
		return skip("no base revision to measure, and measuring the head lets the change supply its own convention")
	}

	files, err := standards.ReadAtRevision(ctx, gitTree{dir: checkout}, base, standardsSkipDir)
	if err != nil {
		return skip(fmt.Sprintf("read %s: %v", base, err))
	}

	opts := standards.Options{
		Floor:    standards.Floor{MinShare: cfg.Standards.MinShare, MinSites: cfg.Standards.MinSites},
		Disabled: cfg.Standards.Disabled,
	}
	rep := standards.Measure(files, opts)

	st := StandardsStatus{State: StandardsActive, Standards: len(rep.Standards())}
	for _, r := range rep.Results {
		if r.Standing == standards.StandingContested {
			st.Contested++
		}
	}
	log.Info("standards measured", "base", base, "standards", st.Standards, "contested", st.Contested)
	return &StandardsRef{report: rep}, st
}

// standardsSkipDir names directories a measurement should not walk.
//
// testdata is the one worth arguing: Go's convention is that it holds code
// written to be wrong, so measuring it asks whether a repository's fixtures
// follow its conventions, which is a question nobody has.
func standardsSkipDir(name string) bool {
	switch name {
	case "website", "dist", "public", ".website", "testdata":
		return true
	}
	return false
}

// standardsSection renders the rules a pass should read.
//
// After the diff and beside the knowledge entries, and worded the same way: the
// model judges whether a rule applies rather than obeying it. A convention is
// not a defect, and a reviewer that reports every departure from house style as
// a finding is the dilution the generation scope exists to prevent.
func standardsSection(rules []standards.Result) string {
	if len(rules) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n#### What this repository does, measured\n\n")
	b.WriteString("Reference material, not findings. Each line is a convention counted over the " +
		"base revision, with the share of places that follow it. A departure is worth raising " +
		"only where it costs a reader or breaks something; report nothing on the strength of " +
		"this section alone, and never report a rule's own count back as a defect.\n\n")
	for _, r := range rules {
		fmt.Fprintf(&b, "- %s (%s) %s\n",
			bundle.PromptSafe(r.Rule), r.Evidence(), bundle.PromptSafe(r.Why))
	}
	b.WriteString("\n")
	return b.String()
}

// standardsClasses are the classes a pass should be offered.
//
// The same split knowledgeClasses uses, and for the same reason: a style
// convention in front of the defect pass is context it should not act on.
func (e *Engine) standardsClasses(style bool) map[config.Class]bool {
	return e.knowledgeClasses(style)
}

// SkipsForStandards reports whether a measurement skips a directory of this
// name.
//
// Exported so the command can check its own copy of the list against this one.
// They cannot share a definition, since internal packages do not import cmd,
// and two lists that drift measure different sets.
func SkipsForStandards(name string) bool { return standardsSkipDir(name) }
