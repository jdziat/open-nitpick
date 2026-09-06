package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("%s: %v\n%s", path, err, data)
	}
	return m
}

func TestMCPInstallWritesEachProjectClientAndKeepsWhatIsThere(t *testing.T) {
	repo := t.TempDir()
	// An existing Cursor file with another server and a setting survives.
	if err := os.MkdirAll(filepath.Join(repo, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".cursor", "mcp.json"), []byte(`{"mcpServers":{"other":{"command":"x"}},"theme":"dark"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"claude-code", "cursor", "vscode", "opencode", "gemini-cli"} {
		var out bytes.Buffer
		if err := runMCPInstall([]string{"-repo", repo, name}, &out); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.Contains(out.String(), "wrote ") {
			t.Errorf("%s: %s", name, out.String())
		}
	}
	cursor := readJSON(t, filepath.Join(repo, ".cursor", "mcp.json"))
	servers := cursor["mcpServers"].(map[string]any)
	if servers["other"] == nil || cursor["theme"] != "dark" {
		t.Errorf("cursor's existing content was lost: %v", cursor)
	}
	np := servers["nitpick"].(map[string]any)
	if np["command"] != "nitpick" || np["args"].([]any)[0] != "mcp" {
		t.Errorf("cursor server = %v", np)
	}
	claude := readJSON(t, filepath.Join(repo, ".mcp.json"))
	if claude["mcpServers"].(map[string]any)["nitpick"] == nil {
		t.Errorf("claude-code = %v", claude)
	}
	vscode := readJSON(t, filepath.Join(repo, ".vscode", "mcp.json"))
	if vscode["servers"].(map[string]any)["nitpick"].(map[string]any)["type"] != "stdio" {
		t.Errorf("vscode = %v", vscode)
	}
	oc := readJSON(t, filepath.Join(repo, "opencode.json"))
	ocs := oc["mcp"].(map[string]any)["nitpick"].(map[string]any)
	if ocs["type"] != "local" || ocs["enabled"] != true || oc["$schema"] == nil {
		t.Errorf("opencode = %v", oc)
	}
	if cmd := ocs["command"].([]any); len(cmd) != 2 || cmd[0] != "nitpick" || cmd[1] != "mcp" {
		t.Errorf("opencode command = %v", cmd)
	}
	gem := readJSON(t, filepath.Join(repo, ".gemini", "settings.json"))
	if gem["mcpServers"].(map[string]any)["nitpick"] == nil {
		t.Errorf("gemini = %v", gem)
	}
}

func TestMCPInstallUserScopeUsesTheHomeAndThisBinary(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "appdata"))
	exe, _ := os.Executable()

	var out bytes.Buffer
	if err := runMCPInstall([]string{"-user", "cursor"}, &out); err != nil {
		t.Fatal(err)
	}
	cur := readJSON(t, filepath.Join(home, ".cursor", "mcp.json"))
	if got := cur["mcpServers"].(map[string]any)["nitpick"].(map[string]any)["command"]; got != exe {
		t.Errorf("user-scoped command = %v, want this binary %s", got, exe)
	}

	// Codex is TOML, appended to, and replaced on a second install rather
	// than duplicated.
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".codex", "config.toml"), []byte("model = \"o3\"\n\n[mcp_servers.nitpick]\ncommand = \"old\"\nargs = [\"mcp\"]\n\n[mcp_servers.other]\ncommand = \"y\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := runMCPInstall([]string{"-user", "-command", "nitpick", "codex"}, &out); err != nil {
			t.Fatal(err)
		}
	}
	toml, _ := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	got := string(toml)
	if strings.Count(got, "[mcp_servers.nitpick]") != 1 || !strings.Contains(got, "model = \"o3\"") || !strings.Contains(got, "[mcp_servers.other]") || strings.Contains(got, "\"old\"") {
		t.Errorf("codex config:\n%s", got)
	}
	if !strings.Contains(got, "command = \"nitpick\"\nargs = [\"mcp\"]") {
		t.Errorf("codex server block:\n%s", got)
	}

	// Claude Code has no user file this command writes; it prints the command.
	out.Reset()
	if err := runMCPInstall([]string{"-user", "claude-code"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "claude mcp add --scope user nitpick --") {
		t.Errorf("claude-code -user = %s", out.String())
	}
}

func TestMCPInstallPrintDoesNotWriteAndRefusesUnknownClients(t *testing.T) {
	repo := t.TempDir()
	var out bytes.Buffer
	if err := runMCPInstall([]string{"-repo", repo, "-print", "cursor"}, &out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".cursor", "mcp.json")); err == nil {
		t.Error("-print wrote the file")
	}
	if !strings.Contains(out.String(), `"nitpick"`) {
		t.Errorf("-print output: %s", out.String())
	}
	if err := runMCPInstall([]string{"-repo", repo, "zed"}, &out); err == nil || !strings.Contains(err.Error(), "unknown client") {
		t.Errorf("unknown client err = %v", err)
	}
	if err := runMCPInstall([]string{"-repo", repo, "windsurf"}, &out); err == nil || !strings.Contains(err.Error(), "-user") {
		t.Errorf("windsurf without -user err = %v", err)
	}
	if err := runMCPInstall([]string{"-repo", repo, "-print", "-user", "windsurf"}, &out); err != nil {
		t.Fatal(err)
	}
	if got := mcpClientNames(); len(got) != 8 {
		t.Errorf("clients = %v", got)
	}
}
