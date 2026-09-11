package standards

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"unicode"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/config"
)

type sourceToken struct {
	chroma.Token
	line int
}

// codeTokens keeps literal boundaries so declarations cannot bridge a string.
// Chroma reports some lexer failures through its iterator as panics.
func (s *source) codeTokens() (tokens []sourceToken) {
	if s.lexed {
		return s.tokens
	}
	s.lexed = true
	defer func() {
		if problem := recover(); problem != nil {
			s.lexError = fmt.Sprintf("lexer failed: %v", problem)
			s.tokens, tokens = nil, nil
		}
	}()
	language := bundle.Language(s.path)
	if language == "java" && javaUnicodeEscape.MatchString(strings.Join(s.lines, "\n")) {
		s.lexError = "Java Unicode escapes are not supported by lexical probes"
		return nil
	}
	lexer := lexers.Get(language)
	if language == "ruby" {
		lexer = rubyLexer()
	}
	if language == "javascript" {
		lexer = javascriptLexer()
	}
	if lexer == nil {
		s.lexError = "no lexer for " + language
		return nil
	}
	iterator, err := chroma.Coalesce(lexer).Tokenise(nil, strings.Join(s.lines, "\n"))
	if err != nil {
		s.lexError = err.Error()
		return nil
	}
	line := 1
	for token := iterator(); token != chroma.EOF; token = iterator() {
		if token.Type == chroma.Error {
			s.lexError = fmt.Sprintf("unrecognized token at line %d", line)
			return nil
		}
		if !token.Type.InCategory(chroma.Comment) && strings.TrimSpace(token.Value) != "" {
			tokens = append(tokens, sourceToken{Token: token, line: line})
		}
		line += strings.Count(token.Value, "\n")
	}
	s.tokens = tokens
	return tokens
}

func (s *source) tokenSite(t sourceToken, conforms bool) Site {
	return Site{Path: s.path, Line: t.line, Conforms: conforms, Excerpt: s.at(t.line)}
}

