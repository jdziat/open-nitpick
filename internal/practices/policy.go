package practices

import (
	"slices"

	"github.com/jdziat/open-nitpick/internal/config"
)

// ApplyPolicy makes unscheduled required checks visible before applying gates.
func ApplyPolicy(report *Report, policy config.Practices) {
	required := slices.Clone(policy.Required)
	if len(policy.Boundaries) > 0 && !slices.Contains(required, "design-boundaries") {
		required = append(required, "design-boundaries")
	}
	for _, id := range required {
		if !slices.ContainsFunc(report.Checks, func(c Check) bool { return c.ID == id }) {
			instrument := Deterministic
			if id == "slop" || id == "design" {
				instrument = Model
			}
			report.Checks = append(report.Checks, Check{ID: id, Version: "1", Instrument: instrument, Required: true, State: Unavailable, Reason: "required check was not scheduled"})
		}
	}
	for i := range report.Checks {
		check := &report.Checks[i]
		check.Required = check.Required || slices.Contains(policy.Required, check.ID)
		if check.ID == "design-boundaries" && len(policy.Boundaries) > 0 {
			check.Required = true
		}
		if threshold, ok := policy.FailOn[check.ID]; ok {
			for j := range check.Findings {
				check.Findings[j].Blocking = check.Findings[j].Uncertainty == "" && config.Severity(check.Findings[j].Severity).AtLeast(threshold)
			}
		}
	}
}
