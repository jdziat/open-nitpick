package config

import (
	"fmt"
	"strings"
)

// Persona controls how the reviewer talks and how much it chooses to say.
//
// These are separated from Review deliberately. Everything in Review changes
// WHAT gets reported, budgets, gates, which files. Everything here changes
// how the same finding is WORDED and whether marginal observations are worth
// raising at all. Teams disagree strongly about the second, and the disagreement
// is about taste rather than correctness, so it belongs in configuration rather
// than in the prompt's fixed text.
type Persona struct {
	// Verbosity controls how much prose accompanies each finding.
	Verbosity Verbosity `yaml:"verbosity"`

	// Politeness controls softening language and acknowledgement.
	Politeness Politeness `yaml:"politeness"`

	// Confidence controls hedging. Hedged wording is honest about uncertainty
	// but reads as noise when overused; direct wording is scannable but claims
	// more than the model always knows.
	Confidence Confidence `yaml:"confidence"`

	// Nitpick sets how far beyond outright defects the reviewer ranges.
	Nitpick NitpickLevel `yaml:"nitpick"`

	// Emoji prefixes severities with a coloured marker.
	Emoji *bool `yaml:"emoji"`

	// Address selects second person ("you dropped the error") or impersonal
	// ("the error is dropped"). Impersonal is the safer default: review
	// comments are read by the author, and "you" reads as blame to some people.
	Address Address `yaml:"address"`

	// Praise permits acknowledging good work. Off by default because
	// a bot that compliments everything trains readers to skim.
	Praise *bool `yaml:"praise"`

	// Persona is a free-text overlay appended last, for teams that want a
	// house voice the built-in axes do not cover.
	Custom string `yaml:"custom"`
}

// Verbosity levels.
type Verbosity string

// Supported verbosity levels.
const (
	// VerbosityTerse is one line of rationale, no restatement.
	VerbosityTerse Verbosity = "terse"
	// VerbosityNormal is one to three sentences.
	VerbosityNormal Verbosity = "normal"
	// VerbosityDetailed adds the reasoning chain and an alternative.
	VerbosityDetailed Verbosity = "detailed"
)

// Politeness levels.
type Politeness string

// Supported politeness levels.
const (
	// PolitenessBlunt states the defect with no softening at all.
	PolitenessBlunt Politeness = "blunt"
	// PolitenessNeutral is plain professional register.
	PolitenessNeutral Politeness = "neutral"
	// PolitenessWarm acknowledges intent and frames findings as suggestions.
	PolitenessWarm Politeness = "warm"
)

// Confidence levels.
type Confidence string

// Supported confidence levels.
const (
	// ConfidenceDirect asserts findings plainly.
	ConfidenceDirect Confidence = "direct"
	// ConfidenceHedged marks uncertainty explicitly.
	ConfidenceHedged Confidence = "hedged"
)

// Address forms.
type Address string

// Supported address forms.
const (
	// AddressImpersonal describes the code.
	AddressImpersonal Address = "impersonal"
	// AddressAuthor speaks to the author directly.
	AddressAuthor Address = "author"
)

// NitpickLevel sets how far past outright defects the reviewer ranges.
//
// This is the single most contested setting in a review tool. It selects which
// CLASSES of finding get published, where min_severity selects how serious they
// must be, different questions, applied independently.
//
// It is a post-hoc filter, not a change to the prompt. Every review is
// generated at one fixed scope and narrowed afterwards, which keeps levels
// comparable (a difference between two levels is the filter, not model
// variance) and stops a wider setting from diluting the defect hunt. See
// config.GenerationLevel.
type NitpickLevel string

// Supported nitpick levels.
const (
	// NitpickOff reports only defects with a demonstrable failure: correctness,
	// concurrency, security, resource handling, data loss.
	NitpickOff NitpickLevel = "off"

	// NitpickMinimal adds contract and compatibility breakage.
	NitpickMinimal NitpickLevel = "minimal"

	// NitpickNormal adds missing tests for risky logic and maintainability
	// problems whose cost can be named concretely.
	NitpickNormal NitpickLevel = "normal"

	// NitpickPedantic additionally permits naming, documentation, idiom, and
	// consistency observations, the things every other level forbids.
	NitpickPedantic NitpickLevel = "pedantic"
)

// DefaultPersona is the built-in voice: plain, impersonal, non-hedging, and
// scoped to defects that matter. It is what a good human reviewer sounds like
// on a busy day.
func DefaultPersona() Persona {
	no, yes := false, true

	return Persona{
		Verbosity:  VerbosityNormal,
		Politeness: PolitenessNeutral,
		Confidence: ConfidenceDirect,
		Nitpick:    NitpickNormal,
		Address:    AddressImpersonal,
		Emoji:      &yes,
		Praise:     &no,
	}
}

// EmojiEnabled reports whether severity markers are rendered.
func (p Persona) EmojiEnabled() bool { return p.Emoji == nil || *p.Emoji }

// PraiseEnabled reports whether the reviewer may compliment.
func (p Persona) PraiseEnabled() bool { return p.Praise != nil && *p.Praise }

// Resolve fills unset fields from the defaults, so a config naming one axis
// does not blank the others.
func (p Persona) Resolve() Persona {
	d := DefaultPersona()

	if p.Verbosity == "" {
		p.Verbosity = d.Verbosity
	}
	if p.Politeness == "" {
		p.Politeness = d.Politeness
	}
	if p.Confidence == "" {
		p.Confidence = d.Confidence
	}
	if p.Nitpick == "" {
		p.Nitpick = d.Nitpick
	}
	if p.Address == "" {
		p.Address = d.Address
	}
	if p.Emoji == nil {
		p.Emoji = d.Emoji
	}
	if p.Praise == nil {
		p.Praise = d.Praise
	}

	return p
}

// validate checks the persona's enumerated fields.
func (p Persona) validate() []error {
	var errs []error

	check := func(field, value string, allowed ...string) {
		if value == "" {
			return
		}
		for _, a := range allowed {
			if value == a {
				return
			}
		}
		errs = append(errs, fmt.Errorf("persona.%s %q is not one of: %s",
			field, value, strings.Join(allowed, ", ")))
	}

	check("verbosity", string(p.Verbosity), "terse", "normal", "detailed")
	check("politeness", string(p.Politeness), "blunt", "neutral", "warm")
	check("confidence", string(p.Confidence), "direct", "hedged")
	check("nitpick", string(p.Nitpick), "off", "minimal", "normal", "pedantic")
	check("address", string(p.Address), "impersonal", "author")

	if len(p.Custom) > 2000 {
		errs = append(errs, fmt.Errorf("persona.custom is %d characters; keep it under 2000", len(p.Custom)))
	}

	return errs
}
