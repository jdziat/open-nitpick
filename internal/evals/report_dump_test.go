package evals

import (
	"fmt"
	"os"
	"path"
	"sort"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/v2/internal/review"
)

// TestReportFromDump re-derives the judge-free columns from a retained run,
// broken down by language, so a per-language comparison costs nothing to
// print and reproduces from the file. It runs only when NITPICK_EVAL_REPORT
// names a dump (or several, comma-separated); every fixture the dump names
// must still exist with the same content, or that fixture is skipped and
// said so.
func TestReportFromDump(t *testing.T) {
	paths := strings.TrimSpace(os.Getenv("NITPICK_EVAL_REPORT"))
	if paths == "" {
		t.Skip("set NITPICK_EVAL_REPORT=<dump.jsonl>[,<dump.jsonl>] to print a per-language report")
	}

	fixtures := map[string]Fixture{}
	for _, f := range EveryFixture() {
		fixtures[f.Name] = f
	}

	type cell struct {
		reviews, plants, located, noise, anchor int
	}
	type key struct{ contender, language string }
	cells := map[key]*cell{}
	var skipped []string

	for _, p := range strings.Split(paths, ",") {
		records, err := ReadDump(strings.TrimSpace(p))
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		// One row per finding, plus a silent row per review that produced
		// none; regroup them into reviews.
		type reviewKey struct {
			model, variant, fixture string
			run                     int
		}
		grouped := map[reviewKey][]review.Finding{}
		var order []reviewKey
		for _, rec := range records {
			k := reviewKey{rec.Model, rec.Variant, rec.Fixture, rec.Run}
			if _, seen := grouped[k]; !seen {
				order = append(order, k)
				grouped[k] = nil
			}
			if rec.Path == "" {
				continue
			}
			grouped[k] = append(grouped[k], review.Finding{
				Path: rec.Path, Line: rec.Line, EndLine: rec.EndLine, AlsoAt: rec.AlsoAt,
				Severity: rec.Severity, Class: rec.Class, Category: rec.Category,
				Title: rec.Title, Rationale: rec.Rationale,
			})
		}
		for _, k := range order {
			f, ok := fixtures[k.fixture]
			if !ok || fixtureFingerprint(f) != hashFor(records, k.fixture) {
				skipped = append(skipped, k.fixture)
				continue
			}
			name := k.model
			if k.variant != "" {
				name += " " + k.variant
			}
			d := ScoreDetection(f, grouped[k])
			for _, lang := range []string{fixtureLanguage(f), "all"} {
				c := cells[key{name, lang}]
				if c == nil {
					c = &cell{}
					cells[key{name, lang}] = c
				}
				c.reviews++
				c.plants += len(f.Defects)
				c.located += d.Matched
				c.noise += d.Noise()
				c.anchor = max(c.anchor, d.WidestAnchor)
			}
		}
	}

	var contenders, languages []string
	seenC, seenL := map[string]bool{}, map[string]bool{}
	for k := range cells {
		if !seenC[k.contender] {
			seenC[k.contender] = true
			contenders = append(contenders, k.contender)
		}
		if !seenL[k.language] {
			seenL[k.language] = true
			languages = append(languages, k.language)
		}
	}
	sort.Strings(contenders)
	sort.Strings(languages)

	var b strings.Builder
	fmt.Fprintf(&b, "\n%-12s", "language")
	for _, c := range contenders {
		fmt.Fprintf(&b, " %-30s", shortName(c))
	}
	b.WriteString("\n")
	for _, lang := range languages {
		fmt.Fprintf(&b, "%-12s", lang)
		for _, c := range contenders {
			cl := cells[key{c, lang}]
			if cl == nil || cl.reviews == 0 {
				fmt.Fprintf(&b, " %-30s", "-")
				continue
			}
			fmt.Fprintf(&b, " %-30s", fmt.Sprintf("R %d/%d N %d/%d A %d", cl.located, cl.plants, cl.noise, cl.reviews, cl.anchor))
		}
		b.WriteString("\n")
	}
	b.WriteString("\nR = located/plants, N = noise findings/reviews, A = widest anchor in lines.\n")
	if len(skipped) > 0 {
		fmt.Fprintf(&b, "skipped (fixture changed since the run): %s\n", strings.Join(dedupe(skipped), ", "))
	}
	t.Log(b.String())
}

// hashFor returns the fixture hash the dump recorded for a fixture.
func hashFor(records []DumpRecord, fixture string) string {
	for _, r := range records {
		if r.Fixture == fixture && r.FixtureHash != "" {
			return r.FixtureHash
		}
	}
	return ""
}

func shortName(n string) string {
	if strings.HasSuffix(n, "/cli") {
		return strings.TrimSuffix(n, "/cli")
	}
	if i := strings.Index(n, "/"); i >= 0 {
		n = n[i+1:]
	}
	if len(n) > 30 {
		n = n[:30]
	}
	return n
}

// fixtureLanguage names a fixture's language from the extension of the
// files its change touches, majority wins.
func fixtureLanguage(f Fixture) string {
	counts := map[string]int{}
	for p, head := range f.Head {
		if base, ok := f.Base[p]; ok && base == head {
			continue
		}
		ext := strings.TrimPrefix(path.Ext(p), ".")
		switch ext {
		case "go", "py", "ts", "tsx", "js", "rb", "rs", "java", "kt", "cs", "php", "swift", "c", "cc", "cpp", "sh", "sql", "kts":
			counts[ext]++
		}
	}
	best, n := "other", 0
	for ext, c := range counts {
		if c > n || (c == n && ext < best) {
			best, n = ext, c
		}
	}
	switch best {
	case "tsx":
		return "ts"
	case "kts":
		return "kt"
	case "cc", "cpp":
		return "c"
	}
	return best
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

var _ = review.Finding{}
