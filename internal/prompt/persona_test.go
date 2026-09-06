package prompt

import (
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/v2/internal/config"
)

func personaWith(mutate func(*config.Persona)) config.Persona {
	p := config.DefaultPersona()
	mutate(&p)
	return p.Resolve()
}

// TestNitpickDoesNotChangeTheGenerationPrompt is the core invariant of the
// post-hoc filtering design.
//
// Every review is generated at one scope and the configured level is applied
// afterwards as a class filter. That is what makes levels comparable — a
// difference between two levels is now the filter, not model variance — and it
// stops a narrower level from changing what the model was asked to look for.
func TestNitpickDoesNotChangeTheGenerationPrompt(t *testing.T) {
	want := Persona(personaWith(func(p *config.Persona) { p.Nitpick = config.NitpickNormal }))

	for _, level := range []config.NitpickLevel{
		config.NitpickOff, config.NitpickMinimal, config.NitpickNormal, config.NitpickPedantic,
	} {
		got := Persona(personaWith(func(p *config.Persona) { p.Nitpick = level }))
		if got != want {
			t.Errorf("nitpick=%s changed the generation prompt; levels must filter, not regenerate", level)
		}
	}
}

// TestGenerationScopeExcludesStyle pins the scope every review is generated at.
// Style must be absent, because including it measurably degraded the defect
// hunt — and because pedantic reaches style through its own pass instead.
func TestGenerationScopeExcludesStyle(t *testing.T) {
	text := Persona(config.DefaultPersona())

	for _, want := range []string{"naming preferences", "formatting", "import order"} {
		if !strings.Contains(strings.ToLower(text), want) {
			t.Errorf("generation scope should exclude %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "You may ALSO report naming") {
		t.Error("the generation prompt must not invite style commentary")
	}
	if !strings.Contains(text, "empty findings list") {
		t.Error("an empty result must remain an explicitly valid outcome")
	}
}

// TestStylePassIsSeparateAndNarrow checks the pedantic pass asks for style only
// and does not re-run the defect hunt.
func TestStylePassIsSeparateAndNarrow(t *testing.T) {
	text := StylePass(config.DefaultPersona())

	if !strings.Contains(text, "ONLY naming") {
		t.Errorf("style pass should be restricted to style:\n%s", text)
	}
	if !strings.Contains(text, "do not duplicate") {
		t.Error("style pass should tell the model another reviewer covers defects")
	}
	if !strings.Contains(text, `class`+"\"style\"") && !strings.Contains(text, "\"style\"") {
		t.Error("style pass should pin the class")
	}
	// It must still demand a stated cost, or it becomes a licence to pad.
	if !strings.Contains(text, "costs a future reader") {
		t.Error("style findings must still justify themselves")
	}
	// And it must remain droppable.
	if !strings.Contains(text, "empty findings list") {
		t.Error("an empty style pass must be acceptable")
	}
}

// TestVoiceAxesAreIndependent checks each wording axis changes the prompt on its
// own, so tone can be tuned without disturbing scope.
func TestVoiceAxesAreIndependent(t *testing.T) {
	base := Persona(config.DefaultPersona())

	cases := []struct {
		name   string
		mutate func(*config.Persona)
	}{
		{"verbosity", func(p *config.Persona) { p.Verbosity = config.VerbosityTerse }},
		{"politeness", func(p *config.Persona) { p.Politeness = config.PolitenessBlunt }},
		{"confidence", func(p *config.Persona) { p.Confidence = config.ConfidenceHedged }},
		{"address", func(p *config.Persona) { p.Address = config.AddressAuthor }},
		{"praise", func(p *config.Persona) { yes := true; p.Praise = &yes }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Persona(personaWith(tc.mutate))
			if got == base {
				t.Errorf("changing %s did not change the prompt", tc.name)
			}
		})
	}
}

// TestVoiceNeverLowersTheBar is the invariant the README advertises: tone
// controls WORDING only. If a voice setting could also relax what counts as
// reportable, the knob would silently become a quality knob, and two teams
// would get different reviews of the same code while believing they had only
// picked a different register.
//
// An earlier version of this test compared only the text BEFORE the voice
// section — a region no voice axis can write to. It passed with
// "Only report a finding if it is critical" injected into a voice branch, i.e.
// it tested a different, trivially-true property. It now inspects exactly the
// lines a voice setting ADDS.
func TestVoiceNeverLowersTheBar(t *testing.T) {
	// Phrases that would narrow what gets reported rather than how it is said.
	// A voice axis has no business emitting any of them.
	banned := []string{
		"only report", "skip everything", "do not report a finding",
		"at most", "ignore", "omit findings", "no more than",
		"limit yourself", "report fewer", "critical only",
	}

	baseLines := map[string]bool{}
	for _, l := range strings.Split(Persona(config.DefaultPersona()), "\n") {
		baseLines[l] = true
	}

	cases := []struct {
		name   string
		mutate func(*config.Persona)
	}{
		{"terse", func(p *config.Persona) { p.Verbosity = config.VerbosityTerse }},
		{"detailed", func(p *config.Persona) { p.Verbosity = config.VerbosityDetailed }},
		{"blunt", func(p *config.Persona) { p.Politeness = config.PolitenessBlunt }},
		{"warm", func(p *config.Persona) { p.Politeness = config.PolitenessWarm }},
		{"hedged", func(p *config.Persona) { p.Confidence = config.ConfidenceHedged }},
		{"author", func(p *config.Persona) { p.Address = config.AddressAuthor }},
		{"praise", func(p *config.Persona) { yes := true; p.Praise = &yes }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text := Persona(personaWith(tc.mutate))

			// The scope section must be byte-identical: a voice axis may not
			// touch it at all.
			gotScope, _, _ := strings.Cut(text, "\nWrite each finding like this:")
			wantScope, _, _ := strings.Cut(Persona(config.DefaultPersona()), "\nWrite each finding like this:")
			if gotScope != wantScope {
				t.Errorf("voice setting %s changed the scope section", tc.name)
			}

			// And every line this setting ADDS anywhere in the prompt must be
			// about wording, not about what to report.
			for _, line := range strings.Split(text, "\n") {
				if baseLines[line] || strings.TrimSpace(line) == "" {
					continue
				}
				lower := strings.ToLower(line)

				// Only lines that talk about findings can narrow what gets
				// reported. "at most once per review" limiting PRAISE is a
				// wording rule, not a scope rule.
				if !strings.Contains(lower, "finding") && !strings.Contains(lower, "report") &&
					!strings.Contains(lower, "defect") && !strings.Contains(lower, "issue") {
					continue
				}

				for _, phrase := range banned {
					if strings.Contains(lower, phrase) {
						t.Errorf("voice setting %s adds a line that narrows reporting scope: %q\n"+
							"(matched banned phrase %q)", tc.name, strings.TrimSpace(line), phrase)
					}
				}
			}
		})
	}
}

