package review

import "fmt"

// The no-new-claims contract: triage may SELECT, DROP, GROUP and RE-ANCHOR
// findings, and may not author them.
//
// The existing guard checks the wrong thing. engine.go rejects a finding whose
// PATH nobody reported, and Finding.Valid checks a nonempty path and a positive
// line. Neither reads the words. A triage model that keeps a reported path and
// rewrites the title and rationale passes both, and what it wrote is published
// under the original reporter's name, because Source is restored from the
// review pass a few lines later.
//
// It is worse than a general prose risk. Finding.Key() is path, line and the
// NORMALIZED TITLE, so the restoration of class and source only fires when
// triage left the title alone. Reword the title and the finding stops matching
// its own origin, keeps triage's guess at the class, and loses the attribution
// chain this tool sells. The rewrite is exactly the case the restore was
// written for and exactly the case it misses.
//
// So identity here is PATH and LINE, not the title, and the reviewer's words
// are restored over whatever triage returned.

// anchorTolerance is how far a triage finding may move from the line its
// origin reported and still be recognized as that finding.
//
// Not zero, because moving a finding onto the line that changed is
// work worth keeping and the anchor pass downstream does the same thing. Not
// unbounded, because a finding that travelled fifty lines is a claim about
// somewhere else.
const anchorTolerance = 10

// origins indexes the findings triage was given, by path.
type origins map[string][]Finding

func originsOf(findings []Finding) origins {
	out := origins{}
	for _, f := range findings {
		out[f.Path] = append(out[f.Path], f)
	}
	return out
}

// find returns the finding a triage answer at path:line is a restatement of,
// preferring an exact line and falling back to the nearest within tolerance.
func (o origins) find(path string, line int) (Finding, bool) {
	candidates := o[path]
	if len(candidates) == 0 {
		return Finding{}, false
	}

	best, bestDist := Finding{}, anchorTolerance+1
	for _, c := range candidates {
		d := c.Line - line
		if d < 0 {
			d = -d
		}
		if d < bestDist {
			best, bestDist = c, d
		}
	}
	if bestDist > anchorTolerance {
		return Finding{}, false
	}
	return best, true
}

// restore puts the reviewer's own words back on a triage finding, keeping the
// line triage chose.
//
// Title, Rationale and Suggestion are the three fields that make a claim. The
// line is normally triage's to move, because a line is a location rather than
// an assertion and moving it onto changed code is the anchor pass's job too.
//
// A SUGGESTION IS THE EXCEPTION, and it is the one that would have shipped a
// wrong patch. A suggestion is rendered as a forge suggestion block, which
// replaces the lines it is attached to. The reviewer wrote it against its own
// line; restoring that text onto a line triage moved would offer a one-click
// commit that overwrites the wrong code. So when the origin carries a
// suggestion and triage moved the finding, the line comes back with it.
func restore(f *Finding, origin Finding) (changed []string) {
	if origin.Suggestion != "" && f.Line != origin.Line {
		changed = append(changed, "line")
		f.Line = origin.Line
		f.EndLine = origin.EndLine
	}

	if f.Title != origin.Title {
		changed = append(changed, "title")
		f.Title = origin.Title
	}
	if f.Rationale != origin.Rationale {
		changed = append(changed, "rationale")
		f.Rationale = origin.Rationale
	}
	if f.Suggestion != origin.Suggestion {
		changed = append(changed, "suggestion")
		f.Suggestion = origin.Suggestion
	}
	return changed
}

// String names a finding for a log line.
func describe(f Finding) string { return fmt.Sprintf("%s:%d", f.Path, f.Line) }
