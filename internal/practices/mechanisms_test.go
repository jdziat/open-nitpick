package practices

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/standards"
)

func TestDesignMechanismFixturesBuildAndPinBehavior(t *testing.T) {
	for _, mechanism := range []string{"boundaries", "contracts", "coupled-duplication", "indirection", "ineffective-tests"} {
		for _, variant := range []string{"bad", "good"} {
			t.Run(mechanism+"/"+variant, func(t *testing.T) {
				root := materializeMechanism(t, mechanism, variant)
				runMechanism(t, root, true)
				switch mechanism {
				case "boundaries":
					var files []standards.File
					for _, name := range []string{"go.mod", "ui/ui.go", "service/service.go", "storage/storage.go"} {
						body, err := os.ReadFile(filepath.Join(root, name))
						if err != nil {
							t.Fatal(err)
						}
						files = append(files, standards.File{Path: name, Src: body})
					}
					inventory, check := InspectDesign(files, []config.PracticeBoundary{{From: "example.com/fixture/ui", Forbid: []string{"example.com/fixture/storage"}, Reason: "UI uses the service boundary"}})
					want := 0
					if variant == "bad" {
						want = 1
					}
					if len(inventory.Errors) != 0 || check.State != Completed || len(check.Examined) != 3 || len(check.Findings) != want {
						t.Fatalf("boundary control lost its scope or distinction: inventory=%+v check=%+v", inventory, check)
					}
				case "contracts":
					writeMechanism(t, root, "oracle_test.go", `package fixture
import "testing"
func TestLegacyNameSurvivesUpgrade(t *testing.T) {
 name, err := Decode([]byte(`+"`"+`{"version":1,"name":"Ada"}`+"`"+`))
 if err != nil || name != "Ada" { t.Fatalf("name=%q error=%v", name, err) }
}
`)
					runMechanism(t, root, variant == "good")
				case "coupled-duplication":
					body := `package fixture
import "testing"
func TestRetailPreviewMatchesCheckout(t *testing.T) {
 if PreviewShipping(5500) != CheckoutShipping(5500) { t.Fatal("two quotes for the same retail order") }
}
`
					if variant == "good" {
						body = `package fixture
import "testing"
func TestSeparateContractsKeepTheirOwnThresholds(t *testing.T) {
 if RetailShipping(5500) != 0 || WholesaleShipping(5500) != 500 { t.Fatal("independent shipping contract changed") }
}
`
					}
					writeMechanism(t, root, "oracle_test.go", body)
					runMechanism(t, root, variant == "good")
				case "ineffective-tests":
					source, err := os.ReadFile(filepath.Join(root, "session.go"))
					if err != nil {
						t.Fatal(err)
					}
					mutated := strings.Replace(string(source), "return now < expires", "return true", 1)
					if mutated == string(source) {
						t.Fatal("expiration mutation no longer applies")
					}
					writeMechanism(t, root, "session.go", mutated)
					runMechanism(t, root, variant == "bad")
				}
			})
		}
	}
}

func materializeMechanism(t *testing.T, mechanism, variant string) string {
	t.Helper()
	root := t.TempDir()
	source := filepath.Join("testdata", mechanism, variant)
	err := filepath.WalkDir(source, func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		if !strings.HasSuffix(name, ".txt") {
			return nil
		}
		rel, err := filepath.Rel(source, name)
		if err != nil {
			return err
		}
		body, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		writeMechanism(t, root, strings.TrimSuffix(rel, ".txt"), string(body))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func writeMechanism(t *testing.T, root, name, body string) {
	t.Helper()
	name = filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func runMechanism(t *testing.T, root string, wantPass bool) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "go", "test", "./...")
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if (err == nil) != wantPass {
		t.Fatalf("fixture pass=%t, wanted %t: %v\n%s", err == nil, wantPass, err, output)
	}
	if !wantPass && !strings.Contains(string(output), "--- FAIL: Test") {
		t.Fatalf("fixture failed without exercising its assertion: %v\n%s", err, output)
	}
}
