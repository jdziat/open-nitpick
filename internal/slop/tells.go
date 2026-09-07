// Package slop finds the tells of generated prose without a model: the
// habits a reader can check on the page, each with a fix. It is the
// deterministic half of `nitpick slop`; the model's nine slop rules are the
// other half and run through the review engine.
package slop

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

// Tell is one occurrence of a rule at a line.
type Tell struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Rule    string `json:"rule"`
	Excerpt string `json:"excerpt"`
	Fix     string `json:"fix"`
}

// Rule names what a tell is and how to fix it, so the report can group by
// it and a reader can act on the group rather than the occurrence.
type Rule struct {
	Name string
	What string
	Fix  string
}

// Rules are the tells, in report order.
var Rules = []Rule{
	{"em-dash", "an em dash in prose", "rewrite the sentence with a comma, a colon, parentheses, or a full stop; never a bare hyphen"},
	{"en-dash-separator", "an en dash used as a clause separator", "a comma or a colon; keep en dashes only inside numeric ranges"},
	{"arrow-in-prose", "an arrow (→ or ->) standing in for a word", "write the word: then, becomes, to, gives"},
	{"filler-qualifier", "a qualifier that adds nothing (genuinely, honestly, actually, truly, simply, crucially, importantly)", "delete the word; if the sentence loses meaning, say the meaning"},
	{"chat-prose", "a comment written as a chat reply (Sure!, Here's, Note that, hope this helps)", "delete it, or state the fact the reply was wrapping"},
	{"restating-comment", "a comment that restates the line below it", "delete it, or say why the line is there rather than what it does"},
	{"oversized-doc-comment", "a doc comment longer than the declaration it documents", "keep the one sentence a caller needs; move the rest to a design note or delete it"},
	{"triplet-rhythm", "three parallel adjectives or nouns in a row (fast, reliable, and secure)", "keep the one that is true and specific; the other two are padding"},
	{"antithesis", "a sentence that sets up a contrast to sound decisive (not a nicety, it is a correctness matter)", "state the second half only; the negated half was never the claim"},
	{"prose-cadence", "a whole file written in one rhythm: appositive tails, colon expansions and three-part lists, above 10 per 100 lines", "vary the sentences. Split the longest into two, and let some of them end where the fact ends"},
}

var (
	emDash  = regexp.MustCompile(`—`)
	enDash  = regexp.MustCompile(`\s–\s`)
	arrow   = regexp.MustCompile(`(\s|^)(→|->)(\s|$)`)
	filler  = regexp.MustCompile(`(?i)\b(genuinely|honestly|actually|truly|simply|crucially|importantly|it'?s worth noting( that)?)\b`)
	chat    = regexp.MustCompile(`(?i)\b(sure!|here'?s (the|a|an|your)|note that this|hope this helps|let me know if|as an ai|i hope this|great question)\b`)
	triplet = regexp.MustCompile(`(?i)\b(\w+), (\w+),? and (\w+)\b`)

	// antithesis is the shape "it is not X, it is Y" and "not a X, but a Y".
	// It reads as decisive and carries only the second half, since the first
	// half is a claim nobody made.
	antithesis = regexp.MustCompile(`(?i)\b(is|was|are|were)\s+not\s+(a|an|the)?[^,.;:!?]{2,50},\s*(it|they|that)\s+(is|are|was|were)\b|\bnot\s+(a|an)\s[^,.;:!?]{2,40},\s*but\s+(a|an)\b`)

	// The three components of the cadence rule. None is a fault on its own,
	// which is why they are counted over a file rather than flagged on a line:
	// one appositive tail is a sentence, forty of them is a voice.
	cadenceTriplet = regexp.MustCompile(`[^.;:!?]{4,}?,[^.;:!?]{4,}?, and [^.;:!?]{3,}`)
	cadenceColon   = regexp.MustCompile(`\b(is|are|was|were|means|says)\b[^.;!?]{0,40}:\s+[a-z]`)
	cadenceTail    = regexp.MustCompile(`,\s+which is [^.;!?]{5,}`)
	identRe        = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
	declRe         = regexp.MustCompile(`^\s*(func |def |class |type |export |function |const |let |var |public |private |protected |static |async )`)
	stopword       = map[string]bool{"the": true, "and": true, "for": true, "this": true, "that": true, "with": true, "from": true, "into": true, "then": true, "returns": true, "return": true, "a": true, "an": true, "of": true, "to": true, "is": true}
)

