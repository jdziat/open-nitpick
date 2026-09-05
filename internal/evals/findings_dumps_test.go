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
)

// The callers section of docs/findings.md names the dumps behind each of its
// tables. Two review rounds each found a sentence in that section that the
// named dump contradicted: the numbers were right and the explanation was
// wrong. This test re-derives the numbers and the named fixtures from those
// dumps and holds the prose to them. It runs only where the dumps are, since
// .eval-runs is not committed; a checkout without them skips.
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
		m, _ := filepath.Glob(filepath.Join(".eval-runs", stem+"*.jsonl"))
		if len(m) != 1 {
			t.Skipf("dump %s not present", stem)
		}
		return m[0]
	}

	// Callers corpus: two runs each; recall over 8 plants, noise per review
	// over 12 reviews, controls silent.
	for _, c := range []struct{ stem, want string }{
		{"multifile-callers-20260905T163816Z", "0.62 (5/8) | 0.17"},
		{"multifile-callers-20260905T164614Z", "0.88 (7/8) | 0.08"},
	} {
		g := readGrid(t, dump(c.stem), 2)
		hits, reviews, noise := g.tally("+ctx")
		row := fmt.Sprintf("%.2f (%d/8) | %.2f", float64(hits)/8, hits, float64(noise)/float64(reviews))
		if row != c.want || !strings.Contains(prose, c.want) {
			t.Errorf("%s: dump says %q, prose wants %q", c.stem, row, c.want)
		}
		if h, _, _ := g.tally(""); h != 0 || !strings.Contains(prose, "0.00 (0/8)") {
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
	for _, f := range MultiFileFixtures() {
		if !f.Clean() {
			planted[f.Name] = true
		}
	}
	walkHits, walkLost, diffHits, diffLost := 0, 0, 0, 0
	subsetWalk, subsetDiff, subsetN := 0, 0, 0
	var regressed []string
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
	}
	sort.Strings(regressed)
	walkRow := fmt.Sprintf("0.92 (%d/%d, %d lost)", walkHits, 36-walkLost, g.lost("+ctx"))
	diffRow := fmt.Sprintf("0.61 (%d/%d, %d lost)", diffHits, 36-diffLost, g.lost(""))
	subset := fmt.Sprintf("the walk is %d/%d against %d/%d", subsetWalk, subsetN*3, subsetDiff, subsetN*3)
	for _, want := range []string{walkRow, diffRow, subset} {
		if !strings.Contains(prose, want) {
			t.Errorf("prose lacks %q, which the dumps give", want)
		}
	}
	if want := []string{"go-cache-get-unchecked", "go-query-without-deadline", "python-retry-nonidempotent"}; strings.Join(regressed, ",") != strings.Join(want, ",") {
		t.Errorf("fixtures the walk lost runs on: %v", regressed)
	}
	for _, f := range regressed {
		if !strings.Contains(prose, "`"+f+"`") {
			t.Errorf("prose does not name %s among the fixtures that went the other way", f)
		}
	}
	// The python-retry loss is a finding on the plant line, not an anchor miss.
	if r := g.finding("python-retry-nonidempotent", "+ctx", 0); r == nil || r.Line != 11 || r.Matched {
		t.Errorf("python-retry-nonidempotent walk-on run 0: %+v", r)
	} else if !strings.Contains(prose, "anchored on the plant itself") {
		t.Errorf("prose does not describe the python-retry loss as anchored on the plant")
	}
}

type gridRow struct {
	Fixture string  `json:"fixture"`
	Run     int     `json:"run"`
	Variant string  `json:"variant"`
	Model   string  `json:"model"`
	Silent  *bool   `json:"silent"`
	Path    string  `json:"path"`
	Line    int     `json:"line"`
	Matched bool    `json:"matched"`
	Title   *string `json:"title"`
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
		g.fixtures[r.Fixture] = true
		if strings.HasPrefix(r.Model, "z-ai/") {
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

// tally sums hits, reviews present and noise findings across the arm.
func (g *grid) tally(variant string) (hits, reviews, noise int) {
	for f := range g.fixtures {
		h, l := g.perFixture(f, variant)
		hits += h
		reviews += g.runs - l
	}
	for _, r := range g.rows {
		if r.Variant == variant && r.Path != "" && !r.Matched && (r.Silent == nil || !*r.Silent) {
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
