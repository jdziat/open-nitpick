package bundle

// Packing harness.
//
// batch() decides how many files share one model call, and therefore how much
// attention each file gets. Nothing exercised it: every eval fixture is a
// single file, so batch() had only ever produced one batch holding one entry
// and the configured concurrency of 4 had never been used. This file builds
// synthetic changes of exact, chosen shapes, pushes them through Assemble, and
// both prints and asserts what came out.
//
// Synthetic rather than recorded on purpose: packing is a function of sizes,
// and a fixture corpus that happens to contain the right sizes is a corpus
// that stops testing this the moment someone edits a fixture.

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"text/tabwriter"

	llms "github.com/nocturnium/llm-go-sdk/v6"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
)

// synthLineBytes is the exact width of every synthetic body line, newline
// included. Fixing it is what lets a case say "a 256 KiB file" and get one:
// bytes are lines*synthLineBytes with no rounding to reason about.
const synthLineBytes = 64

// synthContext is how many context lines each synthetic hunk carries, matching
// git's default so the generated diffs parse and read like real ones.
const synthContext = 3

// synthLine renders body line n of synthetic file idx, padded to exactly
// synthLineBytes-1 characters.
func synthLine(idx, n int) string {
	head := fmt.Sprintf("f%02d line %06d ", idx, n)
	pad := synthLineBytes - 1 - len(head)
	if pad < 0 {
		// Widths past this point would silently change every token count in
		// the table, so refuse rather than quietly produce a longer line.
		panic(fmt.Sprintf("synthLine: idx=%d n=%d overflows %d bytes", idx, n, synthLineBytes))
	}
	return head + strings.Repeat("x", pad)
}

// fileSpec describes one synthetic file: how long it is and how it changed.
type fileSpec struct {
	// Lines is the file's length after the change.
	Lines int

	// Hunks is how many separate changed regions, spread evenly through the
	// file. Ignored when Starts is set.
	Hunks int

	// HunkLines is how many lines each hunk adds.
	HunkLines int

	// Starts places the hunks at exact new-file lines instead of spreading
	// them evenly. Position, not just count, is what decides whether a window
	// helps: three hunks at the top of a 4000-line file elide most of it,
	// thirty hunks throughout elide almost none and the window saves nothing.
	Starts []int
}

// bytes is the file's size on disk, which is what MaxFileBytes is compared to.
func (s fileSpec) bytes() int { return s.Lines * synthLineBytes }

// hunkStarts resolves where this spec's hunks begin, and rejects any layout
// whose hunks would overlap, overlapping hunks would make the requested hunk
// count a lie and quietly change what the window keeps.
func (s fileSpec) hunkStarts(t *testing.T) []int {
	t.Helper()

	starts := s.Starts
	if len(starts) == 0 {
		span := s.Lines / max(1, s.Hunks)
		starts = make([]int, 0, s.Hunks)
		for h := range s.Hunks {
			// A third into its span, so the window has room on both sides
			// rather than clipping against the start of the file.
			starts = append(starts, h*span+span/3+1)
		}
	}

	// Each hunk owns its added lines plus context on both sides; two hunks any
	// closer than that would merge into one.
	minGap := s.HunkLines + 2*synthContext + 1

	for i, start := range starts {
		if start <= synthContext || start+s.HunkLines+synthContext-1 > s.Lines {
			t.Fatalf("fileSpec %+v: hunk at line %d has no room for %d context lines within %d lines",
				s, start, synthContext, s.Lines)
		}
		if i > 0 && start-starts[i-1] < minGap {
			t.Fatalf("fileSpec %+v: hunks at %d and %d are closer than %d lines and would overlap",
				s, starts[i-1], start, minGap)
		}
	}

	return starts
}

// spread returns count hunk positions every step lines from first: the layout
// of a file edited throughout rather than in one place.
func spread(count, first, step int) []int {
	out := make([]int, count)
	for i := range out {
		out[i] = first + i*step
	}
	return out
}

// Named sizes used across the cases. The huge/overLimit pair straddles the
// default MaxFileBytes of 256 KiB (4096 synthetic lines) deliberately: that
// boundary is where a file stops being windowed and starts being reviewed
// diff-only.
var (
	tinyFile  = fileSpec{Lines: 20, Hunks: 1, HunkLines: 2}
	smallFile = fileSpec{Lines: 200, Hunks: 2, HunkLines: 4}
	midFile   = fileSpec{Lines: 1000, Hunks: 2, HunkLines: 4}
	bigFile   = fileSpec{Lines: 2000, Hunks: 2, HunkLines: 4}
	hugeFile  = fileSpec{Lines: 4000, Hunks: 3, HunkLines: 4}
	overLimit = fileSpec{Lines: 4400, Hunks: 2, HunkLines: 4}
)

// repeatSpec is a table-readability helper: n copies of the same shape.
func repeatSpec(s fileSpec, n int) []fileSpec {
	out := make([]fileSpec, n)
	for i := range out {
		out[i] = s
	}
	return out
}

