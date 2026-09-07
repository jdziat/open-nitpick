package prompt

import "strings"

// LayerModel is the layer name for guidance addressed to the model family
// doing the reviewing.
const LayerModel = "model"

// Family is a group of models that share a training lineage and, in the eval
// battery, a failure mode worth a sentence of correction.
type Family string

// The families with guidance. A model outside them gets none, which is the
// right default: guidance written for one family's measured habit is noise to
// a family that does not have it.
const (
	FamilyNone     Family = ""
	FamilyGLM      Family = "glm"
	FamilyQwen     Family = "qwen"
	FamilyDeepSeek Family = "deepseek"
)

// FamilyOf classifies a model id the way the routers spell it: an optional
// vendor prefix ("z-ai/", "qwen/", "hf:Qwen/") and then the model name. It
// reads the name, not the vendor, because the same weights are served under
// several vendor prefixes and the habit travels with the weights.
func FamilyOf(model string) Family {
	name := strings.ToLower(strings.TrimSpace(model))
	// Drop a router or host prefix such as "hf:" and the vendor segment.
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	switch {
	case strings.HasPrefix(name, "glm"):
		return FamilyGLM
	case strings.HasPrefix(name, "qwen"), strings.HasPrefix(name, "qwq"):
		return FamilyQwen
	case strings.HasPrefix(name, "deepseek"):
		return FamilyDeepSeek
	}
	return FamilyNone
}

// ModelGuidance returns the layer text for a model, empty when its family has
// none.
//
// Every sentence here corrects a habit observed in that family's reviews on
// the eval corpora and recorded in docs/comparison.md; none of it changes the
// reporting bar, which is the base prompt's alone. A family's text is written
// as behaviour to follow, not as a description of the family, because a model
// told "you tend to X" tends to X.
func ModelGuidance(model string) string {
	switch FamilyOf(model) {
	case FamilyQwen:
		// Measured on the tuning, multi-file and info corpora with related
		// context on, two runs each, against the same prompt without this
		// layer: recall rises from 0.72 to 0.81, from 0.92 to 0.96 and from
		// 0.60 to 0.65, with noise equal or lower on every corpus. See
		// docs/comparison.md.
		return "## Notes for this reviewer\n\n" +
			"- Read every hunk of the diff and decide about each before reading the definitions " +
			"section that may follow it. Those definitions resolve names the diff uses; they do not " +
			"replace reading the change, and a defect in the diff is not less of a defect because " +
			"the definitions are long.\n" +
			"- A small change with one hunk gets the same reading as a large one. The first file in " +
			"the batch is not the only file in the batch.\n"
	case FamilyGLM, FamilyDeepSeek:
		// A note for GLM was measured against its absence on the same three
		// corpora: no recall change, and noise moved both ways, from 0.12 to
		// 0.00 on info and from 0.19 to 0.30 on multi-file. A layer that
		// cannot show its contribution does not ship. DeepSeek's habit looks
		// like Qwen's in the sweep, but its note was never measured, and an
		// unmeasured note is the same thing.
		return ""
	}
	return ""
}
