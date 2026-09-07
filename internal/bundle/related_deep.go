package bundle

import (
	"encoding/json"
	"path"
	"regexp"
	"strings"
)

// The second layer of resolution for the four languages most of this
// project's users write: the cases the plain import-to-file walk misses and a
// real repository hits on the first pull request.
//
//   - TypeScript: a path alias from tsconfig.json (`@/lib/money`) and a barrel
//     file that re-exports what it does not define.
//   - Python: a package whose __init__.py re-exports a name from a submodule.
//   - Go: a method called on a value of an imported type, whose doc comment is
//     the contract, where the type's struct declaration says nothing.
//   - Ruby: a Rails constant that no require names, found by Zeitwerk's rule,
//     where `Billing::Refund` lives at app/*/billing/refund.rb.
//
// Each follows at most two hops, and each fails closed: a name that cannot be
// traced is context not attached.

// --- TypeScript: tsconfig paths and barrels -----------------------------------

var (
	tsReexportNamed = regexp.MustCompile(`(?m)^\s*export\s+(?:type\s+)?\{([^}]*)\}\s+from\s+['"]([^'"]+)['"]`)
	tsReexportAll   = regexp.MustCompile(`(?m)^\s*export\s+\*\s+(?:as\s+\w+\s+)?from\s+['"]([^'"]+)['"]`)
	jsonComment     = regexp.MustCompile(`(?s)/\*.*?\*/|//[^\n]*`)
	jsonTrailing    = regexp.MustCompile(`,(\s*[}\]])`)
)

// tsConfig is the part of a tsconfig.json alias resolution needs.
type tsConfig struct {
	dir     string
	baseURL string
	paths   map[string][]string
}

// tsConfigFor finds the nearest tsconfig.json or jsconfig.json above a file
// and reads its baseUrl and paths, following one `extends`.
func (c *relatedCollector) tsConfigFor(file string) *tsConfig {
	dir := path.Dir(file)
	if dir == "." {
		dir = ""
	}
	for d := dir; ; {
		for _, name := range []string{"tsconfig.json", "jsconfig.json"} {
			p := name
			if d != "" {
				p = d + "/" + name
			}
			content, ok := c.read(p)
			if !ok {
				continue
			}
			cfg := parseTSConfig(d, content)
			if cfg.paths == nil && cfg.baseURL == "" {
				// Try one level of extends for the options.
				var raw struct {
					Extends string `json:"extends"`
				}
				_ = json.Unmarshal([]byte(jsonc(content)), &raw)
				if raw.Extends != "" && (strings.HasPrefix(raw.Extends, "./") || strings.HasPrefix(raw.Extends, "../")) {
					parent := path.Join(d, raw.Extends)
					if !strings.HasSuffix(parent, ".json") {
						parent += ".json"
					}
					if pc, ok := c.read(parent); ok {
						pcfg := parseTSConfig(path.Dir(parent), pc)
						pcfg.dir = d
						return &pcfg
					}
				}
			}
			return &cfg
		}
		if d == "" {
			return nil
		}
		d = path.Dir(d)
		if d == "." {
			d = ""
		}
	}
}

// jsonc strips comments and trailing commas, which tsconfig files carry.
func jsonc(s string) string {
	return jsonTrailing.ReplaceAllString(jsonComment.ReplaceAllString(s, ""), "$1")
}

func parseTSConfig(dir, content string) tsConfig {
	var raw struct {
		CompilerOptions struct {
			BaseURL string              `json:"baseUrl"`
			Paths   map[string][]string `json:"paths"`
		} `json:"compilerOptions"`
	}
	_ = json.Unmarshal([]byte(jsonc(content)), &raw)
	return tsConfig{dir: dir, baseURL: raw.CompilerOptions.BaseURL, paths: raw.CompilerOptions.Paths}
}

// tsAliasCandidates expands a non-relative specifier through the tsconfig's
// paths and baseUrl into repository-relative bases to try.
func (cfg *tsConfig) tsAliasCandidates(spec string) []string {
	if cfg == nil {
		return nil
	}
	var out []string
	base := cfg.baseURL
	if base == "" {
		base = "."
	}
	root := path.Join(cfg.dir, base)
	for pattern, targets := range cfg.paths {
		star := strings.Index(pattern, "*")
		var rest string
		switch {
		case star < 0:
			if spec != pattern {
				continue
			}
		default:
			prefix, suffix := pattern[:star], pattern[star+1:]
			if !strings.HasPrefix(spec, prefix) || !strings.HasSuffix(spec, suffix) || len(spec) < len(prefix)+len(suffix) {
				continue
			}
			rest = spec[len(prefix) : len(spec)-len(suffix)]
		}
		for _, t := range targets {
			out = append(out, path.Join(root, strings.Replace(t, "*", rest, 1)))
		}
	}
	if cfg.baseURL != "" && !strings.HasPrefix(spec, ".") {
		out = append(out, path.Join(root, spec))
	}
	var kept []string
	for _, o := range out {
		o = strings.TrimPrefix(o, "./")
		if o == ".." || strings.HasPrefix(o, "../") || strings.HasPrefix(o, "/") {
			continue
		}
		kept = append(kept, o)
	}
	return kept
}

