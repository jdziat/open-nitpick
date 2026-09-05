package bundle

import (
	"path/filepath"
	"sort"
	"strings"
)

// Language names the language a path's extension implies, empty for one the
// map does not know. The names are the ones a route's `languages` matches on,
// so they are spelled out ("python", not "py") and lower-case.
func Language(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == "" {
		switch strings.ToLower(filepath.Base(path)) {
		case "dockerfile":
			return "docker"
		case "makefile", "gnumakefile":
			return "make"
		}
		return ""
	}
	return languages[ext]
}

// Languages returns the distinct languages of the paths, sorted.
func Languages(paths []string) []string {
	seen := map[string]bool{}
	for _, p := range paths {
		if l := Language(p); l != "" {
			seen[l] = true
		}
	}
	out := make([]string, 0, len(seen))
	for l := range seen {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

var languages = map[string]string{
	".go": "go",
	".py": "python", ".pyi": "python",
	".js": "javascript", ".mjs": "javascript", ".cjs": "javascript", ".jsx": "javascript",
	".ts": "typescript", ".tsx": "typescript", ".mts": "typescript", ".cts": "typescript",
	".rb": "ruby", ".rake": "ruby",
	".php":  "php",
	".java": "java",
	".kt":   "kotlin", ".kts": "kotlin",
	".rs":    "rust",
	".cs":    "csharp",
	".swift": "swift",
	".scala": "scala",
	".c":     "c", ".h": "c",
	".cc": "cpp", ".cpp": "cpp", ".cxx": "cpp", ".hpp": "cpp", ".hh": "cpp",
	".sh": "shell", ".bash": "shell", ".zsh": "shell",
	".sql":  "sql",
	".yaml": "yaml", ".yml": "yaml",
	".json": "json",
	".toml": "toml",
	".tf":   "terraform", ".hcl": "terraform",
	".proto": "protobuf",
	".md":    "markdown",
	".lua":   "lua",
	".dart":  "dart",
	".ex":    "elixir", ".exs": "elixir",
}
