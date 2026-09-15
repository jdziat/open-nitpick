package flowhtml

import (
	"context"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/jdziat/open-nitpick/internal/prflow"
)

// sourceLink builds an evidence URL. An empty or unusable base yields an empty
// string, and the caller falls back to plain path:line text rather than
// emitting a link that points at the reader's own filesystem.
func sourceLink(base, filePath string, line int) string {
	if base == "" || filePath == "" || line < 1 {
		return ""
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return ""
	}
	if !safeRelativePath(filePath) {
		return ""
	}
	target := *parsed
	target.Path = strings.TrimRight(parsed.Path, "/") + "/" + filePath
	target.RawPath = ""
	target.Fragment = "L" + strconv.Itoa(line)
	return target.String()
}

// safeRelativePath rejects anything that could escape the repository root
// before it becomes part of a URL or a file read.
func safeRelativePath(name string) bool {
	if name == "" || path.IsAbs(name) || path.Clean(name) != name {
		return false
	}
	if name == ".." || strings.HasPrefix(name, "../") {
		return false
	}
	return !strings.ContainsAny(name, "\\\r\n\x00")
}

// attachSnippets reads each distinct source file once and attaches a bounded
// window around every node declared in it. It returns the total embedded bytes
// and whether the byte cap stopped it early, so the document can say that the
// source it shows is incomplete instead of appearing to show all of it.
func attachSnippets(ctx context.Context, nodes map[string]docNode, result prflow.Result, reader SourceReader, o Options) (int, bool) {
	type request struct {
		path     string
		revision prflow.Revision
	}
	// Group by file and revision: a removed declaration reads the base side and
	// a current one reads head, and the same path can appear on both.
	wanted := map[request][]string{}
	for id, node := range nodes {
		if node.Boundary || node.SourcePath == "" || node.SourceLine < 1 {
			continue
		}
		if !safeRelativePath(node.SourcePath) {
			continue
		}
		revision := result.Head
		if node.State == "removed" {
			revision = result.Base
		}
		key := request{path: node.SourcePath, revision: revision}
		wanted[key] = append(wanted[key], id)
	}
	keys := make([]request, 0, len(wanted))
	for key := range wanted {
		keys = append(keys, key)
	}
	sort.SliceStable(keys, func(i, j int) bool {
		if keys[i].path != keys[j].path {
			return keys[i].path < keys[j].path
		}
		return keys[i].revision.SHA < keys[j].revision.SHA
	})

	total, truncated := 0, false
	for _, key := range keys {
		if ctx.Err() != nil {
			return total, true
		}
		if total >= o.MaxSnippetBytes {
			truncated = true
			break
		}
		data, err := reader.ReadFile(ctx, key.revision, key.path)
		if err != nil {
			// A file that cannot be read leaves its nodes link-only. The
			// document already reports coverage, so this is not a silent drop.
			continue
		}
		total += len(data)
		if total > o.MaxSnippetBytes {
			truncated = true
			break
		}
		source := string(data)
		ids := append([]string(nil), wanted[key]...)
		sort.Strings(ids)
		for _, id := range ids {
			node := nodes[id]
			window := extractSnippet(key.path, source, node.SourceLine, o.SnippetBefore, o.SnippetAfter)
			node.Snippet = &window
			nodes[id] = node
		}
	}
	return total, truncated
}
