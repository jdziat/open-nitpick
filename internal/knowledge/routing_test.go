package knowledge

import (
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
)

func entry(id string, langs []string, classes ...config.Class) Entry {
	return Entry{ID: id, Title: id, Body: id, Languages: langs, Classes: classes}
}

// The failure the language cut exists to prevent, restated because generic
// entries are a new way to lose it: "a value shared when it looked copied"
// describes Python's mutable default and Go's slice aliasing alike, and the
// two sit close together in any embedding.
func TestALanguageRuleDoesNotCrossLanguages(t *testing.T) {
	corpus := []Entry{
		entry("py", []string{"python"}, config.ClassCorrectness),
		entry("go", []string{"go"}, config.ClassCorrectness),
	}

	got := ForLanguages(corpus, map[string]bool{"go": true})
	if len(got) != 1 || got[0].ID != "go" {
		t.Fatalf("a Go change retrieved %v, want only the Go entry", ids(got))
	}
}

// A rule that really does cross languages has to say so, and then it survives
// whichever language the change is in.
func TestAGenericEntryReachesEveryKnownLanguage(t *testing.T) {
	corpus := []Entry{
		entry("generic", []string{AnyLanguage}, config.ClassSecurity),
		entry("go", []string{"go"}, config.ClassSecurity),
	}

	for _, lang := range []string{"go", "python", "rust"} {
		got := ForLanguages(corpus, map[string]bool{lang: true})
		if !hasID(ids(got), "generic") {
			t.Errorf("a %s change did not retrieve the generic entry: %v", lang, ids(got))
		}
	}

	// And a change of files nobody can name still retrieves nothing. An
	// unknown extension contributes no language, and generic entries must not
	// turn that silence into a corpus dump.
	if got := ForLanguages(corpus, map[string]bool{}); len(got) != 0 {
		t.Errorf("a change of unknown files retrieved %v, want nothing", ids(got))
	}
}

// A mixed-language change gets each language's entries and no others.
func TestAMixedChangeGetsBothLanguagesAndNoThird(t *testing.T) {
	corpus := []Entry{
		entry("go", []string{"go"}, config.ClassCorrectness),
		entry("ts", []string{"typescript"}, config.ClassCorrectness),
		entry("rs", []string{"rust"}, config.ClassCorrectness),
	}

	got := ids(ForLanguages(corpus, map[string]bool{"go": true, "typescript": true}))
	if len(got) != 2 || !hasID(got, "go") || !hasID(got, "ts") {
		t.Errorf("a Go plus TypeScript change retrieved %v, want the go and ts entries", got)
	}
}

// A file implementing another language's parser is still the language it is
// written in. languageOf reads the extension, so a Go parser for TypeScript
// retrieves Go rules, which is the answer that matches what the reviewer sees.
func TestLanguageComesFromTheExtensionNotTheSubject(t *testing.T) {
	if got := LanguagesOf([]string{"internal/parser/typescript.go"}); !got["go"] || got["typescript"] {
		t.Errorf("typescript.go resolved to %v, want go alone", got)
	}
}

// An unknown extension names no language, so it retrieves nothing rather than
// everything.
func TestAnUnknownExtensionNamesNoLanguage(t *testing.T) {
	if got := LanguagesOf([]string{"vendor/thing.zig", "CHANGELOG"}); len(got) != 0 {
		t.Errorf("unknown files resolved to %v, want nothing", got)
	}
}

