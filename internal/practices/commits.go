package practices

import "github.com/jdziat/open-nitpick/internal/commits"

// CommitCheck retains each commit as evidence, including exempt merge commits.
func CommitCheck(entries []commits.Commit, policy commits.Policy) Check {
	c := Check{ID: "commits", Version: "1", Instrument: Deterministic, Required: true, State: Completed, Tool: "nitpick"}
	if err := policy.Check(); err != nil {
		c.State, c.Reason = Failed, err.Error()
		return c
	}
	if len(entries) == 0 {
		c.State, c.Reason = NotApplicable, "the resolved commit range is empty"
		return c
	}
	for _, entry := range entries {
		target := Target{Kind: CommitTarget, ID: entry.SHA}
		c.Planned = append(c.Planned, target)
		c.Examined = append(c.Examined, target)
		for _, violation := range commits.Validate(entry, policy) {
			c.Findings = append(c.Findings, Finding{Rule: violation.Rule, Target: target, Title: violation.Message,
				Rationale: entry.Subject, Remedy: "write a subject conforming to the selected commit policy", Sources: []string{"nitpick.commits"}, Severity: "warning", Blocking: true})
		}
	}
	return c
}

// TitleCheck evaluates the intended squash title without claiming commit coverage.
func TitleCheck(title string, policy commits.Policy) Check {
	c := CommitCheck([]commits.Commit{{SHA: "squash-title", Subject: title}}, policy)
	c.ID = "commit-title"
	for i := range c.Planned {
		c.Planned[i].Kind = TitleTarget
	}
	for i := range c.Examined {
		c.Examined[i].Kind = TitleTarget
	}
	for i := range c.Findings {
		c.Findings[i].Target.Kind = TitleTarget
	}
	return c
}
