package evals

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/review"
)

// The callers section of docs/findings.md names the dumps behind each of its
// tables. Two review rounds each found a sentence in that section that the
// named dump contradicted: the numbers were right and the explanation was
// wrong. This test re-derives every row, the noise figures, the subset, the
// fixtures named as flipped or lost, and the vocabulary claim from the
// reviewed model's rows of those dumps, committed under
// testdata/findings-dumps, and holds the prose to them, row by row.
func TestCallersSectionMatchesItsDumps(t *testing.T) {
	findings, err := os.ReadFile("../../docs/findings.md")
	if err != nil {
		t.Skip("docs/findings.md not present")
	}
	section := strings.SplitN(string(findings), "## Callers (2026-09-05)", 2)
	if len(section) != 2 {
		t.Fatal("findings.md has no callers section")
	}
	// Bold markers are typography, not content.
	prose := strings.ReplaceAll(section[1], "**", "")

	dump := func(stem string) string {
		return filepath.Join("testdata", "findings-dumps", stem+".jsonl")
	}

	// Callers corpus: two runs each; recall over 8 plants, noise per review
	// over 12 reviews, controls silent.
	callerDefects := map[string][]Defect{}
	for _, f := range CallerFixtures() {
		callerDefects[f.Name] = f.Defects
	}
	for _, c := range []struct{ stem, label string }{
		{"multifile-callers-20260905T163816Z", "| glm + callers, first cut |"},
		{"multifile-callers-20260905T164614Z", "| glm + callers, constants attached |"},
	} {
		g := readGrid(t, dump(c.stem), 2)
		hits, reviews, noise := g.tally("+ctx", callerDefects)
		row := fmt.Sprintf("%s %.2f (%d/8) | %.2f |", c.label, float64(hits)/8, hits, float64(noise)/float64(reviews))
		if !strings.Contains(prose, row) {
			t.Errorf("%s: prose lacks the row the dump gives: %q", c.stem, row)
		}
		if h, _, _ := g.tally("", callerDefects); h != 0 || !strings.Contains(prose, "| glm, diff only | 0.00 (0/8) |") {
			t.Errorf("%s: diff-only hits = %d, prose says 0/8", c.stem, h)
		}
		for _, f := range []string{"go-clean-wrapped-sentinel", "python-clean-precondition-satisfied"} {
			if n := g.findingsOn(f); n != 0 {
				t.Errorf("%s: %d finding(s) on control %s; prose says our controls were silent", c.stem, n, f)
			}
		}
	}

	// Multi-file regression: three runs across two dumps.
	g := readGrid(t, dump("multifile-multifile-20260905T170442Z"), 1)
	g.merge(readGrid(t, dump("multifile-multifile-20260905T170918Z"), 2))
	planted := map[string]bool{}
	multiDefects := map[string][]Defect{}
	for _, f := range MultiFileFixtures() {
		multiDefects[f.Name] = f.Defects
		if !f.Clean() {
			planted[f.Name] = true
		}
	}
	for f := range planted {
		if !g.fixtures[f] {
			t.Fatalf("planted fixture %s is not in the dumps; the corpus has moved since they were taken", f)
		}
	}
	walkHits, walkLost, diffHits, diffLost := 0, 0, 0, 0
	subsetWalk, subsetDiff, subsetN := 0, 0, 0
	var regressed, mustName []string
	for f := range planted {
		w, wl := g.perFixture(f, "+ctx")
		d, dl := g.perFixture(f, "")
		walkHits += w
		walkLost += wl
		diffHits += d
		diffLost += dl
		if wl == 0 && dl == 0 {
			subsetN++
			subsetWalk += w
			subsetDiff += d
		}
		if w < d {
			regressed = append(regressed, f)
		}
		// Every fixture with a lost review on either arm, and every one the
		// diff-only arm never located while the walk always did, is a
		// sentence in the prose.
		if wl > 0 || dl > 0 || (d == 0 && w == g.runs) {
			mustName = append(mustName, f)
		}
	}
	sort.Strings(regressed)
	total := len(planted) * g.runs
	_, walkReviews, walkNoise := g.tally("+ctx", multiDefects)
	_, diffReviews, diffNoise := g.tally("", multiDefects)
	walkRow := fmt.Sprintf("| glm + related context, walk on, 3 runs | %.2f (%d/%d, %d lost) | %.2f |",
		float64(walkHits)/float64(total-walkLost), walkHits, total-walkLost, g.lost("+ctx"), float64(walkNoise)/float64(walkReviews))
	diffRow := fmt.Sprintf("| glm, diff only, 3 runs | %.2f (%d/%d, %d lost) | %.2f |",
		float64(diffHits)/float64(total-diffLost), diffHits, total-diffLost, g.lost(""), float64(diffNoise)/float64(diffReviews))
	subset := fmt.Sprintf("the walk is %d/%d against %d/%d", subsetWalk, subsetN*g.runs, subsetDiff, subsetN*g.runs)
	for _, want := range []string{walkRow, diffRow, subset} {
		if !strings.Contains(prose, want) {
			t.Errorf("prose lacks %q, which the dumps give", want)
		}
	}
	if want := []string{"go-cache-get-unchecked", "go-query-without-deadline", "python-retry-nonidempotent"}; strings.Join(regressed, ",") != strings.Join(want, ",") {
		t.Errorf("fixtures the walk lost runs on: %v", regressed)
	}
	for _, f := range append(regressed, mustName...) {
		if !strings.Contains(prose, "`"+f+"`") {
			t.Errorf("prose does not name %s, which the grid singles out", f)
		}
	}
	// The python-retry loss is a finding on the plant line whose wording
	// misses the keywords: the quoted phrases must be in the finding, the
	// quoted keywords in the plant, and no keyword in the finding.
	r := g.finding("python-retry-nonidempotent", "+ctx", 0)
	if r == nil || r.Line != 11 || r.Matched {
		t.Fatalf("python-retry-nonidempotent walk-on run 0: %+v", r)
	}
	if !strings.Contains(prose, "anchored on the plant itself") {
		t.Errorf("prose does not describe the python-retry loss as anchored on the plant")
	}
	text := strings.ToLower(deref(r.Title) + " " + r.Rationale)
	for _, phrase := range []string{"double-charging", "issued again"} {
		if !strings.Contains(prose, `"`+phrase+`"`) || !strings.Contains(text, phrase) {
			t.Errorf("quoted wording %q: in prose %v, in finding %v", phrase, strings.Contains(prose, phrase), strings.Contains(text, phrase))
		}
	}
	keywords := multiDefects["python-retry-nonidempotent"][0].Keywords
	for _, k := range []string{"double-charge", "charged again"} {
		if !strings.Contains(prose, `"`+k+`"`) || !contains(keywords, k) {
			t.Errorf("quoted keyword %q: in prose %v, in plant %v", k, strings.Contains(prose, k), contains(keywords, k))
		}
	}
	// The instrument's own rule, not a restatement of it.
	if mentionsAny(r.finding(), keywords) {
		t.Errorf("the scorer credits this finding's wording; the prose calls it a vocabulary miss")
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

type gridRow struct {
	Fixture   string  `json:"fixture"`
	Run       int     `json:"run"`
	Variant   string  `json:"variant"`
	Model     string  `json:"model"`
	Silent    *bool   `json:"silent"`
	Path      string  `json:"path"`
	Line      int     `json:"line"`
	Matched   bool    `json:"matched"`
	Title     *string `json:"title"`
	Rationale string  `json:"rationale"`
	Category  string  `json:"category"`
}

func (r gridRow) finding() review.Finding {
	return review.Finding{Path: r.Path, Line: r.Line, Title: deref(r.Title), Rationale: r.Rationale, Category: r.Category}
}

type grid struct {
	rows     []gridRow
	runs     int
	fixtures map[string]bool
}

func readGrid(t *testing.T, path string, runs int) *grid {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	g := &grid{runs: runs, fixtures: map[string]bool{}}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		var r gridRow
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(r.Model, "z-ai/") {
			g.fixtures[r.Fixture] = true
			g.rows = append(g.rows, r)
		}
	}
	return g
}

// merge appends another dump's runs after this one's.
func (g *grid) merge(o *grid) {
	for _, r := range o.rows {
		r.Run += g.runs
		g.rows = append(g.rows, r)
	}
	g.runs += o.runs
	for f := range o.fixtures {
		g.fixtures[f] = true
	}
}

// present reports whether a review exists for the fixture-run.
func (g *grid) present(fixture, variant string, run int) bool {
	for _, r := range g.rows {
		if r.Fixture == fixture && r.Variant == variant && r.Run == run {
			return true
		}
	}
	return false
}

func (g *grid) hit(fixture, variant string, run int) bool {
	for _, r := range g.rows {
		if r.Fixture == fixture && r.Variant == variant && r.Run == run && r.Matched {
			return true
		}
	}
	return false
}

// perFixture counts runs located and runs lost for one fixture and arm.
func (g *grid) perFixture(fixture, variant string) (hits, lost int) {
	for run := 0; run < g.runs; run++ {
		switch {
		case !g.present(fixture, variant, run):
			lost++
		case g.hit(fixture, variant, run):
			hits++
		}
	}
	return
}

func (g *grid) lost(variant string) int {
	n := 0
	for f := range g.fixtures {
		_, l := g.perFixture(f, variant)
		n += l
	}
	return n
}

// tally sums hits, reviews present and noise findings across the arm. Noise
// is the scorer's rule: an unmatched finding that explains no plant of its
// fixture (explainsAny), not merely an unmatched one.
func (g *grid) tally(variant string, defects map[string][]Defect) (hits, reviews, noise int) {
	for f := range g.fixtures {
		h, l := g.perFixture(f, variant)
		hits += h
		reviews += g.runs - l
	}
	for _, r := range g.rows {
		if r.Variant != variant || r.Path == "" || r.Matched || (r.Silent != nil && *r.Silent) {
			continue
		}
		if !explainsAny(r.finding(), defects[r.Fixture]) {
			noise++
		}
	}
	return
}

func (g *grid) findingsOn(fixture string) int {
	n := 0
	for _, r := range g.rows {
		if r.Fixture == fixture && r.Path != "" && (r.Silent == nil || !*r.Silent) {
			n++
		}
	}
	return n
}

func (g *grid) finding(fixture, variant string, run int) *gridRow {
	for i := range g.rows {
		r := &g.rows[i]
		if r.Fixture == fixture && r.Variant == variant && r.Run == run && r.Path != "" {
			return r
		}
	}
	return nil
}
