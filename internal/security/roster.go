package security

import (
	"fmt"
	"slices"

	"github.com/jdziat/open-nitpick/internal/review"
)

// ScannerStatus is one deterministic scanner's terminal status on a security run.
type ScannerStatus struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// Scanner terminal statuses.
const (
	StatusRan           = "ran"
	StatusNotApplicable = "not_applicable"
	StatusFailed        = "failed"
	StatusSkipped       = "skipped"
)

// ModelStatus is the optional model instrument's terminal status.
type ModelStatus struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// Model terminal statuses.
const (
	ModelRan             = "ran"
	ModelSkippedByFlag   = "skipped_by_flag"
	ModelSkippedByConfig = "skipped_by_config"
	ModelFailed          = "failed"
)

// Roster is the machine-readable completeness record for a security run.
type Roster struct {
	Scanners     []ScannerStatus `json:"scanners"`
	Model        ModelStatus     `json:"model"`
	GateWaived   bool            `json:"gate_waived,omitempty"`
	FailedStages []string        `json:"failed_stages,omitempty"`
	Complete     bool            `json:"complete"`
}

// BuildRoster maps linter outcomes onto the security roster. Every required id
// appears; a missing required id is failed. golangci-lint that ran without
// gosec:enabled in State is failed. RequiredAlways ids are always listed.
func BuildRoster(linterStatuses []review.LinterStatus, requiredIDs []string, model ModelStatus, gateWaived bool) Roster {
	byID := make(map[string]review.LinterStatus, len(linterStatuses))
	order := make([]string, 0, len(linterStatuses))
	for _, st := range linterStatuses {
		if _, ok := byID[st.Linter]; !ok {
			order = append(order, st.Linter)
		}
		byID[st.Linter] = st
	}

	required := mergeRequired(requiredIDs)
	scanners := make([]ScannerStatus, 0, len(required)+len(order))
	seen := make(map[string]bool, len(required))

	for _, id := range required {
		scanners = append(scanners, statusFor(id, byID))
		seen[id] = true
	}
	for _, id := range order {
		if seen[id] {
			continue
		}
		scanners = append(scanners, statusFor(id, byID))
		seen[id] = true
	}

	complete, failed := evaluateComplete(scanners, required, model)
	return Roster{
		Scanners:     scanners,
		Model:        model,
		GateWaived:   gateWaived,
		FailedStages: failed,
		Complete:     complete,
	}
}

func mergeRequired(requiredIDs []string) []string {
	out := make([]string, 0, len(RequiredAlways)+len(requiredIDs))
	for _, id := range RequiredAlways {
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	for _, id := range requiredIDs {
		if id == "" || slices.Contains(out, id) {
			continue
		}
		out = append(out, id)
	}
	return out
}

func statusFor(id string, byID map[string]review.LinterStatus) ScannerStatus {
	st, ok := byID[id]
	if !ok {
		return ScannerStatus{ID: id, Status: StatusFailed, Reason: "required scanner not in roster"}
	}
	out := ScannerStatus{ID: id, Reason: st.State}
	switch st.Outcome {
	case review.LinterRan:
		out.Status = StatusRan
	case review.LinterSkipped:
		// Only a typed no-targets skip is not_applicable. Any other skip
		// stays skipped so RequiredAlways cannot greenwash a disabled or
		// aborted attempt as a satisfied N/A.
		if st.NoTargets {
			out.Status = StatusNotApplicable
		} else {
			out.Status = StatusSkipped
			if out.Reason == "" {
				out.Reason = "skipped without a no-targets reason"
			}
		}
	case review.LinterFailed:
		out.Status = StatusFailed
	default:
		out.Status = StatusFailed
		if out.Reason == "" {
			out.Reason = fmt.Sprintf("unknown linter outcome %q", st.Outcome)
		}
	}
	if id == "golangci-lint" && out.Status == StatusRan && !st.GosecEnabled {
		out.Status = StatusFailed
		out.Reason = "gosec not enabled"
	}
	return out
}