// synthDiff renders a unified diff for one synthetic file. It goes through
// diff.Parse rather than building diff.File structs by hand so the harness
// measures the packer against the same hunk shapes the parser really produces.
func synthDiff(t *testing.T, idx int, path string, s fileSpec) string {
	t.Helper()

	var b strings.Builder
	fmt.Fprintf(&b, "diff --git a/%s b/%s\n--- a/%s\n+++ b/%s\n", path, path, path, path)

	// added tracks the new-side drift so each hunk's old-side start stays
	// consistent, the way a real diff's headers do.
	added := 0

	for _, start := range s.hunkStarts(t) {
		post := min(synthContext, s.Lines-(start+s.HunkLines)+1)
		newCount := synthContext + s.HunkLines + post
		oldCount := synthContext + post

		fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n",
			start-synthContext-added, oldCount, start-synthContext, newCount)

		for n := start - synthContext; n < start; n++ {
			fmt.Fprintf(&b, " %s\n", synthLine(idx, n))
		}
		for n := start; n < start+s.HunkLines; n++ {
			fmt.Fprintf(&b, "+%s\n", synthLine(idx, n))
		}
		for n := start + s.HunkLines; n < start+s.HunkLines+post; n++ {
			fmt.Fprintf(&b, " %s\n", synthLine(idx, n))
		}

		added += s.HunkLines
	}

	return b.String()
}

// buildCorpus turns specs into a parsed diff plus a fetcher serving matching
// file bodies. Paths are f00.go, f01.go, ... in spec order, which is also the
// order the packer sees them.
func buildCorpus(t *testing.T, specs []fileSpec) (diff.Files, ContentFetcher) {
	t.Helper()

	var (
		raw    strings.Builder
		bodies = make(map[string]string, len(specs))
	)

	for i, s := range specs {
		path := fmt.Sprintf("f%02d.go", i)
		raw.WriteString(synthDiff(t, i, path, s))

		var body strings.Builder
		body.Grow(s.bytes())
		for n := 1; n <= s.Lines; n++ {
			body.WriteString(synthLine(i, n))
			body.WriteByte('\n')
		}
		bodies[path] = body.String()
	}

	files, err := diff.Parse([]byte(raw.String()))
	if err != nil {
		t.Fatalf("parse synthetic diff: %v", err)
	}
	if len(files) != len(specs) {
		t.Fatalf("parsed %d files from the synthetic diff, want %d", len(files), len(specs))
	}

	fetch := func(_ context.Context, path string) ([]byte, error) {
		body, ok := bodies[path]
		if !ok {
			t.Errorf("fetch for unknown path %q", path)
			return nil, fmt.Errorf("no such synthetic file: %s", path)
		}
		return []byte(body), nil
	}

	return files, fetch
}

// packCase is one measurement: a set of files and the limits to pack them
// under. Zero limits mean the shipped defaults, which is the configuration
// whose behaviour matters.
type packCase struct {
	Name  string
	Specs []fileSpec

	// Budget overrides review.token_budget_per_request when non-zero.
	Budget int

	// PerRequest overrides review.max_files_per_request when non-zero.
	PerRequest int
}

func (c packCase) config() *config.Config {
	cfg := baseConfig()
	if c.Budget > 0 {
		cfg.Review.TokenBudgetPerRequest = c.Budget
	}
	if c.PerRequest > 0 {
		cfg.Review.MaxFilesPerRequest = c.PerRequest
	}
	return cfg
}

// entryState is how much of a file's content survived into the prompt.
type entryState string

const (
	// stateFull is the whole file attached.
	stateFull entryState = "full"
	// stateWindow is regions around the changes only.
	stateWindow entryState = "window"
	// stateFetchDrop is content the fetcher refused: unreadable, or not text.
	// No window could have helped, so it is not a sizing measurement.
	stateFetchDrop entryState = "fetch-drop"
	// stateSizeDrop is content fitEntry gave up on because no window fit the
	// token budget or the byte cap. Both this and a fetch drop are recorded in
	// Plan.Degraded, and telling them apart takes reading the reason: only a
	// size drop names a limit. Counting them in one column attributed the
	// sizing policy's own decisions to the fetcher, which had returned the
	// whole body successfully.
	stateSizeDrop entryState = "size-drop"
	// stateUnrecorded is a contentless entry the Plan says nothing about. It is
	// the silent zero: indistinguishable, to any reader of the report, from a
	// file reviewed with its full content. It must never occur, and the column
	// exists so that a row showing it is impossible to miss.
	stateUnrecorded entryState = "unrecorded"
)

// sizeDrop reports whether a degradation came from fitEntry rather than from
// the fetcher. Only fitEntry's reasons open with the name of the limit that
// bound, which is what makes them tellable apart without matching on prose.
func sizeDrop(reason string) bool {
	return strings.HasPrefix(reason, ReasonTooLarge) || strings.HasPrefix(reason, ReasonOverBudget)
}

