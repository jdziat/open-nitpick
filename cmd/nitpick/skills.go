package main

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/jdziat/open-nitpick/plugins"
)

// skillPrefix namespaces the installed folders. The shipped names (security,
// code-smell) are ordinary words a person may already have a skill called,
// and a harness loading both would keep one. Claude Code's plugin form gets a
// namespace for free; a copied folder does not.
const skillPrefix = "nitpick-"

// skillSet is one skill's files as they will be written.
type skillSet struct {
	name  string
	files map[string][]byte // path relative to the skill folder
}

// bundledSkills reads the embedded skills and renames each to skillPrefix+name:
// the folder, the name in its front matter (a harness requires the two to
// match), and the backticked mentions of the other skills in its text.
func bundledSkills() ([]skillSet, error) {
	entries, err := fs.ReadDir(plugins.Skills, plugins.SkillsRoot)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	var oldNames []string
	oldNames = append(oldNames, names...)
	var out []skillSet
	for _, name := range names {
		set := skillSet{name: skillPrefix + name, files: map[string][]byte{}}
		root := path.Join(plugins.SkillsRoot, name)
		err := fs.WalkDir(plugins.Skills, root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			data, err := fs.ReadFile(plugins.Skills, p)
			if err != nil {
				return err
			}
			rel := strings.TrimPrefix(p, root+"/")
			if rel == "SKILL.md" {
				data = []byte(renameSkill(string(data), name, oldNames))
			}
			set.files[rel] = data
			return nil
		})
		if err != nil {
			return nil, err
		}
		if _, ok := set.files["SKILL.md"]; !ok {
			return nil, fmt.Errorf("embedded skill %s has no SKILL.md", name)
		}
		out = append(out, set)
	}
	return out, nil
}

var frontName = regexp.MustCompile(`(?m)^name:\s*\S+`)

func renameSkill(text, name string, all []string) string {
	text = frontName.ReplaceAllString(text, "name: "+skillPrefix+name)
	for _, other := range all {
		text = strings.ReplaceAll(text, "`"+other+"`", "`"+skillPrefix+other+"`")
	}
	return text
}

// skillDir returns the folder a client reads skills from, or "" when it has
// none in that scope.
func skillDir(c mcpClient, user bool, repo string) (string, error) {
	if user {
		if len(c.skillsUser) == 0 {
			return "", nil
		}
		return homePath(c.skillsUser...)
	}
	if len(c.skillsProject) == 0 {
		return "", nil
	}
	root, err := filepath.Abs(repo)
	if err != nil {
		return "", err
	}
	return filepath.Join(append([]string{root}, c.skillsProject...)...), nil
}

// skillPlan is what installing the bundled skills into one client would do:
// which are absent or differ from the bundled text, and which already match.
type skillPlan struct {
	dir      string // "" when the client has no skills folder in this scope
	pending  []skillSet
	replaced int // pending skills that exist on disk with other contents
	current  int // skills already identical to the bundled ones
}

func planSkills(c mcpClient, req mcpInstall) (skillPlan, error) {
	dir, err := skillDir(c, req.user, req.repo)
	if err != nil || dir == "" {
		return skillPlan{}, err
	}
	sets, err := bundledSkills()
	if err != nil {
		return skillPlan{}, err
	}
	p := skillPlan{dir: dir}
	for _, s := range sets {
		state := skillState(filepath.Join(dir, s.name), s)
		switch state {
		case skillCurrent:
			p.current++
		case skillOutdated:
			p.replaced++
			p.pending = append(p.pending, s)
		default:
			p.pending = append(p.pending, s)
		}
	}
	return p, nil
}

const (
	skillMissing = iota
	skillCurrent
	skillOutdated
)

// skillState compares a skill folder on disk with the bundled files. A file
// the bundle does not carry is ignored, since the folder may hold the user's
// own additions; a missing or different bundled file makes it outdated.
func skillState(folder string, s skillSet) int {
	if _, err := os.Stat(folder); err != nil {
		return skillMissing
	}
	for rel, want := range s.files {
		got, err := os.ReadFile(filepath.Join(folder, filepath.FromSlash(rel)))
		if err != nil || !bytes.Equal(got, want) {
			return skillOutdated
		}
	}
	return skillCurrent
}

func (p skillPlan) apply(c mcpClient, req mcpInstall, stdout io.Writer) error {
	say := func(format string, a ...any) { _, _ = fmt.Fprintf(stdout, format, a...) }
	if p.dir == "" {
		scope := "project"
		if req.user {
			scope = "user"
		}
		say("  skills: %s has no %s skills folder; skipped\n", c.name, scope)
		return nil
	}
	if len(p.pending) == 0 {
		say("  skills: %d already up to date in %s\n", p.current, p.dir)
		return nil
	}
	if req.dryRun {
		say("  would write %d skills to %s\n", len(p.pending), p.dir)
		return nil
	}
	for _, s := range p.pending {
		for rel, data := range s.files {
			path := filepath.Join(p.dir, s.name, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(path, data, 0o644); err != nil {
				return err
			}
		}
	}
	extra := ""
	if p.replaced > 0 {
		extra = fmt.Sprintf(" (%d updated)", p.replaced)
	}
	if p.current > 0 {
		extra += fmt.Sprintf(", %d already current", p.current)
	}
	say("  wrote %d skills to %s%s\n", len(p.pending), p.dir, extra)
	return nil
}
