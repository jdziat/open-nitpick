package main

import (
	"bufio"
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
// file instead. Before it writes into the repository it lists the files and
// asks; anything already in place is reported and left alone.
func runSetup(args []string, stdin io.Reader, stdout io.Writer) error {
	var (
		user    bool
		dryRun  bool
		command string
		repo    string
		only    string
		noSkill bool
		yes     bool
	)
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	fs.BoolVar(&user, "user", false, "write every client's user-wide configuration rather than the project's")
	fs.BoolVar(&dryRun, "dry-run", false, "show what would be written and write nothing")
	fs.StringVar(&command, "command", "", "the command the clients run (default: nitpick for a project file, this binary's absolute path for a user file)")
	fs.StringVar(&repo, "repo", ".", "repository root, for project files")
	fs.StringVar(&only, "client", "", "comma-separated clients to set up, detected or not (default: every client detected here)")
	fs.BoolVar(&yes, "yes", false, "write project files without asking")
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

	// Plan everything first: what each client would get, and which of it is
	// not already in place. Nothing is written until the project files, the
	// ones that end up in someone's git status, have been approved.
	type step struct {
		client    mcpClient
		req       mcpInstall
		mcp       mcpPlan
		mcpErr    error
		mcpSkip   bool // changed after the consent list was shown
		skills    skillPlan
		skillEr   error
		skillSkip bool // changed after the consent list was shown
	}
	var steps []*step
	var projectFiles []string
	for _, c := range targets {
		s := &step{client: c, req: mcpInstall{user: user || c.project == "", dryRun: dryRun, terse: true, command: command, repo: repo}}
		s.mcp, s.mcpErr = planMCP(c, s.req)
		if !noSkill && s.mcpErr == nil {
			s.skills, s.skillEr = planSkills(c, s.req)
		}
		if !s.req.user {
			if s.mcpErr == nil && s.mcp.path != "" && !s.mcp.current {
				projectFiles = append(projectFiles, s.mcp.path)
			}
			if s.skillEr == nil && len(s.skills.pending) > 0 {
				projectFiles = append(projectFiles, s.skills.dir+string(filepath.Separator)+skillPrefix+"*")
			}
		}
		steps = append(steps, s)
	}

	approved := true
	if len(projectFiles) > 0 && !dryRun && !yes {
		approved = confirmProjectWrites(stdin, stdout, repo, projectFiles)
	}
	if approved && !dryRun {
		// The person may spend time reading the list before answering. Plan
		// again after that wait. A server that was already current can have
		// changed too; notice it rather than claiming it still is. It is not
		// overwritten, since its path was absent from the consent list.
		for _, s := range steps {
			if s.mcp.path != "" {
				old := s.mcp
				next, err := planMCP(s.client, s.req)
				switch {
				case err != nil && old.current:
					s.mcpSkip = true
				case err != nil:
					s.mcpErr = err
				default:
					s.mcp = next
					s.mcpSkip = !yes && old.current && !next.current
				}
			}
			if !noSkill && s.skillEr == nil {
				old := s.skills
				next, err := planSkills(s.client, s.req)
				switch {
				case err != nil && old.current > 0:
					s.skillSkip = true
				case err != nil:
					s.skillEr = err
				default:
					s.skills = next
					s.skillSkip = !yes && old.current > 0 && len(next.pending) > 0
				}
			}
		}
	}

	var failed []string
	for _, s := range steps {
		_, _ = fmt.Fprintf(stdout, "%s:\n", s.client.name)
		if s.mcpErr != nil {
			_, _ = fmt.Fprintf(stdout, "  failed: %v\n", s.mcpErr)
			failed = append(failed, s.client.name)
			continue
		}
		if !s.req.user && !approved && (!s.mcp.current || len(s.skills.pending) > 0) {
			_, _ = fmt.Fprintf(stdout, "  project files not written; rerun with -yes, or -user for your home directory\n")
			continue
		}
		if s.mcpSkip {
			_, _ = fmt.Fprintf(stdout, "  server changed after approval; not overwriting %s without a new confirmation\n", s.mcp.path)
		} else if err := s.mcp.apply(s.req, stdout); err != nil {
			_, _ = fmt.Fprintf(stdout, "  failed: %v\n", err)
			failed = append(failed, s.client.name)
			continue
		}
		if noSkill {
			continue
		}
		if s.skillEr != nil {
			_, _ = fmt.Fprintf(stdout, "  skills failed: %v\n", s.skillEr)
			failed = append(failed, s.client.name)
			continue
		}
		if s.skillSkip {
			_, _ = fmt.Fprintf(stdout, "  skills changed after approval; not overwriting %s without a new confirmation\n", s.skills.dir)
			continue
		}
		if err := s.skills.apply(s.client, s.req, stdout); err != nil {
			_, _ = fmt.Fprintf(stdout, "  skills failed: %v\n", err)
			failed = append(failed, s.client.name)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("setup failed for %s", strings.Join(failed, ", "))
	}
	return nil
}

// confirmProjectWrites lists the files setup is about to create or change in
// the repository and asks once. Project files are the ones that show up in
// the next git status, so they are not written on an assumption. An input
// that is not a terminal cannot answer, which counts as no: a script opts in
// with -yes.
func confirmProjectWrites(stdin io.Reader, stdout io.Writer, repo string, files []string) bool {
	root, err := filepath.Abs(repo)
	if err != nil {
		root = repo
	}
	_, _ = fmt.Fprintf(stdout, "setup will create or change these files in %s:\n", root)
	for _, f := range files {
		rel, err := filepath.Rel(root, f)
		if err != nil {
			rel = f
		}
		_, _ = fmt.Fprintf(stdout, "  %s\n", rel)
	}
	if !canPrompt(stdin) {
		_, _ = fmt.Fprintln(stdout, "nothing to ask on (stdin is closed); skipping them")
		return false
	}
	_, _ = fmt.Fprint(stdout, "Write them? [y/N] ")
	line, _ := bufio.NewReader(stdin).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}

// canPrompt reports whether r is an interactive terminal. A pipe can remain
// open with no writer, so blocking on one would hang a script or CI step; it
// opts in with -yes instead.
func canPrompt(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return r != nil
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	if null, err := os.Stat(os.DevNull); err == nil && os.SameFile(fi, null) {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
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