// stateOf classifies an entry, given the plan's recorded degradations.
func stateOf(e Entry, degraded map[string]string) entryState {
	switch reason := degraded[e.File.Path]; {
	case e.Truncated:
		return stateWindow
	case e.HasContent():
		return stateFull
	case sizeDrop(reason):
		return stateSizeDrop
	case reason != "":
		return stateFetchDrop
	default:
		return stateUnrecorded
	}
}

// measure packs one case and returns the config it was packed under alongside
// the plan, since half the assertions are against the limits.
func measure(t *testing.T, c packCase) (*config.Config, *Plan) {
	t.Helper()

	cfg := c.config()
	// stateOf reads a contentless, unrecorded entry as a silent drop. That
	// inference only holds while full files are being requested at all;
	// without this, turning IncludeFullFiles off would make every row in the
	// table report a silent drop that never happened.
	if !cfg.Review.IncludeFullFiles {
		t.Fatal("the harness requires review.include_full_files: without it no entry carries content and the table means nothing")
	}

	files, fetch := buildCorpus(t, c.Specs)

	plan, err := Assemble(context.Background(), cfg, files, fetch)
	if err != nil {
		t.Fatalf("%s: Assemble: %v", c.Name, err)
	}
	return cfg, plan
}

// degradedByPath indexes Plan.Degraded for classification.
func degradedByPath(p *Plan) map[string]string {
	out := make(map[string]string, len(p.Degraded))
	for _, d := range p.Degraded {
		out[d.Path] = d.Reason
	}
	return out
}

// row renders one case as a table line: the packing a human needs to see.
func row(w *tabwriter.Writer, c packCase, cfg *config.Config, plan *Plan) {
	degraded := degradedByPath(plan)

	var (
		perBatchFiles  []string
		perBatchTokens []string
		states         = map[entryState]int{}
		peak, total    int
	)

	for _, b := range plan.Batches {
		perBatchFiles = append(perBatchFiles, fmt.Sprint(len(b.Entries)))
		perBatchTokens = append(perBatchTokens, fmt.Sprint(b.Tokens))
		peak = max(peak, b.Tokens)
		total += b.Tokens

		for _, e := range b.Entries {
			states[stateOf(e, degraded)]++
		}
	}

	// Budget use of the fullest batch: the number that says whether packing is
	// leaving the request mostly empty.
	use := 0.0
	if cfg.Review.TokenBudgetPerRequest > 0 {
		use = 100 * float64(peak) / float64(cfg.Review.TokenBudgetPerRequest)
	}

	// Widths of the windows that shipped, which is the sizing decision the
	// table exists to expose: two files can both read as "window" and be shown
	// 20 lines or 2000.
	widths := map[int]int{}
	for _, b := range plan.Batches {
		for _, e := range b.Entries {
			if e.Truncated {
				widths[e.ContextLines]++
			}
		}
	}

	fmt.Fprintf(w, "%s\t%d\t%d\t%d\t%d\t%s\t%s\t%.1f%%\t%d\t%d/%d/%d/%d/%d\t%s\n",
		c.Name,
		cfg.Review.TokenBudgetPerRequest,
		cfg.Review.MaxFilesPerRequest,
		plan.Files(),
		len(plan.Batches),
		strings.Join(perBatchFiles, ","),
		strings.Join(perBatchTokens, ","),
		use,
		total,
		states[stateFull], states[stateWindow], states[stateFetchDrop], states[stateSizeDrop], states[stateUnrecorded],
		widthList(widths),
	)
}

// widthList renders the context widths a case settled on, most common first.
func widthList(widths map[int]int) string {
	if len(widths) == 0 {
		return "-"
	}

	out := make([]string, 0, len(widths))
	for w, n := range widths {
		out = append(out, fmt.Sprintf("%dx%d", n, w))
	}
	// Sorted so the row is stable across runs; map order is not.
	slices.Sort(out)
	return strings.Join(out, ",")
}

