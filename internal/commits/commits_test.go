package commits

import (
	"strings"
	"testing"
)

func TestSubjectRulesAcceptConventionsAndRejectSpoofedMerges(t *testing.T) {
	for _, tc := range []struct {
		subject string
		parents int
		valid   bool
	}{
		{"feat: add coverage", 1, true}, {"fix(api)!: reject stale input", 1, true},
		{"Merge innocent change", 1, false}, {"Merge branch 'topic'", 2, true},
		{"updates", 1, false}, {"feat: ", 1, false}, {"feat:  extra space", 1, false},
		{"feat: " + strings.Repeat("é", 72), 1, true}, {"feat: " + strings.Repeat("é", 73), 1, false},
		{"fixup! feat: add coverage", 1, false}, {"squash! feat: add coverage", 1, false},
		{"revert: undo coverage", 1, true}, {"Revert \"feat: coverage\"", 1, false},
		{"feat: line\nsecond", 1, false}, {"feat: line\x1b[0m", 1, false},
	} {
		t.Run(tc.subject, func(t *testing.T) {
			got := Validate(Commit{SHA: "abc", Subject: tc.subject, ParentCount: tc.parents}, Defaults())
			if (len(got) == 0) != tc.valid {
				t.Errorf("valid=%t findings=%+v", tc.valid, got)
			}
		})
	}
}

func TestExplicitCommitPolicyControlsTypesLengthAndMergeExemptions(t *testing.T) {
	p := Defaults()
	p.Types, p.MaxDescriptionRunes, p.ExemptMerges = []string{"change"}, 5, false
	if len(Validate(Commit{Subject: "change: hello"}, p)) != 0 {
		t.Fatal("custom policy rejected")
	}
	for _, c := range []Commit{{Subject: "feat: hello"}, {Subject: "change: longer"}, {Subject: "Merge branch", ParentCount: 2}} {
		if len(Validate(c, p)) == 0 {
			t.Errorf("policy bypass: %+v", c)
		}
	}
}

func TestMalformedCommitPoliciesAreRejected(t *testing.T) {
	for _, p := range []Policy{{}, {Types: []string{"feat"}, MaxDescriptionRunes: -1}, {Types: []string{"feat|fix"}, MaxDescriptionRunes: 72}} {
		if p.Check() == nil {
			t.Errorf("invalid policy accepted: %+v", p)
		}
	}
}
