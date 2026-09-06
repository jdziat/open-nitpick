package config

import "testing"

// The default set is closed. This is the test that fails if somebody widens it
// without meaning to, which is the mistake that costs money on a public repo.
func TestByDefaultOnlyTheRepositorysOwnPeopleCanSpendItsCredit(t *testing.T) {
	var r Respond

	for _, assoc := range []string{"OWNER", "MEMBER", "COLLABORATOR"} {
		if !r.Allows(assoc) {
			t.Errorf("%s is refused by default, but they are the repository", assoc)
		}
	}
	for _, assoc := range []string{"CONTRIBUTOR", "FIRST_TIME_CONTRIBUTOR", "FIRST_TIMER", "MANNEQUIN", "NONE"} {
		if r.Allows(assoc) {
			t.Errorf("%s is allowed by default and can spend the model credit", assoc)
		}
	}
}

// A payload without the field is refused. Defaulting to allow on a shape
// nobody understands is how a spending control becomes decorative.
func TestAnAbsentAssociationIsRefused(t *testing.T) {
	var r Respond
	for _, assoc := range []string{"", "   ", "SOMETHING_NEW"} {
		if r.Allows(assoc) {
			t.Errorf("association %q was allowed", assoc)
		}
	}
}

// Matching ignores case, since the forge sends upper case and a config file is
// written in lower.
func TestTheCheckIsCaseInsensitive(t *testing.T) {
	r := Respond{From: []Association{AssocContributor}}
	if !r.Allows("CONTRIBUTOR") {
		t.Error("CONTRIBUTOR refused by a config naming contributor")
	}
}

// An operator can open it to everyone, and has to say so in the file. It
// cannot happen by leaving something out.
func TestOpeningItToEveryoneIsExplicit(t *testing.T) {
	open := Respond{From: []Association{AssocNone}}
	if !open.Allows("NONE") || !open.Allows("FIRST_TIMER") {
		t.Error("a config naming none still refuses strangers")
	}
}

func TestUnknownAssociationsAreRejectedByValidation(t *testing.T) {
	if errs := (Respond{From: []Association{"maintainer"}}).validate(); len(errs) == 0 {
		t.Error("an association the forge does not send was accepted")
	}
	if errs := (Respond{MaxPerPullRequest: -1}).validate(); len(errs) == 0 {
		t.Error("a negative cap was accepted")
	}
	if errs := (Respond{From: []Association{AssocOwner, AssocNone}, MaxPerPullRequest: 5}).validate(); len(errs) != 0 {
		t.Errorf("a valid block was rejected: %v", errs)
	}
}