// packCases is the corpus the eval fixtures never produced: counts either side
// of MaxFilesPerRequest, and the tiny/huge mixture a real pull request is.
func packCases() []packCase {
	return []packCase{
		{Name: "1 tiny", Specs: repeatSpec(tinyFile, 1)},
		{Name: "2 tiny", Specs: repeatSpec(tinyFile, 2)},
		{Name: "6 tiny", Specs: repeatSpec(tinyFile, 6)},
		{Name: "7 tiny", Specs: repeatSpec(tinyFile, 7)},
		{Name: "20 tiny", Specs: repeatSpec(tinyFile, 20)},

		{Name: "6 small (200L)", Specs: repeatSpec(smallFile, 6)},
		{Name: "6 mid (1000L)", Specs: repeatSpec(midFile, 6)},
		{Name: "6 big (2000L)", Specs: repeatSpec(bigFile, 6)},
		{Name: "6 huge (4000L)", Specs: repeatSpec(hugeFile, 6)},
		{Name: "2 over 256KiB", Specs: repeatSpec(overLimit, 2)},

		// Same file length, same budget, different edit shape. What a window
		// is worth depends on where the changes are, so a size-only sizing
		// rule cannot predict it.
		{Name: "1 huge, 3 hunks at the top", Specs: []fileSpec{{Lines: 4000, HunkLines: 4, Starts: []int{100, 400, 700}}}},
		{Name: "1 huge, 30 hunks throughout", Specs: []fileSpec{{Lines: 4000, HunkLines: 4, Starts: spread(30, 100, 130)}}},
		{Name: "1 huge, 60 hunks throughout", Specs: []fileSpec{{Lines: 4000, HunkLines: 4, Starts: spread(60, 60, 65)}}},

		{Name: "mixed: 1 big + 5 tiny", Specs: append([]fileSpec{bigFile}, repeatSpec(tinyFile, 5)...)},
		{Name: "mixed: 5 tiny + 1 big", Specs: append(repeatSpec(tinyFile, 5), bigFile)},
		{Name: "mixed: 20 tiny + 3 big", Specs: append(repeatSpec(tinyFile, 20), repeatSpec(bigFile, 3)...)},
		{Name: "mixed: alternating tiny/big x10", Specs: alternating(tinyFile, bigFile, 10)},

		// Regimes the shipped numbers do not reach, kept so a future change to
		// the defaults has a measured comparison to land against.
		{Name: "20 tiny, 1 file/request", Specs: repeatSpec(tinyFile, 20), PerRequest: 1},
		{Name: "20 tiny, 60 files/request", Specs: repeatSpec(tinyFile, 20), PerRequest: 60},
		{Name: "6 tiny, tight budget", Specs: repeatSpec(tinyFile, 6), Budget: 1200},
		// Small enough that even a window will not fit, which is the only way
		// to reach fitEntry's last cliff and produce a budget-drop.
		{Name: "1 mid, 500-token budget", Specs: []fileSpec{midFile}, Budget: 500},
		// Diffs alone past the budget, so every entry is over-budget before
		// batching starts. Without this the shared properties below never see
		// the regime where dropping an entry is the tempting shortcut.
		{
			Name:   "3 files, diff alone over budget",
			Specs:  repeatSpec(fileSpec{Lines: 600, Hunks: 4, HunkLines: 20}, 3),
			Budget: 120,
		},
	}
}

// alternating interleaves two shapes, n of each: the mix that makes a packer's
// greedy-fill behaviour visible, because a big file between two small ones
// forces a flush that reordering would avoid.
func alternating(a, b fileSpec, n int) []fileSpec {
	out := make([]fileSpec, 0, 2*n)
	for range n {
		out = append(out, a, b)
	}
	return out
}

// TestPackingTable prints the packing behaviour of every case. It exists to be
// read (go test -run TestPackingTable -v ./internal/bundle), not only to pass:
// the numbers are the argument for or against the current policy.
func TestPackingTable(t *testing.T) {
	var out strings.Builder
	w := tabwriter.NewWriter(&out, 0, 0, 2, ' ', 0)

	// Total tokens is the spend: sizing each entry against the whole request
	// budget means a file that needs a window grows until it fills a request by
	// itself, so context per file and requests per run trade directly against
	// each other. The column exists so that trade is priced rather than assumed.
	fmt.Fprintln(w, "case\tbudget\tmax/req\tfiles\tbatches\tfiles per batch\ttokens per batch\tpeak use\ttotal tokens\tfull/window/fetch-drop/size-drop/unrecorded\twindow widths")

	// Kept so the notes below are derived from what was just measured rather
	// than from numbers pasted into a comment and left to rot.
	measured := map[string]*Plan{}

	for _, c := range packCases() {
		cfg, plan := measure(t, c)
		row(w, c, cfg, plan)
		measured[c.Name] = plan
	}

	if err := w.Flush(); err != nil {
		t.Fatalf("flush table: %v", err)
	}
	t.Logf("packing behaviour\n%s\n%s", out.String(), packingNotes(t, measured))
}

