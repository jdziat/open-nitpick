package vcs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
)

// ErrSourceLimit reports a file larger than the caller's remaining budget.
var ErrSourceLimit = errors.New("vcs: source exceeds byte limit")

// LimitedFileProvider can read source without allocating an entire large file.
type LimitedFileProvider interface {
	FileContentLimit(context.Context, Ref, string, int) ([]byte, error)
}

// FileContentLimit reads at most limit bytes and rejects larger files.
func (l *Local) FileContentLimit(ctx context.Context, ref Ref, name string, limit int) ([]byte, error) {
	if limit <= 0 {
		return nil, ErrSourceLimit
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if ref.Head == Worktree {
		data, err := l.readContainedLimit(name, int64(limit)+1)
		if len(data) > limit {
			return nil, ErrSourceLimit
		}
		return data, err
	}
	cmd := exec.CommandContext(ctx, "git", "show", "--no-ext-diff", "--no-textconv", ref.Head+":"+name)
	cmd.Dir = l.Dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(stdout, int64(limit)+1))
	if len(data) > limit || readErr != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if len(data) > limit {
		return nil, ErrSourceLimit
	}
	if readErr != nil {
		return nil, readErr
	}
	if waitErr != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%s at %s: %w", name, ref.Head, ErrNotFound)
	}
	return data, nil
}

// FileContentLimit refuses oversized GitHub files before downloading them.
func (g *GitHub) FileContentLimit(ctx context.Context, ref Ref, name string, limit int) ([]byte, error) {
	if limit <= 0 {
		return nil, ErrSourceLimit
	}
	return g.fileContent(ctx, ref, name, limit)
}
