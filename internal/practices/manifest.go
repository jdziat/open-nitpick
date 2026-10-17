package practices

import (
	"path"
	"strings"

	"github.com/jdziat/open-nitpick/internal/diff"
)

// ManifestChangesPackages reports whether a changed go.mod or go.work can
// alter which packages exist or how they import one another. A dependency
// bump rewrites require lines and nothing else, so planning every declaration
// in the module for it produced design tasks too large to fit a request and
// no finding about the bump. Module identity, replace directives and workspace
// membership do move package edges, and an added, deleted or renamed manifest
// moves a module boundary. A change with no hunks is treated as one of those
// because nothing shows it is not.
func ManifestChangesPackages(file *diff.File) bool {
	if file == nil {
		return false
	}
	switch path.Base(file.Path) {
	case "go.work":
		return true
	case "go.mod":
	default:
		return false
	}
	if file.Kind != diff.ChangeModified || file.Binary || len(file.Hunks) == 0 {
		return true
	}
	for _, hunk := range file.Hunks {
		for _, line := range hunk.Lines {
			if line.Kind == diff.LineContext {
				continue
			}
			text := strings.TrimSpace(line.Content)
			if strings.HasPrefix(text, "module") || strings.HasPrefix(text, "replace") || strings.Contains(text, "=>") {
				return true
			}
		}
	}
	return false
}
