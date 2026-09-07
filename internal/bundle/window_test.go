package bundle

// Window properties.
//
// window() decides what the model is shown of a file it cannot be shown all of.
// The failure it can produce is the worst kind this codebase has: if a changed
// line is missing from the content, the defect on it is invisible, the review
// reports nothing, and a silent zero is indistinguishable from a clean file.
// One worked example cannot rule that out across widths, hunk counts and hunk
// positions, so the invariant is asserted over generated shapes instead.

import (
	"context"
	"fmt"
	"math/rand/v2"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	llms "github.com/nocturnium/llm-go-sdk/v6"

	"github.com/jdziat/open-nitpick/internal/diff"
)

// numberedPrefix is the width a line number is padded to, by both numberLines
// and a window. Parsing the output back is the only way to assert on what was
// emitted rather than on what a substring search happens to find.
const numberedPrefix = 6

// emitted parses window() output into the line numbers it emitted and their
// text, plus how many elision markers separated them.
func emitted(t *testing.T, out string) (map[int]string, []int, int) {
	t.Helper()

	var (
		text  = map[int]string{}
		order []int
		gaps  int
	)

	for _, line := range splitLines(out) {
		if len(line) < numberedPrefix+2 {
			t.Fatalf("emitted line %q is too short to carry a %d-wide number", line, numberedPrefix)
		}

		num := strings.TrimSpace(line[:numberedPrefix])
		if num == "" {
			gaps++
			continue
		}

		n, err := strconv.Atoi(num)
		if err != nil {
			t.Fatalf("emitted line %q does not start with a line number: %v", line, err)
		}
		if _, dup := text[n]; dup {
			t.Fatalf("line %d was emitted twice", n)
		}
		text[n] = line[numberedPrefix+2:]
		order = append(order, n)
	}

	return text, order, gaps
}

// shape is one generated file-and-diff pair to window.
type shape struct {
	name    string
	content string
	file    *diff.File
}

// bodyLine is the text of line n of a generated file. Every line is distinct so
// a window that emitted the right count but the wrong lines still fails.
func bodyLine(n int) string { return fmt.Sprintf("body line %d", n) }

// makeShape builds a file of n lines whose diff adds the given new-file lines.
// changed may name lines past n on purpose: ChangedLines yields new-file numbers
// while content can be fetched at a different revision, so the two disagreeing
// is a real state and not a malformed input.
func makeShape(name string, n int, changed []int) shape {
	var body strings.Builder
	for i := 1; i <= n; i++ {
		body.WriteString(bodyLine(i))
		body.WriteByte('\n')
	}

	// One hunk per changed line: hunk grouping does not affect ChangedLines,
	// and this keeps the generator from having to model contiguity.
	hunks := make([]diff.Hunk, 0, len(changed))
	for _, ln := range changed {
		hunks = append(hunks, diff.Hunk{
			NewStart: ln,
			NewLines: 1,
			Lines:    []diff.Line{{Kind: diff.LineAdded, NewLine: ln, Content: bodyLine(ln)}},
		})
	}

	return shape{
		name:    name,
		content: body.String(),
		file:    &diff.File{Path: "a.go", Kind: diff.ChangeModified, Hunks: hunks},
	}
}

// generatedShapes returns the edge shapes by name plus a deterministic spread of
// random ones. Named and random together on purpose: the named cases are the
// ones a reader must be able to see are covered, the random ones are what catch
// the interaction nobody thought to name.
func generatedShapes(t *testing.T) []shape {
	t.Helper()

	shapes := []shape{
		makeShape("empty file", 0, []int{1}),
		makeShape("single line, changed", 1, []int{1}),
		makeShape("hunk at line 1", 200, []int{1}),
		makeShape("hunk at end of file", 200, []int{200}),
		makeShape("hunks at both ends", 200, []int{1, 200}),
		makeShape("every line changed", 60, seq(1, 60)),
		makeShape("no changed lines", 200, nil),
		// Content fetched at a revision where the file was shorter, so the
		// diff's numbers run off the end of what was read.
		makeShape("all changes past end of file", 3, []int{9000, 9001}),
		makeShape("changes straddling end of file", 100, []int{50, 150}),
		makeShape("change just past end of file", 100, []int{101}),
		makeShape("duplicate changed lines", 100, []int{50, 50, 50}),
		makeShape("unsorted changed lines", 100, []int{90, 10, 50}),
	}

	// Fixed seeds: a property test that generates a different corpus per run
	// reports failures nobody can reproduce.
	rng := rand.New(rand.NewPCG(1, 2))
	for i := range 200 {
		lines := rng.IntN(400)
		// Beyond lines, so roughly a fifth of the generated hunks land past the
		// end of the content and exercise the clamp.
		span := lines + lines/4 + 1

		changed := make([]int, 0, 16)
		for range 1 + rng.IntN(15) {
			changed = append(changed, 1+rng.IntN(span))
		}
		shapes = append(shapes, makeShape(fmt.Sprintf("random/%d", i), lines, changed))
	}

	return shapes
}

