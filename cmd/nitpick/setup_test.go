package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// isolateClients points HOME at an empty directory and PATH at a directory
// holding only the named fake executables, so detection sees exactly the
// clients a test installs and none of the ones on the machine running it.
func isolateClients(t *testing.T, bins ...string) (home string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "appdata"))
	bin := t.TempDir()
	for _, b := range bins {
		if err := os.WriteFile(filepath.Join(bin, b), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	return home
}

func clientNames(cs []mcpClient) string {
	var n []string
	for _, c := range cs {
		n = append(n, c.name)
	}
	return strings.Join(n, ",")
}

func TestDetectedClientsFindsExecutablesAndConfigDirectories(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake executables are shell scripts")
	}
	home := isolateClients(t, "opencode", "claude")
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := clientNames(detectedClients()); got != "claude-code,opencode,codex" {
		t.Errorf("detected = %s, want claude-code,opencode,codex", got)
	}
}

func TestDetectedClientsIsEmptyOnAMachineWithNoClients(t *testing.T) {
	isolateClients(t)
	if got := detectedClients(); len(got) != 0 {
		t.Errorf("detected %s on an empty machine", clientNames(got))
	}
	repo := t.TempDir()
	var out bytes.Buffer
	if err := runSetup([]string{"-repo", repo}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no agent clients detected") {
		t.Errorf("output = %q", out.String())
	}
	entries, _ := os.ReadDir(repo)
	if len(entries) != 0 {
		t.Errorf("setup wrote %d files with nothing detected", len(entries))
	}
}

func TestSetupWritesProjectFilesWhereAClientHasOneAndUserFilesWhereItDoesNot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake executables are shell scripts")
	}
	home := isolateClients(t, "opencode", "codex", "claude")
	repo := t.TempDir()
	var out bytes.Buffer
	if err := runSetup([]string{"-repo", repo}, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	for _, p := range []string{filepath.Join(repo, "opencode.json"), filepath.Join(repo, ".mcp.json"), filepath.Join(home, ".codex", "config.toml")} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("setup did not write %s: %v", p, err)
		}
	}
	if _, err := os.Stat(filepath.Join(repo, ".cursor")); err == nil {
		t.Error("setup configured cursor, which is not installed")
	}
}

func TestSetupUserFlagSendsEveryClientToItsUserFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake executables are shell scripts")
	}
	home := isolateClients(t, "gemini")
	repo := t.TempDir()
	var out bytes.Buffer
	if err := runSetup([]string{"-repo", repo, "-user"}, &out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".gemini", "settings.json")); err != nil {
		t.Errorf("user file missing: %v", err)
	}
	if entries, _ := os.ReadDir(repo); len(entries) != 0 {
		t.Error("-user still wrote into the repository")
	}
}

func TestSetupDryRunWritesNothingAndKeepsFileContentsOutOfTheOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake executables are shell scripts")
	}
	home := isolateClients(t, "codex")
	cfg := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	const secret = "experimental_bearer_token = \"s3cret\"\n"
	if err := os.WriteFile(cfg, []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runSetup([]string{"-dry-run", "-repo", t.TempDir()}, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "s3cret") {
		t.Errorf("dry run printed the user's config:\n%s", out.String())
	}
	if got, _ := os.ReadFile(cfg); string(got) != secret {
		t.Errorf("dry run changed the file: %q", got)
	}
}

func TestSetupClientFlagConfiguresNamedClientsAndRejectsUnknownOnes(t *testing.T) {
	isolateClients(t)
	repo := t.TempDir()
	var out bytes.Buffer
	if err := runSetup([]string{"-repo", repo, "-client", "cursor, vscode"}, &out); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{".cursor/mcp.json", ".vscode/mcp.json"} {
		if _, err := os.Stat(filepath.Join(repo, p)); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
	if err := runSetup([]string{"-repo", repo, "-client", "zed"}, &out); err == nil || !strings.Contains(err.Error(), "unknown client") {
		t.Errorf("unknown client err = %v", err)
	}
}

func TestSetupReportsAFailingClientAndStillConfiguresTheRest(t *testing.T) {
	isolateClients(t)
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, ".mcp.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := runSetup([]string{"-repo", repo, "-client", "claude-code,cursor"}, &out)
	if err == nil || !strings.Contains(err.Error(), "claude-code") {
		t.Fatalf("err = %v, want a failure naming claude-code", err)
	}
	if _, statErr := os.Stat(filepath.Join(repo, ".cursor", "mcp.json")); statErr != nil {
		t.Error("one broken client stopped the others")
	}
}