func snakeName(name string) bool {
	name = strings.Trim(name, "_")
	if name == "" {
		return false
	}
	for _, r := range name {
		if r != '_' && !unicode.IsLower(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func pascalName(name string) bool {
	for i, r := range name {
		if i == 0 && !unicode.IsUpper(r) {
			return false
		}
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return name != ""
}

func functionNames(s *source, ruby bool) []Site {
	var sites []Site
	for _, t := range s.codeTokens() {
		if t.Type != chroma.NameFunction && t.Type != chroma.NameFunctionMagic {
			continue
		}
		name := t.Value
		if ruby {
			name = strings.TrimRight(name, "!?=")
			if name == "" || (!unicode.IsLetter([]rune(name)[0]) && name[0] != '_') {
				continue
			}
		}
		sites = append(sites, s.tokenSite(t, snakeName(name)))
	}
	return sites
}

func typeNames(s *source, keywords ...string) []Site {
	var sites []Site
	tokens := s.codeTokens()
	for i, t := range tokens {
		isTypeKeyword := t.Type.InCategory(chroma.Keyword) || t.Type == chroma.NameDecorator && t.Value == "@interface"
		if !isTypeKeyword {
			continue
		}
		for _, keyword := range keywords {
			if strings.TrimPrefix(t.Value, "@") != keyword || i+1 >= len(tokens) {
				continue
			}
			nameIndex := i + 1
			if bundle.Language(s.path) == "ruby" && tokens[nameIndex].Value == "::" && nameIndex+1 < len(tokens) {
				nameIndex++
			}
			name := tokens[nameIndex]
			if !name.Type.InCategory(chroma.Name) {
				continue
			}
			value := name.Value
			if bundle.Language(s.path) == "ruby" {
				for j := nameIndex + 1; j+1 < len(tokens) && tokens[j].Value == "::" && tokens[j+1].Type.InCategory(chroma.Name); j += 2 {
					value += "::" + tokens[j+1].Value
				}
				conforms := true
				for _, part := range strings.Split(strings.TrimPrefix(value, "::"), "::") {
					conforms = conforms && pascalName(part)
				}
				sites = append(sites, s.tokenSite(name, conforms))
				continue
			}
			if bundle.Language(s.path) == "python" {
				value = strings.TrimLeft(value, "_")
			}
			sites = append(sites, s.tokenSite(name, pascalName(value)))
		}
	}
	return sites
}

var pythonFunctionSnakeCase = Probe{
	ID: "python-function-snake-case", Language: "python", Class: config.ClassStyle,
	Rule:  "Name Python functions and methods in snake_case.",
	Why:   "Consistent declaration names make APIs easier to predict.",
	sites: func(s *source) []Site { return functionNames(s, false) },
}
var pythonClassPascalCase = Probe{
	ID: "python-class-pascal-case", Language: "python", Class: config.ClassStyle,
	Rule:  "Name Python classes in PascalCase, allowing leading underscores for private classes.",
	Why:   "Type names stay distinct from functions and variables.",
	sites: func(s *source) []Site { return typeNames(s, "class") },
}
var javascriptClassPascalCase = Probe{
	ID: "javascript-class-pascal-case", Language: "javascript", Class: config.ClassStyle,
	Rule:  "Name JavaScript classes in PascalCase.",
	Why:   "Type names stay distinct from functions and variables.",
	sites: func(s *source) []Site { return typeNames(s, "class") },
}
var javascriptStrictEquality = Probe{
	ID: "javascript-strict-equality", Language: "javascript", Class: config.ClassCorrectness,
	Rule: "Use === and !== for JavaScript equality comparisons.",
	Why:  "Strict equality avoids implicit type coercion.",
	sites: func(s *source) []Site {
		var sites []Site
		for _, t := range s.codeTokens() {
			if t.Type != chroma.Operator {
				continue
			}
			switch t.Value {
			case "==", "!=", "===", "!==":
				sites = append(sites, s.tokenSite(t, len(t.Value) == 3))
			}
		}
		return sites
	},
}
var javaTypePascalCase = Probe{
	ID: "java-type-pascal-case", Language: "java", Class: config.ClassStyle,
	Rule:  "Name Java classes, interfaces, enums, and records in PascalCase.",
	Why:   "Type names stay distinct from methods and variables.",
	sites: func(s *source) []Site { return typeNames(s, "class", "interface", "enum", "record") },
}
var javaPackageLowercase = Probe{
	ID: "java-package-lowercase", Language: "java", Class: config.ClassStyle,
	Rule: "Use lowercase names for Java packages.",
	Why:  "Consistent package spelling avoids case-sensitive import mistakes.",
	sites: func(s *source) []Site {
		var sites []Site
		tokens := s.codeTokens()
		for i, t := range tokens {
			if t.Type != chroma.KeywordNamespace || t.Value != "package" || i+1 >= len(tokens) {
				continue
			}
			name := tokens[i+1]
			if name.Type != chroma.NameNamespace {
				continue
			}
			value := packageName(tokens[i+1:])
			sites = append(sites, s.tokenSite(name, value == strings.ToLower(value)))
		}
		return sites
	},
}
var rubyMethodSnakeCase = Probe{
	ID: "ruby-method-snake-case", Language: "ruby", Class: config.ClassStyle,
	Rule:  "Name Ruby methods in snake_case, allowing predicate, bang, and setter suffixes.",
	Why:   "Method names remain predictable without losing Ruby's suffix conventions.",
	sites: func(s *source) []Site { return functionNames(s, true) },
}
var rubyTypePascalCase = Probe{
	ID: "ruby-type-pascal-case", Language: "ruby", Class: config.ClassStyle,
	Rule:  "Name Ruby classes and modules in PascalCase.",
	Why:   "Type and namespace names stay distinct from methods.",
	sites: func(s *source) []Site { return typeNames(s, "class", "module") },
}

// Chroma's Ruby definition recognizes heredoc openers but leaves their bodies
// as code. Consume the body before it can contribute declaration sites.
var rubyLexer = sync.OnceValue(func() chroma.Lexer {
	original := lexers.Get("ruby").(*chroma.RegexLexer)
	rules := original.MustRules().Clone()
	for state, entries := range rules {
		kept := entries[:0]
		for _, rule := range entries {
			if rule.Type == chroma.LiteralString && strings.Contains(rule.Pattern, "(<<-?)") {
				continue
			}
			kept = append(kept, rule)
		}
		rules[state] = kept
	}
	emitter := chroma.ByGroups(chroma.LiteralString, chroma.LiteralString, chroma.LiteralString, chroma.UsingSelf("root"), chroma.LiteralString)
	quotes := "[\"'`]?"
	rules["root"] = append([]chroma.Rule{
		{Pattern: `module\b`, Type: chroma.Keyword},
		{Pattern: `(?m)(?<!\w)(<<[-~])(` + quotes + `)([a-zA-Z_]\w*)\2([^\n]*\n)(.*?^[\t ]*\3(?:\r?\n|\z))`, Type: emitter},
		{Pattern: `(?m)(?<!\w)(<<)(` + quotes + `)([a-zA-Z_]\w*)\2([^\n]*\n)(.*?^\3(?:\r?\n|\z))`, Type: emitter},
		{Pattern: `(?<!\w)<<~[\"'` + "`" + `]?[a-zA-Z_]\w*[^\n]*(?:\n|\z)`, Type: chroma.Error},
		{Pattern: `(?<=[=(,:]\s*)(<<[-~]?)(` + quotes + `)([a-zA-Z_]\w*)\2[^\n]*(?:\n|\z)`, Type: chroma.Error},
	}, rules["root"]...)
	return chroma.MustNewLexer(original.Config(), func() chroma.Rules { return rules })
})

// JSX children are text until an expression opens; highlighting lexers often
// color that text as JavaScript, which would inflate comparison counts.
var javascriptLexer = sync.OnceValue(func() chroma.Lexer {
	original := lexers.Get("javascript").(*chroma.RegexLexer)
	rules := original.MustRules().Clone()
	opening := []chroma.Rule{
		{Pattern: `<>`, Type: chroma.Punctuation, Mutator: chroma.Push("jsx-content")},
		{Pattern: `(<)([\w.$:-]+)`, Type: chroma.ByGroups(chroma.Punctuation, chroma.NameTag), Mutator: chroma.Push("jsx-tag")},
	}
	rootOpening := append([]chroma.Rule(nil), opening...)
	for i := range rootOpening {
		rootOpening[i].Pattern = `(?<=(?:\A|[=(:,;\[{}?>&|!]|\breturn|\byield)\s*)` + rootOpening[i].Pattern
	}
	rules["root"] = append(rootOpening, rules["root"]...)
	rules["root"] = append([]chroma.Rule{
		{Pattern: `(?<![\w$.])(if|while|for|with|switch|catch)(\s*)(\()`, Type: chroma.ByGroups(chroma.Keyword, chroma.Text, chroma.Punctuation), Mutator: chroma.Push("control-condition")},
	}, rules["root"]...)
	rules["control-condition"] = []chroma.Rule{
		{Pattern: `\(`, Type: chroma.Punctuation, Mutator: chroma.Push("nested-condition")},
		{Pattern: `\)`, Type: chroma.Punctuation, Mutator: chroma.Mutators(chroma.Pop(1), chroma.Push("slashstartsregex"))},
		chroma.Include("root"),
	}
	rules["nested-condition"] = []chroma.Rule{
		{Pattern: `\(`, Type: chroma.Punctuation, Mutator: chroma.Push("nested-condition")},
		{Pattern: `\)`, Type: chroma.Punctuation, Mutator: chroma.Pop(1)},
		chroma.Include("root"),
	}

	rules["jsx-content"] = append([]chroma.Rule{
		{Pattern: `</[\w.$:-]*\s*>`, Type: chroma.Punctuation, Mutator: chroma.Pop(1)},
		{Pattern: `\{`, Type: chroma.Punctuation, Mutator: chroma.Push("jsx-expression")},
		{Pattern: `[^<{]+`, Type: chroma.LiteralString},
	}, opening...)
	rules["jsx-tag"] = []chroma.Rule{
		{Pattern: `\s+`, Type: chroma.Text},
		{Pattern: `/>`, Type: chroma.Punctuation, Mutator: chroma.Pop(1)},
		{Pattern: `>`, Type: chroma.Punctuation, Mutator: chroma.Mutators(chroma.Pop(1), chroma.Push("jsx-content"))},
		{Pattern: `\{`, Type: chroma.Punctuation, Mutator: chroma.Push("jsx-expression")},
		{Pattern: `"[^"]*"|'[^']*'`, Type: chroma.LiteralString},
		{Pattern: `[\w.$:-]+`, Type: chroma.NameAttribute},
		{Pattern: `=`, Type: chroma.Operator},
	}
	rules["jsx-expression"] = []chroma.Rule{
		{Pattern: `\{`, Type: chroma.Punctuation, Mutator: chroma.Push("jsx-expression")},
		{Pattern: `\}`, Type: chroma.Punctuation, Mutator: chroma.Pop(1)},
		chroma.Include("root"),
	}
	return chroma.MustNewLexer(original.Config(), func() chroma.Rules { return rules })
})

func packageName(tokens []sourceToken) string {
	var name strings.Builder
	for _, token := range tokens {
		if token.Value == ";" {
			break
		}
		name.WriteString(token.Value)
	}
	return name.String()
}

var javaUnicodeEscape = regexp.MustCompile(`\\u+[[:xdigit:]]{4}`)
