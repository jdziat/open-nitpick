package evals

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/review"
	"github.com/jdziat/open-nitpick/internal/security"
)

// TestSecurityEvalFlagAppliesInstruction pins NITPICK_EVAL_SECURITY to the
// security command's instruction text. Mutation: return "" always and the
// bake-off would score a generic review on security fixtures.
func TestSecurityEvalFlagAppliesInstruction(t *testing.T) {
	t.Setenv(EnvSecurity, "1")
	t.Setenv(EnvSecurityDepth, "")
	if got := securityPersonaInstruction(); got != security.Instruction {
		t.Fatalf("flag on (deep default): instruction = %q, want security.Instruction", got)
	}
	t.Setenv(EnvSecurityDepth, "light")
	if got := securityPersonaInstruction(); got != security.InstructionLight {
		t.Fatalf("depth light: instruction = %q, want InstructionLight", got)
	}
	t.Setenv(EnvSecurityDepth, "extreme")
	if got := securityPersonaInstruction(); got != security.InstructionExtreme {
		t.Fatalf("depth extreme: instruction = %q, want InstructionExtreme", got)
	}
	t.Setenv(EnvSecurityDepth, "typo")
	if _, err := security.InstructionForDepth("typo"); err == nil {
		t.Fatal("unknown depth must error")
	}
	t.Setenv(EnvSecurity, "0")
	if got := securityPersonaInstruction(); got != "" {
		t.Fatalf("flag off: instruction = %q, want empty", got)
	}
}

func makefileList(t *testing.T, prefix string) []string {
	t.Helper()
	src, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	var line string
	for _, l := range strings.Split(string(src), "\n") {
		if strings.HasPrefix(l, prefix) {
			line = strings.TrimSpace(strings.TrimPrefix(l, prefix))
		}
	}
	if line == "" {
		t.Fatalf("the Makefile has no %s line", prefix)
	}
	var out []string
	for _, n := range strings.Split(line, ",") {
		n = strings.TrimSpace(n)
		if n != "" {
			out = append(out, n)
		}
	}
	return out
}

// TestTheMakefileNamesTheSecurityBakeOffCorpus keeps make eval-security pointed
// at the hard tuning list (plants plus silence, including promoted spent fixtures).
func TestTheMakefileNamesTheSecurityBakeOffCorpus(t *testing.T) {
	want := securityBakeOffNames()
	named := map[string]bool{}
	for _, n := range makefileList(t, "SECURITY :=") {
		named[n] = true
	}
	for _, n := range want {
		if !named[n] {
			t.Errorf("SECURITY line missing %q", n)
		}
	}
	for n := range named {
		found := false
		for _, w := range want {
			if n == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("SECURITY line has unexpected %q", n)
		}
	}
	every := map[string]bool{}
	for _, f := range EveryFixture() {
		every[f.Name] = true
	}
	for _, n := range want {
		if !every[n] {
			t.Errorf("SECURITY names %q, which is not in EveryFixture()", n)
		}
	}
}

// TestTheMakefileNamesTheSecurityHeldOutCorpus pins SECURITY_HELD_OUT to the
// never-spent security held-out set (Rule 7).
func TestTheMakefileNamesTheSecurityHeldOutCorpus(t *testing.T) {
	named := map[string]bool{}
	for _, n := range makefileList(t, "SECURITY_HELD_OUT :=") {
		named[n] = true
	}
	held := map[string]bool{}
	for _, f := range SecurityHeldOutFixtures() {
		held[f.Name] = true
		if !named[f.Name] {
			t.Errorf("SecurityHeldOutFixtures has %q, Makefile SECURITY_HELD_OUT does not", f.Name)
		}
	}
	for n := range named {
		if !held[n] {
			t.Errorf("SECURITY_HELD_OUT names %q, not in SecurityHeldOutFixtures()", n)
		}
	}
	// Spent security fixtures must not reappear as security held-out.
	for _, spent := range []string{"removed-guard", "bash-fixed-temp-path", "clean-sql-allowlist"} {
		if named[spent] {
			t.Errorf("%q was spent on the 2026-09-18 security held-out; it must not be in SECURITY_HELD_OUT", spent)
		}
	}
}

// TestSecurityHeldOutStaysOutOfSecurityTuning keeps Rule 7 for the security persona.
func TestSecurityHeldOutStaysOutOfSecurityTuning(t *testing.T) {
	tuning := map[string]bool{}
	for _, n := range makefileList(t, "SECURITY :=") {
		tuning[n] = true
	}
	for _, f := range SecurityHeldOutFixtures() {
		if tuning[f.Name] {
			t.Errorf("%s is in both SECURITY and SECURITY_HELD_OUT", f.Name)
		}
	}
}

