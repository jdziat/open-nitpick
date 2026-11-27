package converse

import (
	"fmt"
	"strings"
	"unicode"
)

// gateEval evaluates the part of GitHub's expression language the workflow
// gates use: contains, fromJSON, ==, !=, !, &&, ||, parentheses, string and
// null literals, and dotted context paths. Anything else is an error, not a
// guess, so a gate that grows a construct this does not know fails the test
// that reads it instead of passing on a wrong answer.
//
// GitHub's own rules are followed where they matter here: string comparison
// ignores case, contains on a string is a case-insensitive substring and on an
// array is membership, and || and && return an operand, not a bool.
func gateEval(expr string, ctx map[string]any) (any, error) {
	p := &gateParser{src: expr, ctx: ctx}
	v, err := p.or()
	if err != nil {
		return nil, fmt.Errorf("%w in %q", err, expr)
	}
	p.skip()
	if p.pos != len(p.src) {
		return nil, fmt.Errorf("unexpected %q at %d in %q", p.src[p.pos:], p.pos, expr)
	}
	return v, nil
}

type gateParser struct {
	src string
	pos int
	ctx map[string]any
}

func (p *gateParser) skip() {
	for p.pos < len(p.src) && unicode.IsSpace(rune(p.src[p.pos])) {
		p.pos++
	}
}

func (p *gateParser) eat(tok string) bool {
	p.skip()
	if strings.HasPrefix(p.src[p.pos:], tok) {
		p.pos += len(tok)
		return true
	}
	return false
}

func gateTruthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	}
	return true
}

func (p *gateParser) or() (any, error) {
	left, err := p.and()
	if err != nil {
		return nil, err
	}
	for p.eat("||") {
		right, err := p.and()
		if err != nil {
			return nil, err
		}
		if !gateTruthy(left) {
			left = right
		}
	}
	return left, nil
}

func (p *gateParser) and() (any, error) {
	left, err := p.cmp()
	if err != nil {
		return nil, err
	}
	for p.eat("&&") {
		right, err := p.cmp()
		if err != nil {
			return nil, err
		}
		if gateTruthy(left) {
			left = right
		}
	}
	return left, nil
}

func (p *gateParser) cmp() (any, error) {
	left, err := p.unary()
	if err != nil {
		return nil, err
	}
	for {
		switch {
		case p.eat("=="):
			right, err := p.unary()
			if err != nil {
				return nil, err
			}
			left = gateEqual(left, right)
		case p.eat("!="):
			right, err := p.unary()
			if err != nil {
				return nil, err
			}
			left = !gateEqual(left, right)
		default:
			return left, nil
		}
	}
}

func gateEqual(a, b any) bool {
	as, aok := a.(string)
	bs, bok := b.(string)
	if aok && bok {
		return strings.EqualFold(as, bs)
	}
	return a == b
}

func (p *gateParser) unary() (any, error) {
	p.skip()
	if p.pos < len(p.src) && p.src[p.pos] == '!' && !strings.HasPrefix(p.src[p.pos:], "!=") {
		p.pos++
		v, err := p.unary()
		if err != nil {
			return nil, err
		}
		return !gateTruthy(v), nil
	}
	return p.primary()
}

func (p *gateParser) primary() (any, error) {
	p.skip()
	if p.pos >= len(p.src) {
		return nil, fmt.Errorf("expression ends early")
	}
	switch c := p.src[p.pos]; {
	case c == '(':
		p.pos++
		v, err := p.or()
		if err != nil {
			return nil, err
		}
		if !p.eat(")") {
			return nil, fmt.Errorf("missing ) at %d", p.pos)
		}
		return v, nil
	case c == '\'':
		return p.str()
	}
	start := p.pos
	for p.pos < len(p.src) && (unicode.IsLetter(rune(p.src[p.pos])) || unicode.IsDigit(rune(p.src[p.pos])) || strings.ContainsRune("._-", rune(p.src[p.pos]))) {
		p.pos++
	}
	word := p.src[start:p.pos]
	if word == "" {
		return nil, fmt.Errorf("unexpected %q at %d", p.src[p.pos:], p.pos)
	}
	if p.eat("(") {
		return p.call(word)
	}
	switch word {
	case "null":
		return nil, nil
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	return gateLookup(p.ctx, word), nil
}

func (p *gateParser) str() (any, error) {
	p.pos++ // the opening quote
	var b strings.Builder
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if c == '\'' {
			if p.pos+1 < len(p.src) && p.src[p.pos+1] == '\'' {
				b.WriteByte('\'')
				p.pos += 2
				continue
			}
			p.pos++
			return b.String(), nil
		}
		b.WriteByte(c)
		p.pos++
	}
	return nil, fmt.Errorf("unterminated string")
}

func (p *gateParser) call(name string) (any, error) {
	var args []any
	if !p.eat(")") {
		for {
			v, err := p.or()
			if err != nil {
				return nil, err
			}
			args = append(args, v)
			if p.eat(")") {
				break
			}
			if !p.eat(",") {
				return nil, fmt.Errorf("expected , or ) in call to %s at %d", name, p.pos)
			}
		}
	}
	switch strings.ToLower(name) {
	case "contains":
		if len(args) != 2 {
			return nil, fmt.Errorf("contains takes two arguments")
		}
		needle := fmt.Sprint(args[1])
		if list, ok := args[0].([]any); ok {
			for _, item := range list {
				if gateEqual(item, needle) {
					return true, nil
				}
			}
			return false, nil
		}
		if args[0] == nil {
			return false, nil
		}
		return strings.Contains(strings.ToLower(fmt.Sprint(args[0])), strings.ToLower(needle)), nil
	case "fromjson":
		if len(args) != 1 {
			return nil, fmt.Errorf("fromJSON takes one argument")
		}
		return gateJSONList(fmt.Sprint(args[0]))
	}
	return nil, fmt.Errorf("function %s is not supported by the gate evaluator", name)
}

func gateJSONList(s string) (any, error) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "[") || !strings.HasSuffix(s, "]") {
		return nil, fmt.Errorf("fromJSON of %q: only an array of strings is supported", s)
	}
	var out []any
	for _, item := range strings.Split(strings.Trim(s, "[]"), ",") {
		out = append(out, strings.Trim(strings.TrimSpace(item), `"`))
	}
	return out, nil
}

func gateLookup(ctx map[string]any, path string) any {
	var cur any = ctx
	for _, key := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[key]
	}
	return cur
}
