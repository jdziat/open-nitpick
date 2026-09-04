// Package prompt renders the prompts that drive a review.
//
// Prompts are embedded in the binary so a release is self-contained, and are
// layered rather than replaced: a repository adds instructions without losing
// the base reviewing discipline, unless it deliberately overrides it.
package prompt

import (
	"embed"
	"fmt"
	"strings"
	"text/template"
)

//go:embed templates/*.md
var templates embed.FS

// Names of the built-in prompts.
const (
	NameReview = "review"
	NameTriage = "triage"
)

// Layer names, in the order they are applied. Later layers appear later in the
// prompt, where models weight instructions most heavily.
const (
	// LayerBase is the built-in reviewing discipline.
	LayerBase = "base"
	// LayerRepo is the repository's own instruction text.
	LayerRepo = "repository"
	// LayerRun is a per-invocation instruction from the command line.
	LayerRun = "run"
)

// Layer is one contribution to a prompt.
type Layer struct {
	Name string
	Text string
}

// Prompt is a rendered, inspectable prompt.
type Prompt struct {
	Layers []Layer
}

// String concatenates the layers into the text sent to the model.
func (p Prompt) String() string {
	var b strings.Builder

	for i, l := range p.Layers {
		if strings.TrimSpace(l.Text) == "" {
			continue
		}
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(strings.TrimRight(l.Text, "\n"))
		b.WriteString("\n")
	}

	return b.String()
}

// Explain renders the prompt with its layers labeled, for `nitpick
// explain-config`. Being able to read the exact prompt without spending tokens
// is what makes prompt tuning practical.
func (p Prompt) Explain() string {
	var b strings.Builder

	for _, l := range p.Layers {
		if strings.TrimSpace(l.Text) == "" {
			continue
		}
		fmt.Fprintf(&b, "----- layer: %s -----\n", l.Name)
		b.WriteString(strings.TrimRight(l.Text, "\n"))
		b.WriteString("\n\n")
	}

	return b.String()
}

// Options configure prompt construction.
type Options struct {
	// Override replaces the built-in base prompt entirely. Empty keeps it.
	Override string

	// Repository is instruction text from .nitpick.yaml that applies to the
	// whole review rather than to a single path.
	Repository string

	// Run is a per-invocation instruction, typically a CLI flag.
	Run string

	// PersonaText is rendered voice-and-scope guidance, inserted after the base
	// prompt so it can narrow scope, and before repository instructions so a
	// repository can still override it.
	PersonaText string

	// ModelText is guidance addressed to the model family doing the review,
	// from ModelGuidance. It follows the persona and precedes the repository's
	// instructions for the same reason the persona does.
	ModelText string

	// Data is exposed to the template as `.`.
	Data any
}

// Build renders a named built-in prompt with its layers applied.
func Build(name string, opts Options) (Prompt, error) {
	base := opts.Override

	if base == "" {
		raw, err := templates.ReadFile("templates/" + name + ".md")
		if err != nil {
			return Prompt{}, fmt.Errorf("prompt %q: %w", name, err)
		}
		base = string(raw)
	}

	rendered, err := render(name, base, opts.Data)
	if err != nil {
		return Prompt{}, err
	}

	p := Prompt{Layers: []Layer{{Name: LayerBase, Text: rendered}}}

	if strings.TrimSpace(opts.PersonaText) != "" {
		p.Layers = append(p.Layers, Layer{Name: LayerPersona, Text: opts.PersonaText})
	}

	if strings.TrimSpace(opts.ModelText) != "" {
		p.Layers = append(p.Layers, Layer{Name: LayerModel, Text: opts.ModelText})
	}

	if strings.TrimSpace(opts.Repository) != "" {
		text, err := render(name+":repository", opts.Repository, opts.Data)
		if err != nil {
			return Prompt{}, err
		}
		p.Layers = append(p.Layers, Layer{
			Name: LayerRepo,
			Text: "## Repository instructions\n\n" + text,
		})
	}

	if strings.TrimSpace(opts.Run) != "" {
		p.Layers = append(p.Layers, Layer{
			Name: LayerRun,
			Text: "## Instructions for this run\n\n" + opts.Run,
		})
	}

	return p, nil
}

// render executes a template. Templates are authored by the repository owner,
// who can already run arbitrary code in CI, so this is a convenience rather
// than a trust boundary — but missing keys are still an error, because a
// silently empty instruction is worse than a loud failure.
func render(name, text string, data any) (string, error) {
	tmpl, err := template.New(name).Option("missingkey=error").Parse(text)
	if err != nil {
		return "", fmt.Errorf("parse prompt %q: %w", name, err)
	}

	var b strings.Builder
	if err := tmpl.Execute(&b, data); err != nil {
		return "", fmt.Errorf("render prompt %q: %w", name, err)
	}
	return b.String(), nil
}
