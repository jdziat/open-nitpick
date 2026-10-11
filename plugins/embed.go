// Package plugins carries the agent skills in plugins/nitpick/skills inside
// the binary, so nitpick setup can install them into every harness that reads
// the SKILL.md format without a checkout of this repository.
package plugins

import "embed"

// Skills holds one directory per skill, each with a SKILL.md.
//
//go:embed nitpick/skills
var Skills embed.FS

// SkillsRoot is the directory inside Skills that holds the skill folders.
const SkillsRoot = "nitpick/skills"
