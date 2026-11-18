package execution

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

func TestOperatorMappingToolMatchesTheVerifierWithUnicodeClaims(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("cross-language mapping check requires python3")
	}
	head := strings.Repeat("a", 40)
	title := "Build <x> & café\u2028fails"
	rationale := "A claim with a newline\nand an emoji 😃."
	out, err := exec.Command(python, "../../tools/execution/map_claim.py", "--head", head, "--path", "main.go", "--line", "3", "--title", title, "--rationale", rationale).Output()
	if err != nil {
		t.Fatal(err)
	}
	var mapping struct {
		FindingSHA256 string `json:"finding_sha256"`
	}
	if err := json.Unmarshal(out, &mapping); err != nil {
		t.Fatal(err)
	}
	if want := FindingDigest(head, "main.go", 3, title, rationale); mapping.FindingSHA256 != want {
		t.Fatalf("operator mapping cannot match the verifier: got=%s want=%s", mapping.FindingSHA256, want)
	}
}
