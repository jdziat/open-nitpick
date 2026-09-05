// Package fullreview renders what a whole-tree review found in the order a
// reader acts on it. It is a package rather than part of the command so the
// acceptance test in internal/evals can hold the same output to a fixture.
package fullreview

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/review"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// advisoryID is the shape of a vulnerability identifier an analyzer such as
// osv-scanner reports as its rule: a known advisory is deterministic
// evidence, listed as such rather than judged.
var advisoryID = regexp.MustCompile(`^(GHSA-|CVE-|PYSEC-|GO-\d|RUSTSEC-|OSV-|MAL-)`)

// Sections groups what the review found by what a reader does about it:
// known advisories (deterministic, from the dependency scanner, including
// any the review's triage set aside so none is lost), security risks, and
// bugs. Each section says when it is empty, since an absent heading reads
// as "not looked at".
func Sections(report *review.Report) string {
	var advisories, security, bugs, slop []review.Finding
	for _, f := range report.Findings {
		switch {
		case f.FromAnalyzer && advisoryID.MatchString(f.Source):
			advisories = append(advisories, f)
		case f.Class == string(config.ClassSecurity):
			security = append(security, f)
		case f.Class == string(config.ClassSlop):
			slop = append(slop, f)
		default:
			bugs = append(bugs, f)
		}
	}
	var setAside []review.LinterDiscard
	for _, d := range report.Discarded {
		if advisoryID.MatchString(d.Rule) {
			setAside = append(setAside, d)
		}
	}
	for _, list := range [][]review.Finding{advisories, security, bugs, slop} {
		sort.SliceStable(list, func(i, j int) bool {
			ri, rj := config.Severity(list[i].Severity).Rank(), config.Severity(list[j].Severity).Rank()
			if ri != rj {
				return ri > rj
			}
			if list[i].Path != list[j].Path {
				return list[i].Path < list[j].Path
			}
			return list[i].Line < list[j].Line
		})
	}

	var b strings.Builder
	b.WriteString("\nKnown advisories (from the dependency scanner; not judged by the model):\n")
	if len(advisories) == 0 && len(setAside) == 0 {
		b.WriteString("  none reported. If osv-scanner is not installed, no lockfile was scanned; see the analyzer roster above.\n")
	}
	for _, f := range advisories {
		fmt.Fprintf(&b, "  %s  %s:%d  %s\n", f.Source, f.Path, f.Line, f.Title)
	}
	for _, d := range setAside {
		fmt.Fprintf(&b, "  %s  %s:%d  (reported by the scanner, set aside by the review: %s)\n", d.Rule, d.Path, d.Line, d.Reason)
	}
	writeSection(&b, "Security risks", security)
	writeSection(&b, "Bugs", bugs)
	writeSection(&b, "AI slop (rule by rule; see the slop layer of the prompt)", slop)
	return b.String()
}

func writeSection(b *strings.Builder, name string, findings []review.Finding) {
	fmt.Fprintf(b, "\n%s:\n", name)
	if len(findings) == 0 {
		b.WriteString("  none reported.\n")
		return
	}
	for _, f := range findings {
		fmt.Fprintf(b, "  [%s/%s] %s:%d  %s\n", f.Severity, f.Class, f.Path, f.Line, f.Title)
	}
}

// RemediationPlan orders findings most severe first and groups those that
// share a title within a class, since one fix usually clears them together.
// The estimate is in files touched, which is countable; hours are not.
func RemediationPlan(findings []review.Finding) string {
	if len(findings) == 0 {
		return "\nRemediation plan: nothing to remediate.\n"
	}
	type group struct {
		severity config.Severity
		class    string
		title    string
		files    map[string]bool
		first    string
		count    int
	}
	groups := map[string]*group{}
	for _, f := range findings {
		key := f.Class + "\x00" + strings.ToLower(strings.Join(strings.Fields(f.Title), " "))
		g, ok := groups[key]
		if !ok {
			g = &group{severity: config.Severity(f.Severity), class: f.Class, title: f.Title, files: map[string]bool{}, first: fmt.Sprintf("%s:%d", f.Path, f.Line)}
			groups[key] = g
		}
		if config.Severity(f.Severity).Rank() > g.severity.Rank() {
			g.severity = config.Severity(f.Severity)
		}
		g.files[f.Path] = true
		g.count++
		// The earliest anchor by path and line, whatever order the findings
		// arrived in, so the plan's order is a function of its contents.
		if at := fmt.Sprintf("%s:%d", f.Path, f.Line); at < g.first {
			g.first = at
		}
	}
	ordered := make([]*group, 0, len(groups))
	for _, g := range groups {
		ordered = append(ordered, g)
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].severity.Rank() != ordered[j].severity.Rank() {
			return ordered[i].severity.Rank() > ordered[j].severity.Rank()
		}
		if ordered[i].class != ordered[j].class {
			return classOrder(ordered[i].class) < classOrder(ordered[j].class)
		}
		if ordered[i].first != ordered[j].first {
			return ordered[i].first < ordered[j].first
		}
		return ordered[i].title < ordered[j].title
	})

	var b strings.Builder
	b.WriteString("\nRemediation plan, most severe first; findings that share a fix are grouped:\n")
	for i, g := range ordered {
		files := make([]string, 0, len(g.files))
		for p := range g.files {
			files = append(files, p)
		}
		sort.Strings(files)
		fmt.Fprintf(&b, "%3d. [%s/%s] %s\n     %d finding(s) in %d file(s): %s\n", i+1, g.severity, g.class, g.title, g.count, len(files), strings.Join(files, ", "))
	}
	return b.String()
}

// classOrder breaks severity ties: what leaks or breaks before what reads
// badly.
func classOrder(class string) int {
	for i, c := range []string{"security", "correctness", "concurrency", "resource", "contract", "tests", "maintainability", "style"} {
		if c == class {
			return i
		}
	}
	return 99
}

// CoverageNotice says what the tree review did not read, in the voice of the
// review's own notices: silence must not read as clean.
func CoverageNotice(t *vcs.Tree) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\nCovered %d file(s).\n", len(t.Covered))
	if len(t.Unbudgeted) > 0 {
		fmt.Fprintf(&b, "Not reviewed, the budget ran out first (%d file(s)): %s\n", len(t.Unbudgeted), strings.Join(t.Unbudgeted, ", "))
	}
	if len(t.Skipped) > 0 {
		fmt.Fprintf(&b, "Left out (%d file(s)):\n", len(t.Skipped))
		for _, s := range t.Skipped {
			fmt.Fprintf(&b, "  %s: %s\n", s.Path, s.Reason)
		}
	}
	return b.String()
}
