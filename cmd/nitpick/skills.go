package main

import (
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

// installSkills writes the bundled skills where the client reads them, in the
// same scope installMCP used. Folders carry skillPrefix, so a run replaces
// its own earlier copy and touches nothing else in the directory.
func installSkills(c mcpClient, req mcpInstall, stdout io.Writer) error {
	dir, err := skillDir(c, req.user, req.repo)
	if err != nil {
		return err
	}
	if dir == "" {
		scope := "project"
		if req.user {
			scope = "user"
		}
		_, _ = fmt.Fprintf(stdout, "  skills: %s has no %s skills folder; skipped\n", c.name, scope)
		return nil
	}
	sets, err := bundledSkills()
	if err != nil {
		return err
	}
	if req.dryRun {
		_, _ = fmt.Fprintf(stdout, "  would write %d skills to %s\n", len(sets), dir)
		return nil
	}
	for _, s := range sets {
		for rel, data := range s.files {
			p := filepath.Join(dir, s.name, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(p, data, 0o644); err != nil {
				return err
			}
		}
	}
	_, _ = fmt.Fprintf(stdout, "  wrote %d skills to %s\n", len(sets), dir)
	return nil
}
