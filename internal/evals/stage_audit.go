package evals

import "github.com/nitpick-oss/nitpick/internal/review"

// StageAttribution separates matcher credit from evidence of where a claim was lost.
type StageAttribution struct {
	Defect    string `json:"defect"`
	Generated bool   `json:"generated"`
	Published bool   `json:"published"`
	// FirstUncreditedStage is the first stage on the ladder whose snapshot has no
	// credit for the plant. It names the loss only when Unknown is false.
	FirstUncreditedStage string `json:"first_uncredited_stage"`
	// Unknown is true when the evidence cannot say where the claim was lost: a
	// stage up to and including the first uncredited one has no snapshot, or the
	// generation or publication snapshot is missing.
	Unknown bool `json:"unknown"`
	// StagePresent records which expected stages had a snapshot at all.
	StagePresent map[string]bool `json:"stage_present,omitempty"`
}

// Optional stages a manifest may enable. They sit on the ladder only when the
// run enabled them, so a disabled stage's missing snapshot is not lost evidence.
const (
	StageValidation   = "validation"
	StageVerification = "verification"
)

// ExpectedStages is the ladder of snapshot stages a review of the given type
// emits, in the order the engine emits them. Snapshots outside the ladder
// (batch-generation, generation-lookups, triage-input, analyzers, style,
// critique) are intermediate detail and are not attributed to.
//
// deep-review has no registered ladder: its stages are registered from captured
// runs rather than guessed, so it returns nil and every attribution is Unknown.
func ExpectedStages(typ string, optional []string) []string {
	switch typ {
	case TypeReview, TypeSlop, TypeSecurity, "":
	default:
		return nil
	}
	enabled := map[string]bool{}
	for _, stage := range optional {
		enabled[stage] = true
	}
	ladder := []string{"generation", "anchors-before-triage", "triage", "anchors-after-triage"}
	if enabled[StageValidation] {
		ladder = append(ladder, StageValidation)
	}
	if enabled[StageVerification] {
		ladder = append(ladder, StageVerification)
	}
	return append(ladder, "publication-gate")
}

// AttributeStages uses the frozen scorer; semantic disputes remain separate.
// expected is the ladder from ExpectedStages. It is computed from that ladder,
// not from snapshot arrival order, so a missing middle snapshot cannot make a
// later stage look like the point of loss.
func AttributeStages(f Fixture, snapshots []review.Snapshot, expected []string) []StageAttribution {
	return AttributeStagesForPersona(f, snapshots, expected, securityPersonaActive())
}

// AttributeStagesForPersona is AttributeStages with the persona given
// explicitly, so offline replay does not read the caller's environment.
func AttributeStagesForPersona(f Fixture, snapshots []review.Snapshot, expected []string, security bool) []StageAttribution {
	stages := map[string]DetectionScore{}
	for _, snapshot := range snapshots {
		stages[snapshot.Stage] = ScoreDetectionForPersona(f, snapshot.Findings, security)
	}
	present := map[string]bool{}
	for _, stage := range expected {
		_, present[stage] = stages[stage]
	}
	var result []StageAttribution
	for _, defect := range PersonaDefectsFor(f, security) {
		row := StageAttribution{Defect: defect.Why, StagePresent: present}
		_, generated := stages["generation"]
		_, published := stages["publication-gate"]
		row.Unknown = !generated || !published
		row.Generated = stages["generation"].Detected[defect.Why]
		row.Published = stages["publication-gate"].Detected[defect.Why]
		if len(expected) == 0 {
			row.Unknown = true
		}
		for _, stage := range expected {
			if !present[stage] {
				row.Unknown = true
				continue
			}
			if !stages[stage].Detected[defect.Why] {
				row.FirstUncreditedStage = stage
				break
			}
		}
		result = append(result, row)
	}
	return result
}
