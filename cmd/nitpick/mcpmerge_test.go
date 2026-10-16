package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestMCPInstallPreservesCustomServerSettings(t *testing.T) {
	for _, client := range mcpClients {
		if client.toml {
			continue
		}
		t.Run(client.name, func(t *testing.T) {
			root := map[string]any{}
			client.write(root, "old", []string{"mcp"})
			section := "mcpServers"
			if client.name == "opencode" {
				section = "mcp"
			}
			if client.name == "vscode" {
				section = "servers"
			}
			servers := root[section].(map[string]any)
			custom := map[string]any{"env": map[string]any{"TOKEN": "keep"}, "timeout": float64(2400), "disabled": true, "enabled": false}
			for k, v := range custom {
				servers["nitpick"].(map[string]any)[k] = v
			}
			path := filepath.Join(t.TempDir(), "mcp.json")
			data, err := json.Marshal(root)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			merged, err := mergeJSON(path, "new", []string{"mcp"}, client.write)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(merged, &root); err != nil {
				t.Fatal(err)
			}
			server := root[section].(map[string]any)["nitpick"].(map[string]any)
			for k, want := range custom {
				if !reflect.DeepEqual(server[k], want) {
					t.Errorf("%s = %v, want %v", k, server[k], want)
				}
			}
			if section == "mcp" {
				if !reflect.DeepEqual(server["command"], []any{"new", "mcp"}) {
					t.Errorf("command = %v", server["command"])
				}
			} else if server["command"] != "new" {
				t.Errorf("command = %v", server["command"])
			}
		})
	}
}

func TestMCPInstallCodexPreservesCustomSettings(t *testing.T) {
	for name, input := range map[string]string{
		"tables": `# keep model
model = 'o3'
[mcp_servers.nitpick] # keep heading
command = 'old' # keep command comment
args = [
 'mcp',
 '-v',
]
tool_timeout_sec = 7200
startup_timeout_sec = 45
enabled = false
[mcp_servers.nitpick.env]
TOKEN = 'keep'
[mcp_servers.other]
command = 'other'
`,
		"quoted": `[mcp_servers."nitpick"]
command = '''old
command'''
args = ['mcp']
env = { TOKEN = 'keep' }
tool_timeout_sec = 7200
`,
		"dotted": `mcp_servers.nitpick.command = 'old'
mcp_servers.nitpick.args = ['mcp']
mcp_servers.nitpick.tool_timeout_sec = 7200
mcp_servers.nitpick.env.TOKEN = 'keep'
`,
		"inline": `[mcp_servers]
nitpick = { command = 'old', args = ['mcp'], tool_timeout_sec = 7200, env = { TOKEN = 'keep' } }
`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
				t.Fatal(err)
			}
			merged, err := mergeTOML(path, `C:\Program Files\nitpick.exe`, []string{"mcp"})
			if err != nil {
				t.Fatal(err)
			}
			var got, want map[string]any
			if err := toml.Unmarshal([]byte(input), &want); err != nil {
				t.Fatal(err)
			}
			server := want["mcp_servers"].(map[string]any)["nitpick"].(map[string]any)
			server["command"], server["args"] = `C:\Program Files\nitpick.exe`, []any{"mcp"}
			if err := toml.Unmarshal(merged, &got); err != nil {
				t.Fatalf("invalid TOML: %v\n%s", err, merged)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("got %v, want %v", got, want)
			}
			for _, line := range strings.Split(input, "\n") {
				if i := strings.Index(line, "# keep"); i >= 0 && !strings.Contains(string(merged), line[i:]) {
					t.Errorf("lost comment from %q", line)
				}
			}
			if err := os.WriteFile(path, merged, 0o600); err != nil {
				t.Fatal(err)
			}
			again, err := mergeTOML(path, `C:\Program Files\nitpick.exe`, []string{"mcp"})
			if err != nil || !bytes.Equal(merged, again) {
				t.Errorf("reinstall changed config: %v\n%s", err, again)
			}
		})
	}
}

func TestMCPInstallCodexAddsMissingDefaults(t *testing.T) {
	for name, input := range map[string]string{
		"empty": "", "model": `model = 'o3'
`,
		"empty table": `[mcp_servers.nitpick]
`,
		"env only": `[mcp_servers.nitpick.env]
TOKEN = 'keep'
`,
		"dotted": `mcp_servers.nitpick.command = 'old'
`,
		"inline": `mcp_servers = { nitpick = { env = { TOKEN = 'keep' } } }
`,
		"new inline": `mcp_servers = { other = { command = 'keep' } }
`,
		"no newline": "[mcp_servers.nitpick] # keep",
		"parent table": `[mcp_servers]
nitpick.command = 'old'
`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
				t.Fatal(err)
			}
			merged, err := mergeTOML(path, "nitpick", []string{"mcp"})
			if err != nil {
				t.Fatal(err)
			}
			var cfg map[string]any
			if err := toml.Unmarshal(merged, &cfg); err != nil {
				t.Fatalf("%v\n%s", err, merged)
			}
			server := cfg["mcp_servers"].(map[string]any)["nitpick"].(map[string]any)
			if server["command"] != "nitpick" || !reflect.DeepEqual(server["args"], []any{"mcp"}) || server["tool_timeout_sec"] != int64(3600) {
				t.Errorf("server = %v", server)
			}
			if strings.Contains(input, "TOKEN") && !reflect.DeepEqual(server["env"], map[string]any{"TOKEN": "keep"}) {
				t.Errorf("lost env: %v", server)
			}
		})
	}
}

func TestMCPInstallCodexRejectsInvalidConfig(t *testing.T) {
	for name, input := range map[string]string{
		"syntax": "[broken", "duplicate": `[mcp_servers.nitpick]
command = 'one'
command = 'two'
`,
		"servers type": `mcp_servers = 1
`,
		"server type": `[mcp_servers]
nitpick = false
`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := mergeTOML(path, "nitpick", []string{"mcp"}); err == nil {
				t.Fatal("invalid config accepted")
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != input {
				t.Errorf("config changed: %v", err)
			}
		})
	}
}

func TestMCPInstallCodexKeepsCurrentConfigBytes(t *testing.T) {
	input := `# These values already describe the installed server.
[mcp_servers.nitpick]
command = 'nitpick'
args = [
 'mcp', # retain this comment too
]
tool_timeout_sec = 2_400.0 # preserve the user's shorter timeout
[mcp_servers.nitpick.env]
TOKEN = 'keep'
`
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := mergeTOML(path, "nitpick", []string{"mcp"})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != input {
		t.Errorf("current config changed:\n%s", got)
	}
}
