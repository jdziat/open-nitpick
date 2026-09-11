package standards

import (
	"fmt"
	"sort"
	"strings"
)

// percent renders a share, and reserves 100% for a count that is whole.
//
// %.0f rounds 220/221 to 100%, and a tool whose claim is that the count is
// checkable cannot print a perfect score for an imperfect one. Rounding down
// everywhere else keeps the digit a floor rather than a flattery.
func percent(conforming, total int) string {
	switch {
	case total == 0:
		return "n/a"
	case conforming == total:
		return "100%"
	}
	p := int(float64(conforming) / float64(total) * 100)
	if p >= 100 {
		p = 99
	}
	return fmt.Sprintf("%d%%", p)
}

// maxListedOff bounds how many violating sites one rule prints.
//
// The count above the list is the claim and the list is evidence for it, so
// the cut says how many it left out. A report that trails off without saying
// so reads as the whole of the evidence.
const maxListedOff = 8

// Text renders a report for a terminal.
func (r Report) Text() string {
	var b strings.Builder

	files, langs := 0, make([]string, 0, len(r.Files))
	for l, n := range r.Files {
		files += n
		if l != "" {
			langs = append(langs, fmt.Sprintf("%d %s", n, l))
		}
	}
	sort.Strings(langs)
	fmt.Fprintf(&b, "Standards over %d file(s): %s.\n\n", files, strings.Join(langs, ", "))

	if len(r.Results) == 0 {
		b.WriteString("No probe read any of these files.\n")
	}
	for _, res := range r.Results {
		if _, ok := res.Share(); !ok {
			fmt.Fprintf(&b, "  %-24s %13s        %s\n", res.ID, "no sites", res.Standing)
			continue
		}
		fmt.Fprintf(&b, "  %-24s %6d/%-6d %3s  %s\n",
			res.ID, res.Conforming, res.Total, percent(res.Conforming, res.Total), res.Standing)
	}

	// Named rather than left to be noticed. A language no probe reads is a
	// language this report has no opinion about, and an unqualified "standards
	// over 400 files" invites the opposite reading.
	if len(r.Unprobed) > 0 {
		fmt.Fprintf(&b, "\nNo probes for: %s. Those files are in no denominator above.\n",
			strings.Join(r.Unprobed, ", "))
	}

	if len(r.Unmeasured) > 0 {
		fmt.Fprintf(&b, "\nCould not measure: %s.\n", strings.Join(r.Unmeasured, "; "))
	}

	standards := r.Standards()
	contested := 0
	for _, res := range r.Results {
		if res.Standing == StandingContested {
			contested++
		}
	}
	fmt.Fprintf(&b, "\n%d standard(s) at or above %.0f%% over %d+ sites",
		len(standards), r.Floor.MinShare*100, r.Floor.MinSites)
	if contested > 0 {
		fmt.Fprintf(&b, ", %d contested", contested)
	}
	b.WriteString(".\n")
	return b.String()
}

// AdherenceText renders how a change sits against measured standards.
//
// scores is keyed by probe ID, as Score returns it.
func AdherenceText(scores map[string]Adherence) string {
	var b strings.Builder

	ids := make([]string, 0, len(scores))
	conforming, total := 0, 0
	for id, a := range scores {
		ids = append(ids, id)
		conforming += a.Conforming
		total += a.Total
	}
	sort.Strings(ids)

	if total == 0 {
		// Rule 10 again, one layer up. A change that touched no site at all
		// and a change that conformed everywhere are the same number and
		// different facts.
		return "This change touched no site any standard has an opinion about.\n"
	}

	fmt.Fprintf(&b, "Adherence on this change: %d/%d site(s).\n", conforming, total)
	for _, id := range ids {
		a := scores[id]
		if len(a.Off) == 0 {
			continue
		}
		b.WriteString("\n")
		for i, s := range a.Off {
			if i == maxListedOff {
				fmt.Fprintf(&b, "  %-22s %d more not listed\n", "", len(a.Off)-maxListedOff)
				break
			}
			fmt.Fprintf(&b, "  %-22s %s:%d  %s\n", id, s.Path, s.Line, s.Excerpt)
		}
	}
	return b.String()
}

// Recommendations orders the fixes by how many sites each would settle, so a
// reader with an hour spends it where the count is.
func Recommendations(scores map[string]Adherence) []string {
	type row struct {
		id string
		n  int
	}
	var rows []row
	for id, a := range scores {
		if n := len(a.Off); n > 0 {
			rows = append(rows, row{id, n})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].n != rows[j].n {
			return rows[i].n > rows[j].n
		}
		return rows[i].id < rows[j].id
	})

	out := make([]string, 0, len(rows))
	for _, r := range rows {
		p, ok := Find(r.id)
		if !ok {
			continue
		}
		out = append(out, fmt.Sprintf("%s (%d): %s %s", r.id, r.n, p.Rule, p.Why))
	}
	return out
}
