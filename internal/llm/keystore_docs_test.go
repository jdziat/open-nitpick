package llm

import (
	"os"
	"strings"
	"testing"
)

// A stored key is found by the keystore service name, and renaming the product
// does not rename the key. The page that tells a person where their key lives
// must say what the code looks up: a person following it to find or delete a
// key looks under the name it gives. This failed once, when a rename of the
// product swept up the sentence that names the service.
func TestTheProvidersPageNamesTheKeystoreServiceTheCodeUses(t *testing.T) {
	raw, err := os.ReadFile("../../docs/providers.md")
	if err != nil {
		t.Fatal(err)
	}
	page := string(raw)
	if want := "service `" + KeyringService + "`"; !strings.Contains(page, want) {
		t.Errorf("docs/providers.md does not say %s, the service the code reads and writes", want)
	}
	// The sentence must not name a different service as the default.
	if strings.Contains(page, "under the service `nitpick`") {
		t.Error("docs/providers.md names the service `nitpick`, which no stored key is under")
	}
}
