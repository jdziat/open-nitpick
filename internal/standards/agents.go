package standards

import (
	"fmt"
	"strings"
)

// The markers around the generated block.
//
// Everything outside them is written by a person and is copied through
// untouched. Build commands, the gates, the things no probe can see: those are
// the parts of an agent file worth having and the parts this package has no
// way to know. Only what sits between the markers is regenerated, and only
// that is what a drift check can claim to own.
const (
	BeginMarker = "<!-- nitpick:standards:begin -->"
	EndMarker   = "<!-- nitpick:standards:end -->"
)

// agentsMaxLines bounds the generated block. See docs/findings.md for why a
// conventions file fails by growing, and what the ranking spends the budget on.
const agentsMaxLines = 120

// Block renders the generated half of an agent instructions file.
//
// Rules arrive already ranked by evidence, strongest share first, so the tail
// this drops is the tail with the weakest claim behind it.
func (r Report) Block() string {
	var b strings.Builder
	b.WriteString(BeginMarker + "\n")
	b.WriteString("\n## Conventions, measured\n\n")
	b.WriteString("Counted from this repository rather than asserted, so a rule here is one the\n")
	b.WriteString("code demonstrates. The count after each is how many places were read and how\n")
	b.WriteString("many of them follow it. Run `nitpick standards` to see every probe including\n")
	b.WriteString("the contested ones, and `make agents` to regenerate this block.\n\n")

	standards := r.Standards()
	if len(standards) == 0 {
		// Rule 10 in the artifact rather than in the report. An empty block and
		// a repository with no measurable conventions look identical, and only
		// one of them is worth acting on.
		b.WriteString("No convention in this repository clears the evidence floor of ")
		fmt.Fprintf(&b, "%.0f%% over %d sites. `nitpick standards` shows what was counted.\n\n",
			r.Floor.MinShare*100, r.Floor.MinSites)
		b.WriteString(EndMarker + "\n")
		return b.String()
	}

	// Fit the rules to the budget, counting the lines already written and the
	// two the closing needs.
	head := strings.Count(b.String(), "\n")
	room := agentsMaxLines - head - 4
	listed := standards
	if room < len(listed) {
		if room < 0 {
			room = 0
		}
		listed = listed[:room]
	}

	for _, res := range listed {
		fmt.Fprintf(&b, "- %s (%d/%d)\n", res.Rule, res.Conforming, res.Total)
	}

	if dropped := len(standards) - len(listed); dropped > 0 {
		fmt.Fprintf(&b, "\n%d more measured standard(s) are not listed, to keep this block under "+
			"%d lines. `nitpick standards` prints all of them, weakest evidence last.\n",
			dropped, agentsMaxLines)
	}

	b.WriteString("\n" + EndMarker + "\n")
	return b.String()
}

// defaultAgents is the file written when none exists.
//
// The prose outside the markers is a starting point for a person to replace,
// not a claim this package is making. It says so, so that a scaffold nobody
// edited cannot be mistaken for a description of the repository.
const defaultAgents = `# Working in this repository

<!-- Everything outside the generated block below is yours. Put the build and
test commands here, the gates a change has to pass, and anything else an agent
needs that cannot be counted. Nothing here is regenerated. -->

`

// Render places the generated block into an existing agent file, preserving
// every line outside the markers.
//
// existing is the file's current content, empty when there is none.
func Render(existing string, rep Report) (string, error) {
	block := rep.Block()

	if strings.TrimSpace(existing) == "" {
		return defaultAgents + block, nil
	}

	start := strings.Index(existing, BeginMarker)
	end := strings.Index(existing, EndMarker)
	switch {
	case start < 0 && end < 0:
		// A file written before this existed. The block is appended rather
		// than replacing anything, because everything already there is
		// somebody's.
		out := existing
		if !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		return out + "\n" + block, nil

	case start < 0 || end < 0:
		// Half a pair means an edit went wrong, and guessing where the block
		// was meant to end would overwrite prose nobody can get back.
		return "", fmt.Errorf("the file has %q without %q; restore the pair or delete both and regenerate",
			presentMarker(start >= 0), missingMarker(start >= 0))

	case end < start:
		return "", fmt.Errorf("%q appears before %q", EndMarker, BeginMarker)
	}

	return existing[:start] + strings.TrimSuffix(block, "\n") + existing[end+len(EndMarker):], nil
}

func presentMarker(beginPresent bool) string {
	if beginPresent {
		return BeginMarker
	}
	return EndMarker
}

func missingMarker(beginPresent bool) string {
	if beginPresent {
		return EndMarker
	}
	return BeginMarker
}