// seq returns the inclusive range lo..hi.
func seq(lo, hi int) []int {
	out := make([]int, 0, hi-lo+1)
	for i := lo; i <= hi; i++ {
		out = append(out, i)
	}
	return out
}

// testWidths spans the regime the width search walks (it doubles from
// minContextLines to a ceiling derived per file), plus the degenerate widths a
// future caller could pass. Zero is included deliberately: even a window with
// no context at all must still carry the changed lines themselves.
func testWidths() []int {
	return []int{0, 1, 3, minContextLines, 8, 16, 32, 64, 128, 256, 1000}
}

// windowOf renders one shape at a width, which is the whole of what window
// behaviour is: a file, its edits, and how much of it survives.
func windowOf(s shape, width int) (text string, srcBytes int, elided bool) {
	return newWindower(s.content, s.file).render(width)
}

// sourceBytes reconstructs how much of the original file a numbered window
// retained, by parsing the window back rather than by asking the code that
// produced it. review.max_file_bytes is enforced in these units, so a test that
// took the implementation's own number could not catch it drifting.
func sourceBytes(t *testing.T, windowed string) int {
	t.Helper()

	text, _, _ := emitted(t, windowed)
	n := 0
	for _, line := range text {
		n += len(line) + 1
	}
	return n
}

// TestWindowNeverDropsAChangedLine is the invariant. A changed line absent from
// the content the model receives is a defect the model cannot see, and an unseen
// defect is reported as no defect.
func TestWindowNeverDropsAChangedLine(t *testing.T) {
	// Most of the assertion lives behind "the window elided
	// something", so a generator that stopped producing eliding shapes would
	// turn this test green while testing nothing.
	var checked, kept int

	for _, s := range generatedShapes(t) {
		for _, width := range testWidths() {
			got, _, elided := windowOf(s, width)

			lines := splitLines(s.content)
			if !elided {
				// Nothing was elided, so the whole file is the answer and it must come
				// back untouched, Render numbers unelided content itself and would
				// otherwise number it twice.
				if got != s.content {
					t.Fatalf("%s width %d: nothing elided but content changed:\n%q", s.name, width, got)
				}
				continue
			}

			checked++

			text, _, _ := emitted(t, got)
			for _, ln := range s.file.ChangedLines() {
				if ln < 1 || ln > len(lines) {
					// Not in the content that was read, so there is nothing
					// here to keep. Covered by the end-of-file cases above.
					continue
				}
				if _, ok := text[ln]; !ok {
					t.Fatalf("%s width %d: changed line %d was windowed out; the defect on it would be invisible and the review would report nothing\n%s",
						s.name, width, ln, got)
				}
				kept++
			}
		}
	}

	// Thresholds well under what the current generator produces, so they catch
	// a corpus that collapsed rather than one that merely shifted.
	if checked < 500 || kept < 1000 {
		t.Errorf("only %d windows elided anything and only %d changed lines were checked inside them; the corpus stopped exercising the invariant",
			checked, kept)
	}
}

