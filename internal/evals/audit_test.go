package evals

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jdziat/open-nitpick/internal/llm"
)

func TestQualityVariantsPreserveInputsWithoutBuyingOtherModes(t *testing.T) {
	variants, err := qualityVariants("+default, +ctx")
	if err != nil || len(variants) != 2 {
		t.Fatalf("production variants: %+v, %v", variants, err)
	}
	if variants[0].suffix != " +default" || variants[0].context != [2]bool{true, false} ||
		variants[1].suffix != " +ctx" || variants[1].context != [2]bool{true, true} {
		t.Fatalf("selecting variants changed the inputs: %+v", variants)
	}
	all, err := qualityVariants("")
	if err != nil || len(all) != 3 || all[0].suffix != "" || all[0].context != [2]bool{false, false} {
		t.Fatalf("original diff-only mode was lost: %+v, %v", all, err)
	}
	for _, invalid := range []string{"+default,+default", "+default,", "default", "+lookup"} {
		if _, err := qualityVariants(invalid); err == nil {
			t.Errorf("ambiguous selection %q accepted", invalid)
		}
	}
}

func TestQualityPurchasesRequireExplicitModelCacheAndBudget(t *testing.T) {
	t.Setenv(EnvModels, "test/model")
	t.Setenv(llm.EnvResponseCache, t.TempDir())
	t.Setenv(llm.EnvPurchaseBudget, "budget.json")
	t.Setenv(llm.EnvReplayOnly, "")
	if err := requireQualityPurchaseSetup(); err != nil {
		t.Fatalf("explicit purchase setup rejected: %v", err)
	}
	for _, missing := range []string{EnvModels, llm.EnvResponseCache, llm.EnvPurchaseBudget} {
		t.Run(missing, func(t *testing.T) {
			t.Setenv(missing, "")
			if err := requireQualityPurchaseSetup(); err == nil {
				t.Fatalf("unbounded purchase permitted without %s", missing)
			}
		})
	}
	t.Setenv(llm.EnvReplayOnly, "1")
	t.Setenv(llm.EnvPurchaseBudget, "")
	if err := requireQualityPurchaseSetup(); err != nil {
		t.Fatalf("offline replay unnecessarily requires purchase budget: %v", err)
	}
}

func TestOfflineAuditCountsMissingSamplesAndFailsClosed(t *testing.T) {
	f := Fixtures()[0]
	manifest := ExperimentManifest{Version: 1, Models: []Model{{ID: "test"}}, Runs: 2,
		FixtureHashes: map[string]string{f.Name: fixtureFingerprint(f)}, Variants: map[string][2]bool{"": {false, false}}}
	records := []DumpRecord{{Model: "test", Fixture: f.Name, FixtureHash: fixtureFingerprint(f), Run: 0, Index: -1, Silent: true}}
	cells, err := RebuildQuality(manifest, records, []Fixture{f})
	if err == nil || len(cells) != 1 || cells[0].Scheduled != 2 || cells[0].Failed != 1 ||
		cells[0].Plants != 2*len(PersonaDefects(f)) || cells[0].Recall != 0 {
		t.Fatalf("missing attempt inflated score: cells=%+v err=%v", cells, err)
	}
}

func TestOfflineAuditRejectsChangedFixturesAndUnprovedSilence(t *testing.T) {
	f := Fixtures()[0]
	manifest := ExperimentManifest{Version: 1, Models: []Model{{ID: "test"}}, Runs: 1,
		FixtureHashes: map[string]string{f.Name: fixtureFingerprint(f)}, Variants: map[string][2]bool{"": {false, false}}}
	record := DumpRecord{Model: "test", Fixture: f.Name, FixtureHash: fixtureFingerprint(f), Index: -1}
	if _, err := RebuildQuality(manifest, []DumpRecord{record}, []Fixture{f}); err == nil {
		t.Fatal("no findings and no execution evidence passed as silence")
	}
	record.Silent = true
	record.FixtureHash = "different"
	if _, err := RebuildQuality(manifest, []DumpRecord{record}, []Fixture{f}); err == nil {
		t.Fatal("changed fixture was credited")
	}
}

func TestManifestRetainsStableInputsAndRefusesOverwrite(t *testing.T) {
	path := t.TempDir() + "/manifest.json"
	opts := Options{Models: []Model{{ID: "test"}}, Fixtures: Fixtures()[:1], Runs: 2}
	variants := map[string][2]bool{"": {false, false}}
	if err := WriteExperimentManifest(path, opts, variants, 100, TypeReview, "tuning", nil); err != nil {
		t.Fatal(err)
	}
	if err := WriteExperimentManifest(path, opts, variants, 100, TypeReview, "tuning", nil); err == nil {
		t.Fatal("existing registration was overwritten")
	}
}

func TestManifestReadsVersionOneFixture(t *testing.T) {
	manifest, err := ReadExperimentManifest("testdata/manifest-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Version != 1 || manifest.Type != TypeReview || manifest.HashRule != HashRuleAllMarkdown || manifest.Corpus != "" {
		t.Fatalf("version 1 did not normalise to a review under hash rule 1: %+v", manifest)
	}
	if manifest.Runs != 2 || manifest.SampleOffset != 100 || len(manifest.FixtureHashes) != 1 {
		t.Fatalf("version 1 inputs were not preserved: %+v", manifest)
	}
}

