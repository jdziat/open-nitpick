package config

import "testing"

// TestLevelsAreNested is what makes post-hoc filtering sound.
//
// Each level must be a superset of the one before it. If they were not nested,
// narrowing could require a finding the generation pass never produced, and a
// filter can only ever remove.
func TestLevelsAreNested(t *testing.T) {
	order := []NitpickLevel{NitpickOff, NitpickMinimal, NitpickNormal, NitpickPedantic}

	for i := 1; i < len(order); i++ {
		narrower, wider := order[i-1], order[i]
		for _, c := range Classes() {
			if narrower.Publishes(c) && !wider.Publishes(c) {
				t.Errorf("%s publishes %s but %s does not; levels must nest", narrower, c, wider)
			}
		}
	}
}

// TestOnlyPedanticNeedsAPass pins which level cannot be served by filtering.
func TestOnlyPedanticNeedsAPass(t *testing.T) {
	for _, level := range []NitpickLevel{NitpickOff, NitpickMinimal, NitpickNormal} {
		if level.NeedsStylePass() {
			t.Errorf("%s should be reachable by filtering alone", level)
		}
		if level.Publishes(ClassStyle) {
			t.Errorf("%s must not publish style", level)
		}
	}

	if !NitpickPedantic.NeedsStylePass() {
		t.Error("pedantic wants style, which the generation scope does not produce")
	}
	if !NitpickPedantic.Publishes(ClassStyle) {
		t.Error("pedantic should publish style")
	}
}

// TestDefectClassesSurviveEveryLevel: no configuration may silence a real
// defect. Nitpick tunes taste, never safety.
func TestDefectClassesSurviveEveryLevel(t *testing.T) {
	for _, level := range []NitpickLevel{NitpickOff, NitpickMinimal, NitpickNormal, NitpickPedantic} {
		for _, c := range []Class{ClassCorrectness, ClassConcurrency, ClassSecurity, ClassResource, ClassDataLoss} {
			if !level.Publishes(c) {
				t.Errorf("%s drops %s; no level may silence a real defect", level, c)
			}
		}
	}
}

func TestClassNormalize(t *testing.T) {
	cases := map[string]Class{
		"security": ClassSecurity, "SECURITY": ClassSecurity,
		"race": ClassConcurrency, "injection": ClassSecurity,
		"leak": ClassResource, "api": ClassContract,
		"naming": ClassStyle, "docs": ClassStyle,
		// Unknown values land in ClassUnknown, which every level publishes.
		// Routing them to a filtered class would turn an unexpected word into a
		// disappeared defect; min_severity still gates them.
		"vibes": ClassUnknown, "": ClassUnknown,
	}
	for in, want := range cases {
		got, _ := Class(in).Normalize()
		if got != want {
			t.Errorf("Class(%q).Normalize() = %q, want %q", in, got, want)
		}
	}

	if _, ok := Class("vibes").Normalize(); ok {
		t.Error("an unknown class should report that it was not recognized")
	}
}

// TestUnknownClassIsNeverFiltered is the regression test for a defect
// disappearing because the model wrote a word we did not expect.
func TestUnknownClassIsNeverFiltered(t *testing.T) {
	for _, level := range []NitpickLevel{NitpickOff, NitpickMinimal, NitpickNormal, NitpickPedantic} {
		if !level.Publishes(ClassUnknown) {
			t.Errorf("%s drops unrecognized classes; they must stay visible", level)
		}
		// And an arbitrary unrecognized string must survive too, since that is
		// how it arrives.
		if !level.Publishes(Class("wat")) {
			t.Errorf("%s drops an unrecognized class string", level)
		}
	}
}

// TestUnknownIsNotOfferedToTheModel: it is an internal fallback, not a choice.
func TestUnknownIsNotOfferedToTheModel(t *testing.T) {
	for _, n := range ClassNames() {
		if n == string(ClassUnknown) {
			t.Error("ClassUnknown must not appear in the schema enum")
		}
	}
}

func TestGenerationLevelIsNormal(t *testing.T) {
	// Widest scope that excludes style: style degraded the defect hunt in every
	// measured run, and pedantic reaches it through a separate pass.
	if GenerationLevel != NitpickNormal {
		t.Errorf("GenerationLevel = %q, want normal", GenerationLevel)
	}
	if GenerationLevel.Publishes(ClassStyle) {
		t.Error("the generation level must not include style")
	}
}

func TestNoNitpickLevelPublishesSlop(t *testing.T) {
	for _, level := range []NitpickLevel{NitpickOff, NitpickMinimal, NitpickNormal, NitpickPedantic} {
		if level.Publishes(ClassSlop) {
			t.Errorf("level %s publishes slop; review.slop alone may", level)
		}
	}
	if !NitpickPedantic.Publishes(ClassStyle) {
		t.Errorf("pedantic no longer publishes style")
	}
}
