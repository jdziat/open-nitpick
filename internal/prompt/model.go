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
	case FamilyGLM:
		return "## Notes for this reviewer\n\n" +
			"- Before filing a finding at `info` or `nit`, reread its rationale. If it says the " +
			"harm needs a caller not shown, a future change, or an input the types do not admit, " +
			"the finding is not filed. That sentence in a rationale is a decision, not a caveat.\n" +
			"- On a change touching several files, every finding is about a line this change " +
			"added or altered. Behaviour the change left alone is not reported, at any level.\n" +
			"- One consequence, one finding. A second finding that restates the first consequence " +
			"from another line or another angle is a duplicate.\n"
	case FamilyQwen, FamilyDeepSeek:
		return "## Notes for this reviewer\n\n" +
			"- Read every hunk of the diff and decide about each before reading the definitions " +
			"section that may follow it. Those definitions resolve names the diff uses; they do not " +
			"replace reading the change, and a defect in the diff is not less of a defect because " +
			"the definitions are long.\n" +
			"- A small change with one hunk gets the same reading as a large one. The first file in " +
			"the batch is not the only file in the batch.\n"
	}
	return ""
}
