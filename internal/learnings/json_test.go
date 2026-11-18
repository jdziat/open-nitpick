package learnings

import "testing"

func TestReviewedJSONCannotHideADuplicateOrUnknownPolicyKey(t *testing.T) {
	for _, raw := range []string{
		`{"version":1,"version":2,"entries":[]}`,
		`{"version":1,"entries":[],"disable_security":true}`,
		`{"version":1,"entries":[]} {"version":2}`,
		`{"version":1,"entries":[{"id":"shown","id":"hidden"}]}`,
	} {
		var manifest Manifest
		if err := Decode([]byte(raw), &manifest); err == nil {
			t.Fatalf("ambiguous or unreviewed data accepted: %s", raw)
		}
	}
	var manifest Manifest
	if err := Decode([]byte(`{"version":1,"entries":[]}`), &manifest); err != nil {
		t.Fatalf("valid empty manifest rejected: %v", err)
	}
}
