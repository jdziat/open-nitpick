package config

import "testing"

// TestSeverityNoneIsNotAFindingSeverity is the regression test for the sentinel
// outranking every real level.
//
// SeverityNone ranks above critical so that `fail_on: none` matches nothing.
// That is correct for a threshold and catastrophic for a finding: a model
// returning severity "none" would satisfy every gate, including fail_on:
// critical, and block the merge.
func TestSeverityNoneIsNotAFindingSeverity(t *testing.T) {
	if SeverityNone.AtLeast(SeverityCritical) {
		t.Error(`a finding claiming severity "none" must not satisfy a critical gate`)
	}
	if SeverityNone.AtLeast(SeverityNit) {
		t.Error(`severity "none" must not satisfy any gate`)
	}
	if SeverityNone.IsFinding() {
		t.Error("none is a threshold, not a severity a finding may carry")
	}

	// The threshold behavior it exists for must still hold.
	for _, s := range []Severity{SeverityNit, SeverityInfo, SeverityWarning, SeverityError, SeverityCritical} {
		if s.AtLeast(SeverityNone) {
			t.Errorf("%s should not satisfy the none threshold (fail_on: none never fails)", s)
		}
		if !s.IsFinding() {
			t.Errorf("%s should be a valid finding severity", s)
		}
	}
}

func TestSeverityNormalize(t *testing.T) {
	cases := []struct {
		in     Severity
		want   Severity
		wantOK bool
	}{
		{"error", SeverityError, true},
		{"ERROR", SeverityError, true},
		{"  Warning  ", SeverityWarning, true},
		{"critical", SeverityCritical, true},
		// Unknown vocabularies become a visible, non-gating finding rather
		// than vanishing or blocking a merge.
		{"P1", SeverityInfo, false},
		{"blocker", SeverityInfo, false},
		{"major", SeverityInfo, false},
		{"", SeverityInfo, false},
		// The sentinel is not a finding severity.
		{"none", SeverityInfo, false},
	}

	for _, tc := range cases {
		got, ok := tc.in.Normalize()
		if got != tc.want || ok != tc.wantOK {
			t.Errorf("Severity(%q).Normalize() = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.want, tc.wantOK)
		}
		// Whatever it normalizes to must be a usable finding severity.
		if !got.IsFinding() {
			t.Errorf("Normalize(%q) produced %q, which is not a finding severity", tc.in, got)
		}
	}
}

func TestNormalizedSeverityGatesConsistently(t *testing.T) {
	// After normalization, gate behavior must be predictable for every input a
	// model might supply.
	for _, raw := range []Severity{"none", "P1", "ERROR", "critical", "nit", ""} {
		normalized, _ := raw.Normalize()

		if normalized == SeverityCritical && raw != "critical" {
			t.Errorf("%q normalized to critical; only an explicit critical should", raw)
		}
		// Nothing unrecognized may reach the strictest gate.
		if !normalized.Valid() {
			t.Errorf("%q normalized to an invalid severity %q", raw, normalized)
		}
	}
}
