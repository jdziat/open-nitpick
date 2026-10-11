package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// mcpPlan is what registering the server with one client would do, worked out
// before anything is written so setup can report what is already in place and
// ask before touching a repository.
type mcpPlan struct {
	path    string
	content []byte
	command string
	current bool   // the file already holds exactly this
	manual  string // set when the client has no file to write; the command to run instead
}

// planMCP resolves where a client keeps its server list and what the file
// would contain, without writing it.
func planMCP(client mcpClient, req mcpInstall) (mcpPlan, error) {
	global, command := req.user, req.command

	// A project file is shared through version control, so it names the
	// command a teammate will have on PATH; a user file is this machine's,
	// so it names the binary that is here, whether or not PATH finds it.
	if command == "" {
		command = "nitpick"
		if global {
			if exe, err := os.Executable(); err == nil {
				command = exe
			}
		}
	}
	serverArgs := []string{"mcp"}

	var path string
	switch {
	case global && client.user != nil:
		p, err := client.user()
		if err != nil {
			return mcpPlan{}, err
		}
		path = p
	case global:
		if client.name == "claude-code" {
			return mcpPlan{command: command, manual: fmt.Sprintf("Claude Code keeps user-scoped servers in its own store; run:\n\n  claude mcp add --scope user nitpick -- %s mcp\n", command)}, nil
		}
		return mcpPlan{}, fmt.Errorf("%s has no user-wide file this command knows; drop -user", client.name)
	case client.project != "":
		root, err := filepath.Abs(req.repo)
		if err != nil {
			return mcpPlan{}, err
		}
		path = filepath.Join(root, client.project)
	default:
		return mcpPlan{}, fmt.Errorf("%s has no project file; pass -user", client.name)
	}

	var content []byte
	var err error
	if client.toml {
		content, err = mergeTOML(path, command, serverArgs)
	} else {
		content, err = mergeJSON(path, command, serverArgs, client.write)
	}
	if err != nil {
		return mcpPlan{}, err
	}
	existing, readErr := os.ReadFile(path)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return mcpPlan{}, readErr
	}
	return mcpPlan{path: path, content: content, command: command, current: readErr == nil && bytes.Equal(existing, content)}, nil
}

// apply writes the planned file, or with dryRun says what it would write.
func (p mcpPlan) apply(req mcpInstall, stdout io.Writer) error {
	say := func(format string, a ...any) { _, _ = fmt.Fprintf(stdout, format, a...) }
	if p.manual != "" {
		say("%s", p.manual)
		return nil
	}
	if req.dryRun {
		if req.terse {
			// A user file can hold credentials for unrelated servers, so a
			// preview across every client names the file and leaves its
			// contents alone.
			say("would write %s (server nitpick: %s mcp)\n", p.path, p.command)
			return nil
		}
		say("%s:\n%s", p.path, p.content)
		return nil
	}
	if p.current {
		say("server already registered in %s\n", p.path)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p.path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(p.path, p.content, 0o644); err != nil {
		return err
	}
	say("wrote %s (server nitpick: %s mcp)\n", p.path, p.command)
	if _, err := exec.LookPath(p.command); err != nil && !filepath.IsAbs(p.command) {
		say("note: %q is not on PATH here; the client needs it on PATH, or pass -command with the binary's path\n", p.command)
	}
	return nil
}

// installMCP writes, or with dryRun shows, the nitpick server in one client's
// configuration. mcp install and setup both plan with planMCP, so a client is
// written the same way whichever command asked.
func installMCP(client mcpClient, req mcpInstall, stdout io.Writer) error {
	p, err := planMCP(client, req)
	if err != nil {
		return err
	}
	return p.apply(req, stdout)
}
