package bundle

import (
	"path"
	"regexp"
	"slices"
	"strings"
)

// Related-context resolvers for Rust, Ruby, Java, Kotlin and C/C++, on the
// same terms as the three in related.go: a changed line names a binding an
// import brought in, the import is followed to a file in the repository, and
// the definition is read out with its doc comment. Each resolver is written
// against the language's ordinary layout — Cargo's src/, Ruby's
// require_relative, Maven's src/main/java, C's quoted includes — and gives
// up quietly on anything else: a definition not found is context not
// attached, never a wrong file attached.

// --- Rust ----------------------------------------------------------------------

var (
	rustUse = regexp.MustCompile(`(?m)^\s*(?:pub(?:\([^)]*\))?\s+)?use\s+([^;]+);`)
	rustMod = regexp.MustCompile(`(?m)^\s*(?:pub(?:\([^)]*\))?\s+)?mod\s+([A-Za-z_]\w*)\s*;`)
)

// rustCrateRoot finds the src/ directory of the crate a file belongs to: the
// nearest ancestor holding Cargo.toml, plus src.
func (c *relatedCollector) rustCrateRoot(dir string) string {
	for d := dir; ; {
		cargo := "Cargo.toml"
		if d != "" {
			cargo = d + "/Cargo.toml"
		}
		if _, ok := c.read(cargo); ok {
			return path.Join(d, "src")
		}
		if d == "" {
			return ""
		}
		d = path.Dir(d)
		if d == "." {
			d = ""
		}
	}
}

// rustModuleFile resolves a module path (segments after crate::) to a file:
// a/b.rs or a/b/mod.rs under the crate root.
func (c *relatedCollector) rustModuleFile(root string, segments []string) string {
	if len(segments) == 0 {
		return ""
	}
	base := path.Join(append([]string{root}, segments...)...)
	for _, cand := range []string{base + ".rs", path.Join(base, "mod.rs")} {
		if c.exists(cand) {
			return cand
		}
	}
	return ""
}

// rustUseTargets reads every `use` in the file and returns, for each binding
// it introduces from inside the crate, the file that should define it.
func (c *relatedCollector) rustUseTargets(e *Entry, root string) map[string]string {
	targets := map[string]string{} // local name -> file
	dir := path.Dir(e.File.Path)
	if dir == "." {
		dir = ""
	}
	selfSegments := strings.Split(strings.TrimPrefix(strings.TrimSuffix(strings.TrimPrefix(dir, root), "/"), "/"), "/")
	if dir == root || strings.TrimPrefix(dir, root) == "" {
		selfSegments = nil
	}

	var walk func(prefix []string, tree string)
	walk = func(prefix []string, tree string) {
		tree = strings.TrimSpace(tree)
		if strings.HasPrefix(tree, "{") && strings.HasSuffix(tree, "}") {
			for part := range strings.SplitSeq(splitTopLevel(tree[1:len(tree)-1]), "\x00") {
				walk(prefix, part)
			}
			return
		}
		segs := strings.Split(tree, "::")
		for i := range segs {
			segs[i] = strings.TrimSpace(segs[i])
		}
		if idx := strings.Index(segs[len(segs)-1], "{"); idx >= 0 {
			head := strings.TrimSpace(segs[len(segs)-1][:idx])
			rest := segs[len(segs)-1][idx:]
			// An unbalanced tree (`use a::{b;`) reaches here as "{b", which
			// is neither a braced group nor a path, and walking it again
			// walks the same text forever. A use that does not parse names
			// nothing.
			if rest == tree {
				return
			}
			base := append(append([]string{}, prefix...), segs[:len(segs)-1]...)
			if head != "" {
				base = append(base, head)
			}
			walk(base, rest)
			return
		}
		full := append(append([]string{}, prefix...), segs...)
		if len(full) < 2 {
			return
		}
		local := full[len(full)-1]
		if a, b, ok := strings.Cut(local, " as "); ok {
			full[len(full)-1] = strings.TrimSpace(a)
			local = strings.TrimSpace(b)
		}
		if local == "*" || local == "self" {
			return
		}
		var modSegs []string
		switch full[0] {
		case "crate":
			modSegs = full[1 : len(full)-1]
		case "super":
			up := 0
			for up < len(full) && full[up] == "super" {
				up++
			}
			if up > len(selfSegments) {
				return
			}
			modSegs = append(append([]string{}, selfSegments[:len(selfSegments)-up]...), full[up:len(full)-1]...)
		case "self":
			modSegs = append(append([]string{}, selfSegments...), full[1:len(full)-1]...)
		default:
			return // an external crate
		}
		// `use crate::util;` imports a MODULE, used as util::helper; an item
		// import resolves to the module that holds it.
		if file := c.rustModuleFile(root, append(append([]string{}, modSegs...), full[len(full)-1])); file != "" && full[len(full)-1] == local {
			targets["mod:"+local] = file
			return
		}
		if file := c.rustModuleFile(root, modSegs); file != "" {
			targets[local] = file
		}
	}

	for _, m := range rustUse.FindAllStringSubmatch(e.Content, -1) {
		walk(nil, m[1])
	}
	// `mod foo;` declares a child module used as foo::Name.
	for _, m := range rustMod.FindAllStringSubmatch(e.Content, -1) {
		segs := append(append([]string{}, selfSegments...), m[1])
		if file := c.rustModuleFile(root, segs); file != "" {
			targets["mod:"+m[1]] = file
		}
	}
	return targets
}