// TestWindowEmitsExactlyTheLinesWithinTheWidth pins what a window IS. Without
// it, "never drops a changed line" is satisfied by returning the whole file,
// and the width would stop meaning anything. Which is what makes widening the
// window to fill the budget a real gain rather than a relabelling.
func TestWindowEmitsExactlyTheLinesWithinTheWidth(t *testing.T) {
	for _, s := range generatedShapes(t) {
		for _, width := range testWidths() {
			got, srcBytes, elided := windowOf(s, width)
			if !elided {
				continue
			}

			lines := splitLines(s.content)
			changed := s.file.ChangedLines()

			want := map[int]bool{}
			for i := 1; i <= len(lines); i++ {
				for _, ln := range changed {
					if abs(i-ln) <= width {
						want[i] = true
						break
					}
				}
			}

			text, order, gaps := emitted(t, got)
			for i := range want {
				if _, ok := text[i]; !ok {
					t.Fatalf("%s width %d: line %d is within %d of a change but was not emitted", s.name, width, i, width)
				}
			}
			for i, line := range text {
				if !want[i] {
					t.Fatalf("%s width %d: line %d is not within %d of any change but was emitted as %q",
						s.name, width, i, width, line)
				}
				// The number a citation would use has to name the line the
				// reader is looking at, or every finding lands one line off.
				if line != bodyLine(i) {
					t.Fatalf("%s width %d: line %d reads %q, want %q", s.name, width, i, line, bodyLine(i))
				}
			}

			// The retained size is reported in the file's OWN bytes, because
			// review.max_file_bytes is compared against both it and the whole
			// file. Measured on the numbered text instead, a window came out
			// as much as 3.5x larger than the file it was cut from, so a cap
			// that admitted the file whole rejected every window of it.
			source := 0
			for _, line := range text {
				source += len(line) + 1
			}
			if srcBytes != source {
				t.Fatalf("%s width %d: reported %d source bytes, want %d from the lines it emitted",
					s.name, width, srcBytes, source)
			}
			if srcBytes > len(s.content) {
				t.Fatalf("%s width %d: a window of a %d-byte file reports %d bytes; a subset of a file cannot be bigger than it",
					s.name, width, len(s.content), srcBytes)
			}

			for i := 1; i < len(order); i++ {
				if order[i] <= order[i-1] {
					t.Fatalf("%s width %d: emitted %d after %d, want ascending order", s.name, width, order[i], order[i-1])
				}
			}
			// One marker per run of missing lines, and none anywhere else. A
			// marker with nothing behind it tells the model code was withheld
			// between two lines it is looking at, which invites exactly the
			// hedged "I cannot see the rest of this" non-finding the window
			// exists to avoid.
			runs, inGap := 0, false
			for i := 1; i <= len(lines); i++ {
				if want[i] {
					inGap = false
					continue
				}
				if !inGap {
					runs++
					inGap = true
				}
			}
			if gaps != runs {
				t.Fatalf("%s width %d: emitted %d elision markers for %d runs of omitted lines:\n%s",
					s.name, width, gaps, runs, got)
			}
			if gaps == 0 {
				t.Fatalf("%s width %d: reported eliding but emitted no elision marker, so the model is shown a gap it cannot see:\n%s",
					s.name, width, got)
			}
		}
	}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// TestChangedLinesSurviveWhateverWidthTheBudgetPicks carries the invariant
// through fitEntry. window() choosing correctly is worthless if the width
// search, the byte cap, or a later edit to Render can still lose a changed line
// from what ships.
//
// Swept over file sizes as well as limits, because the width is now chosen per
// file: a size that lands between two of the search's steps is exactly where a
// changed line would go missing, and one file length cannot find it.
func TestChangedLinesSurviveWhateverWidthTheBudgetPicks(t *testing.T) {
	sizes := []int{1, 2, 40, 200, 999, 3_000, 12_500, 40_000}
	budgets := []int{200, 1_000, 3_000, 6_000, 12_000, 25_000, 60_000, 200_000}
	capsBytes := []int{1 << 10, 16 << 10, 256 << 10, 4 << 20}

	var windowed, checked int

	for _, lines := range sizes {
		var body strings.Builder
		for i := 1; i <= lines; i++ {
			fmt.Fprintf(&body, "%s // padding to make the file expensive\n", bodyLine(i))
		}
		fetch := func(context.Context, string) ([]byte, error) { return []byte(body.String()), nil }

		// Edits at both edges, in a tight cluster, and alone in the middle: the
		// positions that decide where a window's boundaries land.
		var changed []int
		for _, ln := range []int{1, lines / 4, lines/4 + 1, lines / 2, lines - 1, lines} {
			if ln >= 1 && ln <= lines && !slices.Contains(changed, ln) {
				changed = append(changed, ln)
			}
		}
		slices.Sort(changed)

		hunks := make([]diff.Hunk, 0, len(changed))
		for _, ln := range changed {
			hunks = append(hunks, diff.Hunk{
				NewStart: ln,
				NewLines: 1,
				Lines: []diff.Line{{
					Kind:    diff.LineAdded,
					NewLine: ln,
					Content: bodyLine(ln) + " // padding to make the file expensive",
				}},
			})
		}
		files := diff.Files{{Path: "a.go", Kind: diff.ChangeModified, Hunks: hunks}}

		for _, budget := range budgets {
			for _, maxBytes := range capsBytes {
				where := fmt.Sprintf("%d lines, budget %d, cap %d", lines, budget, maxBytes)

				cfg := baseConfig()
				cfg.Review.TokenBudgetPerRequest = budget
				cfg.Review.MaxFileBytes = maxBytes

				plan, err := Assemble(context.Background(), cfg, files, fetch)
				if err != nil {
					t.Fatalf("%s: Assemble: %v", where, err)
				}

				entry := plan.Batches[0].Entries[0]
				if !entry.HasContent() {
					// Diff-only is a legitimate outcome at the tightest
					// budgets, but only when it was recorded: an entry that
					// reads as fully attached while carrying nothing is the
					// silent zero.
					if len(plan.Degraded) != 1 {
						t.Fatalf("%s: content was dropped without a Degraded record; the run would report the file as reviewed in full",
							where)
					}
					continue
				}
				checked++

				if !entry.Truncated {
					// A whole file is numbered by Render, not by the window, so the invariant
					// is that every changed line is present at all. Which it is by
					// construction. Nothing to parse.
					continue
				}
				windowed++

				text, _, _ := emitted(t, entry.Content)
				for _, ln := range changed {
					if _, ok := text[ln]; !ok {
						t.Fatalf("%s: chose a %d-line window that lost changed line %d",
							where, entry.ContextLines, ln)
					}
				}
				if got := sourceBytes(t, entry.Content); got > maxBytes {
					t.Errorf("%s: the window retains %d bytes of the file, over the cap", where, got)
				}
			}
		}
	}

	// Most of this test's value is behind "the entry was windowed at all", so a
	// sweep that stopped producing windows would pass while testing nothing.
	if windowed < 50 || checked < 120 {
		t.Errorf("only %d of %d attached entries were windowed; the sweep stopped exercising the width search", windowed, checked)
	}
	t.Logf("%d configurations (%d sizes x %d budgets x %d caps): %d attached content, %d of those windowed; every changed line present in all of them, every drop recorded",
		len(sizes)*len(budgets)*len(capsBytes), len(sizes), len(budgets), len(capsBytes), checked, windowed)
}

// oneEditFile builds an n-line file whose diff adds a single line, plus a
// fetcher serving it: the shape a window exists for, at whatever size a test
// needs.
func oneEditFile(n, at int) (diff.Files, ContentFetcher) {
	var body strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&body, "%s // padding to make the file expensive\n", bodyLine(i))
	}
	fetch := func(context.Context, string) ([]byte, error) { return []byte(body.String()), nil }

	files := diff.Files{{Path: "a.go", Kind: diff.ChangeModified, Hunks: []diff.Hunk{{
		NewStart: at,
		NewLines: 1,
		Lines:    []diff.Line{{Kind: diff.LineAdded, NewLine: at, Content: "edited"}},
	}}}}
	return files, fetch
}