// The class cut is a cut, not a ranking signal. A style rule the defect pass
// cannot publish is a wrong answer, and leaving it in the pool lets the
// closest wrong answer displace a right one.
func TestTheClassCutKeepsOnlyWhatThePassPublishes(t *testing.T) {
	corpus := []Entry{
		entry("bug", []string{"go"}, config.ClassCorrectness),
		entry("naming", []string{"go"}, config.ClassStyle),
		entry("slop", []string{"go"}, config.ClassSlop),
		entry("both", []string{"go"}, config.ClassStyle, config.ClassMaintainability),
	}

	defect := ids(ForClasses(corpus, map[config.Class]bool{
		config.ClassCorrectness: true, config.ClassMaintainability: true,
	}))
	if hasID(defect, "naming") && !hasID(defect, "both") {
		t.Errorf("the defect pass took a pure style entry: %v", defect)
	}
	if !hasID(defect, "bug") {
		t.Errorf("the defect pass lost the correctness entry: %v", defect)
	}
	if hasID(defect, "slop") {
		t.Errorf("the defect pass took a slop entry with slop off: %v", defect)
	}

	style := ids(ForClasses(corpus, map[config.Class]bool{
		config.ClassStyle: true, config.ClassMaintainability: true,
	}))
	if !hasID(style, "naming") || hasID(style, "bug") {
		t.Errorf("the style pass took %v, want the style entries and not the bug", style)
	}

	if got := ForClasses(corpus, nil); len(got) != 0 {
		t.Errorf("no allowed classes retrieved %v, want nothing", ids(got))
	}
}

// Every shipped entry names a class, or nothing routes it and nothing
// retrieves it.
func TestEveryShippedEntryIsRoutable(t *testing.T) {
	entries, err := Corpus()
	if err != nil {
		t.Fatalf("Corpus: %v", err)
	}
	known := map[config.Class]bool{}
	for _, c := range config.Classes() {
		known[c] = true
	}
	for _, e := range entries {
		if len(e.Classes) == 0 {
			t.Errorf("%s names no class", e.ID)
		}
		for _, c := range e.Classes {
			if !known[c] {
				t.Errorf("%s names class %q, which is not in the taxonomy", e.ID, c)
			}
		}
	}
}

func ids(entries []Entry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.ID)
	}
	return out
}

func hasID(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}

// An entry retrieved for four files is one entry. Repeating it would spend
// four of five slots on one fact and teach the model the section is
// boilerplate.
func TestMergeKeepsOneRowPerEntryAtItsBestScore(t *testing.T) {
	a := Hit{Entry: Entry{ID: "a"}, Score: 0.4, Path: "one.go"}
	aAgain := Hit{Entry: Entry{ID: "a"}, Score: 0.9, Path: "two.go"}
	b := Hit{Entry: Entry{ID: "b"}, Score: 0.5, Path: "one.go"}

	got := Merge([][]Hit{{a, b}, {aAgain}}, 5)
	if len(got) != 2 {
		t.Fatalf("merge returned %d hits, want 2", len(got))
	}
	if got[0].Entry.ID != "a" || got[0].Score != 0.9 {
		t.Errorf("best hit = %s at %.2f, want a at 0.90", got[0].Entry.ID, got[0].Score)
	}
	// Attribution survives, or a per-file query bought nothing.
	if got[0].Path != "two.go" {
		t.Errorf("kept path = %q, want the file that retrieved it most strongly", got[0].Path)
	}
	if got := Merge([][]Hit{{a, b}, {aAgain}}, 1); len(got) != 1 {
		t.Errorf("keep=1 returned %d hits", len(got))
	}
}

// A change resembling nothing in the corpus gets nothing, rather than its five
// least distant entries. Off by default, because the entry cut for scoring 0.4
// is as real a failure as the five irrelevant ones.
func TestAbstentionDropsWhatIsTooDistant(t *testing.T) {
	hits := []Hit{{Entry: Entry{ID: "near"}, Score: 0.8}, {Entry: Entry{ID: "far"}, Score: 0.2}}

	if got := above(hits, 0); len(got) != 2 {
		t.Errorf("a zero floor dropped %d hits; off is what shipped", 2-len(got))
	}
	kept := above(append([]Hit(nil), hits...), 0.5)
	if len(kept) != 1 || kept[0].Entry.ID != "near" {
		t.Errorf("a 0.5 floor kept %v, want the near hit alone", ids(entriesOf(kept)))
	}
}

func entriesOf(hits []Hit) []Entry {
	out := make([]Entry, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.Entry)
	}
	return out
}