// tsResolveBase tries a base path with the extension and index conventions.
func (c *relatedCollector) tsResolveBase(base string) string {
	var candidates []string
	if ext := path.Ext(base); ext != "" {
		candidates = append(candidates, base)
		if ext == ".js" || ext == ".mjs" || ext == ".cjs" {
			stem := strings.TrimSuffix(base, ext)
			candidates = append(candidates, stem+".ts", stem+".tsx")
		}
	}
	for _, ext := range tsExts {
		candidates = append(candidates, base+ext)
	}
	for _, ext := range tsExts {
		candidates = append(candidates, path.Join(base, "index"+ext))
	}
	for _, cand := range candidates {
		if c.exists(cand) {
			return cand
		}
	}
	return ""
}

// tsDefiningFile follows barrel re-exports from file until a file that
// defines name, at most two hops.
func (c *relatedCollector) tsDefiningFile(file, name string) string {
	for hop := 0; hop < 3; hop++ {
		content, ok := c.read(file)
		if !ok {
			return ""
		}
		if _, defined := tsDefinition(content, name); defined {
			return file
		}
		next := ""
		for _, m := range tsReexportNamed.FindAllStringSubmatch(content, -1) {
			for part := range strings.SplitSeq(m[1], ",") {
				part = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(part), "type "))
				exported, local := part, part
				if a, b, ok := strings.Cut(part, " as "); ok {
					exported, local = strings.TrimSpace(a), strings.TrimSpace(b)
				}
				if local == name {
					next = c.tsResolve(file, m[2])
					name = exported
				}
			}
		}
		if next == "" {
			for _, m := range tsReexportAll.FindAllStringSubmatch(content, -1) {
				cand := c.tsResolve(file, m[1])
				if cand == "" {
					continue
				}
				if content2, ok := c.read(cand); ok {
					if _, defined := tsDefinition(content2, name); defined {
						return cand
					}
				}
			}
		}
		if next == "" || next == file {
			return ""
		}
		file = next
	}
	return ""
}

// --- Python: package re-exports --------------------------------------------------

var pyRelImportInInit = regexp.MustCompile(`(?m)^\s*from\s+(\.[\w.]*)\s+import\s+(\(?[^\n]*)`)

// pyDefiningFile follows `from .mod import name` in an __init__.py to the
// module that defines name, at most two hops.
func (c *relatedCollector) pyDefiningFile(file, name string) string {
	for hop := 0; hop < 3; hop++ {
		content, ok := c.read(file)
		if !ok {
			return ""
		}
		if _, defined := pyDefinition(content, name); defined {
			return file
		}
		next := ""
		for _, m := range pyRelImportInInit.FindAllStringSubmatch(content, -1) {
			names := strings.Trim(m[2], "() ")
			for part := range strings.SplitSeq(names, ",") {
				part = strings.TrimSpace(part)
				exported, local := part, part
				if a, b, ok := strings.Cut(part, " as "); ok {
					exported, local = strings.TrimSpace(a), strings.TrimSpace(b)
				}
				if local == name || part == "*" {
					if cand := c.pyResolve(file, m[1]); cand != "" && cand != file {
						if part == "*" {
							if c2, ok := c.read(cand); ok {
								if _, defined := pyDefinition(c2, name); !defined {
									continue
								}
							}
						}
						next = cand
						if part != "*" {
							name = exported
						}
					}
				}
			}
		}
		if next == "" {
			return ""
		}
		file = next
	}
	return ""
}

// --- Go: methods on imported types --------------------------------------------

