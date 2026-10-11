package main

import (
	"bytes"
	"io"
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
	if err := runSetup([]string{"-repo", repo}, strings.NewReader("y\n"), &out); err != nil {
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
	if err := runSetup([]string{"-repo", repo}, strings.NewReader("y\n"), &out); err != nil {
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
	if err := runSetup([]string{"-repo", repo, "-user"}, strings.NewReader("y\n"), &out); err != nil {
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
	if err := runSetup([]string{"-dry-run", "-repo", t.TempDir()}, strings.NewReader("y\n"), &out); err != nil {
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
	if err := runSetup([]string{"-repo", repo, "-client", "cursor, vscode"}, strings.NewReader("y\n"), &out); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{".cursor/mcp.json", ".vscode/mcp.json"} {
		if _, err := os.Stat(filepath.Join(repo, p)); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
	if err := runSetup([]string{"-repo", repo, "-client", "zed"}, strings.NewReader("y\n"), &out); err == nil || !strings.Contains(err.Error(), "unknown client") {
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
	err := runSetup([]string{"-repo", repo, "-client", "claude-code,cursor"}, strings.NewReader("y\n"), &out)
	if err == nil || !strings.Contains(err.Error(), "claude-code") {
		t.Fatalf("err = %v, want a failure naming claude-code", err)
	}
	if _, statErr := os.Stat(filepath.Join(repo, ".cursor", "mcp.json")); statErr != nil {
		t.Error("one broken client stopped the others")
	}
}

func TestSetupAsksBeforeWritingProjectFilesAndWritesNothingOnNo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake executables are shell scripts")
	}
	isolateClients(t, "claude", "opencode")
	for _, answer := range []string{"n\n", "\n", ""} {
		repo := t.TempDir()
		var out bytes.Buffer
		if err := runSetup([]string{"-repo", repo}, strings.NewReader(answer), &out); err != nil {
			t.Fatalf("answer %q: %v", answer, err)
		}
		if !strings.Contains(out.String(), ".mcp.json") || !strings.Contains(out.String(), "Write them?") {
			t.Errorf("answer %q: the files were not listed before asking:\n%s", answer, out.String())
		}
		if entries, _ := os.ReadDir(repo); len(entries) != 0 {
			t.Errorf("answer %q still wrote %d entries into the repository", answer, len(entries))
		}
	}
}

func TestSetupYesSkipsThePromptAndUserScopeNeverAsks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake executables are shell scripts")
	}
	home := isolateClients(t, "claude", "codex")
	repo := t.TempDir()
	var out bytes.Buffer
	// A reader that fails the test if setup reads from it.
	if err := runSetup([]string{"-repo", repo, "-yes"}, failingReader{t}, &out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".mcp.json")); err != nil {
		t.Errorf("-yes did not write the project file: %v", err)
	}

	out.Reset()
	repo2 := t.TempDir()
	if err := runSetup([]string{"-repo", repo2, "-user"}, failingReader{t}, &out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "config.toml")); err != nil {
		t.Errorf("user file missing: %v", err)
	}
	if strings.Contains(out.String(), "Write them?") {
		t.Error("-user asked about files in the home directory")
	}
}

type failingReader struct{ t *testing.T }

func (f failingReader) Read([]byte) (int, error) {
	f.t.Helper()
	f.t.Error("setup read from stdin when it had no need to")
	return 0, io.EOF
}