// Scan finds the tells in one file. Prose files (Markdown, plain text) are
// scanned whole; source files are scanned in their comments only, so a
// string literal or a code line is never a tell.
func Scan(p, content string) []Tell {
	var out []Tell
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	prose, comment := kind(p)
	inFence := false
	var block []int // comment lines gathered for the two multi-line rules
	flush := func(next string) {
		if len(block) > 0 {
			out = append(out, commentRules(p, lines, block, next, comment)...)
		}
		block = nil
	}
	for i, raw := range lines {
		n := i + 1
		var text string
		if prose {
			t := strings.TrimSpace(raw)
			if strings.HasPrefix(t, "```") {
				inFence = !inFence
				continue
			}
			if inFence || strings.HasPrefix(t, "|") || strings.HasPrefix(raw, "    ") || strings.HasPrefix(raw, "\t") {
				continue // fenced code, tables, indented code
			}
			text = stripInlineCode(raw)
		} else {
			c, ok := commentText(raw, comment)
			if !ok {
				flush(raw)
				// A comment after code on the same line carries the line
				// rules too; the marker with a space on each side keeps a
				// URL's // and a shell's $# out of it.
				if comment != "" {
					if idx := strings.Index(raw, " "+comment+" "); idx >= 0 {
						out = append(out, lineRules(p, n, raw[idx+len(comment)+2:])...)
					}
				}
				continue
			}
			block = append(block, i)
			text = c
		}
		out = append(out, lineRules(p, n, text)...)
	}
	flush("")
	if prose {
		out = append(out, cadence(p, lines)...)
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].Line != out[b].Line {
			return out[a].Line < out[b].Line
		}
		return out[a].Rule < out[b].Rule
	})
	return out
}

// cadenceLimit is how many cadence markers per hundred prose lines stop being
// sentences and start being a voice.
//
// It is a threshold rather than a per-line rule because every component is
// ordinary English. The number comes from this repository's own documentation
// at the point a reader called it out as machine-written: the front page was at
// 15.9 and the trust model at 16.4, while the pages nobody complained about sat
// between 6 and 9. Ten is the gap between them.
const cadenceLimit = 10.0

// cadence measures a prose file's sentence rhythm as a whole.
//
// The per-line rules above cannot see this. They found ONE tell in 5,080 lines
// of documentation that a reader identified as machine-written on sight, which
// is the instrument bug this rule exists to close: the tells are not words, they
// are the same sentence shape arriving over and over.
func cadence(p string, lines []string) []Tell {
	var prose, hits int
	inFence := false
	for _, raw := range lines {
		t := strings.TrimSpace(raw)
		if strings.HasPrefix(t, "```") {
			inFence = !inFence
			continue
		}
		if inFence || t == "" || strings.HasPrefix(t, "|") || strings.HasPrefix(t, "#") ||
			strings.HasPrefix(raw, "    ") || strings.HasPrefix(raw, "\t") {
			continue
		}
		prose++
		// Outside inline code, the same as every per-line rule. A config key
		// written `a, b, and c` is a value, not a cadence, and counting it
		// would score a reference page by how many options it documents.
		t = stripInlineCode(t)
		hits += len(cadenceTriplet.FindAllString(t, -1))
		hits += len(cadenceColon.FindAllString(t, -1))
		hits += len(cadenceTail.FindAllString(t, -1))
	}

	// Too short to have a rhythm. A three-line file with one colon is not a
	// voice, and scoring it as one would make the rule noise.
	if prose < 40 {
		return nil
	}

	per := float64(hits) / float64(prose) * 100
	if per <= cadenceLimit {
		return nil
	}

	return []Tell{{
		Path: p, Line: 1, Rule: "prose-cadence",
		Excerpt: fmt.Sprintf("%d cadence markers over %d prose lines, %.1f per 100 against a limit of %.0f",
			hits, prose, per, cadenceLimit),
		Fix: fixFor("prose-cadence"),
	}}
}

