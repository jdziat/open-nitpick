package vcs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jdziat/open-nitpick/internal/commits"
)

// CommitRange pins the ancestry used to enumerate commit-policy targets.
type CommitRange struct {
	BaseSHA string           `json:"base_sha"`
	HeadSHA string           `json:"head_sha"`
	Commits []commits.Commit `json:"commits"`
}

// CommitSHA resolves a commit object without interpreting a ref as an option.
func (l *Local) CommitSHA(ctx context.Context, ref string) (string, error) {
	if strings.TrimSpace(ref) == "" {
		return "", errors.New("a commit revision is required")
	}
	out, err := l.git(ctx, "--no-replace-objects", "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// PolicyFile reads a regular file from pinned history without following links
// or treating Git failures as evidence that no accepted policy exists.
func (l *Local) PolicyFile(ctx context.Context, sha, path string) ([]byte, error) {
	resolved, err := l.CommitSHA(ctx, sha)
	if err != nil {
		return nil, err
	}
	out, err := l.gitRaw(ctx, "--no-replace-objects", "ls-tree", "-z", resolved, "--", ":(literal)"+path)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, ErrNotFound
	}
	header, name, ok := bytes.Cut(bytes.TrimSuffix(out, []byte{0}), []byte{'\t'})
	fields := strings.Fields(string(header))
	if !ok || string(name) != path || len(fields) != 3 || fields[1] != "blob" || (fields[0] != "100644" && fields[0] != "100755") {
		return nil, errors.New("accepted policy must be a regular file, not a link or directory")
	}
	return l.gitRaw(ctx, "--no-replace-objects", "cat-file", "blob", fields[2])
}

// Commits enumerates head's commits absent from base, including side branches.
// Shallow history is rejected because missing parents can turn ordinary commits
// into apparent roots and silently exclude policy targets.
func (l *Local) Commits(ctx context.Context, base, head string) (CommitRange, error) {
	var result CommitRange
	shallow, err := l.git(ctx, "rev-parse", "--is-shallow-repository")
	if err != nil {
		return result, err
	}
	if strings.TrimSpace(shallow) != "false" {
		return result, errors.New("commit coverage requires complete Git history; shallow repository detected")
	}
	result.BaseSHA, err = l.CommitSHA(ctx, base)
	if err != nil {
		return result, fmt.Errorf("resolve commit base: %w", err)
	}
	result.HeadSHA, err = l.CommitSHA(ctx, head)
	if err != nil {
		return result, fmt.Errorf("resolve commit head: %w", err)
	}
	out, err := l.gitRaw(ctx, "--no-replace-objects", "log", "-z", "--no-show-signature", "--no-notes", "--format=%H%x00%P%x00%s", result.BaseSHA+".."+result.HeadSHA, "--")
	if err != nil {
		return result, err
	}
	if len(out) == 0 {
		result.Commits = []commits.Commit{}
		return result, nil
	}
	fields := bytes.Split(bytes.TrimSuffix(out, []byte{0}), []byte{0})
	if len(fields)%3 != 0 {
		return result, errors.New("malformed commit metadata; cannot establish range coverage")
	}
	for i := 0; i < len(fields); i += 3 {
		result.Commits = append(result.Commits, commits.Commit{SHA: string(fields[i]), ParentCount: len(strings.Fields(string(fields[i+1]))), Subject: string(fields[i+2])})
	}
	return result, nil
}