// pathUses counts the distinct items of a Rust module used as mod::Item in
// text, keyed by item.
func pathUses(text, mod string) map[string]int {
	re := regexp.MustCompile(`\b` + regexp.QuoteMeta(mod) + `::([A-Za-z_]\w*)`)
	out := map[string]int{}
	for _, m := range re.FindAllStringSubmatch(text, -1) {
		out[m[1]]++
	}
	return out
}

// splitTopLevel splits a brace-nested list on top-level commas, joining the
// parts with NUL so the caller can split them again without re-parsing.
func splitTopLevel(s string) string {
	var b strings.Builder
	depth := 0
	for _, r := range s {
		switch r {
		case '{':
			depth++
		case '}':
			depth--
		case ',':
			if depth == 0 {
				b.WriteByte(0)
				continue
			}
		}
		b.WriteRune(r)
	}
	return b.String()
}

func (c *relatedCollector) rustWants(e *Entry) []want {
	dir := path.Dir(e.File.Path)
	if dir == "." {
		dir = ""
	}
	root := c.rustCrateRoot(dir)
	if root == "" {
		return nil
	}
	added := addedText(e.File)
	var wants []want
	for local, file := range c.rustUseTargets(e, root) {
		if c.changed[file] {
			continue
		}
		if strings.HasPrefix(local, "mod:") {
			for name, n := range pathUses(added, strings.TrimPrefix(local, "mod:")) {
				wants = append(wants, want{file: file, name: name, uses: n, extract: rustDefinition})
			}
			continue
		}
		if n := countUses(added, local); n > 0 {
			wants = append(wants, want{file: file, name: local, uses: n, extract: rustDefinition})
		}
	}
	return wants
}

func rustDefinition(content, name string) (Related, bool) {
	lines := splitLines(content)
	q := regexp.QuoteMeta(name)
	re := regexp.MustCompile(`^\s*(?:pub(?:\([^)]*\))?\s+)?(?:(?:async|const|unsafe|extern\s+"[^"]*")\s+)*(?:fn|struct|enum|trait|type|const|static|union|mod)\s+` + q + `\b`)
	for i, line := range lines {
		if !re.MatchString(line) {
			continue
		}
		start := withLeadingComments(lines, i, "///", "//!", "#[")
		end := braceExtent(lines, i)
		if end == i+1 && !strings.HasSuffix(strings.TrimSpace(line), ";") {
			for end < len(lines) && end-i < maxDefinitionLines {
				t := strings.TrimSpace(lines[end])
				end++
				if strings.HasSuffix(t, ";") || strings.HasSuffix(t, "}") {
					break
				}
			}
		}
		return Related{Line: start + 1, Snippet: join(lines[start:end])}, true
	}
	return Related{}, false
}

// --- Ruby ----------------------------------------------------------------------

var (
	rubyRequireRel = regexp.MustCompile(`(?m)^\s*require_relative\s+['"]([^'"]+)['"]`)
	rubyRequire    = regexp.MustCompile(`(?m)^\s*require\s+['"]([^'"]+)['"]`)
)

