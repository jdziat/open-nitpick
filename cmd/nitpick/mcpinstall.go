package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// mcpClient is one agent client that reads MCP servers from a file. Each
// has a project-scoped file, a user-scoped file, or both, and a shape the
// file expects; what is written is the same server in that shape.
type mcpClient struct {
	name          string
	note          string
	project       string                                                  // repository-relative path, or ""
	user          func() (string, error)                                  // absolute path, or ""
	write         func(cfg map[string]any, command string, args []string) // JSON clients
	toml          bool                                                    // Codex: a TOML file, appended to
	bins          []string                                                // executables on PATH that mean the client is installed
	dir           []string                                                // home-relative directory that means it, for a client with no user file to look beside
	skillsProject []string                                                // repository-relative folder the client reads skills from, or nil
	skillsUser    []string                                                // home-relative folder, or nil
}

// The shapes below were read from each client's own documentation on
// 2026-09-06; a client that changes its file will need this table changed.
var mcpClients = []mcpClient{
	{
		name: "claude-code", skillsProject: []string{".claude", "skills"}, skillsUser: []string{".claude", "skills"}, bins: []string{"claude"}, dir: []string{".claude"}, note: "project: .mcp.json at the repository root; user: `claude mcp add --scope user nitpick -- nitpick mcp` (printed, not run)",
		project: ".mcp.json",
		write:   writeMCPServers,
	},
	{
		name: "claude-desktop", note: "user only: claude_desktop_config.json",
		user: func() (string, error) {
			switch runtime.GOOS {
			case "darwin":
				return homePath("Library", "Application Support", "Claude", "claude_desktop_config.json")
			case "windows":
				if d := os.Getenv("APPDATA"); d != "" {
					return filepath.Join(d, "Claude", "claude_desktop_config.json"), nil
				}
				return "", errors.New("APPDATA is not set")
			default:
				return homePath(".config", "Claude", "claude_desktop_config.json")
			}
		},
		write: writeMCPServers,
	},
	{
		name: "cursor", skillsProject: []string{".cursor", "skills"}, skillsUser: []string{".cursor", "skills"}, bins: []string{"cursor"}, note: "project: .cursor/mcp.json; user: ~/.cursor/mcp.json",
		project: filepath.Join(".cursor", "mcp.json"),
		user:    func() (string, error) { return homePath(".cursor", "mcp.json") },
		write:   writeMCPServers,
	},
	{
		name: "windsurf", skillsProject: []string{".windsurf", "skills"}, skillsUser: []string{".codeium", "windsurf", "skills"}, bins: []string{"windsurf"}, note: "user only: ~/.codeium/windsurf/mcp_config.json",
		user:  func() (string, error) { return homePath(".codeium", "windsurf", "mcp_config.json") },
		write: writeMCPServers,
	},
	{
		name: "vscode", skillsProject: []string{".github", "skills"}, skillsUser: []string{}, bins: []string{"code"}, note: "project: .vscode/mcp.json (Copilot agent mode)",
		project: filepath.Join(".vscode", "mcp.json"),
		write: func(cfg map[string]any, command string, args []string) {
			servers := sub(cfg, "servers")
			servers["nitpick"] = map[string]any{"type": "stdio", "command": command, "args": args}
		},
	},
	{
		name: "opencode", skillsProject: []string{".opencode", "skills"}, skillsUser: []string{".config", "opencode", "skills"}, bins: []string{"opencode"}, note: "project: opencode.json; user: ~/.config/opencode/opencode.json",
		project: "opencode.json",
		user:    func() (string, error) { return homePath(".config", "opencode", "opencode.json") },
		write: func(cfg map[string]any, command string, args []string) {
			if _, ok := cfg["$schema"]; !ok {
				cfg["$schema"] = "https://opencode.ai/config.json"
			}
			servers := sub(cfg, "mcp")
			cmd := append([]any{command}, toAny(args)...)
			servers["nitpick"] = map[string]any{"type": "local", "command": cmd, "enabled": true}
		},
	},
	{
		name: "gemini-cli", skillsProject: []string{".gemini", "skills"}, skillsUser: []string{".gemini", "skills"}, bins: []string{"gemini"}, note: "project: .gemini/settings.json; user: ~/.gemini/settings.json",
		project: filepath.Join(".gemini", "settings.json"),
		user:    func() (string, error) { return homePath(".gemini", "settings.json") },
		write:   writeMCPServers,
	},
	{
		name: "codex", skillsProject: []string{".agents", "skills"}, skillsUser: []string{".agents", "skills"}, bins: []string{"codex"}, note: "user only: ~/.codex/config.toml, appended to",
		user: func() (string, error) { return homePath(".codex", "config.toml") },
		toml: true,
	},
}

