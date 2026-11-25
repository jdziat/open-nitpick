package linters

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nitpick-oss/nitpick/internal/config"
)

func checkpointRepo(tb testing.TB) string {
	tb.Helper()
	repo := tb.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "app.py"), []byte("print('hello')\n"), 0o600); err != nil {
		tb.Fatal(err)
	}
	checkpointGit(tb, repo, "init", "--quiet")
	checkpointGit(tb, repo, "add", ".")
	checkpointGit(tb, repo, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--quiet", "-m", "test: source")
	return repo
}

func checkpointGit(tb testing.TB, repo string, args ...string) {
	tb.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		tb.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func TestAnalyzerCheckpointBindsCleanSourcePolicyAndExecutableContent(t *testing.T) {
	repo := checkpointRepo(t)
	bin := t.TempDir()
	executable := filepath.Join(bin, "ruff")
	if err := os.WriteFile(executable, []byte("\x7fELFidentity one"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	cfg := config.Defaults()
	cfg.Linters.Enabled = []string{"ruff"}
	autoDetect := false
	cfg.Linters.AutoDetect = &autoDetect
	files := parse(t, "diff --git a/app.py b/app.py\n--- a/app.py\n+++ b/app.py\n@@ -0,0 +1 @@\n+print(1)\n")
	key := func() string { return New(repo, cfg, nil).CheckpointKey(context.Background(), files) }
	first := key()
	if first == "" || key() != first {
		t.Fatal("clean unchanged source has no stable identity")
	}
	if err := os.WriteFile(executable, []byte("\x7fELFidentity two"), 0o755); err != nil {
		t.Fatal(err)
	}
	toolChange := key()
	if toolChange == "" || toolChange == first {
		t.Fatal("executable update kept a stale checkpoint key")
	}
	cfg.Linters.Mode = config.LinterStrict
	policyChange := key()
	if policyChange == "" || policyChange == toolChange {
		t.Fatal("policy update kept a stale checkpoint key")
	}
	t.Setenv("RUFF_TEST_IDENTITY", "new environment")
	envChange := key()
	if envChange == "" || envChange == policyChange {
		t.Fatal("analyzer environment update kept a stale checkpoint key")
	}
	writeFile(t, repo, "app.py", "print('changed')\n")
	if key() != "" {
		t.Fatal("dirty checkout was eligible for cached analysis")
	}
	checkpointGit(t, repo, "add", "app.py")
	checkpointGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--quiet", "-m", "test: changed source")
	if key() == "" || key() == envChange {
		t.Fatal("new source revision retained the old key")
	}
	writeFile(t, repo, "untracked.py", "print('untracked')\n")
	if key() != "" {
		t.Fatal("untracked source was omitted from identity checks")
	}
	if err := os.Remove(filepath.Join(repo, "untracked.py")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, repo, ".gitignore", "ignored/\n")
	checkpointGit(t, repo, "add", ".gitignore")
	checkpointGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--quiet", "-m", "test: ignored source")
	writeFile(t, repo, "ignored/dependency.py", "print('external dependency')\n")
	if key() != "" {
		t.Fatal("ignored dependencies were omitted from identity checks")
	}
}

func TestAnalyzerCheckpointRefusesUnboundOrIndirectInputs(t *testing.T) {
	for _, mode := range []string{"no git", "script tool", "external config", "unknown analyzer", "no targets", "cancelled", "tracked symlink", "assume unchanged", "skip worktree"} {
		t.Run(mode, func(t *testing.T) {
			repo := checkpointRepo(t)
			bin := t.TempDir()
			tool := filepath.Join(bin, "ruff")
			if err := os.WriteFile(tool, []byte("\x7fELFidentity"), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			cfg := config.Defaults()
			cfg.Linters.Enabled = []string{"ruff"}
			autoDetect := false
			cfg.Linters.AutoDetect = &autoDetect
			files := parse(t, "diff --git a/app.py b/app.py\n--- a/app.py\n+++ b/app.py\n@@ -0,0 +1 @@\n+print(1)\n")
			ctx := context.Background()
			switch mode {
			case "no git":
				if err := os.RemoveAll(filepath.Join(repo, ".git")); err != nil {
					t.Fatal(err)
				}
			case "script tool":
				if err := os.WriteFile(tool, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
					t.Fatal(err)
				}
			case "external config":
				p := filepath.Join(t.TempDir(), "ruff.toml")
				if err := os.WriteFile(p, []byte("line-length=80\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				cfg.Linters.RuffConfig = p
			case "unknown analyzer":
				cfg.Linters.Enabled = []string{"eslint"}
			case "no targets":
				files[0].Path = "README.md"
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "tracked symlink":
				if err := os.Symlink(t.TempDir(), filepath.Join(repo, "dependency")); err != nil {
					t.Fatal(err)
				}
				checkpointGit(t, repo, "add", "dependency")
				checkpointGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--quiet", "-m", "test: external source")
			case "assume unchanged", "skip worktree":
				flag := "--assume-unchanged"
				if mode == "skip worktree" {
					flag = "--skip-worktree"
				}
				checkpointGit(t, repo, "update-index", flag, "app.py")
				writeFile(t, repo, "app.py", "print('hidden change')\n")
			}
			if got := New(repo, cfg, nil).CheckpointKey(ctx, files); strings.TrimSpace(got) != "" {
				t.Fatalf("unbound inputs have cache identity %s", got)
			}
		})
	}
}

func TestAnalyzerCheckpointBoundsGoWorkspaceAndLocalReplacementInputs(t *testing.T) {
	for _, mode := range []string{"local module", "tracked replacement", "untracked replacement", "outside replacement", "workspace"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("GOWORK", "off")
			t.Setenv("CGO_ENABLED", "0")
			repo := checkpointRepo(t)
			body := "module example.com/local\n\ngo 1.24\n"
			if mode == "outside replacement" {
				body += "replace example.com/dependency => ../dependency\n"
			}
			if mode == "untracked replacement" {
				body += "replace example.com/dependency => ./.git/dependency\n"
				writeFile(t, repo, ".git/dependency/go.mod", "module example.com/dependency\n\ngo 1.24\n")
			}
			if mode == "tracked replacement" {
				body += "replace example.com/dependency => ./dependency\n"
				writeFile(t, repo, "dependency/go.mod", "module example.com/dependency\n\ngo 1.24\n")
				checkpointGit(t, repo, "add", "dependency/go.mod")
			}
			writeFile(t, repo, "go.mod", body)
			checkpointGit(t, repo, "add", "go.mod")
			checkpointGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--quiet", "-m", "test: module inputs")
			if mode == "workspace" {
				work := filepath.Join(t.TempDir(), "go.work")
				if err := os.WriteFile(work, []byte("go 1.24\n\nuse "+repo+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				t.Setenv("GOWORK", work)
			}
			set := New(repo, config.Defaults(), nil)
			tracked, err := set.checkpointCommand(context.Background(), "git", "ls-files", "--stage", "-z")
			if err != nil {
				t.Fatal(err)
			}
			_, eligible := set.checkpointGoInputs(context.Background(), tracked)
			if eligible != (mode == "local module" || mode == "tracked replacement") {
				t.Fatalf("%s eligible=%t", mode, eligible)
			}
		})
	}
}

func TestAnalyzerCheckpointIncludesPersistedGoEnvironmentPolicy(t *testing.T) {
	t.Setenv("GOWORK", "off")
	t.Setenv("CGO_ENABLED", "0")
	repo := checkpointRepo(t)
	writeFile(t, repo, "go.mod", "module example.com/local\n\ngo 1.24\n")
	checkpointGit(t, repo, "add", "go.mod")
	checkpointGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--quiet", "-m", "test: module policy")
	goenv := filepath.Join(t.TempDir(), "goenv")
	t.Setenv("GOENV", goenv)
	t.Setenv("GOFLAGS", "")
	set := New(repo, config.Defaults(), nil)
	tracked, err := set.checkpointCommand(context.Background(), "git", "ls-files", "--stage", "-z")
	if err != nil {
		t.Fatal(err)
	}
	identity := func(flags string) string {
		t.Helper()
		if err := os.WriteFile(goenv, []byte("GOFLAGS="+flags+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		inputs, ok := set.checkpointGoInputs(context.Background(), tracked)
		if !ok {
			t.Fatal("clean local module was not eligible")
		}
		data, err := json.Marshal(inputs)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	first := identity("-tags=first")
	if identity("-tags=first") != first {
		t.Fatal("temporary Go build paths invalidated identical inputs")
	}
	if identity("-tags=second") == first {
		t.Fatal("persisted Go flags changed without invalidating analysis")
	}
}

func TestAnalyzerCheckpointKeepsRuffArtifactsOutsideSourceIdentity(t *testing.T) {
	if _, ok := checkpointExecutable("ruff"); !ok {
		t.Skip("installed native Ruff is unavailable")
	}
	repo := checkpointRepo(t)
	cfg := config.Defaults()
	cfg.Linters.Enabled = []string{"ruff"}
	autoDetect := false
	cfg.Linters.AutoDetect = &autoDetect
	files := parse(t, "diff --git a/app.py b/app.py\n--- a/app.py\n+++ b/app.py\n@@ -0,0 +1 @@\n+print('hello')\n")
	set := New(repo, cfg, nil)
	before := set.CheckpointKey(context.Background(), files)
	if before == "" {
		t.Fatal("fresh checkout was ineligible")
	}
	if _, err := set.Run(context.Background(), files); err != nil {
		t.Fatal(err)
	}
	if after := set.CheckpointKey(context.Background(), files); after != before {
		t.Fatal("analyzer output changed the source identity")
	}
	if _, err := os.Stat(filepath.Join(repo, ".ruff_cache")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("analyzer populated a source-tree cache: %v", err)
	}
}

func TestAnalyzerCheckpointRejectsUnboundGoFlagsAndDrivers(t *testing.T) {
	for _, mode := range []string{"overlay", "modfile", "toolexec", "pkgdir", "package driver", "auto package driver", "cache driver", "cgo"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("GOWORK", "off")
			t.Setenv("CGO_ENABLED", "0")
			t.Setenv("GOFLAGS", "")
			t.Setenv("GOCACHEPROG", "")
			t.Setenv("GOPACKAGESDRIVER", "off")
			repo := checkpointRepo(t)
			writeFile(t, repo, "go.mod", "module example.com/local\n\ngo 1.24\n")
			checkpointGit(t, repo, "add", "go.mod")
			checkpointGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--quiet", "-m", "test: module inputs")
			external := t.TempDir()
			switch mode {
			case "overlay":
				writeFile(t, external, "overlay.json", `{"Replace":{}}`)
				t.Setenv("GOFLAGS", "-overlay="+filepath.Join(external, "overlay.json"))
			case "modfile":
				writeFile(t, external, "alternate.mod", "module example.com/alternate\n\ngo 1.24\n")
				t.Setenv("GOFLAGS", "-modfile="+filepath.Join(external, "alternate.mod"))
			case "toolexec", "pkgdir":
				t.Setenv("GOFLAGS", "-"+mode+"="+external)
			case "package driver":
				t.Setenv("GOPACKAGESDRIVER", filepath.Join(external, "driver"))
			case "auto package driver":
				t.Setenv("GOPACKAGESDRIVER", "")
				if err := os.WriteFile(filepath.Join(external, "gopackagesdriver"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
					t.Fatal(err)
				}
				t.Setenv("PATH", external+string(os.PathListSeparator)+os.Getenv("PATH"))
			case "cache driver":
				t.Setenv("GOCACHEPROG", filepath.Join(external, "driver"))
			case "cgo":
				t.Setenv("CGO_ENABLED", "1")
			}
			set := New(repo, config.Defaults(), nil)
			tracked, err := set.checkpointCommand(context.Background(), "git", "ls-files", "--stage", "-z")
			if err != nil {
				t.Fatal(err)
			}
			if _, eligible := set.checkpointGoInputs(context.Background(), tracked); eligible {
				t.Fatal("external Go inputs were eligible without their contents")
			}
		})
	}
}

func TestAnalyzerCheckpointUsesTheSameLocalGoToolchainAsTheRunner(t *testing.T) {
	t.Setenv("GOTOOLCHAIN", "auto")
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOSUMDB", "off")
	t.Setenv("GOWORK", "off")
	t.Setenv("CGO_ENABLED", "0")
	repo := checkpointRepo(t)
	writeFile(t, repo, "go.mod", "module example.com/local\n\ngo 1.24\n\ntoolchain go1.99.98\n")
	checkpointGit(t, repo, "add", "go.mod")
	checkpointGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--quiet", "-m", "test: requested toolchain")
	set := New(repo, config.Defaults(), nil)
	tracked, err := set.checkpointCommand(context.Background(), "git", "ls-files", "--stage", "-z")
	if err != nil {
		t.Fatal(err)
	}
	inputs, eligible := set.checkpointGoInputs(context.Background(), tracked)
	if !eligible || inputs[1].(map[string]string)["GOTOOLCHAIN"] != "local" {
		t.Fatal("identity probe followed the repository toolchain hint")
	}
}
