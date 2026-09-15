package flowhtml

import (
	"go/scanner"
	"go/token"
	"html"
	"strings"
)

// tokenClass names the CSS class used for one span of highlighted source.
type tokenClass string

const (
	classPlain   tokenClass = ""
	classKeyword tokenClass = "k"
	classString  tokenClass = "s"
	classNumber  tokenClass = "n"
	classComment tokenClass = "c"
	classType    tokenClass = "t"
	classFunc    tokenClass = "f"
	classPunct   tokenClass = "p"
)

// builtinTypes are highlighted as types even though Go treats them as ordinary
// predeclared identifiers, because a reader scanning a signature looks for them.
var builtinTypes = map[string]bool{
	"any": true, "bool": true, "byte": true, "comparable": true, "complex64": true,
	"complex128": true, "error": true, "float32": true, "float64": true, "int": true,
	"int8": true, "int16": true, "int32": true, "int64": true, "rune": true,
	"string": true, "uint": true, "uint8": true, "uint16": true, "uint32": true,
	"uint64": true, "uintptr": true,
}

var builtinFuncs = map[string]bool{
	"append": true, "cap": true, "clear": true, "close": true, "complex": true,
	"copy": true, "delete": true, "imag": true, "len": true, "make": true,
	"max": true, "min": true, "new": true, "panic": true, "print": true,
	"println": true, "real": true, "recover": true,
}

var builtinConsts = map[string]bool{"true": true, "false": true, "iota": true, "nil": true}

// highlightGo converts Go source into HTML spans. It never returns unescaped
// input: a file that fails to scan is emitted as escaped plain text, so a
// syntax error degrades the colouring rather than the safety of the document.
func highlightGo(src string) string {
	var b strings.Builder
	b.Grow(len(src) + len(src)/2)
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(src))
	var s scanner.Scanner
	failed := false
	s.Init(file, []byte(src), func(token.Position, string) { failed = true }, scanner.ScanComments)

	type span struct {
		start, end int
		class      tokenClass
	}
	var spans []span
	previous := token.ILLEGAL
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		offset := file.Offset(pos)
		if offset < 0 || offset > len(src) {
			break
		}
		width := len(lit)
		if width == 0 {
			width = len(tok.String())
		}
		if offset+width > len(src) {
			width = len(src) - offset
		}
		if width <= 0 {
			previous = tok
			continue
		}
		spans = append(spans, span{start: offset, end: offset + width, class: classify(tok, lit, previous, src, offset+width)})
		if tok != token.COMMENT {
			previous = tok
		}
	}
	if failed && len(spans) == 0 {
		return html.EscapeString(src)
	}
	cursor := 0
	for _, sp := range spans {
		if sp.start < cursor {
			continue
		}
		b.WriteString(html.EscapeString(src[cursor:sp.start]))
		text := html.EscapeString(src[sp.start:sp.end])
		if sp.class == classPlain {
			b.WriteString(text)
		} else {
			b.WriteString("<span class=\"" + string(sp.class) + "\">" + text + "</span>")
		}
		cursor = sp.end
	}
	if cursor < len(src) {
		b.WriteString(html.EscapeString(src[cursor:]))
	}
	return b.String()
}

func classify(tok token.Token, lit string, previous token.Token, src string, end int) tokenClass {
	switch {
	case tok == token.COMMENT:
		return classComment
	case tok == token.STRING || tok == token.CHAR:
		return classString
	case tok == token.INT || tok == token.FLOAT || tok == token.IMAG:
		return classNumber
	case tok.IsKeyword():
		return classKeyword
	case tok == token.IDENT:
		switch {
		case builtinConsts[lit]:
			return classKeyword
		case builtinTypes[lit]:
			return classType
		case builtinFuncs[lit]:
			return classFunc
		case previous == token.FUNC:
			return classFunc
		case previous == token.TYPE:
			return classType
		case followedByCall(src, end):
			return classFunc
		}
		return classPlain
	case tok.IsOperator():
		return classPunct
	}
	return classPlain
}

// followedByCall reports whether the next non-space byte opens a call, which is
// the cheapest signal that an identifier names a function at this position.
func followedByCall(src string, end int) bool {
	for i := end; i < len(src); i++ {
		switch src[i] {
		case ' ', '\t':
			continue
		case '(':
			return true
		default:
			return false
		}
	}
	return false
}

// snippet is a bounded window of a source file centred on a declaration.
type snippet struct {
	Path      string
	FirstLine int
	Focus     int
	Lines     []snippetLine
}

type snippetLine struct {
	Number int
	HTML   string
	Focus  bool
}

// extractSnippet returns up to before+after lines around focus, highlighted.
// Highlighting runs over the whole file so a window that opens inside a string
// or comment is coloured as its enclosing token rather than as fresh code.
func extractSnippet(path, source string, focus, before, after int) snippet {
	if focus < 1 {
		focus = 1
	}
	highlighted := strings.Split(highlightGo(source), "\n")
	start := focus - before
	if start < 1 {
		start = 1
	}
	end := focus + after
	if end > len(highlighted) {
		end = len(highlighted)
	}
	out := snippet{Path: path, FirstLine: start, Focus: focus}
	for n := start; n <= end; n++ {
		out.Lines = append(out.Lines, snippetLine{
			Number: n,
			HTML:   balanceSpans(highlighted[n-1]),
			Focus:  n == focus,
		})
	}
	return out
}

// balanceSpans closes spans left open by splitting highlighted HTML on newlines
// and reopens nothing, so one line of a multi-line string or comment stays
// well-formed on its own row.
func balanceSpans(line string) string {
	open := strings.Count(line, "<span")
	closed := strings.Count(line, "</span>")
	switch {
	case open > closed:
		return line + strings.Repeat("</span>", open-closed)
	case closed > open:
		return strings.Repeat("<span class=\"c\">", closed-open) + line
	}
	return line
}