// TestWiderBudgetBuysWiderContext pins the point of sizing the window against
// the budget. Headroom left unspent is context the reviewer could have had and
// did not, and the failure this rules out is a plateau: with the ceiling
// pinned at 200 lines, every budget from 5,000 to 235,507 tokens bought the
// same 401-line window (98% of the largest of those requests unused), and the
// next token bought the whole 20,000-line file.
func TestWiderBudgetBuysWiderContext(t *testing.T) {
	files, fetch := oneEditFile(6000, 3000)

	widthAt := func(budget int) (width, tokens int) {
		t.Helper()

		cfg := baseConfig()
		cfg.Review.TokenBudgetPerRequest = budget

		plan, err := Assemble(context.Background(), cfg, files, fetch)
		if err != nil {
			t.Fatalf("budget %d: Assemble: %v", budget, err)
		}
		entry := plan.Batches[0].Entries[0]
		if !entry.Truncated {
			t.Fatalf("budget %d: file should not have fit whole", budget)
		}
		return entry.ContextLines, entry.Tokens
	}

	budgets := []int{2_000, 8_000, 30_000, 60_000}

	prev, prevBudget := 0, 0
	for _, budget := range budgets {
		width, tokens := widthAt(budget)
		if width <= prev {
			t.Errorf("a %d-token budget bought %d lines of context where %d bought %d: headroom must buy context",
				budget, width, prevBudget, prev)
		}
		// A window that leaves most of the request empty is the plateau this
		// test exists to catch, stated as a fraction so it survives a change in
		// how entries are costed.
		if tokens*2 < budget {
			t.Errorf("budget %d settled on a %d-line window costing %d tokens, under half the request: the search stopped short of what the budget would pay for",
				budget, width, tokens)
		}
		prev, prevBudget = width, budget
	}
}