func TestCustomPersonaIsAppendedLast(t *testing.T) {
	text := Persona(personaWith(func(p *config.Persona) {
		p.Custom = "Sign off every review with a haiku."
	}))

	if !strings.Contains(text, "haiku") {
		t.Error("custom persona text should reach the prompt")
	}
	// It must not be able to lower the bar, only change the voice.
	if !strings.Contains(text, "never the reporting bar") {
		t.Error("custom text should be scoped to wording, not to what gets reported")
	}
	if strings.Index(text, "haiku") < strings.Index(text, "Write each finding") {
		t.Error("custom text should come last so it overrides the built-in wording guidance")
	}
}

func TestPersonaIsInsertedAsItsOwnLayer(t *testing.T) {
	p, err := Build(NameReview, Options{
		PersonaText: Persona(config.DefaultPersona()),
		Repository:  "REPO",
		Run:         "RUN",
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	out := p.String()
	base := strings.Index(out, "meticulous senior engineer")
	persona := strings.Index(out, "## Voice and scope")
	repo := strings.Index(out, "REPO")
	run := strings.Index(out, "RUN")

	if base < 0 || persona < 0 || repo < 0 || run < 0 {
		t.Fatalf("a layer is missing:\n%s", out)
	}
	// Persona narrows the base, and the repository can still override persona.
	if base >= persona || persona >= repo || repo >= run {
		t.Errorf("layer order wrong: base=%d persona=%d repo=%d run=%d", base, persona, repo, run)
	}

	if !strings.Contains(p.Explain(), "layer: "+LayerPersona) {
		t.Error("explain should label the persona layer")
	}
}

func TestDescribeCoversEveryAxis(t *testing.T) {
	got := Describe(config.DefaultPersona())
	for _, axis := range []string{"verbosity", "politeness", "confidence", "nitpick", "address", "emoji", "praise"} {
		if !strings.Contains(got, axis) {
			t.Errorf("Describe should mention %s, got %q", axis, got)
		}
	}
}