// packingNotes states the facts in the table a reader would otherwise have to
// derive by hand, each a property of the policy rather than of any one case.
//
// Every number is measured and every claim is asserted against the plan it
// describes. The previous version interpolated live numbers into fixed prose,
// which is how it came to print that a 60-hunk file "cannot fit a window, so
// All of its content is discarded" and that "nothing in the Plan records that
// drop", four lines under a row showing that same file windowed, truncated and
// recorded. Numbers that move under prose that does not are worse than no
// notes: this table exists to be read by someone deciding packing policy.
func packingNotes(t *testing.T, measured map[string]*Plan) string {
	t.Helper()

	get := func(name string) *Plan {
		p, ok := measured[name]
		if !ok {
			t.Fatalf("packingNotes: no measurement named %q; the case list and the notes have drifted apart", name)
		}
		if len(p.Batches) == 0 || len(p.Batches[0].Entries) == 0 {
			// Indexing blind would panic, and a panic takes the whole test
			// binary down with it: the regression with the worst consequences
			// would produce the least diagnostic output.
			t.Fatalf("packingNotes: %q packed nothing into any batch; every file in it would go unreviewed while the run reports success", name)
		}
		return p
	}

	// first is the single entry of a one-file case, which is what the shape
	// comparisons below are about.
	first := func(name string) Entry {
		p := get(name)
		if p.Files() != 1 {
			t.Fatalf("packingNotes: %q holds %d files, want the 1 its claim is about", name, p.Files())
		}
		return p.Batches[0].Entries[0]
	}

	var (
		tiny      = get("6 tiny")
		small     = get("6 small (200L)")
		big       = get("6 big (2000L)")
		clustered = first("1 huge, 3 hunks at the top")
		scattered = first("1 huge, 60 hunks throughout")
	)

	budget := config.Defaults().Review.TokenBudgetPerRequest
	perRequest := config.Defaults().Review.MaxFilesPerRequest

	tinyUse := 100 * float64(tiny.Batches[0].Tokens) / float64(budget)
	smallUse := 100 * float64(small.Batches[0].Tokens) / float64(budget)

	// Claim 1: small files are split by the file ceiling and never by the
	// budget.
	if len(tiny.Batches) != 1 || len(tiny.Batches[0].Entries) != perRequest {
		t.Errorf("6 tiny packed %d batches of %d, want 1 of %d: the note below claims the file ceiling is what binds",
			len(tiny.Batches), len(tiny.Batches[0].Entries), perRequest)
	}
	// Claim 2: big files are split by the budget before the ceiling is reached.
	for i, b := range big.Batches {
		if len(b.Entries) >= perRequest {
			t.Errorf("6 big batch %d holds %d files, reaching the %d ceiling: the note below claims the budget binds first",
				i, len(b.Entries), perRequest)
		}
	}
	// Claim 3: both shapes of the same file keep a window, and the widths differ.
	// Which is the whole reason sizing cannot be a function of file size alone.
	for name, e := range map[string]Entry{"3 hunks at the top": clustered, "60 hunks throughout": scattered} {
		if !e.Truncated || !e.HasContent() {
			t.Errorf("%s: truncated=%v content=%d, want a window: the note below reports the width it settled on",
				name, e.Truncated, len(e.Content))
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "notes, derived from and asserted against the rows above:\n")
	fmt.Fprintf(&b, "  - six 20-line files fill one request to %.1f%% of the %d-token budget, and six\n"+
		"    200-line files to %.1f%%. Both are split by the %d-file ceiling and never by the\n"+
		"    budget, so most of every such request is bought and not used.\n",
		tinyUse, budget, smallUse, perRequest)
	fmt.Fprintf(&b, "  - six 2000-line files take %d request(s) and six 4000-line files take %d, none of\n"+
		"    them reaching the %d-file ceiling. Past a few thousand lines the budget is what\n"+
		"    splits a change, and max_files_per_request stops being reachable at all.\n",
		len(big.Batches), len(get("6 huge (4000L)").Batches), perRequest)
	fmt.Fprintf(&b, "  - the same 4000-line file, edited in 3 places and in 60, keeps a window either\n"+
		"    way: %d lines of context around each of the 3 (%d bytes attached), %d lines around\n"+
		"    each of the 60 (%d bytes). Where the edits are decides what a window is worth, so\n"+
		"    a sizing rule that reads only the file's size cannot predict it.\n",
		clustered.ContextLines, len(clustered.Content),
		scattered.ContextLines, len(scattered.Content))

	// The trade the sizing policy makes, priced. fitEntry widens a window until
	// it fills the whole request budget, so a file that needs one never shares
	// a request with anything: context per file is bought with requests per
	// run, and this is the row where that is visible.
	huge := get("6 huge (4000L)")
	total := 0
	for i, batch := range huge.Batches {
		total += batch.Tokens
		if len(batch.Entries) != 1 || !batch.Entries[0].Truncated {
			t.Errorf("6 huge batch %d holds %d entries (first truncated=%v), want one windowed file: the note below prices a trade that is no longer being made",
				i, len(batch.Entries), batch.Entries[0].Truncated)
		}
	}
	fmt.Fprintf(&b, "  - six 4000-line files cost %d tokens over %d requests, %d each, because a\n"+
		"    window widens until it fills the whole request budget and so never shares one.\n"+
		"    Context per file is bought with requests per run at roughly that rate. Sizing\n"+
		"    against a share of the budget instead would refill those requests; it needs a\n"+
		"    setting nobody has chosen yet, and this is the number to choose it against.\n",
		total, len(huge.Batches), total/max(1, len(huge.Batches)))

	return b.String()
}

// TestEveryReviewableFileLandsInExactlyOneBatch pins the property whose
// violation is invisible: a file missing from every batch is never reviewed,
// yet the run reports success, and a duplicated file is paid for twice and can
// draw two contradictory comments on the same line.
func TestEveryReviewableFileLandsInExactlyOneBatch(t *testing.T) {
	for _, c := range packCases() {
		t.Run(c.Name, func(t *testing.T) {
			_, plan := measure(t, c)

			seen := make(map[string]int, len(c.Specs))
			for _, b := range plan.Batches {
				for _, e := range b.Entries {
					seen[e.File.Path]++
				}
			}

			for i := range c.Specs {
				path := fmt.Sprintf("f%02d.go", i)
				switch seen[path] {
				case 1:
				case 0:
					t.Errorf("%s appears in no batch: it would go unreviewed while the run reports success", path)
				default:
					t.Errorf("%s appears in %d batches, want exactly 1", path, seen[path])
				}
			}

			// Nothing that was never submitted may appear either.
			if len(seen) != len(c.Specs) {
				t.Errorf("packed %d distinct paths, want %d", len(seen), len(c.Specs))
			}
			// No file in this corpus is ignorable, so every one must be packed
			// rather than skipped.
			if len(plan.Skipped) != 0 {
				t.Errorf("skipped = %+v, want none: every synthetic file is reviewable", plan.Skipped)
			}
		})
	}
}

// fingerprint renders a plan's packing exactly enough that any change in batch
// composition, order, or sizing shows up as a string difference.
func fingerprint(p *Plan) string {
	var b strings.Builder
	for i, batch := range p.Batches {
		fmt.Fprintf(&b, "batch %d tokens=%d\n", i, batch.Tokens)
		for _, e := range batch.Entries {
			fmt.Fprintf(&b, "  %s tokens=%d truncated=%v content=%d\n",
				e.File.Path, e.Tokens, e.Truncated, len(e.Content))
		}
	}
	return b.String()
}

// TestPackingIsDeterministic pins reproducibility. An unstable packer makes
// every downstream measurement (eval scores, token cost, latency), noise,
// because two runs of the same pull request would send different requests.
func TestPackingIsDeterministic(t *testing.T) {
	for _, c := range packCases() {
		t.Run(c.Name, func(t *testing.T) {
			_, first := measure(t, c)
			want := fingerprint(first)

			// Repeated rather than compared once: map iteration order is the
			// usual source of instability and it only sometimes differs. Three
			// repeats per case is cheap; the confidence comes from every case
			// in the table running them, not from one case running many.
			for i := range 3 {
				_, plan := measure(t, c)
				if got := fingerprint(plan); got != want {
					t.Fatalf("run %d packed differently:\n--- first ---\n%s\n--- run %d ---\n%s", i+1, want, i+1, got)
				}
			}
		})
	}
}

// TestBatchNeverExceedsMaxFilesPerRequest pins the file ceiling across every
// case and several settings of the ceiling itself.
func TestBatchNeverExceedsMaxFilesPerRequest(t *testing.T) {
	for _, perRequest := range []int{1, 2, 6, 7, 60} {
		t.Run(fmt.Sprintf("max=%d", perRequest), func(t *testing.T) {
			for _, c := range packCases() {
				c.PerRequest = perRequest

				cfg, plan := measure(t, c)
				for i, b := range plan.Batches {
					if len(b.Entries) > cfg.Review.MaxFilesPerRequest {
						t.Errorf("%s: batch %d holds %d files, over the %d ceiling",
							c.Name, i, len(b.Entries), cfg.Review.MaxFilesPerRequest)
					}
				}
			}
		})
	}
}

// TestOversizedEntryGetsItsOwnBatch proves the claim batch()'s comment makes.
// Dropping such a file would silently skip a real change; sending it over
// budget is at least a visible, diagnosable failure.
func TestOversizedEntryGetsItsOwnBatch(t *testing.T) {
	// Chosen so exactly one entry cannot be made to fit: the 4x20-line diff
	// alone costs ~1800 tokens and fitEntry cannot trim a diff, while a
	// 20-line file costs ~500 and two of them share a request comfortably. A
	// budget that puts every entry over it makes the assertion below
	// unfalsifiable.
	const budget = 1500

	// Two packable files on each side, so the case distinguishes "isolates the
	// oversized entry" from "never packs anything", and so the oversized entry
	// is neither first nor last.
	c := packCase{
		Name:   "tiny x2, oversized, tiny x2",
		Specs:  []fileSpec{tinyFile, tinyFile, {Lines: 600, Hunks: 4, HunkLines: 20}, tinyFile, tinyFile},
		Budget: budget,
	}

	cfg, plan := measure(t, c)
	if plan.Files() != 5 {
		t.Fatalf("files = %d, want all 5 packed: an entry that fits nowhere must not be dropped", plan.Files())
	}

	var over, packed int
	for i, b := range plan.Batches {
		if len(b.Entries) > 1 {
			packed++
		}
		if b.Tokens <= cfg.Review.TokenBudgetPerRequest {
			continue
		}
		over++
		if len(b.Entries) != 1 {
			t.Errorf("batch %d is over budget (%d > %d) with %d entries; an over-budget batch must hold exactly the one entry that caused it, or the others are lost with it when the provider rejects the request",
				i, b.Tokens, cfg.Review.TokenBudgetPerRequest, len(b.Entries))
		}
	}
	if over != 1 {
		t.Fatalf("%d batches exceeded the %d-token budget, want exactly the 1 oversized entry; batches=%s", over, budget, fingerprint(plan))
	}
	if packed == 0 {
		t.Errorf("no batch holds more than one file, so this case cannot tell isolating the oversized entry from never packing at all; batches=%s", fingerprint(plan))
	}
}

// TestPlanFilesEqualsPackedEntries pins Plan.Files() to the batches. The report
// prints it as the count of files reviewed, so a drift between the two would
// misstate the run's coverage.
func TestPlanFilesEqualsPackedEntries(t *testing.T) {
	for _, c := range packCases() {
		t.Run(c.Name, func(t *testing.T) {
			_, plan := measure(t, c)

			want := 0
			for _, b := range plan.Batches {
				want += len(b.Entries)
			}
			if plan.Files() != want {
				t.Errorf("Plan.Files() = %d, want %d packed entries", plan.Files(), want)
			}
			// Nothing in these corpora is skippable, so the count must also
			// equal what was submitted.
			if plan.Files() != len(c.Specs) {
				t.Errorf("Plan.Files() = %d, want the %d submitted files", plan.Files(), len(c.Specs))
			}
		})
	}
}

// TestBatchTokensMatchItsEntries pins the batch total to its contents. Tokens
// is what the budget is enforced against, so a total that drifts from the
// entries would let a batch pass the check and still be rejected by the
// provider.
func TestBatchTokensMatchItsEntries(t *testing.T) {
	for _, c := range packCases() {
		t.Run(c.Name, func(t *testing.T) {
			_, plan := measure(t, c)

			for i, b := range plan.Batches {
				sum := 0
				for _, e := range b.Entries {
					sum += e.Tokens
				}
				if b.Tokens != sum {
					t.Errorf("batch %d Tokens = %d, want %d from its entries", i, b.Tokens, sum)
				}
			}
		})
	}
}

// TestEntryTokensDescribeWhatTheEntryRenders pins the foundation the batch
// total rests on. Batch.Tokens matching the sum of its entries is worth nothing
// if every entry's own number is wrong, and a stale one is consistent with
// itself and therefore invisible: an fitEntry that costs the whole file and
// then attaches a window leaves each entry claiming an order of magnitude more
// than it sends, so requests that would share one call are issued separately
// and the totals reported to the engine are fiction.
//
// Demonstrated by deleting the re-cost in fitEntry's accept path: the entire
// package stayed green while the table showed single-file batches at 127% of
// budget, every one of which the provider would reject.
func TestEntryTokensDescribeWhatTheEntryRenders(t *testing.T) {
	estimator := llms.DefaultTokenEstimator()

	for _, c := range packCases() {
		t.Run(c.Name, func(t *testing.T) {
			_, plan := measure(t, c)

			for _, b := range plan.Batches {
				for _, e := range b.Entries {
					if want := estimator.EstimateTokens(Render(e)); e.Tokens != want {
						t.Errorf("%s: Entry.Tokens = %d but rendering it costs %d (truncated=%v, %d bytes of content); every packing decision is made against the wrong number",
							e.File.Path, e.Tokens, want, e.Truncated, len(e.Content))
					}
				}
			}
		})
	}
}

// TestNoEntryLosesItsContentSilently pins the property the state columns exist
// to expose. An entry with no content that the Plan says nothing about is
// reported to the reader exactly like a file reviewed in full, so the review
// understates what it missed in the one direction nobody can detect.
func TestNoEntryLosesItsContentSilently(t *testing.T) {
	for _, c := range packCases() {
		t.Run(c.Name, func(t *testing.T) {
			_, plan := measure(t, c)

			degraded := degradedByPath(plan)
			for _, b := range plan.Batches {
				for _, e := range b.Entries {
					if stateOf(e, degraded) == stateUnrecorded {
						t.Errorf("%s carries no content and appears in neither Degraded nor Windowed: the report cannot tell it from a file reviewed in full",
							e.File.Path)
					}
				}
			}
		})
	}
}

// TestBatchWithUnsetLimitsStillPacksEveryEntry drives batch() directly with
// limits config validation would have rejected. Nothing forces a caller to
// validate a hand-built Config, and the failure mode to rule out is not a bad
// split but a lost file: whatever the limits say, every entry must come out
// the other side, because an entry that vanishes here is a file the run
// reports as reviewed and never sent.
func TestBatchWithUnsetLimitsStillPacksEveryEntry(t *testing.T) {
	entries := []Entry{
		{File: &diff.File{Path: "a.go"}, Tokens: 10},
		{File: &diff.File{Path: "b.go"}, Tokens: 90_000},
		{File: &diff.File{Path: "c.go"}, Tokens: 10},
	}

	for _, tc := range []struct {
		name             string
		maxFiles, budget int
	}{
		{"shipped defaults", 6, 60_000},
		{"no file ceiling", 0, 60_000},
		{"no budget", 6, 0},
		{"neither", 0, 0},
		{"negative", -1, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var packed []string
			for _, b := range batch(entries, tc.maxFiles, tc.budget) {
				packed = append(packed, b.Paths()...)
			}

			want := []string{"a.go", "b.go", "c.go"}
			if strings.Join(packed, ",") != strings.Join(want, ",") {
				t.Errorf("packed %v, want every entry exactly once in order: %v", packed, want)
			}
		})
	}
}

