package config

import "strings"

// Class is what kind of problem a finding describes. It exists so nitpick
// level is a filter over one review corpus rather than a change to what the
// model is asked to look for: generating at a wider scope degraded the defect
// hunt across four eval runs, and changing the prompt per level confounded
// scope with model variance. One corpus plus deterministic filters evaluates
// offline at zero cost, and free-text categories cannot support that, so this
// is a closed set the schema enforces.
type Class string

// Finding classes, ordered roughly by how universally teams want them.
const (
	// ClassCorrectness is logic that produces a wrong result.
	ClassCorrectness Class = "correctness"
	// ClassConcurrency is races, deadlocks and missing synchronization.
	ClassConcurrency Class = "concurrency"
	// ClassSecurity is injection, authorization, secrets and traversal.
	ClassSecurity Class = "security"
	// ClassResource is leaks and unbounded growth.
	ClassResource Class = "resource"
	// ClassDataLoss is destruction or corruption of persisted data.
	ClassDataLoss Class = "data-loss"
	// ClassContract is a change that breaks existing callers.
	ClassContract Class = "contract"
	// ClassTests is missing coverage for risky new logic.
	ClassTests Class = "tests"
	// ClassMaintainability is a structural cost that can be named concretely.
	ClassMaintainability Class = "maintainability"
	// ClassStyle is naming, documentation, idiom and consistency.
	ClassStyle Class = "style"
	// ClassSlop is generated-looking code that costs a reader: a comment that
	// restates its line, a check against a condition the types exclude, an
	// error swallowed and carried on from, a test that asserts nothing. It is
	// published by review.slop alone, never by the nitpick level, so a team
	// that has not asked for it never sees it.
	ClassSlop Class = "slop"

	// ClassUnknown is where an unrecognized class lands.
	//
	// It is published at every level on purpose. severity.go makes the same
	// call for the same reason: an unexpected vocabulary should produce a
	// visible, non-gating finding rather than silently vanishing. Routing
	// unknowns to a filtered class instead would turn "the model wrote a word
	// we did not expect" into "a real defect disappeared", the exact failure
	// this codebase is built to avoid.
	ClassUnknown Class = "unknown"
)

// Classes returns every class, in schema order.
func Classes() []Class {
	return []Class{
		ClassCorrectness, ClassConcurrency, ClassSecurity, ClassResource,
		ClassDataLoss, ClassContract, ClassTests, ClassMaintainability, ClassStyle,
		ClassSlop,
	}
}

// ClassNames returns the classes a model may choose from.
//
// ClassUnknown is deliberately absent: it is the internal landing place for a
// value we did not recognize, not an option to offer.
func ClassNames() []string {
	all := Classes()
	out := make([]string, 0, len(all))
	for _, c := range all {
		out = append(out, string(c))
	}
	return out
}

// defectClasses are the problems every level reports: something demonstrably
// goes wrong at runtime.
var defectClasses = []Class{
	ClassCorrectness, ClassConcurrency, ClassSecurity, ClassResource, ClassDataLoss,
	// Unrecognized classes ride with the defects so they are never filtered
	// away unseen. min_severity still gates them.
	ClassUnknown,
}

// allowedClasses maps a nitpick level to the classes it publishes.
//
// The levels are nested, and that nesting is what makes post-hoc filtering
// sound. Each level is a superset of the one before it, so narrowing never
// needs a finding that was not generated.
var allowedClasses = map[NitpickLevel]map[Class]bool{
	NitpickOff:     classSet(defectClasses...),
	NitpickMinimal: classSet(append(append([]Class{}, defectClasses...), ClassContract)...),
	NitpickNormal:  classSet(append(append([]Class{}, defectClasses...), ClassContract, ClassTests, ClassMaintainability)...),
	// Pedantic is every class but slop: that one is published by
	// review.slop alone, so no level's set holds it.
	NitpickPedantic: classSet(append(levelClasses(), ClassUnknown)...),
}

// levelClasses is Classes without ClassSlop, the one class no nitpick level
// publishes.
func levelClasses() []Class {
	var out []Class
	for _, c := range Classes() {
		if c != ClassSlop {
			out = append(out, c)
		}
	}
	return out
}

func classSet(cs ...Class) map[Class]bool {
	out := make(map[Class]bool, len(cs))
	for _, c := range cs {
		out[c] = true
	}
	return out
}

// GenerationLevel is the scope every review is generated at, regardless of the
// configured nitpick level.
//
// Normal is the widest scope that does not ask for style. Everything narrower
// is reached by filtering; style is reached by a separate pass, so the defect
// hunt is never diluted by it.
const GenerationLevel = NitpickNormal

// Normalize maps a model-supplied class onto a known one, through a few
// aliases models reach for unprompted. An unrecognized value lands in
// ClassUnknown, with false: published at every level rather than hidden,
// and never silently promoted into the defect classes that drive gating.
func (c Class) Normalize() (Class, bool) {
	n := Class(strings.ToLower(strings.TrimSpace(string(c))))

	for _, known := range Classes() {
		if n == known {
			return n, true
		}
	}

	// A few aliases models reach for unprompted.
	switch n {
	case "bug", "logic", "correctness-bug":
		return ClassCorrectness, true
	case "race", "thread-safety", "threading":
		return ClassConcurrency, true
	case "vulnerability", "injection", "authz", "auth":
		return ClassSecurity, true
	case "leak", "resources", "resource-leak", "performance":
		return ClassResource, true
	case "api", "compatibility", "breaking-change":
		return ClassContract, true
	case "test", "testing", "coverage":
		return ClassTests, true
	case "naming", "docs", "documentation", "formatting", "idiom", "nit":
		return ClassStyle, true
	}

	return ClassUnknown, false
}

// Publishes reports whether a nitpick level publishes findings of this class.
func (level NitpickLevel) Publishes(c Class) bool {
	allowed, ok := allowedClasses[level]
	if !ok {
		allowed = allowedClasses[NitpickNormal]
	}

	// Normalize defensively. An unrecognized or empty class reaching here would
	// otherwise match nothing and silently drop the finding, turning a missing
	// field into a disappeared defect, the failure mode this whole codebase is
	// built to avoid.
	normalized, _ := c.Normalize()
	return allowed[normalized]
}

// NeedsStylePass reports whether a level requires findings the generation scope
// deliberately does not produce.
//
// Only pedantic does. Post-hoc filtering can narrow a corpus but never widen
// it, so style findings, which the generation prompt forbids, have to come
// from somewhere else.
func (level NitpickLevel) NeedsStylePass() bool {
	return level == NitpickPedantic
}

// ClassesValues reports what validation.classes accepts, for the generated
// reference.
//
// It exists because the generator reads a field's type to find a closed set,
// and this field is a []Class rather than a Class, so the type it saw was a
// slice and it printed the sentence without the ten names the loader rejects a
// bad value with.
func (v Validation) ClassesValues() []string { return ClassNames() }
