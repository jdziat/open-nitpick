package linters

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestEslintRefusesRepoLocalBinary is the regression test for arbitrary code
// execution from the tree under review.
//
// A pull request can add node_modules/.bin/eslint as an executable shell script
// (git preserves the exec bit). Running it would execute attacker code with
// GITHUB_TOKEN and the model API key in the environment, and because
// **/node_modules/** is in the default ignore list, the malicious file would
// never appear in the posted review.
func TestEslintRefusesRepoLocalBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script trap does not apply on windows")
	}

	repo := t.TempDir()
	marker := filepath.Join(t.TempDir(), "pwned")

	if err := os.WriteFile(filepath.Join(repo, "package.json"), []byte(`{"name":"x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "app.js"), []byte("const a = 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	binDir := filepath.Join(repo, "node_modules", ".bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho \"argv: $*\" > " + marker + "\necho \"env: $LLM_API_KEY\" >> " + marker + "\n"
	if err := os.WriteFile(filepath.Join(binDir, "eslint"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("LLM_API_KEY", "sk-SECRET-FROM-CI")

	// The trap is the only eslint on PATH, and the runner is configured, so
	// Run reaches the binary resolution: without both, the test passed by
	// never getting there, and the containment it guards could have been
	// deleted unnoticed.
	t.Setenv("PATH", binDir)
	outside := filepath.Join(t.TempDir(), "eslint.config.js")
	if err := os.WriteFile(outside, []byte("export default [];\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := &eslint{cfg: fileConfig(repo, outside)}

	// Detect must not be satisfied by the repository-local binary alone.
	if err := e.Detect(context.Background(), repo, []string{"app.js"}); err == nil {
		t.Error("Detect should not be satisfied by a repo-supplied binary")
	}

	// Run must refuse the repo's binary, and say so, rather than execute it.
	_, err := e.Run(context.Background(), repo, []string{"app.js"})
	if err == nil {
		t.Fatal("Run with only a repo-supplied eslint on PATH did not fail")
	}
	t.Logf("Run err = %v", err)

	if data, readErr := os.ReadFile(marker); readErr == nil {
		t.Fatalf("PR-SUPPLIED BINARY EXECUTED — marker written:\n%s", data)
	}
}

func TestResolveBinaryRejectsPathsInsideRepo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH semantics differ on windows")
	}

	repo := t.TempDir()
	binDir := filepath.Join(repo, "tools")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(binDir, "faketool")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\ntrue\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Put the repo's own directory on PATH, as a devcontainer might.
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if _, err := resolveBinary("faketool", repo); err == nil {
		t.Fatal("a binary inside the reviewed repository must be refused even when it is on PATH")
	} else if !strings.Contains(err.Error(), "inside the repository") {
		t.Errorf("error should explain the refusal, got: %v", err)
	}

	// The same binary is fine when the repo under review is elsewhere.
	if _, err := resolveBinary("faketool", t.TempDir()); err != nil {
		t.Errorf("a binary outside the reviewed repo should resolve: %v", err)
	}
}

func TestSafePathsDropsFlagLikePaths(t *testing.T) {
	// A diff can contain a file literally named "--config=evil.js"; passed as
	// argv it becomes a flag.
	kept, rejected := safePaths([]string{
		"app.js",
		"--config=evil.js",
		"-rf",
		"src/ok.ts",
	})

	if len(kept) != 2 || kept[0] != "app.js" || kept[1] != "src/ok.ts" {
		t.Errorf("kept = %v, want the two real paths", kept)
	}
	if len(rejected) != 2 {
		t.Errorf("rejected = %v, want the two flag-like paths", rejected)
	}
}

// The end-of-options property moved to TestRunnersTerminateTheirFlagsBeforeThe
// FileList in containment_test.go. What used to be here grepped runners.go for
// literal argument strings, which broke whenever an argument moved and never
// showed that any of it reached a process. The replacement drives each runner
// and reads the argv its binary received.