// TestPackerFillsEachBatchUntilALimitBinds pins that no batch is closed early.
// A batch flushed while both limits still had room is a request paid for and
// half used, and (since concurrency is bounded), a slot another batch could
// have had. Stated against the limits rather than against measured sizes, so
// it keeps its teeth when the sizing policy changes.
func TestPackerFillsEachBatchUntilALimitBinds(t *testing.T) {
	for _, c := range packCases() {
		t.Run(c.Name, func(t *testing.T) {
			cfg, plan := measure(t, c)

			// The last batch is exempt: it closed because the input ran out.
			for i := range len(plan.Batches) - 1 {
				b := plan.Batches[i]
				next := plan.Batches[i+1].Entries[0]

				atCeiling := len(b.Entries) >= cfg.Review.MaxFilesPerRequest
				wouldOverflow := b.Tokens+next.Tokens > cfg.Review.TokenBudgetPerRequest

				if !atCeiling && !wouldOverflow {
					t.Errorf("batch %d closed with %d/%d files and %d/%d tokens, yet %s (%d tokens) would still have fit: the request was wasted",
						i, len(b.Entries), cfg.Review.MaxFilesPerRequest,
						b.Tokens, cfg.Review.TokenBudgetPerRequest,
						next.File.Path, next.Tokens)
				}
			}
		})
	}
}

