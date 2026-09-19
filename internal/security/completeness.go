package security

import "fmt"

// evaluateComplete reports whether every required scanner and the model
// instrument finished in a status that counts as complete under Rule 10.
func evaluateComplete(scanners []ScannerStatus, required []string, model ModelStatus) (complete bool, failed []string) {
	byID := make(map[string]ScannerStatus, len(scanners))
	for _, s := range scanners {
		byID[s.ID] = s
	}
	for _, id := range required {
		s, ok := byID[id]
		if !ok {
			failed = append(failed, fmt.Sprintf("%s: required scanner not in roster", id))
			continue
		}
		if !scannerSatisfied(s) {
			failed = append(failed, stageReason(s.ID, s.Reason, s.Status))
		}
	}
	if !modelSatisfied(model) {
		failed = append(failed, stageReason("model", model.Reason, model.Status))
	}
	return len(failed) == 0, failed
}

// scannerSatisfied is true when a required scanner finished without a hole.
func scannerSatisfied(s ScannerStatus) bool {
	return s.Status == StatusRan || s.Status == StatusNotApplicable
}

// modelSatisfied is true when the model ran or was deliberately skipped.
func modelSatisfied(m ModelStatus) bool {
	switch m.Status {
	case ModelRan, ModelSkippedByFlag, ModelSkippedByConfig:
		return true
	default:
		return false
	}
}

func stageReason(id, reason, status string) string {
	if reason != "" {
		return fmt.Sprintf("%s: %s", id, reason)
	}
	return fmt.Sprintf("%s: %s", id, status)
}
