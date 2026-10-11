package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestBundledSkillsAreRenamedConsistentlyAndCrossReferencesFollow(t *testing.T) {
	sets, err := bundledSkills()
	if err != nil {
		t.Fatal(err)
	}
	if len(sets) < 5 {
		t.Fatalf("bundled %d skills, want the five shipped under plugins/nitpick/skills", len(sets))
	}
	name := regexp.MustCompile(`(?m)^name: (\S+)`)
	for _, s := range sets {
		body := string(s.files["SKILL.md"])
		m := name.FindStringSubmatch(body)
		if m == nil || m[1] != s.name || !strings.HasPrefix(s.name, skillPrefix) {
			t.Errorf("%s: front matter name %v must equal its folder", s.name, m)
		}
		for _, other := range sets {
			bare := strings.TrimPrefix(other.name, skillPrefix)
			if strings.Contains(body, "`"+bare+"`") {
				t.Errorf("%s still points at `%s`, which is installed as %s", s.name, bare, other.name)
			}
		}
	}
}

func TestSetupInstallsSkillsWhereEachClientReadsThemAndLeavesOthersAlone(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake executables are shell scripts")
	}
	home := isolateClients(t, "claude", "codex", "opencode")
	repo := t.TempDir()
	own := filepath.Join(repo, ".claude", "skills", "mine", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(own), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(own, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	for range 2 { // a second run replaces its own copy rather than failing or duplicating
		if err := runSetup([]string{"-repo", repo}, strings.NewReader("y\n"), &out); err != nil {
			t.Fatalf("%v\n%s", err, out.String())
		}
	}
	for _, p := range []string{
		filepath.Join(repo, ".claude", "skills", "nitpick-review-change", "SKILL.md"),
		filepath.Join(repo, ".opencode", "skills", "nitpick-security", "SKILL.md"),
		filepath.Join(home, ".agents", "skills", "nitpick-full-review", "SKILL.md"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}
	if got, _ := os.ReadFile(own); string(got) != "mine" {
		t.Error("setup touched a skill it did not write")
	}
}

func TestSetupNoSkillsAndDryRunInstallNoSkills(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake executables are shell scripts")
	}
	isolateClients(t, "claude")
	repo := t.TempDir()
	var out bytes.Buffer
	for _, args := range [][]string{{"-no-skills"}, {"-dry-run"}} {
		if err := runSetup(append([]string{"-repo", repo}, args...), strings.NewReader("y\n"), &out); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(repo, ".claude", "skills")); err == nil {
			t.Fatalf("%v wrote skills", args)
		}
	}
	if !strings.Contains(out.String(), "would write 5 skills") {
		t.Errorf("dry run did not say what it would do: %s", out.String())
	}
}
