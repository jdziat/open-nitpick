package review

import (
	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// reviewEvent decides how a review is submitted.
//
// Comment is the answer for everything except a review that found nothing and
// read everything it planned to, under an operator who asked for approvals.
// review.approve is off by default, so nothing changes for a repository that
// has not opted in.
func reviewEvent(report *Report, cfg *config.Config) vcs.ReviewEvent {
	if cfg == nil || !cfg.Review.Approve.Enabled || report == nil {
		return vcs.EventComment
	}
	if len(report.Findings) > 0 {
		return vcs.EventComment
	}
	if report.Practices != nil && report.Practices.ExitCode() != 0 {
		return vcs.EventComment
	}

	// An earlier run's comment threads outlive the run that made them, and a
	// narrowed run never re-produces a finding on a file it did not re-read,
	// so Findings and AlreadyReported are both empty while a thread stands
	// open. Approving there describes a review nobody performed.
	//
	// Superseded is what this run closed. Anything left is counted as
	// standing, including a thread a person resolved by hand, which this tool
	// cannot see: that refuses an approval it might have earned, and the
	// direction to be wrong in is the one that publishes a comment.
	if len(report.AlreadyReported) > 0 || report.PriorComments > len(report.Superseded) {
		return vcs.EventComment
	}

	// A run whose batches partly failed published no findings for the files it
	// never read, and a run whose triage died published findings nothing
	// ranked. Reading either as clean is how an approval comes to mean less
	// than nothing.
	if !report.PipelineComplete() {
		return vcs.EventComment
	}

	if cfg.Review.Approve.RequireAnalyzers && !analyzersCovered(report, cfg) {
		return vcs.EventComment
	}
	return vcs.EventApprove
}

// analyzersCovered reports whether the deterministic half of the review
// happened over the whole change.
//
// An analyzer that was skipped or failed is not a clean result, and neither is
// one that ran over less of the change than "ran" implies: golangciLint.Uncovered
// records a file a build constraint excluded or a suppression the change added,
// and an approval that ignores those says the code was checked when it was not.
func analyzersCovered(report *Report, cfg *config.Config) bool {
	// An empty roster satisfies "every analyzer ran" vacuously, and that is
	// how the setting forbidding an unchecked approval comes to permit one:
	// -no-linters leaves Linters nil, and so does any run that never built an
	// analyzer set. A configuration that enables analyzers and a report that
	// names none did not run them.
	if len(report.Linters) == 0 && cfg.Linters.Mode != config.LinterOff {
		return false
	}
	if len(report.Uncovered) > 0 {
		return false
	}
	for _, s := range report.Linters {
		if s.Outcome != LinterRan {
			return false
		}
	}
	return true
}
