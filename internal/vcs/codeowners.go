package vcs

import (
	"bufio"
	"bytes"
	"regexp"
	"strings"
)

// CodeownersPaths are where a forge looks for the file, in the order GitHub
// uses: the first that exists is the whole file, they are not merged.
var CodeownersPaths = []string{".github/CODEOWNERS", "CODEOWNERS", "docs/CODEOWNERS"}

// Codeowners is a parsed CODEOWNERS file.
type Codeowners struct {
	rules []ownerRule
}

type ownerRule struct {
	re     *regexp.Regexp
	owners []string
}

// ParseCodeowners reads a CODEOWNERS file.
//
// A pattern that cannot be translated is skipped rather than failing the
// parse: the caller uses this to decide who may close a finding, and one
// unreadable line must not turn the whole file into "nobody owns anything",
// which would hand the decision back to the weaker association rule.
func ParseCodeowners(data []byte) *Codeowners {
	c := &Codeowners{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		pattern := fields[0]
		// GitHub does not support negation or character classes; reading them
		// as gitignore would assign owners the file never meant.
		if strings.HasPrefix(pattern, "!") || strings.ContainsAny(pattern, "[]") {
			continue
		}
		var owners []string
		for _, f := range fields[1:] {
			if strings.HasPrefix(f, "#") {
				break
			}
			owners = append(owners, f)
		}
		re, err := regexp.Compile(ownerPattern(pattern))
		if err != nil {
			continue
		}
		c.rules = append(c.rules, ownerRule{re: re, owners: owners})
	}
	return c
}

// Empty reports whether the file has no usable rule.
func (c *Codeowners) Empty() bool { return c == nil || len(c.rules) == 0 }

// OwnersOf returns the owners of a repository-relative path. The last rule
// that matches wins, and a winning rule with no owners means the path is
// deliberately unowned, so the result is nil in both that case and when no
// rule matches.
func (c *Codeowners) OwnersOf(path string) []string {
	if c == nil {
		return nil
	}
	path = strings.TrimPrefix(path, "/")
	for i := len(c.rules) - 1; i >= 0; i-- {
		if c.rules[i].re.MatchString(path) {
			return c.rules[i].owners
		}
	}
	return nil
}

// ownerPattern translates a CODEOWNERS pattern to a regular expression over a
// slash-separated path.
func ownerPattern(p string) string {
	p = strings.TrimPrefix(p, `\`)
	dirOnly := strings.HasSuffix(p, "/")
	body := strings.Trim(p, "/")
	// A slash at the start or in the middle anchors the pattern to the root;
	// without one it matches at any depth.
	anchored := strings.HasPrefix(p, "/") || strings.Contains(body, "/")

	var b strings.Builder
	b.WriteString("^")
	if !anchored {
		b.WriteString("(?:.*/)?")
	}
	for i := 0; i < len(body); i++ {
		switch {
		case strings.HasPrefix(body[i:], "**/"):
			b.WriteString("(?:.*/)?")
			i += 2
		case strings.HasPrefix(body[i:], "/**") && i+3 == len(body):
			b.WriteString("/.*")
			i += 2
		case strings.HasPrefix(body[i:], "**"):
			b.WriteString(".*")
			i++
		case body[i] == '*':
			b.WriteString("[^/]*")
		case body[i] == '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(body[i])))
		}
	}
	switch {
	case dirOnly:
		b.WriteString("/.*$")
	case strings.Contains(lastSegment(body), "*") && !strings.HasSuffix(body, "**"):
		// GitHub: "docs/*" owns docs/getting-started.md and not files nested
		// further down, so a wildcard in the last segment names files at that
		// depth only.
		b.WriteString("$")
	default:
		b.WriteString("(?:/.*)?$")
	}
	return b.String()
}

func lastSegment(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}