// goMethodWants returns, for a type T of package pkg that the change uses, the
// methods called on ANY receiver in the added lines that pkg defines on T. The
// receiver's static type is not known here, so a method is attached on the
// strength of its name alone, which over-attaches only when two types in scope
// share a method name, and under-attaches never.
func (c *relatedCollector) goMethodWants(added string, goFiles []string, typeName string) []want {
	called := map[string]int{}
	for _, m := range regexp.MustCompile(`\.([A-Z]\w*)\s*\(`).FindAllStringSubmatch(added, -1) {
		called[m[1]]++
	}
	if len(called) == 0 {
		return nil
	}
	var wants []want
	for _, file := range goFiles {
		content, ok := c.read(file)
		if !ok {
			continue
		}
		for method, n := range called {
			re := regexp.MustCompile(`(?m)^func\s+\([^)]*\*?` + regexp.QuoteMeta(typeName) + `\)\s+` + regexp.QuoteMeta(method) + `\(`)
			if re.MatchString(content) {
				wants = append(wants, want{file: file, name: typeName + "." + method, uses: n, extract: goMethodDefinition(typeName, method)})
			}
		}
	}
	return wants
}

func goMethodDefinition(typeName, method string) func(content, name string) (Related, bool) {
	re := regexp.MustCompile(`^func\s+\([^)]*\*?` + regexp.QuoteMeta(typeName) + `\)\s+` + regexp.QuoteMeta(method) + `\(`)
	return func(content, _ string) (Related, bool) {
		lines := splitLines(content)
		for i, line := range lines {
			if !re.MatchString(line) {
				continue
			}
			start := withLeadingComments(lines, i, "//")
			end := braceExtent(lines, i)
			return Related{Line: start + 1, Snippet: join(lines[start:end])}, true
		}
		return Related{}, false
	}
}

// --- Ruby: Rails constants ---------------------------------------------------------

var (
	rubyConstUse = regexp.MustCompile(`\b([A-Z][A-Za-z0-9]*(?:::[A-Z][A-Za-z0-9]*)*)\b`)
	railsRoots   = []string{"app/models", "app/controllers", "app/services", "app/jobs", "app/mailers", "app/helpers",
		"app/policies", "app/serializers", "app/presenters", "app/queries", "app/forms", "app/validators", "app/channels", "app/lib", "lib"}
)

// rubyAutoloadWants finds constants the change uses that a Rails autoloader
// would resolve, by Zeitwerk's rule: Billing::Refund is billing/refund.rb
// under one of the autoload roots.
func (c *relatedCollector) rubyAutoloadWants(e *Entry, added string) []want {
	if !c.exists("config/application.rb") && !c.exists("Gemfile") {
		return nil
	}
	own := map[string]bool{}
	for _, name := range rubyTopLevelNames(e.Content) {
		own[name] = true
	}
	seen := map[string]bool{}
	var wants []want
	for _, m := range rubyConstUse.FindAllStringSubmatch(added, -1) {
		name := m[1]
		if seen[name] || own[name] || rubyCoreConstants[name] {
			continue
		}
		seen[name] = true
		rel := rubyUnderscore(name) + ".rb"
		for _, root := range railsRoots {
			file := path.Join(root, rel)
			if c.changed[file] || !c.exists(file) {
				continue
			}
			short := name
			if i := strings.LastIndex(name, "::"); i >= 0 {
				short = name[i+2:]
			}
			wants = append(wants, want{file: file, name: short, uses: countUses(added, name), extract: rubyDefinition})
			break
		}
	}
	return wants
}

var rubyCoreConstants = map[string]bool{
	"String": true, "Integer": true, "Float": true, "Hash": true, "Array": true, "Time": true, "Date": true,
	"DateTime": true, "Rails": true, "ActiveRecord": true, "ActionController": true, "ApplicationRecord": true,
	"ApplicationController": true, "ApplicationJob": true, "ApplicationMailer": true, "StandardError": true,
	"ArgumentError": true, "RuntimeError": true, "JSON": true, "Set": true, "Struct": true, "Kernel": true,
	"Object": true, "Class": true, "Module": true, "Proc": true, "Symbol": true, "Regexp": true, "Comparable": true,
	"Enumerable": true, "File": true, "Dir": true, "IO": true, "Logger": true, "ENV": true, "BigDecimal": true,
}

// rubyUnderscore is ActiveSupport's underscore: CamelCase to snake_case,
// `::` to a directory separator.
func rubyUnderscore(name string) string {
	var b strings.Builder
	for _, seg := range strings.Split(name, "::") {
		if b.Len() > 0 {
			b.WriteByte('/')
		}
		for i, r := range seg {
			if r >= 'A' && r <= 'Z' {
				if i > 0 {
					prev := seg[i-1]
					next := byte(0)
					if i+1 < len(seg) {
						next = seg[i+1]
					}
					if (prev >= 'a' && prev <= 'z') || (prev >= '0' && prev <= '9') || (next >= 'a' && next <= 'z') {
						b.WriteByte('_')
					}
				}
				b.WriteRune(r - 'A' + 'a')
				continue
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}