// TestTheWidthChosenIsNearlyTheWidestThatFits pins the search's precision, not
// merely its direction. Doubling alone locates the answer only within a factor
// of two, so half the context the limits would have paid for could be left on
// the table and every other test here would still pass: they all check that
// MORE budget buys MORE context, which a search that always settles for half
// does perfectly well.
//
// The tolerance is derived from windowBisectSteps rather than picked. The
// doubling leaves a gap of at most the accepted width; each bisection halves
// it; after four the width that failed is within 1/16 of the width that won. A
// window an eighth wider must therefore be one that was tried and refused.
func TestTheWidthChosenIsNearlyTheWidestThatFits(t *testing.T) {
	estimator := llms.DefaultTokenEstimator()

	type probe struct {
		name             string
		lines, at        int
		budget, maxBytes int
	}
	var probes []probe
	for _, lines := range []int{2_000, 8_000, 30_000} {
		for _, budget := range []int{3_000, 12_000, 60_000} {
			probes = append(probes, probe{
				name: fmt.Sprintf("%dL budget %d", lines, budget),
				at:   lines / 2, lines: lines, budget: budget, maxBytes: 4 << 20,
			})
		}
		// A cap tight enough that it, not the budget, decides the width.
		probes = append(probes, probe{
			name: fmt.Sprintf("%dL cap-bound", lines),
			at:   lines / 2, lines: lines, budget: 1 << 20, maxBytes: 24 << 10,
		})
	}

	tested := 0
	for _, p := range probes {
		t.Run(p.name, func(t *testing.T) {
			files, fetch := oneEditFile(p.lines, p.at)

			cfg := baseConfig()
			cfg.Review.TokenBudgetPerRequest = p.budget
			cfg.Review.MaxFileBytes = p.maxBytes

			plan, err := Assemble(context.Background(), cfg, files, fetch)
			if err != nil {
				t.Fatalf("Assemble: %v", err)
			}
			entry := plan.Batches[0].Entries[0]
			if !entry.Truncated {
				t.Skip("the whole file fit, so no width was chosen")
			}
			tested++

			body, err := fetch(context.Background(), "a.go")
			if err != nil {
				t.Fatalf("fetch: %v", err)
			}

			wider := entry.ContextLines + max(1, entry.ContextLines/8)
			text, srcBytes, elided := newWindower(string(body), files[0]).render(wider)
			if !elided {
				return // Nothing wider is a window at all; the search hit the ceiling.
			}
			if srcBytes > p.maxBytes {
				return // Refused by the cap, as the search must also have found.
			}

			trial := entry
			trial.Content, trial.ContextLines = text, wider
			if cost := estimator.EstimateTokens(Render(trial)); cost <= p.budget {
				t.Errorf("settled on a %d-line window costing %d, when %d lines cost %d and the budget is %d: the search stopped an eighth short of what it could afford",
					entry.ContextLines, entry.Tokens, wider, cost, p.budget)
			}
		})
	}

	if tested < 6 {
		t.Errorf("only %d probes reached a windowed entry; the sweep stopped testing the search", tested)
	}
}

// TestWindowCeilingIsTheWidestThatElides pins the derived ceiling, which is
// what replaced a fixed one. It has to be exact in both directions: one line
// too high and the search wastes a render on a "window" that is the whole file,
// one line too low and the file loses context its budget would have paid for.
func TestWindowCeilingIsTheWidestThatElides(t *testing.T) {
	for _, s := range generatedShapes(t) {
		w := newWindower(s.content, s.file)
		ceiling := w.ceiling()

		if ceiling < 0 {
			// No anchor landed in the content, so no width can elide anything.
			for _, width := range testWidths() {
				if _, _, elided := windowOf(s, width); elided {
					t.Fatalf("%s: ceiling reports no window is possible, yet width %d elided", s.name, width)
				}
			}
			continue
		}

		if _, _, elided := windowOf(s, ceiling); !elided {
			t.Errorf("%s: ceiling %d elides nothing, so the search would spend a render on the whole file", s.name, ceiling)
		}
		if _, _, elided := windowOf(s, ceiling+1); elided {
			t.Errorf("%s: width %d still elides, so the ceiling of %d gave up context the budget might have paid for",
				s.name, ceiling+1, ceiling)
		}
	}
}

