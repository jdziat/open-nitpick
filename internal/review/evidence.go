package review

import (
	"sort"

	"github.com/jdziat/open-nitpick/internal/knowledge"
)

// Attributing retrieved knowledge to a finding.
//
// The reviewer is shown a batch's entries and answers with a list of findings.
// Nothing connects one to the other: no identifier crosses the model, and the
// schema deliberately does not let it assert one. So this records what the
// reviewer read, which is checkable, rather than what persuaded it, which is
// not.

// evidenceCap bounds how many entries a finding carries.
//
// A batch retrieves at most five, and a finding that named all five would tell
// a reader nothing about which mattered. Three is what a person can hold while
// reading one finding.
const evidenceCap = 3

// evidenceFor names the entries the reviewer had in front of it for one
// finding, nearest first.
//
// Entries retrieved by this finding's own file come first: retrieval runs per
// file where it can, and an entry that a different file pulled in is weaker
// evidence about this one. Within that, the order is the retrieval order,
// which is by score. Nothing is filtered out on class, only ordered: a
// resource entry beside a correctness finding is often the reason the finding
// is right.
func evidenceFor(f Finding, hits []knowledge.Hit) []string {
	if len(hits) == 0 {
		return nil
	}

	ranked := make([]knowledge.Hit, len(hits))
	copy(ranked, hits)

	// Stable, so entries that tie keep the order retrieval gave them and a
	// review is reproducible.
	sort.SliceStable(ranked, func(i, j int) bool {
		return mine(ranked[i], f) && !mine(ranked[j], f)
	})

	out := make([]string, 0, min(evidenceCap, len(ranked)))
	for _, h := range ranked {
		if len(out) == evidenceCap {
			break
		}
		out = append(out, h.Entry.ID)
	}
	return out
}

// mine reports whether a hit was retrieved by the finding's own file. A
// batch-wide hit has no path and belongs to every finding equally.
func mine(h knowledge.Hit, f Finding) bool {
	return h.Path == "" || h.Path == f.Path
}
