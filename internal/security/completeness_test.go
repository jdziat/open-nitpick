package security

import "testing"

func TestScannerSatisfiedOnlyRanOrNotApplicable(t *testing.T) {
	for _, tc := range []struct {
		status string
		ok     bool
	}{
		{StatusRan, true},
		{StatusNotApplicable, true},
		{StatusFailed, false},
		{StatusSkipped, false},
		{"", false},
	} {
		if got := scannerSatisfied(ScannerStatus{Status: tc.status}); got != tc.ok {
			t.Fatalf("status %q satisfied=%v, want %v", tc.status, got, tc.ok)
		}
	}
}

func TestModelSatisfiedAllowsDeliberateSkips(t *testing.T) {
	for _, tc := range []struct {
		status string
		ok     bool
	}{
		{ModelRan, true},
		{ModelSkippedByFlag, true},
		{ModelSkippedByConfig, true},
		{ModelFailed, false},
		{"", false},
	} {
		if got := modelSatisfied(ModelStatus{Status: tc.status}); got != tc.ok {
			t.Fatalf("model %q satisfied=%v, want %v", tc.status, got, tc.ok)
		}
	}
}
