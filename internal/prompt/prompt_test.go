package prompt

import (
	"strings"
	"testing"
)

func TestBuildIncludesTheBuiltInPrompt(t *testing.T) {
	p, err := Build(NameReview, Options{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	out := p.String()
	if !strings.Contains(out, "senior engineer") {
		t.Errorf("review prompt should be embedded:\n%s", out)
	}
	// The discipline that keeps output useful must survive.
	if !strings.Contains(out, "Reporting nothing is a valid") {
		t.Error("review prompt should permit an empty result")
	}
}

func TestBuildTriagePrompt(t *testing.T) {
	p, err := Build(NameTriage, Options{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(p.String(), "triaging findings") {
		t.Error("triage prompt should be embedded")
	}
}

func TestUnknownPromptIsAnError(t *testing.T) {
	if _, err := Build("nope", Options{}); err == nil {
		t.Fatal("want an error for an unknown prompt name")
	}
}

func TestLayersAreOrderedBaseThenRepoThenRun(t *testing.T) {
	// Models weight later instructions most heavily, so a per-run instruction
	// must come last or it cannot override anything.
	p, err := Build(NameReview, Options{
		Repository: "REPO_LAYER",
		Run:        "RUN_LAYER",
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	out := p.String()
	base := strings.Index(out, "senior engineer")
	repo := strings.Index(out, "REPO_LAYER")
	run := strings.Index(out, "RUN_LAYER")

	if base < 0 || repo < 0 || run < 0 {
		t.Fatalf("a layer is missing:\n%s", out)
	}
	if base >= repo || repo >= run {
		t.Errorf("layers out of order: base=%d repo=%d run=%d", base, repo, run)
	}
}

func TestEmptyLayersAreOmitted(t *testing.T) {
	p, err := Build(NameReview, Options{Repository: "   ", Run: ""})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if strings.Contains(p.String(), "Repository instructions") {
		t.Error("a blank layer should not produce an empty heading")
	}
}

func TestOverrideReplacesTheBasePrompt(t *testing.T) {
	p, err := Build(NameReview, Options{Override: "ONLY THIS"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	out := p.String()
	if strings.Contains(out, "senior engineer") {
		t.Error("an override should replace the built-in prompt, not append to it")
	}
	if !strings.Contains(out, "ONLY THIS") {
		t.Error("the override text should be used")
	}
}

func TestExplainLabelsEachLayer(t *testing.T) {
	// Being able to read the exact prompt is what makes tuning possible
	// without spending tokens to discover what was sent.
	p, err := Build(NameReview, Options{Repository: "REPO", Run: "RUN"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	out := p.Explain()
	for _, want := range []string{LayerBase, LayerRepo, LayerRun} {
		if !strings.Contains(out, "layer: "+want) {
			t.Errorf("Explain should label the %s layer:\n%s", want, out)
		}
	}
}

func TestTemplateDataIsRendered(t *testing.T) {
	p, err := Build(NameReview, Options{
		Repository: "Project is {{.Name}}.",
		Data:       struct{ Name string }{Name: "widgets"},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(p.String(), "Project is widgets.") {
		t.Errorf("template data should be rendered:\n%s", p.String())
	}
}

func TestMissingTemplateKeyIsAnError(t *testing.T) {
	// A silently empty instruction is worse than a loud failure: the review
	// would run with a prompt the author did not intend.
	_, err := Build(NameReview, Options{
		Repository: "Project is {{.Missing}}.",
		Data:       struct{ Name string }{Name: "widgets"},
	})
	if err == nil {
		t.Fatal("want an error for a missing template key")
	}
}

func TestMalformedTemplateIsAnError(t *testing.T) {
	if _, err := Build(NameReview, Options{Repository: "{{ broken"}); err == nil {
		t.Fatal("want an error for a malformed template")
	}
}
