package modelid

import (
	"math"
	"regexp"
	"strings"
)

// Features are cheap, language-agnostic measurements of a file's shape: how
// it comments, names, wraps, nests and handles errors. They are what a
// reviewer might notice without reading for meaning, which is the only kind
// of signature a model could leave that survives a human edit. Every feature
// is a rate or a mean, so file length does not dominate.
type Features [featureCount]float64

const featureCount = 22

// FeatureNames name each index, for the report.
var FeatureNames = [featureCount]string{
	"comment lines per line", "blank lines per line", "mean comment length", "restating comments per comment",
	"chat phrases per comment", "mean line length", "line length spread", "long lines per line",
	"mean identifier length", "snake_case share", "camelCase share", "single-letter share",
	"max nesting depth", "mean nesting depth", "functions per 100 lines", "mean function length",
	"error checks per 100 lines", "swallowed errors per check", "docstring or doc comment share", "distinct token ratio",
	"trailing whitespace per line", "todo or note markers per 100 lines",
}

var (
	identRe   = regexp.MustCompile(`\b[A-Za-z_][A-Za-z0-9_]*\b`)
	tokenRe   = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*|\d+|[^\sA-Za-z0-9_]`)
	chatRe    = regexp.MustCompile(`(?i)\b(sure|here'?s|note that|this function will|hope this helps|let'?s|we (can|will|need)|you can)\b`)
	todoRe    = regexp.MustCompile(`(?i)\b(todo|fixme|note:|xxx)\b`)
	funcRe    = regexp.MustCompile(`(?m)^\s*(func\s|def\s|function\s|(export\s+)?(async\s+)?function\b|[A-Za-z_$][\w$]*\s*=\s*(async\s*)?\([^)]*\)\s*=>|(public|private|protected|static|async)\s+[A-Za-z_$][\w$]*\s*\()`)
	errCheck  = regexp.MustCompile(`(?m)^\s*(if err != nil|except\b|catch\b|\.catch\()`)
	swallowRe = regexp.MustCompile(`(?m)^\s*(except[^:]*:\s*(pass|\.\.\.)\s*$|except[^:]*:\s*$\n\s*pass\b|catch\s*(\([^)]*\))?\s*\{\s*\}|_ = err\b|if err != nil \{\s*\}$)`)
	docRe     = regexp.MustCompile(`(?m)^\s*(///|/\*\*|"""|'''|// [A-Z][A-Za-z]+ )`)
)

// Extract measures one file. lang selects the comment syntax.
func Extract(content, lang string) Features {
	var f Features
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	n := float64(max(len(lines), 1))

	commentPrefix := "//"
	if lang == "python" {
		commentPrefix = "#"
	}

	var (
		comments, blanks, commentLen, restating, chat, longLines, trailing int
		lineLens                                                           []float64
		depths                                                             []float64
		maxDepth                                                           int
		prevWasComment                                                     bool
		prevComment                                                        string
	)
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" {
			blanks++
			prevWasComment = false
			continue
		}
		if strings.HasSuffix(line, " ") || strings.HasSuffix(line, "\t") {
			trailing++
		}
		lineLens = append(lineLens, float64(len(line)))
		if len(line) > 100 {
			longLines++
		}
		depth := (len(line) - len(strings.TrimLeft(line, " \t"))) / indentUnit(line)
		depths = append(depths, float64(depth))
		if depth > maxDepth {
			maxDepth = depth
		}
		isComment := strings.HasPrefix(t, commentPrefix) || strings.HasPrefix(t, "/*") || strings.HasPrefix(t, "*") || strings.HasPrefix(t, `"""`) || strings.HasPrefix(t, "'''")
		if isComment {
			comments++
			commentLen += len(t)
			if chatRe.MatchString(t) {
				chat++
			}
			prevWasComment = true
			prevComment = t
			continue
		}
		if prevWasComment && restates(prevComment, t) {
			restating++
		}
		prevWasComment = false
	}
	f[0] = float64(comments) / n
	f[1] = float64(blanks) / n
	if comments > 0 {
		f[2] = float64(commentLen) / float64(comments)
		f[3] = float64(restating) / float64(comments)
		f[4] = float64(chat) / float64(comments)
	}
	f[5], f[6] = meanStd(lineLens)
	f[7] = float64(longLines) / n

	idents := identRe.FindAllString(content, -1)
	var snake, camel, single, identLen int
	for _, id := range idents {
		identLen += len(id)
		switch {
		case len(id) == 1:
			single++
		case strings.Contains(id, "_") && strings.ToLower(id) == id:
			snake++
		case strings.ToLower(id[:1]) == id[:1] && strings.ToLower(id) != id:
			camel++
		}
	}
	if len(idents) > 0 {
		f[8] = float64(identLen) / float64(len(idents))
		f[9] = float64(snake) / float64(len(idents))
		f[10] = float64(camel) / float64(len(idents))
		f[11] = float64(single) / float64(len(idents))
	}
	f[12] = float64(maxDepth)
	f[13], _ = meanStd(depths)

	funcs := len(funcRe.FindAllString(content, -1))
	f[14] = float64(funcs) / n * 100
	if funcs > 0 {
		f[15] = n / float64(funcs)
	}
	checks := len(errCheck.FindAllString(content, -1))
	f[16] = float64(checks) / n * 100
	if checks > 0 {
		f[17] = float64(len(swallowRe.FindAllString(content, -1))) / float64(checks)
	}
	if funcs > 0 {
		f[18] = math.Min(1, float64(len(docRe.FindAllString(content, -1)))/float64(funcs))
	}
	// Over a fixed window, since a type-token ratio falls with length.
	tokens := tokenRe.FindAllString(content, 300)
	if len(tokens) > 0 {
		distinct := map[string]bool{}
		for _, tok := range tokens {
			distinct[tok] = true
		}
		f[19] = float64(len(distinct)) / float64(len(tokens))
	}
	f[20] = float64(trailing) / n
	f[21] = float64(len(todoRe.FindAllString(content, -1))) / n * 100
	return f
}

// restates reports whether a comment says what the next line does: most of
// the comment's words appear in the code line.
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

// stopword lists the words a restating comment shares with any line.
var stopword = map[string]bool{"the": true, "and": true, "for": true, "this": true, "that": true, "with": true, "from": true, "into": true, "then": true}

func indentUnit(line string) int {
	if strings.HasPrefix(line, "\t") {
		return 1
	}
	return 4
}

func meanStd(xs []float64) (mean, std float64) {
	if len(xs) == 0 {
		return 0, 0
	}
	for _, x := range xs {
		mean += x
	}
	mean /= float64(len(xs))
	for _, x := range xs {
		std += (x - mean) * (x - mean)
	}
	return mean, math.Sqrt(std / float64(len(xs)))
}