// TestOversizedFileIsNotCostedBeforeItIsRejected pins the one property here
// whose failure is an outage rather than a bad review. review.max_file_bytes
// can force a window on its own, so a file over it has a whole-file cost that
// will never be used, and computing it anyway meant rendering, numbering and
// token-scanning the entire file first, at a size nothing bounds. Measured on
// a 22.8 MB file: 243 MiB of allocation and 800,676 mallocs to produce a 29
// KiB window, against 21.7 MiB when the cap was checked first. An OOM here
// takes the whole run down, which is the largest silent zero available.
//
// Asserted as a multiple of the file rather than an absolute, so it measures
// the shape of the work and not the machine. Windowing a file it intends to
// keep costs a few passes over it; costing the whole file first costs an order
// of magnitude more, and the two do not overlap.
func TestOversizedFileIsNotCostedBeforeItIsRejected(t *testing.T) {
	const (
		lines     = 60_000
		lineBytes = 64
	)

	var body strings.Builder
	body.Grow(lines * lineBytes)
	for i := 1; i <= lines; i++ {
		fmt.Fprintf(&body, "%-*s\n", lineBytes-1, bodyLine(i))
	}
	files := diff.Files{{Path: "a.go", Kind: diff.ChangeModified, Hunks: []diff.Hunk{{
		NewStart: lines / 2, NewLines: 1,
		Lines: []diff.Line{{Kind: diff.LineAdded, NewLine: lines / 2, Content: "edited"}},
	}}}}

	raw := []byte(body.String())
	fetch := func(context.Context, string) ([]byte, error) { return raw, nil }

	cfg := baseConfig()
	if len(raw) <= cfg.Review.MaxFileBytes {
		t.Fatalf("body is %d bytes, want it over the %d-byte cap", len(raw), cfg.Review.MaxFileBytes)
	}

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)

	plan, err := Assemble(context.Background(), cfg, files, fetch)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	runtime.ReadMemStats(&after)

	entry := plan.Batches[0].Entries[0]
	if !entry.Truncated {
		t.Fatalf("the file should have been windowed; degraded=%+v", plan.Degraded)
	}

	allocated := after.TotalAlloc - before.TotalAlloc
	if limit := uint64(6 * len(raw)); allocated > limit {
		t.Errorf("allocated %d bytes windowing a %d-byte file (%.1fx, %d mallocs), over the %dx this should take: the whole file is being costed before the cap that rejects it",
			allocated, len(raw), float64(allocated)/float64(len(raw)), after.Mallocs-before.Mallocs, 6)
	}
	t.Logf("windowing a %d-byte file to %d bytes at width %d allocated %.1fx the file in %d mallocs",
		len(raw), len(entry.Content), entry.ContextLines,
		float64(allocated)/float64(len(raw)), after.Mallocs-before.Mallocs)
}

// TestWindowKeepsTheSiteOfADeletion pins that a window is built around edits,
// not merely around additions. Render tells the model it is seeing the regions
// around every edit; a pure-deletion hunk whose site was elided made that a
// false claim, and a removed guard is invisible when the code that needed it is
// not shown.
func TestWindowKeepsTheSiteOfADeletion(t *testing.T) {
	const lines = 2000

	var body strings.Builder
	for i := 1; i <= lines; i++ {
		body.WriteString(bodyLine(i))
		body.WriteByte('\n')
	}

	// Hunk one adds new line 10. Hunk two removes an old line that sat between
	// new lines 899 and 900, and adds nothing.
	f := &diff.File{Path: "a.go", Kind: diff.ChangeModified, Hunks: []diff.Hunk{
		{
			NewStart: 10, NewLines: 1,
			Lines: []diff.Line{{Kind: diff.LineAdded, NewLine: 10, Content: bodyLine(10)}},
		},
		{
			NewStart: 899, NewLines: 1,
			Lines: []diff.Line{
				{Kind: diff.LineContext, OldLine: 900, NewLine: 899, Content: bodyLine(899)},
				{Kind: diff.LineRemoved, OldLine: 901, Content: "the guard that was deleted"},
				{Kind: diff.LineContext, OldLine: 902, NewLine: 900, Content: bodyLine(900)},
			},
		},
	}}

	got, _, elided := newWindower(body.String(), f).render(20)
	if !elided {
		t.Fatal("a 20-line window of a 2000-line file should have elided something")
	}

	text, _, _ := emitted(t, got)
	for _, ln := range []int{10, 899, 900} {
		if _, ok := text[ln]; !ok {
			t.Errorf("line %d was windowed out; it is within 20 lines of an edit and the prompt says every such line is shown", ln)
		}
	}
	// Still a window, not the whole file dressed as one.
	if _, ok := text[500]; ok {
		t.Error("line 500 is 480 lines from any edit and should have been elided")
	}
}