func TestManifestRejectsUnknownVersionTypeAndHashRule(t *testing.T) {
	for name, body := range map[string]string{
		"version 3":       `{"version":3,"type":"review","hash_rule":2}`,
		"version 0":       `{"runs":1}`,
		"unknown type":    `{"version":2,"type":"respond","hash_rule":2}`,
		"missing type":    `{"version":2,"hash_rule":2}`,
		"v2 old hashrule": `{"version":2,"type":"review","hash_rule":1}`,
		"not json":        `{`,
	} {
		path := t.TempDir() + "/m.json"
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadExperimentManifest(path); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestWriterRefusesUnknownTypeAndRoundTripsVersionTwo(t *testing.T) {
	opts := Options{Models: []Model{{ID: "test"}}, Fixtures: Fixtures()[:1], Runs: 1}
	variants := map[string][2]bool{"": {false, false}}
	if err := WriteExperimentManifest(t.TempDir()+"/m.json", opts, variants, 0, "respond", "x", nil); err == nil {
		t.Fatal("an out-of-scope type was registered")
	}
	path := t.TempDir() + "/m.json"
	if err := WriteExperimentManifest(path, opts, variants, 7, TypeSecurity, "security-tuning", []string{StageValidation}); err != nil {
		t.Fatal(err)
	}
	got, err := ReadExperimentManifest(path)
	if err != nil {
		t.Fatalf("the reader refuses what the writer wrote: %v", err)
	}
	if got.Version != 2 || got.Type != TypeSecurity || got.Corpus != "security-tuning" ||
		got.HashRule != HashRuleTemplatesOnly || len(got.Stages) != 1 || got.SampleOffset != 7 {
		t.Fatalf("round trip lost registration: %+v", got)
	}
}

func TestRebuildQualityAcceptsBothManifestVersionsAndCarriesType(t *testing.T) {
	f := Fixtures()[0]
	records := []DumpRecord{{Model: "test", Fixture: f.Name, FixtureHash: fixtureFingerprint(f), Index: -1, Silent: true}}
	for _, version := range []int{1, 2} {
		manifest := ExperimentManifest{Version: version, Type: TypeReview, Models: []Model{{ID: "test"}}, Runs: 1,
			FixtureHashes: map[string]string{f.Name: fixtureFingerprint(f)}, Variants: map[string][2]bool{"": {false, false}}}
		cells, err := RebuildQuality(manifest, records, []Fixture{f})
		if err != nil || len(cells) != 1 || cells[0].Type != TypeReview {
			t.Fatalf("version %d: cells=%+v err=%v", version, cells, err)
		}
	}
	bad := ExperimentManifest{Version: 3, Models: []Model{{ID: "test"}}, Runs: 1,
		FixtureHashes: map[string]string{f.Name: fixtureFingerprint(f)}, Variants: map[string][2]bool{"": {false, false}}}
	if _, err := RebuildQuality(bad, records, []Fixture{f}); err == nil {
		t.Fatal("an unknown manifest version was rebuilt")
	}
}

func TestSourceHashRuleTwoIgnoresDocumentationButNotTemplates(t *testing.T) {
	root := "/repo"
	for path, want := range map[string]bool{
		root + "/internal/review/engine.go":               true,
		root + "/internal/review/README.md":               false,
		root + "/cmd/nitpick/NOTES.md":                    false,
		root + "/internal/prompt/templates/review.md":     true,
		root + "/internal/prompt/templates/sub/triage.md": true,
		root + "/internal/prompt/other/review.md":         false,
		root + "/internal/prompt/templates/image.png":     false,
	} {
		if got := hashedUnderRuleTwo(root, path); got != want {
			t.Errorf("hashedUnderRuleTwo(%s) = %v, want %v", path, got, want)
		}
	}
}

func TestSourceHashRuleTwoIsStableAcrossDocumentationEditsButNotTemplates(t *testing.T) {
	root := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module x\n")
	write("go.sum", "")
	write("internal/a/a.go", "package a\n")
	write("internal/a/README.md", "one")
	write("internal/prompt/templates/review.md", "template one")
	write("cmd/b/b.go", "package b\n")
	base, err := sourceHashUnderRuleTwo(root)
	if err != nil {
		t.Fatal(err)
	}
	write("internal/a/README.md", "edited documentation")
	if got, _ := sourceHashUnderRuleTwo(root); got != base {
		t.Fatal("a documentation edit changed the rule 2 source hash")
	}
	write("internal/prompt/templates/review.md", "template two")
	if got, _ := sourceHashUnderRuleTwo(root); got == base {
		t.Fatal("a prompt template edit did not change the source hash")
	}
	write("internal/prompt/templates/review.md", "template one")
	write("internal/a/a.go", "package a // edited\n")
	if got, _ := sourceHashUnderRuleTwo(root); got == base {
		t.Fatal("a Go source edit did not change the source hash")
	}
}