func (c *relatedCollector) rubyWants(e *Entry) []want {
	added := addedText(e.File)
	var files []string
	dir := path.Dir(e.File.Path)
	if dir == "." {
		dir = ""
	}
	for _, m := range rubyRequireRel.FindAllStringSubmatch(e.Content, -1) {
		cand := path.Join(dir, m[1])
		if !strings.HasSuffix(cand, ".rb") {
			cand += ".rb"
		}
		if strings.HasPrefix(cand, "../") || c.changed[cand] || !c.exists(cand) {
			continue
		}
		files = append(files, cand)
	}
	for _, m := range rubyRequire.FindAllStringSubmatch(e.Content, -1) {
		// A bare require names a gem or a file on the load path; lib/ is
		// the conventional in-repo load path.
		for _, cand := range []string{"lib/" + m[1] + ".rb", m[1] + ".rb"} {
			if !c.changed[cand] && c.exists(cand) {
				files = append(files, cand)
				break
			}
		}
	}
	// Constants a Rails autoloader would resolve need no require at all.
	wants := c.rubyAutoloadWants(e, added)
	if len(files) == 0 {
		return wants
	}
	seen := map[string]bool{}
	files = slices.DeleteFunc(files, func(f string) bool {
		if seen[f] {
			return true
		}
		seen[f] = true
		return false
	})

	// Ruby has no import list: whatever a required file defines at top
	// level is in scope. The names worth attaching are the constants and
	// methods the change actually uses, so each required file's top-level
	// definitions are read and matched against the added lines.
	for _, file := range files {
		content, ok := c.read(file)
		if !ok {
			continue
		}
		for _, name := range rubyTopLevelNames(content) {
			n := countUses(added, name)
			if n == 0 {
				continue
			}
			wants = append(wants, want{file: file, name: name, uses: n, extract: rubyDefinition})
		}
	}
	return wants
}

// rubyDef matches definitions at column 0 only: a method inside a class is
// reached through the class, which is attached whole when the change names
// it, and matching it here would attach `charge` to every change that calls
// some other object's charge.
var rubyDef = regexp.MustCompile(`(?m)^(?:def\s+(?:self\.)?([A-Za-z_]\w*[?!=]?)|(?:class|module)\s+([A-Z]\w*)|([A-Z][A-Z0-9_]*)\s*=)`)