// lineRules are the tells one line shows on its own.
func lineRules(p string, n int, text string) []Tell {
	var out []Tell
	add := func(rule string, loc []int) {
		if loc == nil {
			return
		}
		out = append(out, Tell{Path: p, Line: n, Rule: rule, Excerpt: excerpt(text, loc[0]), Fix: fixFor(rule)})
	}
	add("em-dash", emDash.FindStringIndex(text))
	if loc := enDash.FindStringIndex(text); loc != nil && !numericRange(text, loc) {
		add("en-dash-separator", loc)
	}
	add("arrow-in-prose", arrow.FindStringIndex(text))
	add("filler-qualifier", filler.FindStringIndex(text))
	add("chat-prose", chat.FindStringIndex(text))
	add("antithesis", antithesis.FindStringIndex(text))
	if m := triplet.FindStringSubmatchIndex(text); m != nil {
		a, b, c := strings.ToLower(text[m[2]:m[3]]), strings.ToLower(text[m[4]:m[5]]), strings.ToLower(text[m[6]:m[7]])
		// A list of three nouns (tools, files, names) is a list; the tell
		// is three adjectives of praise, which the suffix and the short
		// list below catch and a list of tool names does not.
		if adjective(a) && adjective(b) && adjective(c) && a != b && b != c {
			add("triplet-rhythm", m[:2])
		}
	}
	return out
}

var praise = map[string]bool{"fast": true, "safe": true, "clean": true, "simple": true, "robust": true, "secure": true, "modern": true, "easy": true, "quick": true, "smart": true, "rich": true, "lightweight": true, "seamless": true, "elegant": true, "intuitive": true, "transparent": true, "consistent": true, "efficient": true, "resilient": true}

func adjective(w string) bool {
	if praise[w] {
		return true
	}
	for _, suf := range []string{"able", "ible", "ive", "ous", "ful", "less", "ent", "ant", "ic"} {
		if strings.HasSuffix(w, suf) && len(w) > len(suf)+2 {
			return true
		}
	}
	return false
}

// commentRules are the tells a comment block shows against the code after
// it: a comment that restates its line, and a doc comment longer than the
// declaration it documents.
func commentRules(p string, lines []string, block []int, next, comment string) []Tell {
	var out []Tell
	last := block[len(block)-1]
	if last+1 < len(lines) {
		code := lines[last+1]
		c, _ := commentText(lines[last], comment)
		if restates(c, code) && !declRe.MatchString(code) {
			out = append(out, Tell{Path: p, Line: last + 1, Rule: "restating-comment", Excerpt: excerpt(strings.TrimSpace(lines[last]), 0), Fix: fixFor("restating-comment")})
		}
	}
	if declRe.MatchString(next) && len(block) >= 8 {
		// The declaration's own length: lines to the closing brace at the
		// same indent, or to the next blank line for languages without one.
		body := declLength(lines, last+1)
		if body > 0 && len(block) > body {
			out = append(out, Tell{Path: p, Line: block[0] + 1, Rule: "oversized-doc-comment", Excerpt: fmt.Sprintf("%d comment lines over a %d-line declaration", len(block), body), Fix: fixFor("oversized-doc-comment")})
		}
	}
	return out
}

