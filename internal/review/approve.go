package review

import (
	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// reviewEvent decides how a review is submitted.
//
// Comment is the answer for everything except a review that found nothing and
// read everything it planned to, under an operator who asked for approvals,
// or a residual-eligible run whose judge set ResidualApprove.
// review.approve is off by default, so nothing changes for a repository that
// has not opted in.
func reviewEvent(report *Report, cfg *config.Config) vcs.ReviewEvent {
	if cfg == nil || !cfg.Review.Approve.Enabled || report == nil {
		return vcs.EventComment
	}
	if !approveHardGates(report, cfg) {
		return vcs.EventComment
	}
	if len(report.Findings) == 0 {
		return vcs.EventApprove
	}
	if report.ResidualApprove && residualEligible(report, cfg) {
		return vcs.EventApprove
	}
	return vcs.EventComment
}

// approveHardGates are the completeness and standing-thread checks shared by
// clean and residual approve. Residual never weakens these.
func approveHardGates(report *Report, cfg *config.Config) bool {
	if !approveCompletenessGates(report, cfg) {
		return false
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
	//
	// AlreadyReported is always load-bearing: those findings recurred and are
	// still on the pull request. Residual may clear other standing threads
	// after a yes, but it must not approve while withheld recurrences remain.
	//
	// priorReadFailed means residual asked for the prior and the forge did not
	// answer; treating that as zero standing threads would approve beside
	// comments the run could not enumerate.
	if report.priorReadFailed || len(report.AlreadyReported) > 0 || standingThreadsRemain(report) {
		return false
	}
	return true
}

// standingThreadsRemain reports whether earlier comments still stand after
// this run's supersedes.
func standingThreadsRemain(report *Report) bool {
	return report.PriorComments > len(report.Superseded)
}

// approveCompletenessGates are the non-standing checks shared by the event
// path and the residual judge.
func approveCompletenessGates(report *Report, cfg *config.Config) bool {
	if report.Practices != nil && report.Practices.ExitCode() != 0 {
		return false
	}
	if !report.Complete() || !report.PipelineComplete() {
		return false
	}
	if cfg.Review.Approve.RequireAnalyzers && !analyzersCovered(report, cfg) {
		return false
	}
	return true
}

func residualMaxSeverity(cfg *config.Config) config.Severity {
	switch cfg.Review.Approve.Residual.MaxSeverity {
	case config.SeverityNit, config.SeverityInfo, config.SeverityWarning:
		return cfg.Review.Approve.Residual.MaxSeverity
	default:
		// Empty (unset) and any value validate would have rejected both floor
		// at info, never below nit.
		return config.ResidualMaxSeverityDefault
	}
}

// residualWithinFloor reports whether every published finding is at most max.
// An unrecognized severity cannot pass: Rank would map it to info and greenwash
// a finding that never cleared a known floor.
func residualWithinFloor(findings []Finding, max config.Severity) bool {
	ceiling := max.Rank()
	for _, f := range findings {
		sev := config.Severity(f.Severity)
		if !sev.Valid() || sev.Rank() > ceiling {
			return false
		}
	}
	return true
}

// residualEligible is true when residual findings may earn APPROVE: approve
// and residual are on, hard gates pass, findings are non-empty and within the floor.
func residualEligible(report *Report, cfg *config.Config) bool {
	if cfg == nil || !cfg.Review.Approve.Enabled || !cfg.Review.Approve.Residual.Enabled || report == nil {
		return false
	}
	if len(report.Findings) == 0 {
		return false
	}
	if !approveHardGates(report, cfg) {
		return false
	}
	return residualWithinFloor(report.Findings, residualMaxSeverity(cfg))
}

// residualJudgeEligible is the pre-judge gate: completeness without the
// standing-thread check, so the judge can run before publish closes them.
func residualJudgeEligible(report *Report, cfg *config.Config) bool {
	if cfg == nil || !cfg.Review.Approve.Enabled || !cfg.Review.Approve.Residual.Enabled || report == nil {
		return false
	}
	if len(report.Findings) == 0 || len(report.AlreadyReported) > 0 {
		return false
	}
	if !approveCompletenessGates(report, cfg) {
		return false
	}
	return residualWithinFloor(report.Findings, residualMaxSeverity(cfg))
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