// TestSmallFilesReachTheFileCeiling pins that MaxFilesPerRequest is reachable
// at all. It is the only limit that ever splits a change made of small files
// (six of them use a twentieth of the budget), so if the ceiling stopped
// binding, batching would silently become one request for the whole run.
func TestSmallFilesReachTheFileCeiling(t *testing.T) {
	cfg, six := measure(t, packCase{Name: "6 tiny", Specs: repeatSpec(tinyFile, 6)})
	if len(six.Batches) != 1 {
		t.Fatalf("6 small files packed into %d batches, want 1: they are nowhere near the %d-token budget",
			len(six.Batches), cfg.Review.TokenBudgetPerRequest)
	}
	if got := len(six.Batches[0].Entries); got != cfg.Review.MaxFilesPerRequest {
		t.Errorf("packed %d small files into the batch, want the %d ceiling", got, cfg.Review.MaxFilesPerRequest)
	}

	// One past the ceiling is where it has to bind.
	_, seven := measure(t, packCase{Name: "7 tiny", Specs: repeatSpec(tinyFile, 7)})
	if len(seven.Batches) != 2 {
		t.Fatalf("7 small files packed into %d batches, want 2", len(seven.Batches))
	}
	if got := len(seven.Batches[0].Entries); got != cfg.Review.MaxFilesPerRequest {
		t.Errorf("first batch holds %d files, want the %d ceiling filled before spilling", got, cfg.Review.MaxFilesPerRequest)
	}
	if got := len(seven.Batches[1].Entries); got != 1 {
		t.Errorf("second batch holds %d files, want the 1 that spilled", got)
	}
}