func declLength(lines []string, start int) int {
	if start >= len(lines) {
		return 0
	}
	indent := leading(lines[start])
	for i := start + 1; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if t == "" && !strings.Contains(lines[start], "{") {
			return i - start
		}
		if (t == "}" || strings.HasPrefix(t, "}")) && leading(lines[i]) == indent {
			return i - start + 1
		}
	}
	return len(lines) - start
}

func leading(s string) int { return len(s) - len(strings.TrimLeft(s, " \t")) }

var numberEdge = regexp.MustCompile(`[0-9%$.]`)

// numericRange reports whether the en dash at loc sits between two numbers
// ("0.21 – 0.38", "$3 – $5"), which is a range and not a separator.
func numericRange(text string, loc []int) bool {
	before := strings.TrimSpace(text[:loc[0]])
	after := strings.TrimSpace(text[loc[1]:])
	return before != "" && after != "" && numberEdge.MatchString(before[len(before)-1:]) && numberEdge.MatchString(after[:1])
}

// restates reports whether most of a comment's words appear in the code
// line under it.
func restates(comment, code string) bool {
	codeLower := strings.ToLower(code)
	hits, words := 0, 0
	for _, w := range identRe.FindAllString(strings.ToLower(comment), -1) {
		if len(w) <= 2 || stopword[w] {
			continue
		}
		words++
		if strings.Contains(codeLower, w) {
			hits++
		}
	}
	return words >= 2 && float64(hits)/float64(words) >= 0.5
}

// kind says whether a file is prose and, if not, what starts a comment.
func kind(p string) (prose bool, comment string) {
	switch strings.ToLower(path.Ext(p)) {
	case ".md", ".markdown", ".txt", ".rst":
		return true, ""
	case ".py", ".rb", ".sh", ".bash", ".yaml", ".yml", ".toml", ".r", ".pl":
		return false, "#"
	case ".go", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".java", ".kt", ".kts", ".rs", ".c", ".cc", ".cpp", ".h", ".hpp", ".cs", ".swift", ".scala", ".php", ".dart", ".proto":
		return false, "//"
	}
	return false, ""
}

// commentText returns a line's comment text when the line is a comment in
// the language whose comment marker is given: the marker itself, and for
// the // languages the block forms (/*, *, ///, //!); for the # languages
// the docstring quotes. A bare * is a comment only where /* */ exists.
func commentText(raw, comment string) (string, bool) {
	t := strings.TrimSpace(raw)
	prefixes := []string{"//", "///", "//!", "/*", "*"}
	if comment == "#" {
		prefixes = []string{"#", "\"\"\"", "'''"}
	}
	for _, prefix := range prefixes {
		if rest, ok := strings.CutPrefix(t, prefix); ok {
			return strings.TrimSpace(rest), true
		}
	}
	return "", false
}

var inlineCode = regexp.MustCompile("`[^`]*`")

func stripInlineCode(s string) string { return inlineCode.ReplaceAllString(s, "``") }

func excerpt(text string, at int) string {
	start := max(0, at-30)
	end := min(len(text), at+50)
	e := strings.TrimSpace(text[start:end])
	if start > 0 {
		e = "..." + e
	}
	if end < len(text) {
		e += "..."
	}
	return e
}

func fixFor(rule string) string {
	for _, r := range Rules {
		if r.Name == rule {
			return r.Fix
		}
	}
	return ""
}

// Summary counts tells by rule, in Rules order, dropping empty rules.
func Summary(tells []Tell) []RuleCount {
	counts := map[string]int{}
	for _, t := range tells {
		counts[t.Rule]++
	}
	var out []RuleCount
	for _, r := range Rules {
		if n := counts[r.Name]; n > 0 {
			out = append(out, RuleCount{Rule: r, Count: n})
		}
	}
	return out
}

// RuleCount is one rule's tally.
type RuleCount struct {
	Rule  Rule
	Count int
}
