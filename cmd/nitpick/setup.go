package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// detectedClients returns the agent clients that look installed on this
// machine, in the order mcpClients lists them. A client counts when its
// executable is on PATH or when the directory it keeps its user configuration
// in already exists; either is evidence the person uses it, and neither
// requires the client to have been started once. Claude Desktop and the like
// that ship no executable are found by the second test alone.
func detectedClients() []mcpClient {
	var found []mcpClient
	for _, c := range mcpClients {
		if clientPresent(c) {
			found = append(found, c)
		}
	}
	return found
}

func clientPresent(c mcpClient) bool {
	for _, b := range c.bins {
		if _, err := exec.LookPath(b); err == nil {
			return true
		}
	}
	if c.dir != nil {
		if p, err := homePath(c.dir...); err == nil && isDir(p) {
			return true
		}
	}
	if c.user != nil {
		if p, err := c.user(); err == nil && isDir(filepath.Dir(p)) {
			return true
		}
	}
	return false
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// runSetup registers the nitpick MCP server with every agent client found on
// this machine. A client that keeps a project file gets that file, so the
// setup is shared with the team through version control; a client that has
// only a user file gets the user file. -user sends every client to its user
// file instead.
func runSetup(args []string, stdout io.Writer) error {
	var (
		user    bool
		dryRun  bool
		command string
		repo    string
		only    string
		noSkill bool
	)
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	fs.BoolVar(&user, "user", false, "write every client's user-wide configuration rather than the project's")
	fs.BoolVar(&dryRun, "dry-run", false, "show what would be written and write nothing")
	fs.StringVar(&command, "command", "", "the command the clients run (default: nitpick for a project file, this binary's absolute path for a user file)")
	fs.StringVar(&repo, "repo", ".", "repository root, for project files")
	fs.StringVar(&only, "client", "", "comma-separated clients to set up, detected or not (default: every client detected here)")
	fs.BoolVar(&noSkill, "no-skills", false, "register the MCP server only; do not install the nitpick skills")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: nitpick setup [flags]\n\nFinds the agent clients installed on this machine (%s) and registers the\nnitpick MCP server with each: the project file where the client has one, the\nuser file where it does not. Existing entries for other servers are kept. The\nnitpick skills (nitpick-review-change and the rest) go into the folder each\nclient reads skills from.\n\nFlags:\n", strings.Join(mcpClientNames(), ", "))
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		fs.Usage()
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}

	targets, err := setupTargets(only)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		_, _ = fmt.Fprintf(stdout, "no agent clients detected (looked for %s)\nname one with -client, or see nitpick mcp clients\n", strings.Join(mcpClientNames(), ", "))
		return nil
	}

	var failed []string
	for _, c := range targets {
		req := mcpInstall{user: user || c.project == "", dryRun: dryRun, terse: true, command: command, repo: repo}
		_, _ = fmt.Fprintf(stdout, "%s:\n", c.name)
		if err := installMCP(c, req, stdout); err != nil {
			_, _ = fmt.Fprintf(stdout, "  failed: %v\n", err)
			failed = append(failed, c.name)
			continue
		}
		if noSkill {
			continue
		}
		if err := installSkills(c, req, stdout); err != nil {
			_, _ = fmt.Fprintf(stdout, "  skills failed: %v\n", err)
			failed = append(failed, c.name)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("setup failed for %s", strings.Join(failed, ", "))
	}
	return nil
}

// setupTargets resolves -client, or falls back to detection.
func setupTargets(only string) ([]mcpClient, error) {
	if strings.TrimSpace(only) == "" {
		return detectedClients(), nil
	}
	var out []mcpClient
	for _, name := range strings.Split(only, ",") {
		c, ok := findClient(name)
		if !ok {
			return nil, fmt.Errorf("unknown client %q; known: %s", strings.TrimSpace(name), strings.Join(mcpClientNames(), ", "))
		}
		out = append(out, c)
	}
	if len(out) == 0 {
		return nil, errors.New("-client named no clients")
	}
	return out, nil
}