// TestBudgetBoundFilesNeverReachTheFileCeiling records the interaction the
// harness was built to expose. fitEntry sizes each entry against the whole
// request budget on its own, so a file that consumes more than half of it
// "fits" and is then the only thing its batch can hold. MaxFilesPerRequest is
// a ceiling such files cannot reach.
//
// Asserted as arithmetic, not as today's line counts: if the sizing policy
// changes so nothing lands in this band, the guard below reports that instead
// of failing, because a change that removes the pathology is not a regression.
func TestBudgetBoundFilesNeverReachTheFileCeiling(t *testing.T) {
	cfg, plan := measure(t, packCase{Name: "6 big", Specs: repeatSpec(bigFile, 6)})

	smallest := 0
	for _, b := range plan.Batches {
		for _, e := range b.Entries {
			if smallest == 0 || e.Tokens < smallest {
				smallest = e.Tokens
			}
		}
	}

	if half := cfg.Review.TokenBudgetPerRequest / 2; smallest <= half {
		t.Skipf("smallest entry is %d tokens, at or under half the %d-token budget, so this band is now empty; re-measure with TestPackingTable",
			smallest, cfg.Review.TokenBudgetPerRequest)
	}

	for i, b := range plan.Batches {
		if len(b.Entries) != 1 {
			t.Errorf("batch %d holds %d entries that each exceed half the budget; two of them cannot fit one request",
				i, len(b.Entries))
		}
	}
	// Six files, six requests: the ceiling of 6 bought nothing here.
	if len(plan.Batches) != 6 {
		t.Errorf("6 budget-bound files produced %d batches, want one each", len(plan.Batches))
	}
}
