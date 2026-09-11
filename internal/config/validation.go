package config

import (
	"fmt"
	"strings"
)

// Validation configures the expert-validation pass.
//
// Before a finding is published it is routed to a domain expert, someone who
// knows SQL for an injection claim, memory models for a data race, which
// independently decides whether the claim holds. The point is to stop false
// positives reaching a pull request.
//
// The pass is a precision instrument aimed at a recall-critical pipeline, which
// is why it is opt-in and why every ambiguous answer keeps the finding. See
// review.Validator for the enforcement.
type Validation struct {
	// Enabled turns the pass on. Selected validation that cannot complete keeps
	// its findings visible and marks the review incomplete.
	//
	// Off by default because it is UNMEASURED. It costs one model call per
	// published finding, and its effect on recall, the number of real defects
	// an expert talks itself out of, has not been measured yet. Flipping this
	// one field is what the eval harness A/B tests; nothing else about a run
	// needs to change.
	Enabled bool `yaml:"enabled"`

	// Classes limits validation to these finding classes, so a team can
	// validate security and skip style. Empty validates every class.
	//
	// An unlisted class is published WITHOUT validation, never dropped:
	// narrowing this list can only reduce refutations. That direction is
	// deliberate, a configuration mistake here costs precision, not findings.
	Classes []Class `yaml:"classes"`

	// Targeted shows an expert the knowledge entries the reviewer had in front
	// of it when it wrote the finding, and asks it to name the one that
	// decided the verdict.
	//
	// Off by default, and unmeasured. It narrows what an expert is judging
	// from "is this claim true of this code" to "is this claim true of this
	// code given this rule", which is the question a wrong retrieval makes
	// easy to answer confidently and wrongly. It does nothing at all without
	// review.knowledge, since a finding written without retrieval cites
	// nothing.
	Targeted bool `yaml:"targeted"`
}

// ValidatesClass reports whether a finding of class c is sent to an expert.
func (v Validation) ValidatesClass(c Class) bool {
	if len(v.Classes) == 0 {
		return true
	}

	// Both sides are normalized so an alias in the config file ("injection")
	// still matches the class a finding carries ("security").
	target, _ := c.Normalize()
	for _, allowed := range v.Classes {
		if normalized, _ := allowed.Normalize(); normalized == target {
			return true
		}
	}
	return false
}

// validate checks the validation block.
func (v Validation) validate() []error {
	var errs []error

	// A typo here would normalize to unknown, match no finding, and silently
	// validate nothing, the failure shape this package refuses everywhere
	// else. Fail at load time instead, naming the valid values.
	for i, c := range v.Classes {
		if _, ok := c.Normalize(); !ok {
			errs = append(errs, fmt.Errorf("validation.classes[%d] %q is not one of: %s",
				i, c, strings.Join(ClassNames(), ", ")))
		}
	}

	return errs
}
