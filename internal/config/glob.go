package config

import (
	"path"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// matchGlob reports whether a repository-relative path matches a pattern.
//
// Patterns use doublestar semantics, so ** spans directory separators. Two
// conveniences make hand-written config behave the way people expect:
//
//   - A bare pattern with no separator (for example "*.go") matches at any
//     depth, because "*.go" reading as root-only surprises everyone.
//   - A pattern ending in "/" matches everything beneath that directory.
func matchGlob(pattern, target string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return false
	}

	target = normalizePath(target)

	if strings.HasSuffix(pattern, "/") {
		pattern += "**"
	}
	pattern = normalizePath(pattern)

	if ok, err := doublestar.Match(pattern, target); err == nil && ok {
		return true
	}

	// A separator-free pattern is treated as a basename match at any depth.
	if !strings.Contains(pattern, "/") {
		if ok, err := doublestar.Match(pattern, path.Base(target)); err == nil && ok {
			return true
		}
	}

	return false
}

// normalizePath converts a path to the forward-slash, unrooted form that
// patterns are written against.
func normalizePath(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	p = strings.TrimPrefix(p, "./")
	return strings.TrimPrefix(p, "/")
}

// validGlob reports whether a pattern is syntactically usable.
func validGlob(pattern string) bool {
	return doublestar.ValidatePattern(normalizePath(strings.TrimSuffix(pattern, "/")))
}