func homePath(parts ...string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(append([]string{home}, parts...)...), nil
}

// writeMCPServers is the shape most clients share: mcpServers.<name>.
func writeMCPServers(cfg map[string]any, command string, args []string) {
	servers := sub(cfg, "mcpServers")
	servers["nitpick"] = map[string]any{"command": command, "args": args}
}

// sub returns cfg[key] as a map, creating it when absent or of another type.
func sub(cfg map[string]any, key string) map[string]any {
	if m, ok := cfg[key].(map[string]any); ok {
		return m
	}
	m := map[string]any{}
	cfg[key] = m
	return m
}

func toAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// runMCPInstall writes the nitpick server into a client's MCP configuration.
func runMCPInstall(args []string, stdout io.Writer) error {
	var (
		global  bool
		show    bool
		command string
		repo    string
	)
	fs := flag.NewFlagSet("mcp install", flag.ContinueOnError)
	fs.BoolVar(&global, "user", false, "write the user-wide configuration rather than the project's")
	fs.BoolVar(&show, "print", false, "print what would be written and write nothing")
	fs.StringVar(&command, "command", "", "the command the client runs (default: nitpick for a project file, this binary's absolute path for a user file)")
	fs.StringVar(&repo, "repo", ".", "repository root, for a project file")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: nitpick mcp install <client> [flags]\n\nWrites the nitpick MCP server into an agent client's configuration, keeping what else is there.\nClients:")
		for _, c := range mcpClients {
			fmt.Fprintf(os.Stderr, "  %-15s %s\n", c.name, c.note)
		}
		fmt.Fprintln(os.Stderr, "\nFlags:")
		fs.PrintDefaults()
	}
	// The usage line above is "<client> [flags]", and the documentation and
	// every example in it put the client first. Go's flag package stops at the
	// first non-flag argument, so `mcp install codex -user` parsed as two
	// positionals and was refused, with the -user it had just been handed
	// listed in the usage it printed. Parsing what follows each positional
	// makes the documented order the one that runs.
	var clients []string
	for rest := args; ; {
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if fs.NArg() == 0 {
			break
		}
		clients = append(clients, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	if len(clients) != 1 {
		fs.Usage()
		return errors.New("one client name is required")
	}
	client, ok := findClient(clients[0])
	if !ok {
		return fmt.Errorf("unknown client %q; run nitpick mcp install -h for the list", clients[0])
	}

	return installMCP(client, mcpInstall{user: global, dryRun: show, command: command, repo: repo}, stdout)
}

// mcpInstall is one request to register the server with one client.
type mcpInstall struct {
	user    bool
	dryRun  bool
	terse   bool   // with dryRun, name the file instead of printing it
	command string // "" picks the default for the scope
	repo    string
}

func findClient(name string) (mcpClient, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, c := range mcpClients {
		if c.name == name {
			return c, true
		}
	}
	return mcpClient{}, false
}

// mergeJSON reads the file when it exists, sets the server in the client's
// shape, and returns the whole file; every other key is kept as it was.
func mergeJSON(path, command string, args []string, write func(map[string]any, string, []string)) ([]byte, error) {
	cfg := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		if len(bytes.TrimSpace(data)) > 0 {
			if err := json.Unmarshal(data, &cfg); err != nil {
				return nil, fmt.Errorf("%s is not JSON this command can merge into: %w", path, err)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	write(cfg, command, args)
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// mergeTOML appends the server table to a TOML file, or replaces the one
// already there, without parsing the rest: Codex's file holds settings this
// command has no business rewriting.
func mergeTOML(path, command string, args []string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = fmt.Sprintf("%q", a)
	}
	block := fmt.Sprintf("[mcp_servers.nitpick]\ncommand = %q\nargs = [%s]\n", command, strings.Join(quoted, ", "))
	lines := strings.Split(string(data), "\n")
	var kept []string
	skipping := false
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "[") {
			skipping = t == "[mcp_servers.nitpick]" || strings.HasPrefix(t, "[mcp_servers.nitpick.")
		}
		if !skipping {
			kept = append(kept, line)
		}
	}
	text := strings.TrimRight(strings.Join(kept, "\n"), "\n")
	if text != "" {
		text += "\n\n"
	}
	return []byte(text + block), nil
}

// mcpClientNames lists the clients, for help text and tests.
func mcpClientNames() []string {
	var names []string
	for _, c := range mcpClients {
		names = append(names, c.name)
	}
	sort.Strings(names)
	return names
}
