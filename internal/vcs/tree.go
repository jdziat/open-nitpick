package vcs

import (
	"bytes"
	"context"
	"fmt"
	"path"
	"sort"
	"strings"
	"unicode/utf8"
)

// Tree is the provider behind `nitpick full-review`: the working tree read as
// one change that adds every file, so the engine that reviews a pull request
// reviews a repository with nothing else changed. Every line is a changed
// line, so analyzers run on the whole file, the model is shown the whole
// file, and a finding may sit anywhere in it.
//
// It wraps Local for everything but the diff, which it synthesizes from the
// files git knows about (tracked, or untracked and not ignored) under the
// paths asked for, in path order, leaving out what is not text, what is
// larger than MaxBytes, and, once Budget tokens have been spent, everything
// after. What it left out is recorded, because a review that covered half
// the tree must not read as a review of the tree.
type Tree struct {
	*Local

	// Paths restricts the review to files under these prefixes (or equal to
	// them). Empty means the whole tree.
	Paths []string

	// MaxBytes leaves out a file larger than this. Zero means no limit.
	MaxBytes int

	// Budget is a ceiling in estimated tokens (bytes over four) on what is
	// reviewed; files in path order are included until it is reached. Zero
	// means no ceiling.
	Budget int

	// Covered, Unbudgeted and Skipped are filled by Diff: the files in the
	// review, the files the budget stopped before, and the files left out
	// for what they are (binary, oversized), each with its reason.
	Covered    []string
	Unbudgeted []string
	Skipped    []TreeSkip
}

// TreeSkip is one file the tree review left out on purpose.
type TreeSkip struct {
	Path   string
	Reason string
}

// NewTree wraps a Local provider for a whole-tree review.
func NewTree(local *Local, paths []string) *Tree {
	clean := make([]string, 0, len(paths))
	for _, p := range paths {
		p = strings.Trim(path.Clean(strings.ReplaceAll(p, "\\", "/")), "/")
		if p == "." {
			p = ""
		}
		clean = append(clean, p)
	}
	return &Tree{Local: local, Paths: clean}
}

// Name identifies the provider in logs and the review header.
func (t *Tree) Name() string { return "tree" }

// Diff lists the tree and renders every included file as an addition.
func (t *Tree) Diff(ctx context.Context, ref Ref) ([]byte, error) {
	if ref.Head != Worktree {
		return nil, fmt.Errorf("a tree review reads the working tree; a revision cannot be reviewed whole")
	}
	names, err := t.list(ctx)
	if err != nil {
		return nil, err
	}

	t.Covered, t.Unbudgeted, t.Skipped = nil, nil, nil
	var out bytes.Buffer
	spent := 0
	for _, name := range names {
		if !t.wanted(name) {
			continue
		}
		if t.Budget > 0 && spent >= t.Budget {
			t.Unbudgeted = append(t.Unbudgeted, name)
			continue
		}
		content, err := t.readContained(name)
		if err != nil {
			t.Skipped = append(t.Skipped, TreeSkip{Path: name, Reason: "unreadable: " + err.Error()})
			continue
		}
		if t.MaxBytes > 0 && len(content) > t.MaxBytes {
			t.Skipped = append(t.Skipped, TreeSkip{Path: name, Reason: fmt.Sprintf("larger than %d bytes", t.MaxBytes)})
			continue
		}
		if len(content) == 0 {
			t.Skipped = append(t.Skipped, TreeSkip{Path: name, Reason: "empty"})
			continue
		}
		if !isText(content) {
			t.Skipped = append(t.Skipped, TreeSkip{Path: name, Reason: "binary"})
			continue
		}
		spent += len(content) / 4
		t.Covered = append(t.Covered, name)
		writeAddition(&out, name, content)
	}
	return out.Bytes(), nil
}

// list names the files git sees in the tree: tracked, plus untracked files
// that are not ignored, in path order.
func (t *Tree) list(ctx context.Context) ([]string, error) {
	raw, err := t.gitRaw(ctx, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return nil, fmt.Errorf("list the tree: %w", err)
	}
	seen := map[string]bool{}
	var names []string
	for name := range strings.SplitSeq(string(raw), "\x00") {
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

func (t *Tree) wanted(name string) bool {
	if len(t.Paths) == 0 {
		return true
	}
	for _, p := range t.Paths {
		if p == "" || name == p || strings.HasPrefix(name, p+"/") {
			return true
		}
	}
	return false
}

// isText refuses what a model cannot read: a NUL byte, or bytes that are not
// UTF-8.
func isText(b []byte) bool {
	return !bytes.ContainsRune(b, 0) && utf8.Valid(b)
}

// writeAddition renders content as a unified diff adding name.
func writeAddition(out *bytes.Buffer, name string, content []byte) {
	text := string(content)
	trailing := strings.HasSuffix(text, "\n")
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	fmt.Fprintf(out, "diff --git a/%s b/%s\nnew file mode 100644\n--- /dev/null\n+++ b/%s\n@@ -0,0 +1,%d @@\n", name, name, name, len(lines))
	for _, line := range lines {
		out.WriteString("+")
		out.WriteString(line)
		out.WriteString("\n")
	}
	if !trailing {
		out.WriteString("\\ No newline at end of file\n")
	}
}
