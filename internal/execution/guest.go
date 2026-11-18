package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type guestCommand func(context.Context, []string, string, io.Writer) error

// RunGuest performs one trusted check inside the isolated image. It is only
// invoked by nitpick-checker; the review process always uses Runner.Run.
func RunGuest(ctx context.Context, snapshot Snapshot, check Check, work string, out io.Writer) int {
	return runGuest(ctx, snapshot, check, work, out, func(ctx context.Context, args []string, dir string, out io.Writer) error {
		cmd := exec.CommandContext(ctx, "/usr/local/go/bin/go", args...)
		cmd.Dir = dir
		cmd.Stdout, cmd.Stderr = out, out
		cmd.WaitDelay = 2 * time.Second
		return cmd.Run()
	})
}

func runGuest(ctx context.Context, snapshot Snapshot, check Check, work string, out io.Writer, command guestCommand) int {
	if err := check.validate(); err != nil {
		return 75
	}
	if err := ExtractSnapshot(snapshot, work); err != nil {
		return 75
	}
	cgo := false
	err := filepath.WalkDir(work, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range file.Imports {
			if imp.Path.Value == `"C"` {
				cgo = true
			}
		}
		return nil
	})
	if err != nil || cgo {
		return 75
	}
	// This preflight distinguishes unavailable offline dependencies from a
	// command that ran and failed. No dependency fetch is permitted.
	if err := command(ctx, []string{"list", "-m", "all"}, work, out); err != nil {
		return 75
	}
	target := "."
	if check.Package != "." {
		target = "./" + check.Package
	}
	dependencyArgs := []string{"list", "-e", "-json", "-deps"}
	if check.Kind == "go_test" {
		dependencyArgs = append(dependencyArgs, "-test")
	}
	dependencyArgs = append(dependencyArgs, target)
	packages := &cappedOutput{limit: 4 << 20}
	if err := command(ctx, dependencyArgs, work, packages); err != nil || packages.truncated {
		return 75
	}
	decoder := json.NewDecoder(bytes.NewReader(packages.data))
	listed := 0
	for {
		var pkg struct {
			Dir      string
			Standard bool
			Error    *struct{ Err string }
		}
		err := decoder.Decode(&pkg)
		if err == io.EOF {
			break
		}
		if err != nil || (pkg.Error != nil && pkg.Dir == "" && !pkg.Standard) {
			return 75
		}
		listed++
	}
	if listed == 0 {
		return 75
	}
	args := []string{"build", target}
	if check.Kind == "go_test" {
		args = []string{"test", "-json", "-count=1", target}
	}
	events := &testEvents{out: out}
	if err := command(ctx, args, work, events); err != nil {
		return 1
	}
	if check.Kind == "go_test" && events.passed == 0 {
		return 75
	}
	return 0
}

// testEvents counts Go's outer events; JSON printed by a test is nested inside
// an output event and cannot impersonate a completed test.
type testEvents struct {
	out      io.Writer
	line     []byte
	passed   int
	overflow bool
}

func (e *testEvents) Write(p []byte) (int, error) {
	n, err := e.out.Write(p)
	for _, b := range p[:n] {
		if b != '\n' {
			if len(e.line) < 64*1024 {
				e.line = append(e.line, b)
			} else {
				e.overflow = true
			}
			continue
		}
		var event struct {
			Action string
			Test   string
		}
		if !e.overflow && json.Unmarshal(bytes.TrimSpace(e.line), &event) == nil && event.Action == "pass" && event.Test != "" {
			e.passed++
		}
		e.line = e.line[:0]
		e.overflow = false
	}
	return n, err
}
