package llm

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A stored key is found by the keystore service name, and renaming the product
// does not rename the key. Every page that tells a person which service their key
// is under, or shows them a value to write in api_key_keyring, has to say what the
// code reads. A rename of the product once rewrote these sentences, and a person
// copying the example would then look up a service nothing is stored under.
func TestEveryDocumentedKeystoreServiceIsTheOneTheCodeUses(t *testing.T) {
	pages, err := filepath.Glob(filepath.Join("..", "..", "docs", "*.md"))
	if err != nil || len(pages) < 10 {
		t.Fatalf("found %d docs pages (%v); the glob is not reading the docs", len(pages), err)
	}
	patterns := map[string]*regexp.Regexp{
		"under the service":           regexp.MustCompile("under the service `([^`]+)`"),
		"a keystore under <provider>": regexp.MustCompile("keystore under `([^`/]+)/<provider>`"),
		"api_key_keyring example":     regexp.MustCompile(`api_key_keyring: ([A-Za-z0-9._-]+)/[A-Za-z0-9._-]+`),
		"service name in the table":   regexp.MustCompile("`service/account`, for example \"([^\"/]+)/"),
	}
	seen := 0
	for _, page := range pages {
		if filepath.Base(page) == "configuration-reference.md" || filepath.Base(page) == "rebrand.md" {
			continue
		}
		raw, err := os.ReadFile(page)
		if err != nil {
			t.Fatal(err)
		}
		// Prose wraps, so a sentence is matched with its whitespace collapsed.
		text := strings.Join(strings.Fields(string(raw)), " ")
		for what, re := range patterns {
			for _, m := range re.FindAllStringSubmatch(text, -1) {
				seen++
				if m[1] != KeyringService {
					t.Errorf("%s: %s names %q, but the code reads %q", filepath.Base(page), what, m[1], KeyringService)
				}
			}
		}
	}
	// Rule 10: the scan found the sentences it is about. A pattern that matches
	// nothing passes every page.
	if seen < 3 {
		t.Fatalf("matched only %d keystore mentions across the docs; the patterns have stopped finding them", seen)
	}
}

// The providers page names the service in the sentence a person reads first, so
// it is held to the constant on its own as well as by the scan above.
func TestTheProvidersPageNamesTheKeystoreServiceTheCodeUses(t *testing.T) {
	raw, err := os.ReadFile("../../docs/providers.md")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(strings.Fields(string(raw)), " ")
	if want := "service `" + KeyringService + "`"; !strings.Contains(text, want) {
		t.Errorf("docs/providers.md does not say %s, the service the code reads and writes", want)
	}
	if strings.Contains(text, "under the service `nitpick`") {
		t.Error("docs/providers.md names the service `nitpick`, which no stored key is under")
	}
}