// TestSecurityPersonaFixturesAreWellFormed checks plants outside AllFixtures:
// Why credits, silence stays clean, and HMAC fixtures import the unchanged contract.
func TestSecurityPersonaFixturesAreWellFormed(t *testing.T) {
	for _, f := range append(SecurityTuningFixtures(), SecurityHeldOutFixtures()...) {
		if f.Name == "" {
			t.Fatal("unnamed security persona fixture")
		}
		if f.Clean() {
			continue
		}
		for _, d := range f.Defects {
			own := review.Finding{Path: d.Path, Line: d.Line, Rationale: d.Why}
			if !matches(own, d) {
				t.Errorf("%s: Why is not credited by keywords: %q", f.Name, d.Why)
			}
		}
	}
	unbound := pythonHMACUnboundCompareFixture()
	head := unbound.Head["app/webhook.py"]
	if !importsFile(head, "app/contract.py") {
		t.Fatal("python-hmac-unbound-compare must import unchanged app/contract.py")
	}
	bound := pythonHMACBoundCleanFixture()
	if !importsFile(bound.Head["app/webhook.py"], "app/contract.py") {
		t.Fatal("python-hmac-bound-clean must import unchanged app/contract.py")
	}
}

// TestSecurityBakeOffIsNotLabeledAMixedHeldOutSpend pins the report header and
// the dump name. Mutation: delete the securityCorpusToken branch and this
// battery is labeled MIXED with three held-out fixtures, which is a
// generalization claim the spent plants are no longer allowed to support.
func TestSecurityBakeOffIsNotLabeledAMixedHeldOutSpend(t *testing.T) {
	byName := map[string]Fixture{}
	for _, f := range EveryFixture() {
		byName[f.Name] = f
	}
	var tuning []Fixture
	for _, n := range securityBakeOffNames() {
		f, ok := byName[n]
		if !ok {
			t.Fatalf("security bake-off names %q, which EveryFixture does not have", n)
		}
		tuning = append(tuning, f)
	}
	label := CorpusLabel(tuning)
	if !strings.Contains(label, "SECURITY tuning") || strings.Contains(label, "MIXED") {
		t.Fatalf("security tuning corpus labeled %q", label)
	}
	name := runDumpName("multifile", tuning, time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC), 1)
	if !strings.Contains(name, "-security-") || strings.Contains(name, "mixed") {
		t.Fatalf("security tuning dump named %q", name)
	}

	heldLabel := CorpusLabel(SecurityHeldOutFixtures())
	if !strings.Contains(heldLabel, "SECURITY HELD-OUT") || strings.Contains(heldLabel, "MIXED") {
		t.Fatalf("security held-out corpus labeled %q", heldLabel)
	}
	if strings.Contains(CorpusLabel(tuning[:1]), "SECURITY tuning") {
		t.Fatal("a one-fixture subset inherited the security-tuning label")
	}
}

// TestSecurityPersonaScoringKeepsMultiDefectHonest pins the validation layer
// that lets multi-defect stay in SECURITY. Mutation: score with
// ScoreDetection under EnvSecurity and a perfect security review of the
// path-traversal plant is 1/3 because the race and descriptor leak still
// sit in the denominator.
func TestSecurityPersonaScoringKeepsMultiDefectHonest(t *testing.T) {
	var multi Fixture
	for _, f := range EveryFixture() {
		if f.Name == "multi-defect" {
			multi = f
			break
		}
	}
	if multi.Name == "" {
		t.Fatal("multi-defect missing from EveryFixture")
	}
	if got := len(multi.Defects); got != 3 {
		t.Fatalf("multi-defect plants %d defects, want 3 (security + two out-of-persona)", got)
	}

	traversal := review.Finding{
		Path: "handler.go", Line: 19, Class: string(config.ClassSecurity),
		Rationale: "path traversal via user-controlled path escapes the upload directory",
	}
	race := review.Finding{
		Path: "handler.go", Line: 25, Class: string(config.ClassConcurrency),
		Rationale: "data race on h.count without the mutex",
	}

	t.Setenv(EnvSecurity, "0")
	raw := ScoreDetection(multi, []review.Finding{traversal})
	if raw.Matched != 1 || len(multi.Defects) != 3 {
		t.Fatalf("raw ScoreDetection: matched %d of %d", raw.Matched, len(multi.Defects))
	}

	t.Setenv(EnvSecurity, "1")
	plants := PersonaDefects(multi)
	if len(plants) != 1 || plants[0].Class != config.ClassSecurity {
		t.Fatalf("persona plants = %+v, want the one ClassSecurity defect", plants)
	}
	perfect := ScoreDetectionForEval(multi, []review.Finding{traversal})
	if perfect.Matched != 1 || perfect.Noise() != 0 {
		t.Fatalf("security-perfect review: matched=%d noise=%d, want 1/0", perfect.Matched, perfect.Noise())
	}
	raceOnly := ScoreDetectionForEval(multi, []review.Finding{race})
	if raceOnly.Matched != 0 || raceOnly.Noise() != 0 {
		t.Fatalf("out-of-persona race finding: matched=%d noise=%d, want 0/0 (out of scope)",
			raceOnly.Matched, raceOnly.Noise())
	}
	rawUnderSecurity := ScoreDetection(multi, []review.Finding{traversal})
	if rawUnderSecurity.Matched != 1 {
		t.Fatal("ScoreDetection itself must stay unscoped; the persona layer is ScoreDetectionForEval")
	}
}