func rubyTopLevelNames(content string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range rubyDef.FindAllStringSubmatch(content, -1) {
		for _, name := range m[1:] {
			if name != "" && !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	return out
}

func rubyDefinition(content, name string) (Related, bool) {
	lines := splitLines(content)
	q := regexp.QuoteMeta(name)
	re := regexp.MustCompile(`^(?P<indent>\s*)(?:def\s+(?:self\.)?` + q + `\b|(?:class|module)\s+` + q + `\b|` + q + `\s*=)`)
	for i, line := range lines {
		m := re.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		start := withLeadingComments(lines, i, "#")
		indent := m[1]
		if strings.HasSuffix(strings.TrimSpace(line), "=") || strings.Contains(line, "=") && !strings.HasPrefix(strings.TrimSpace(line), "def") && !strings.HasPrefix(strings.TrimSpace(line), "class") && !strings.HasPrefix(strings.TrimSpace(line), "module") {
			return Related{Line: start + 1, Snippet: join(lines[start : i+1])}, true
		}
		// A def/class/module runs to the `end` at the same indentation.
		end := i + 1
		for end < len(lines) && end-start < maxDefinitionLines {
			t := lines[end]
			if strings.TrimSpace(t) == "end" && strings.HasPrefix(t, indent) && len(t)-len(strings.TrimLeft(t, " \t")) == len(indent) {
				end++
				break
			}
			end++
		}
		return Related{Line: start + 1, Snippet: join(lines[start:end])}, true
	}
	return Related{}, false
}

// --- Java and Kotlin -----------------------------------------------------------

var (
	jvmImport  = regexp.MustCompile(`(?m)^\s*import\s+(?:static\s+)?([\w.]+)(?:\.\*)?\s*;?\s*$`)
	jvmPackage = regexp.MustCompile(`(?m)^\s*package\s+([\w.]+)\s*;?`)
)

// jvmSourceRoot works out where the package tree starts, from the importing
// file's own package declaration: a file at src/main/java/com/x/A.java in
// package com.x has its root at src/main/java.
func jvmSourceRoot(filePath, pkg string) string {
	dir := path.Dir(filePath)
	if dir == "." {
		dir = ""
	}
	if pkg == "" {
		return dir
	}
	suffix := strings.ReplaceAll(pkg, ".", "/")
	if dir == suffix {
		return ""
	}
	if strings.HasSuffix(dir, "/"+suffix) {
		return strings.TrimSuffix(dir, "/"+suffix)
	}
	return ""
}

func (c *relatedCollector) jvmWants(e *Entry, ext string) []want {
	pkg := ""
	if m := jvmPackage.FindStringSubmatch(e.Content); m != nil {
		pkg = m[1]
	}
	root := jvmSourceRoot(e.File.Path, pkg)
	dir := path.Dir(e.File.Path)
	if dir == "." {
		dir = ""
	}

	added := addedText(e.File)
	var wants []want

	consider := func(name, file string) {
		if c.changed[file] || !c.exists(file) {
			return
		}
		n := countUses(added, name)
		if n == 0 {
			return
		}
		wants = append(wants, want{file: file, name: name, uses: n, extract: jvmDefinitionFor(added)})
	}

	// inPackage finds the file under pkgDir that declares name: the file
	// named after it, or — Kotlin puts top-level declarations in any file —
	// whichever sibling with the extension declares it.
	inPackage := func(pkgDir, name string) string {
		if named := path.Join(pkgDir, name+ext); c.exists(named) {
			return named
		}
		decl := regexp.MustCompile(`(?m)^\s*(?:(?:public|private|protected|internal|abstract|final|open|sealed|data|static|value|inline|suspend|const)\s+)*(?:class|interface|enum|object|record|fun|val|var|typealias|enum class|annotation class)\s+(?:<[^>]*>\s*)?` + regexp.QuoteMeta(name) + `\b`)
		for _, entry := range c.entries(pkgDir) {
			if !strings.HasSuffix(entry, ext) {
				continue
			}
			file := path.Join(pkgDir, entry)
			if content, ok := c.read(file); ok && decl.MatchString(content) {
				return file
			}
		}
		return ""
	}

	for _, m := range jvmImport.FindAllStringSubmatch(e.Content, -1) {
		parts := strings.Split(m[1], ".")
		if len(parts) < 2 {
			continue
		}
		// The class is the first capitalised segment; anything after it is
		// a nested class or a static member. With no capitalised segment
		// the import names a Kotlin top-level function or property.
		classIdx := -1
		for i, p := range parts {
			if p != "" && p[0] >= 'A' && p[0] <= 'Z' {
				classIdx = i
				break
			}
		}
		var pkgRel, name string
		switch {
		case classIdx >= 1:
			pkgRel = strings.Join(parts[:classIdx], "/")
			name = parts[classIdx]
		case classIdx < 0 && ext == ".kt":
			pkgRel = strings.Join(parts[:len(parts)-1], "/")
			name = parts[len(parts)-1]
		default:
			continue
		}
		roots := []string{root}
		if root != "" {
			// A multi-module layout: the declaration may sit under another
			// module's source root with the same convention.
			roots = append(roots, "src/main/java", "src/main/kotlin", "src")
		}
		for _, r := range roots {
			if file := inPackage(path.Join(r, pkgRel), name); file != "" {
				consider(name, file)
				break
			}
		}
	}

	// Same-package classes need no import; a capitalised identifier used on
	// a changed line that names a sibling file is one.
	for _, ident := range capitalisedIdents(added) {
		consider(ident, path.Join(dir, ident+ext))
	}
	return wants
}

var capitalisedIdent = regexp.MustCompile(`\b([A-Z][A-Za-z0-9]+)\b`)

func capitalisedIdents(text string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range capitalisedIdent.FindAllStringSubmatch(text, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	return out
}

// jvmDefinitionFor extracts a class: its declaration with the doc comment,
// and — because a whole class is usually too long — the members the change
// names, each with its own doc comment.
func jvmDefinitionFor(added string) func(content, name string) (Related, bool) {
	return func(content, name string) (Related, bool) {
		lines := splitLines(content)
		q := regexp.QuoteMeta(name)
		decl := regexp.MustCompile(`^\s*(?:(?:public|private|protected|internal|abstract|final|open|sealed|data|static|value|inline|suspend|const)\s+)*(?:class|interface|enum|object|record|fun|val|var|typealias|enum class|annotation class)\s+(?:<[^>]*>\s*)?` + q + `\b`)
		declAt := -1
		for i, line := range lines {
			if decl.MatchString(line) {
				declAt = i
				break
			}
		}
		if declAt < 0 {
			return Related{}, false
		}
		end := braceExtent(lines, declAt)
		start := withLeadingComments(lines, declAt, "/**", "*", "*/", "//", "@")
		if end-start <= maxDefinitionLines {
			return Related{Line: start + 1, Snippet: join(lines[start:end])}, true
		}

		// Too long to attach whole: the declaration line, then each member
		// the change calls, found by name inside the class body.
		members := map[string]bool{}
		for _, m := range regexp.MustCompile(`\b`+q+`\s*\.\s*([a-z_]\w*)\s*\(`).FindAllStringSubmatch(added, -1) {
			members[m[1]] = true
		}
		for _, m := range regexp.MustCompile(`\.\s*([a-z_]\w*)\s*\(`).FindAllStringSubmatch(added, -1) {
			members[m[1]] = true
		}
		var b strings.Builder
		b.WriteString(join(lines[start : declAt+1]))
		b.WriteString("\n    // …")
		for i := declAt + 1; i < len(lines); i++ {
			t := lines[i]
			for member := range members {
				if regexp.MustCompile(`\b`+regexp.QuoteMeta(member)+`\s*\(`).MatchString(t) && !strings.Contains(t, ";") || regexp.MustCompile(`\bfun\s+`+regexp.QuoteMeta(member)+`\b`).MatchString(t) {
					ms := withLeadingComments(lines, i, "/**", "*", "*/", "//", "@")
					me := braceExtent(lines, i)
					b.WriteString("\n")
					b.WriteString(join(lines[ms:me]))
					b.WriteString("\n    // …")
					i = me - 1
					break
				}
			}
		}
		return Related{Line: start + 1, Snippet: b.String()}, true
	}
}

// --- C and C++ -------------------------------------------------------------------

var cInclude = regexp.MustCompile(`(?m)^\s*#\s*include\s+"([^"]+)"`)

func (c *relatedCollector) cWants(e *Entry) []want {
	added := addedText(e.File)
	dir := path.Dir(e.File.Path)
	if dir == "." {
		dir = ""
	}
	var wants []want
	for _, m := range cInclude.FindAllStringSubmatch(e.Content, -1) {
		var file string
		for _, cand := range []string{path.Join(dir, m[1]), m[1], path.Join("include", m[1]), path.Join("src", m[1])} {
			cand = strings.TrimPrefix(cand, "./")
			if strings.HasPrefix(cand, "../") {
				continue
			}
			if c.exists(cand) {
				file = cand
				break
			}
		}
		if file == "" || c.changed[file] {
			continue
		}
		content, ok := c.read(file)
		if !ok {
			continue
		}
		for _, name := range cTopLevelNames(content) {
			if n := countUses(added, name); n > 0 {
				wants = append(wants, want{file: file, name: name, uses: n, extract: cDefinition})
			}
		}
	}
	return wants
}

var cDecl = regexp.MustCompile(`(?m)^(?:#\s*define\s+([A-Za-z_]\w*)|(?:typedef\s+)?(?:struct|union|enum)\s+([A-Za-z_]\w*)\s*\{|[A-Za-z_][\w\s\*]*?\b([A-Za-z_]\w*)\s*\([^;]*\)\s*;|typedef\s+[^;]*\b([A-Za-z_]\w*)\s*;)`)

func cTopLevelNames(content string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range cDecl.FindAllStringSubmatch(content, -1) {
		for _, name := range m[1:] {
			if name != "" && !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	return out
}

func cDefinition(content, name string) (Related, bool) {
	lines := splitLines(content)
	q := regexp.QuoteMeta(name)
	re := regexp.MustCompile(`^(?:#\s*define\s+` + q + `\b|(?:typedef\s+)?(?:struct|union|enum)\s+` + q + `\b|[A-Za-z_][\w\s\*]*?\b` + q + `\s*\(|typedef\s+[^;]*\b` + q + `\s*;)`)
	for i, line := range lines {
		if !re.MatchString(line) {
			continue
		}
		start := withLeadingComments(lines, i, "/**", "/*", "*", "*/", "//")
		end := braceExtent(lines, i)
		if end == i+1 && !strings.HasSuffix(strings.TrimSpace(line), ";") && !strings.HasPrefix(strings.TrimSpace(line), "#") {
			for end < len(lines) && end-i < maxDefinitionLines {
				t := strings.TrimSpace(lines[end])
				end++
				if strings.HasSuffix(t, ";") {
					break
				}
			}
		}
		return Related{Line: start + 1, Snippet: join(lines[start:end])}, true
	}
	return Related{}, false
}