// TestSetupReplansAfterApproval holds the edit-race closed: another editor may
// add an MCP server while the user reads the list and decides to answer yes.
func TestSetupReplansAfterApproval(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake executables are shell scripts")
	}
	isolateClients(t, "claude")
	repo := t.TempDir()
	in := &approveAfterEdit{edit: func() {
		path := filepath.Join(repo, ".mcp.json")
		if err := os.WriteFile(path, []byte(`{"mcpServers":{"other":{"command":"other"}}}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}}
	var out bytes.Buffer
	if err := runSetup([]string{"-repo", repo}, in, &out); err != nil {
		t.Fatal(err)
	}
	got := readJSON(t, filepath.Join(repo, ".mcp.json"))
	servers := got["mcpServers"].(map[string]any)
	if servers["other"] == nil || servers["nitpick"] == nil {
		t.Errorf("replanned config lost a server: %v", servers)
	}
}

type approveAfterEdit struct {
	edit func()
	done bool
}

func (r *approveAfterEdit) Read(p []byte) (int, error) {
	if !r.done {
		r.done = true
		r.edit()
		copy(p, "y\n")
		return 2, nil
	}
	return 0, io.EOF
}

func TestSetupReportsWhatIsAlreadyInstalledAndDoesNotAsk(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake executables are shell scripts")
	}
	isolateClients(t, "claude")
	repo := t.TempDir()
	var out bytes.Buffer
	if err := runSetup([]string{"-repo", repo, "-yes"}, failingReader{t}, &out); err != nil {
		t.Fatal(err)
	}

	// Second run: everything is in place, so there is nothing to confirm.
	out.Reset()
	if err := runSetup([]string{"-repo", repo}, failingReader{t}, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "server already registered") || !strings.Contains(got, "5 already up to date") {
		t.Errorf("second run did not report what was in place:\n%s", got)
	}
	if strings.Contains(got, "Write them?") || strings.Contains(got, "wrote") {
		t.Errorf("second run asked or wrote with nothing to do:\n%s", got)
	}

	// A skill edited on disk is outdated: it is the only one rewritten.
	skill := filepath.Join(repo, ".claude", "skills", "nitpick-security", "SKILL.md")
	if err := os.WriteFile(skill, []byte("edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runSetup([]string{"-repo", repo, "-yes"}, failingReader{t}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "wrote 1 skills") || !strings.Contains(out.String(), "1 updated") || !strings.Contains(out.String(), "4 already current") {
		t.Errorf("an edited skill was not the only one rewritten:\n%s", out.String())
	}
	if got, _ := os.ReadFile(skill); string(got) == "edited" {
		t.Error("the outdated skill was left as edited")
	}
}

// TestSetupDoesNotExpandTheApprovedWriteSet holds the consent boundary: a skill
// that another process edits while the prompt is open was current when listed,
// so answering yes for the missing server does not authorize replacing it.
func TestSetupDoesNotExpandTheApprovedWriteSet(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake executables are shell scripts")
	}
	isolateClients(t, "claude")
	repo := t.TempDir()
	var out bytes.Buffer
	if err := runSetup([]string{"-repo", repo, "-yes"}, failingReader{t}, &out); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(repo, ".mcp.json")); err != nil {
		t.Fatal(err)
	}
	skill := filepath.Join(repo, ".claude", "skills", "nitpick-security", "SKILL.md")
	in := &approveAfterEdit{edit: func() {
		if err := os.WriteFile(skill, []byte("edited while waiting"), 0o644); err != nil {
			t.Fatal(err)
		}
	}}
	out.Reset()
	if err := runSetup([]string{"-repo", repo}, in, &out); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(skill); string(got) != "edited while waiting" {
		t.Error("setup rewrote a skill that was not in the approved write set")
	}
}

// TestCanPromptRefusesOpenPipes pins the noninteractive contract: a pipe may
// have no writer, where ReadString would wait forever.
func TestCanPromptRefusesOpenPipes(t *testing.T) {
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := read.Close(); err != nil {
			t.Error(err)
		}
		if err := write.Close(); err != nil {
			t.Error(err)
		}
	})
	if canPrompt(read) {
		t.Error("an open pipe is not an interactive prompt")
	}
}

// TestSetupDoesNotOverwriteAServerChangedAfterItsCurrentPlan was current when
// the consent list was made. A skill update keeps the prompt open long enough
// to model a concurrent editor changing the server file.
func TestSetupDoesNotOverwriteAServerChangedAfterItsCurrentPlan(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake executables are shell scripts")
	}
	isolateClients(t, "claude")
	repo := t.TempDir()
	var out bytes.Buffer
	if err := runSetup([]string{"-repo", repo, "-yes"}, failingReader{t}, &out); err != nil {
		t.Fatal(err)
	}
	skill := filepath.Join(repo, ".claude", "skills", "nitpick-security", "SKILL.md")
	if err := os.WriteFile(skill, []byte("needs update"), 0o644); err != nil {
		t.Fatal(err)
	}
	in := &approveAfterEdit{edit: func() {
		path := filepath.Join(repo, ".mcp.json")
		if err := os.WriteFile(path, []byte(`{"mcpServers":{"other":{"command":"other"}}}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}}
	out.Reset()
	if err := runSetup([]string{"-repo", repo}, in, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "server changed after approval") {
		t.Errorf("changed current server was not reported:\n%s", out.String())
	}
	servers := readJSON(t, filepath.Join(repo, ".mcp.json"))["mcpServers"].(map[string]any)
	if servers["nitpick"] != nil || servers["other"] == nil {
		t.Errorf("setup overwrote an unapproved server edit: %v", servers)
	}
}

func TestCanPromptRefusesDevNull(t *testing.T) {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.Close(); err != nil {
			t.Error(err)
		}
	})
	if canPrompt(f) {
		t.Error("/dev/null cannot answer an interactive prompt")
	}
}