// denselyEdited builds an n-line file changed every stride lines, plus its
// fetcher. This is the shape that had no window at all: with a fixed ladder
// whose narrowest rung was 12 lines, every rung of it covered a file edited
// more often than every 25 lines, so nothing was ever elided and the search ran
// out of widths.
func denselyEdited(n, stride int) (diff.Files, ContentFetcher) {
	var (
		body  strings.Builder
		hunks []diff.Hunk
	)
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&body, "%s // padding to make the file expensive\n", bodyLine(i))
		if (i-1)%stride == 0 {
			hunks = append(hunks, diff.Hunk{
				NewStart: i, NewLines: 1,
				Lines: []diff.Line{{Kind: diff.LineAdded, NewLine: i, Content: bodyLine(i)}},
			})
		}
	}

	fetch := func(context.Context, string) ([]byte, error) { return []byte(body.String()), nil }
	return diff.Files{{Path: "a.go", Kind: diff.ChangeModified, Hunks: hunks}}, fetch
}

// TestDenselyEditedFileKeepsContextItsBudgetCanPayFor pins the dead zone shut.
// A file whose edits sit closer together than twice the narrowest width used
// to lose ALL of its context, every rung covered the whole file, the search
// fell through, and the request went out with 93% of its budget unspent and no
// file attached. Losing the file entirely is the worst answer available, and
// it was reached while the budget could have paid for most of it.
func TestDenselyEditedFileKeepsContextItsBudgetCanPayFor(t *testing.T) {
	for _, stride := range []int{1, 5, 13, 25, 26, 40} {
		t.Run(fmt.Sprintf("stride=%d", stride), func(t *testing.T) {
			cfg := baseConfig()
			files, fetch := denselyEdited(6000, stride)

			plan, err := Assemble(context.Background(), cfg, files, fetch)
			if err != nil {
				t.Fatalf("Assemble: %v", err)
			}
			if plan.Files() != 1 {
				t.Fatalf("packed %d files, want the 1 submitted; skipped=%+v", plan.Files(), plan.Skipped)
			}

			entry := plan.Batches[0].Entries[0]
			if !entry.HasContent() {
				// Dropping every line is honest only when the narrowest window
				// worth attaching did not fit. Asserted by building
				// that window here: if it would have fit, the search gave up
				// with the budget still able to pay for it, which is the dead
				// zone this test is named after.
				body, err := fetch(context.Background(), "a.go")
				if err != nil {
					t.Fatalf("fetch: %v", err)
				}

				text, srcBytes, elided := newWindower(string(body), files[0]).render(minContextLines)
				if !elided {
					// Edits so dense that even the floor covers the file. No
					// window exists, and diff-only is the only honest answer.
					return
				}

				probe := entry
				probe.Content, probe.Truncated, probe.ContextLines = text, true, minContextLines
				cost := llms.DefaultTokenEstimator().EstimateTokens(Render(probe))

				if cost <= cfg.Review.TokenBudgetPerRequest && srcBytes <= cfg.Review.MaxFileBytes {
					t.Fatalf("all context was dropped, yet a %d-line window costs %d of the %d-token budget and holds %d of the %d-byte cap: it fit and was not taken",
						minContextLines, cost, cfg.Review.TokenBudgetPerRequest, srcBytes, cfg.Review.MaxFileBytes)
				}
				return
			}

			// Content that leaves most of the request unbought is the same
			// failure one rung further along.
			if entry.Tokens*2 < cfg.Review.TokenBudgetPerRequest {
				t.Errorf("settled on a %d-line window costing %d tokens against a %d budget: the search gave up while the request could still have paid for more context",
					entry.ContextLines, entry.Tokens, cfg.Review.TokenBudgetPerRequest)
			}
		})
	}
}

