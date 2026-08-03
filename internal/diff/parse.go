package diff

import (
	"bufio"
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// maxLineBytes bounds the scanner so a minified bundle in a diff cannot
// exhaust memory.
const maxLineBytes = 4 * 1024 * 1024

// Parse reads a unified diff, as produced by `git diff` or GitHub's diff
// media type, into files and hunks.
//
// Malformed hunk headers are an error: silently mis-parsing them would place
// review comments on wrong lines, which is the failure mode most likely to
// destroy trust in the tool.
func Parse(data []byte) (Files, error) {
	var (
		files   Files
		current *File
		hunk    *Hunk

		// position counts diff lines within the current file, as GitHub's
		// legacy position anchor defines it: the line directly below the
		// file's *first* @@ header is position 1. Subsequent hunk headers,
		// blank lines, and "\ No newline" markers all consume a position, so
		// the count must follow raw diff lines rather than parsed content.
		position int

		// seenHunk marks that the file's first @@ header has been consumed,
		// which is what makes that first header the zero point.
		seenHunk bool

		oldLine, newLine int

		// remainingOld and remainingNew count the lines the current hunk
		// header promised. While either is outstanding the hunk is still open
		// and every line is content, which is what stops a line whose own text
		// begins with "++ " or "-- " from being read as a file header.
		remainingOld, remainingNew int
	)

	// hunkOpen reports whether we are inside a hunk that still owes lines.
	hunkOpen := func() bool {
		return hunk != nil && (remainingOld > 0 || remainingNew > 0)
	}

	// flush attaches the in-progress hunk to the current file.
	flush := func() {
		if current != nil && hunk != nil {
			current.Hunks = append(current.Hunks, *hunk)
		}
		hunk = nil
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineBytes)

	for lineNo := 1; scanner.Scan(); lineNo++ {
		line := scanner.Text()

		switch {
		// While a hunk still owes lines, EVERY line is content. This arm must
		// come first: an added line whose own text starts with "++ " renders
		// as "+++ ...", and a removed "-- " renders as "--- ...", either of
		// which would otherwise be parsed as a file header — silently
		// replacing the file's path and shifting every later line number.
		case hunkOpen():
			position++

			// git emits a bare empty line for an empty context line, so the
			// marker byte can be absent entirely.
			marker := byte(' ')
			if line != "" {
				marker = line[0]
			}

			switch marker {
			case '+':
				hunk.Lines = append(hunk.Lines, Line{
					Kind: LineAdded, Content: line[1:], NewLine: newLine, Position: position,
				})
				newLine++
				remainingNew--

			case '-':
				hunk.Lines = append(hunk.Lines, Line{
					Kind: LineRemoved, Content: line[1:], OldLine: oldLine, Position: position,
				})
				oldLine++
				remainingOld--

			case ' ':
				content := ""
				if line != "" {
					content = line[1:]
				}
				hunk.Lines = append(hunk.Lines, Line{
					Kind: LineContext, Content: content,
					OldLine: oldLine, NewLine: newLine, Position: position,
				})
				oldLine++
				newLine++
				remainingOld--
				remainingNew--

			case '\\':
				// "\ No newline at end of file" annotates the preceding line.
				// It owes nothing to the hunk's counts but is a physical line,
				// so it still consumes a position.

			default:
				// An unrecognized marker inside a hunk means the input is not
				// the unified diff we think it is. Guessing risks misplaced
				// comments, so stop.
				return nil, fmt.Errorf("line %d: unexpected diff line %q", lineNo, truncate(line, 60))
			}

		case strings.HasPrefix(line, "diff --git "):
			flush()
			current = &File{Kind: ChangeModified}
			files = append(files, current)
			position, seenHunk = 0, false
			remainingOld, remainingNew = 0, 0

			if old, new, ok := parseGitHeader(line); ok {
				current.OldPath, current.Path = old, new
			}

		case current == nil:
			// Preamble before the first file header (commit message, index
			// lines from `git show`) carries nothing we need.
			continue

		case strings.HasPrefix(line, "similarity index "),
			strings.HasPrefix(line, "index "),
			strings.HasPrefix(line, "old mode "),
			strings.HasPrefix(line, "new mode "),
			strings.HasPrefix(line, "deleted file mode "),
			strings.HasPrefix(line, "new file mode "):
			// Metadata lines that precede the hunks.
			if strings.HasPrefix(line, "new file mode ") {
				current.Kind = ChangeAdded
			}
			if strings.HasPrefix(line, "deleted file mode ") {
				current.Kind = ChangeDeleted
			}

		case strings.HasPrefix(line, "rename from "):
			current.Kind = ChangeRenamed
			current.OldPath = strings.TrimPrefix(line, "rename from ")

		case strings.HasPrefix(line, "rename to "):
			current.Kind = ChangeRenamed
			current.Path = strings.TrimPrefix(line, "rename to ")

		case strings.HasPrefix(line, "Binary files "),
			strings.HasPrefix(line, "GIT binary patch"):
			current.Binary = true

		case strings.HasPrefix(line, "--- "):
			if p, ok := parseFileLine(line, "--- "); ok && p != "" {
				current.OldPath = p
			}

		case strings.HasPrefix(line, "+++ "):
			if p, ok := parseFileLine(line, "+++ "); ok && p != "" {
				current.Path = p
			}

		case strings.HasPrefix(line, "@@"):
			flush()

			h, err := parseHunkHeader(line)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", lineNo, err)
			}
			hunk = h
			oldLine, newLine = h.OldStart, h.NewStart
			remainingOld, remainingNew = h.OldLines, h.NewLines

			// The first header is the zero point; every later one is just
			// another line in the diff and shifts positions by one.
			if seenHunk {
				position++
			}
			seenHunk = true

		case hunk != nil && strings.HasPrefix(line, "\\"):
			// A trailing "\ No newline at end of file" after the hunk's line
			// budget is exhausted. It produces no Line but is a physical line
			// in the diff, so it still consumes a position.
			position++

		default:
			// Anything else outside a hunk (a trailing signature, an unknown
			// extended header) carries nothing we need.
			continue
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read diff: %w", err)
	}
	flush()

	for _, f := range files {
		normalizePaths(f)
	}
	return files, nil
}

// parseGitHeader extracts both paths from a `diff --git a/x b/y` line.
//
// Paths containing spaces are ambiguous in this line, so it is treated as a
// hint only; the ---/+++ lines that follow are authoritative and overwrite it.
func parseGitHeader(line string) (oldPath, newPath string, ok bool) {
	rest := strings.TrimPrefix(line, "diff --git ")

	// Quoted paths appear when they contain unusual bytes.
	if strings.HasPrefix(rest, `"`) {
		return "", "", false
	}

	fields := strings.Fields(rest)
	if len(fields) != 2 {
		return "", "", false
	}
	return stripPathPrefix(fields[0]), stripPathPrefix(fields[1]), true
}

// parseFileLine extracts the path from a --- or +++ line.
func parseFileLine(line, prefix string) (string, bool) {
	rest := strings.TrimPrefix(line, prefix)

	// /dev/null marks a creation or deletion rather than a real path.
	if rest == "/dev/null" {
		return "", true
	}

	// git may append a tab and timestamp.
	if tab := strings.IndexByte(rest, '\t'); tab >= 0 {
		rest = rest[:tab]
	}

	if unquoted, err := strconv.Unquote(rest); err == nil {
		rest = unquoted
	}

	return stripPathPrefix(rest), true
}

// stripPathPrefix removes git's a/ and b/ path prefixes.
func stripPathPrefix(p string) string {
	for _, prefix := range []string{"a/", "b/"} {
		if strings.HasPrefix(p, prefix) {
			return p[len(prefix):]
		}
	}
	return p
}

// parseHunkHeader parses `@@ -old,count +new,count @@ optional header`.
// A missing count means 1, per the unified diff format.
func parseHunkHeader(line string) (*Hunk, error) {
	const marker = "@@"

	rest := strings.TrimPrefix(line, marker)
	end := strings.Index(rest, marker)
	if end < 0 {
		return nil, fmt.Errorf("malformed hunk header %q", truncate(line, 60))
	}

	ranges := strings.Fields(rest[:end])
	if len(ranges) != 2 || !strings.HasPrefix(ranges[0], "-") || !strings.HasPrefix(ranges[1], "+") {
		return nil, fmt.Errorf("malformed hunk ranges in %q", truncate(line, 60))
	}

	oldStart, oldCount, err := parseRange(ranges[0][1:])
	if err != nil {
		return nil, fmt.Errorf("hunk header %q: old range: %w", truncate(line, 60), err)
	}
	newStart, newCount, err := parseRange(ranges[1][1:])
	if err != nil {
		return nil, fmt.Errorf("hunk header %q: new range: %w", truncate(line, 60), err)
	}

	return &Hunk{
		OldStart: oldStart, OldLines: oldCount,
		NewStart: newStart, NewLines: newCount,
		Header: strings.TrimSpace(rest[end+len(marker):]),
	}, nil
}

// parseRange parses "start" or "start,count".
func parseRange(s string) (start, count int, err error) {
	before, after, hasCount := strings.Cut(s, ",")

	start, err = strconv.Atoi(before)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid start %q", before)
	}

	if !hasCount {
		return start, 1, nil
	}

	count, err = strconv.Atoi(after)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid count %q", after)
	}
	return start, count, nil
}

// normalizePaths fills in whichever path side the header did not provide, so
// every file ends up with a usable Path.
func normalizePaths(f *File) {
	switch {
	case f.Path == "" && f.OldPath != "":
		// Deletion: the new side is /dev/null.
		f.Path = f.OldPath
		if f.Kind == ChangeModified {
			f.Kind = ChangeDeleted
		}
	case f.OldPath == "" && f.Path != "":
		if f.Kind == ChangeModified {
			f.Kind = ChangeAdded
		}
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