// TestACapTheWholeFileSatisfiesNeverRejectsItsWindow pins the two call sites of
// review.max_file_bytes to one unit. It was compared against the raw file in
// one place and against the line-NUMBERED window in the other, and numbering
// adds 8 bytes per line: a window of a short-line file measured up to 3.5x the
// whole file it was cut from. A cap that admitted the file outright could then
// reject every window of it, and raising a cap the file already satisfied was
// what restored its context.
func TestACapTheWholeFileSatisfiesNeverRejectsItsWindow(t *testing.T) {
	// One character per line, where numbering inflates hardest.
	const lines = 400

	var (
		body  strings.Builder
		hunks []diff.Hunk
	)
	for i := 1; i <= lines; i++ {
		body.WriteString("x\n")
		if i%100 == 0 {
			hunks = append(hunks, diff.Hunk{
				NewStart: i, NewLines: 1,
				Lines: []diff.Line{{Kind: diff.LineAdded, NewLine: i, Content: "x"}},
			})
		}
	}
	files := diff.Files{{Path: "a.go", Kind: diff.ChangeModified, Hunks: hunks}}
	fetch := func(context.Context, string) ([]byte, error) { return []byte(body.String()), nil }

	cfg := baseConfig()
	// Comfortably above the whole file, so the cap cannot be what binds.
	cfg.Review.MaxFileBytes = body.Len() + 200
	// Below the whole file's cost, so a window is what the entry must get.
	cfg.Review.TokenBudgetPerRequest = 500

	plan, err := Assemble(context.Background(), cfg, files, fetch)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	entry := plan.Batches[0].Entries[0]
	if !entry.HasContent() {
		t.Fatalf("a %d-byte file under a %d-byte cap lost all of its context; degraded=%+v",
			body.Len(), cfg.Review.MaxFileBytes, plan.Degraded)
	}
	for _, w := range plan.Windowed {
		if strings.HasPrefix(w.Reason, ReasonTooLarge) {
			t.Errorf("reason %q blames the byte cap, which the whole file already satisfied", w.Reason)
		}
	}
}

// TestWindowReasonNamesTheLimitAnOperatorMustRaise pins what the reason
// strings are for. Naming "too large" without saying too large for WHAT sends
// a reader to the wrong knob, and the reason used to name the byte cap
// whenever the file was over it, even when the byte cap had nothing to do with
// how wide the window ended up, so raising it changed nothing at all.
func TestWindowReasonNamesTheLimitAnOperatorMustRaise(t *testing.T) {
	files, fetch := oneEditFile(6000, 3000)

	for _, tc := range []struct {
		name             string
		budget, maxBytes int
		want             string
	}{
		// Every candidate window is far under the cap; only tokens decide.
		{"budget binds", 3_000, 256 << 10, ReasonOverBudget},
		// Tokens are free; the cap is what stops the window widening.
		{"cap binds", 1 << 20, 8_000, ReasonTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			widthUnder := func(budget, maxBytes int) (Entry, []Skip) {
				t.Helper()

				cfg := baseConfig()
				cfg.Review.TokenBudgetPerRequest = budget
				cfg.Review.MaxFileBytes = maxBytes

				plan, err := Assemble(context.Background(), cfg, files, fetch)
				if err != nil {
					t.Fatalf("Assemble: %v", err)
				}
				return plan.Batches[0].Entries[0], plan.Windowed
			}

			entry, windowed := widthUnder(tc.budget, tc.maxBytes)
			if !entry.Truncated {
				t.Fatalf("the file was not windowed, so there is no reason to check")
			}
			if len(windowed) != 1 || !strings.HasPrefix(windowed[0].Reason, tc.want) {
				t.Fatalf("windowed = %+v, want the reason to name %q", windowed, tc.want)
			}

			// The claim the reason makes, checked by acting on it: raising the
			// limit it names must buy context. Raising the other must not have
			// been the answer.
			raisedBudget, raisedCap := tc.budget, tc.maxBytes
			if tc.want == ReasonOverBudget {
				raisedBudget *= 4
			} else {
				raisedCap *= 4
			}

			wider, _ := widthUnder(raisedBudget, raisedCap)
			if wider.Truncated && wider.ContextLines <= entry.ContextLines {
				t.Errorf("raising the %s the reason named moved the window from %d lines to %d: the reason points at a knob that does nothing",
					tc.want, entry.ContextLines, wider.ContextLines)
			}
		})
	}
}

// TestWindowFloorStaysClearOfTheDiffsOwnContext guards the one judgement in
// the width search that mechanism cannot check. The diff in the same prompt
// already carries the differ's context lines (three each side is git's
// default), so a window at or below that width shows the model nothing new
// under a heading claiming to be the surrounding file.
func TestWindowFloorStaysClearOfTheDiffsOwnContext(t *testing.T) {
	const differContext = 3

	if minContextLines <= differContext {
		t.Errorf("floor width %d is at or under the %d context lines the diff already carries: it would add nothing while reading as file context",
			minContextLines, differContext)
	}
	// The other direction is the dead zone: a floor far above the differ's
	// context leaves densely edited files with no width that reduces them, and
	// the measured alternative to a thin window was no window at all.
	if minContextLines > 2*differContext {
		t.Errorf("floor width %d is well past the %d lines the diff carries; a file edited more often than every %d lines has no width left to try",
			minContextLines, differContext, 2*minContextLines+1)
	}
}
