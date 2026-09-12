// Package bundle assembles review context: it selects which changed files to
// review, attaches their contents, and groups them into batches that fit a
// token budget.
//
// The package is named bundle rather than context to avoid colliding with the
// standard library in every file that imports both.
package bundle

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	llms "github.com/nocturnium/llm-go-sdk/v6"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// ContentFetcher reads a file's full contents at the reviewed revision.
type ContentFetcher func(ctx context.Context, path string) ([]byte, error)

// Entry is one file prepared for review.
type Entry struct {
	// SourceOnly marks unchanged source supplied as design evidence.
	SourceOnly bool
	File       *diff.File

	// Content is the new-side file, empty when it was unavailable, binary, or
	// excluded by the token budget.
	Content string

	// Truncated marks that Content is a window around the changed hunks
	// rather than the whole file.
	Truncated bool

	// ContextLines is how many lines of surrounding code the window kept on
	// each side of every change, meaningful only when Truncated.
	ContextLines int

	// Instructions are the configured path-scoped prompts that apply here.
	Instructions []string

	// Related are definitions from files the change does not touch, attached
	// because a changed line uses them. See related.go.
	Related []Related

	// Tokens is the estimated cost of rendering this entry.
	Tokens int
}

// HasContent reports whether full-file context is attached.
func (e *Entry) HasContent() bool { return e.Content != "" }

// Batch is a group of entries reviewed in one model call.
type Batch struct {
	// DesignTask identifies the complete package assessment carried by this call.
	DesignTask string
	// Assessment describes the task and its source scope.
	Assessment string
	Entries    []Entry

	// Tokens is the estimated total for the batch.
	Tokens int
}

// Paths returns the batch's file paths.
func (b *Batch) Paths() []string {
	out := make([]string, 0, len(b.Entries))
	for _, e := range b.Entries {
		out = append(out, e.File.Path)
	}
	return out
}

// Plan is the outcome of assembling a review.
type Plan struct {
	Batches []Batch

	// BudgetPerBatch is the token budget a batch's entries were fitted to,
	// after FramingReserved was taken out of review.token_budget_per_request.
	BudgetPerBatch int

	// FramingReserved is what was held back for the system prompt, the pull
	// request context and the response schema. Disclosed because an operator
	// who set a budget and sees smaller batches is owed the reason.
	FramingReserved int

	// Skipped records files that were excluded and why. Reporting these
	// matters: a review that silently ignored half the diff looks identical
	// to one that found nothing wrong.
	Skipped []Skip

	// Degraded records files that were reviewed, but from the diff alone with
	// no file content at all, and why.
	//
	// These are kept apart from Skipped because the two mean opposite things to
	// a reader: Skipped means no review happened, Degraded means one happened
	// without file content. Merging them lists a reviewed file under "Files not
	// reviewed", understating the review in the way Skipped exists to keep it
	// from being overstated.
	Degraded []Skip

	// Windowed records files reviewed with a window around their changes
	// rather than whole, and how wide that window was.
	//
	// Separate from Degraded for the same reason Degraded is separate from
	// Skipped: a windowed file was reviewed with file context, just not all of
	// it, and reporting it as diff-only would understate the review.
	//
	// This list has to be rendered. renderSummary prints it under "Reviewed
	// with reduced file context", a heading kept distinct from both neighbours
	// because a reader who concludes "not reviewed" re-reviews the file by
	// hand, and one who concludes "fully reviewed" trusts an absence of
	// findings in the part that was elided. TestWindowedFilesAreDisclosed is
	// the guard.
	Windowed []Skip

	// RelatedFiles names the files the change does not touch that related context
	// was read from, and RelatedDefinitions counts what was attached. Disclosed
	// so a reader knows the model saw more than the diff and its files, and which
	// files, since a finding may lean on one.
	RelatedFiles       []string
	RelatedDefinitions int

	// CallerWalkTruncated records that the search for callers of what the
	// change redefines stopped at its file ceiling with candidates unread,
	// so a file with no callers attached may have callers all the same.
	// Disclosed for the reason the skips are: silence must not read as
	// "none".
	CallerWalkTruncated bool
}

// Skip records one excluded file.
type Skip struct {
	Path   string
	Reason string
}

// Files counts reviewed files; design context and repeated paths count once.
func (p *Plan) Files() int {
	n := 0
	seen := map[string]bool{}
	for _, b := range p.Batches {
		if b.DesignTask == "" {
			n += len(b.Entries)
			continue
		}
		for _, entry := range b.Entries {
			if !entry.SourceOnly && !seen[entry.File.Path] {
				seen[entry.File.Path] = true
				n++
			}
		}
	}
	return n
}

// Reasons to skip a file.
const (
	ReasonIgnored     = "matched an ignore pattern"
	ReasonBinary      = "binary file"
	ReasonDeleted     = "file was deleted"
	ReasonNoChanges   = "no surviving change anchors to comment on"
	ReasonGenerated   = "generated file"
	ReasonFileLimit   = "exceeded review.max_files"
	ReasonUnavailable = "contents could not be read"
	ReasonTooLarge    = "exceeded review.max_file_bytes"
	ReasonOverBudget  = "exceeded review.token_budget_per_request"
	ReasonNotText     = "not valid UTF-8 text"
)

// windowedReason and diffOnlyReason phrase what a size limit cost a file. Both
// name the limit that bound, because "too large" without saying too large for
// WHAT sends a reader to the wrong knob: the byte cap and the token budget are
// tuned independently and fail at different sizes.
func windowedReason(limit string, contextLines int) string {
	return fmt.Sprintf("%s: reviewed with %d lines of context around each change", limit, contextLines)
}

func diffOnlyReason(limit string) string {
	return fmt.Sprintf("%s: no window fit either, reviewed from the diff alone", limit)
}

// Assemble selects reviewable files, attaches their contents, and groups them
// into batches.
//
// Fetch may be nil, in which case entries carry only their diffs. A fetch
// failure for one file downgrades that file to diff-only rather than failing
// the run, since a partial review is far more useful than none.
func Assemble(ctx context.Context, cfg *config.Config, files diff.Files, fetch ContentFetcher) (*Plan, error) {
	return AssembleWith(ctx, cfg, files, fetch, nil)
}

// Reserve holds back what a batch's budget must leave for everything the
// engine sends alongside the entries: the system prompt, the pull request
// context, and the response schema.
//
// It exists because the budget bounded the entries and nothing else, so a
// request estimated at 24,852 tokens against a 32,000 budget reached the
// provider at 32,653. Measured in issue #81.
type Reserve struct {
	// Tokens is the framing the caller will send. Zero reserves nothing, which
	// is the old behaviour and what a caller that cannot measure its own
	// framing gets.
	Tokens int
}

// AssembleWith is Assemble with a directory lister, which related context
// needs to find the file a Go package or Python module defines a name in.
// A nil lister attaches related context for the languages that can be
// resolved without one and none for Go.
//
// It reserves nothing, for a caller that does not build the prompt and cannot
// measure its framing.
func AssembleWith(ctx context.Context, cfg *config.Config, files diff.Files, fetch ContentFetcher, list DirLister) (*Plan, error) {
	return AssembleReserving(ctx, cfg, files, fetch, list, Reserve{})
}

// AssembleReserving plans a review, leaving room for the caller's framing.
func AssembleReserving(ctx context.Context, cfg *config.Config, files diff.Files, fetch ContentFetcher, list DirLister, reserve Reserve) (*Plan, error) {
	if cfg == nil {
		return nil, errors.New("bundle: nil config")
	}

	plan := &Plan{}
	estimator := llms.DefaultTokenEstimator()

	// The budget available to entries, after the framing that travels with
	// them. Computed here rather than at packing, because a single file is
	// fitted against it too: reserving only at packing let one entry fill the
	// whole budget and then be sent with the system prompt on top, which is
	// the one-batch case issue #81 measured.
	//
	// Floored rather than allowed to reach zero. A reserve larger than the
	// budget is a misconfiguration, and answering it by reviewing nothing
	// would turn a bad number into no review at all.
	perBatch := max(1, cfg.Review.TokenBudgetPerRequest-reserve.Tokens)
	plan.BudgetPerBatch = perBatch
	plan.FramingReserved = reserve.Tokens

	var related *relatedCollector
	if cfg.Review.RelatedContext && fetch != nil {
		related = newRelatedCollector(ctx, files, fetch, list)
		related.maxBytes = cfg.Review.MaxFileBytes
		related.callers = cfg.Review.RelatedContextCallers
	}

	// Selection and content run in one pass so that review.max_files counts
	// files that were reviewed. A generated file is recognisable only once its
	// content is read, so selecting first spends a slot on one that then drops
	// out of the review, and refuses a reviewable file behind it for a limit
	// nothing reviewed has reached: six files at max_files=3 with the first
	// three generated review nothing and report success.
	entries := make([]Entry, 0, len(files))
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		if reason, ok := skipReason(cfg, f); ok {
			plan.Skipped = append(plan.Skipped, Skip{Path: f.Path, Reason: reason})
			continue
		}

		if len(entries) >= cfg.Review.MaxFiles {
			plan.Skipped = append(plan.Skipped, Skip{Path: f.Path, Reason: ReasonFileLimit})
			continue
		}

		entry := Entry{
			File:         f,
			Instructions: cfg.InstructionsFor(f.Path),
		}

		if cfg.Review.IncludeFullFiles && fetch != nil {
			content, skip := fetchContent(ctx, fetch, f)
			switch {
			case skip != "":
				// Not fatal: review the diff without the full file. Recorded as degraded,
				// not skipped. This file is still reviewed.
				plan.Degraded = append(plan.Degraded, Skip{Path: f.Path, Reason: skip})

			case cfg.Review.SkipGenerated && isGenerated(content):
				// Generated files are dropped entirely rather than reviewed
				// diff-only: commenting on output nobody edits by hand is
				// pure noise.
				//
				// Checked on the file as read, before any windowing: the
				// marker convention puts it at the top, and a file whose top
				// was elided would be reviewed as if it were hand-written.
				plan.Skipped = append(plan.Skipped, Skip{Path: f.Path, Reason: ReasonGenerated})
				continue

			default:
				entry.Content = content
			}
		}

		// Both limits are enforced in one place so that whichever binds, the
		// answer is a narrower window rather than no content: the byte cap
		// bounds what is held, the token budget bounds what is sent, and
		// neither is allowed to decide what is understood on its own.
		if reason := fitEntry(&entry, perBatch, cfg.Review.MaxFileBytes, estimator); reason != "" {
			if entry.Truncated {
				plan.Windowed = append(plan.Windowed, Skip{Path: f.Path, Reason: reason})
			} else {
				plan.Degraded = append(plan.Degraded, Skip{Path: f.Path, Reason: reason})
			}
		}

		// After fitEntry, so the file's own window is decided first and the related
		// context takes only what the request has left, never the other way round.
		if related != nil {
			budget := min(cfg.Review.RelatedContextTokens, perBatch-entry.Tokens)
			entry.Tokens += related.collect(&entry, budget, estimator)
			for _, r := range entry.Related {
				plan.RelatedDefinitions++
				if !slices.Contains(plan.RelatedFiles, r.Path) {
					plan.RelatedFiles = append(plan.RelatedFiles, r.Path)
				}
			}
			plan.CallerWalkTruncated = plan.CallerWalkTruncated || related.truncated
		}
		entries = append(entries, entry)
	}

	plan.Batches = batch(entries, cfg.Review.MaxFilesPerRequest, perBatch)
	return plan, nil
}

// fetchContent reads a file's contents. The returned reason is non-empty when
// no content could be attached at all.
//
// It deliberately does not apply review.max_file_bytes. Rejecting a file
// before windowing is considered gives a 255 KiB file a full window and a
// 257 KiB one nothing, turning a cap on how much is read into a cap on how
// much is understood. fitEntry enforces it instead, where it can pick a
// narrower window rather than throw the file's context away. Only encoding is
// decided here, because content that is not text cannot be windowed into text.
func fetchContent(ctx context.Context, fetch ContentFetcher, f *diff.File) (content, reason string) {
	data, err := fetch(ctx, f.Path)
	switch {
	case errors.Is(err, vcs.ErrNotFound):
		return "", ReasonUnavailable
	case err != nil:
		return "", fmt.Sprintf("%s: %v", ReasonUnavailable, err)
	}

	if !utf8.Valid(data) {
		return "", ReasonNotText
	}

	return string(data), ""
}

// skipReason reports why a file is not reviewable, if it is not.
func skipReason(cfg *config.Config, f *diff.File) (string, bool) {
	switch {
	case cfg.Ignored(f.Path):
		return ReasonIgnored, true
	case f.Binary:
		return ReasonBinary, true
	case f.Kind == diff.ChangeDeleted:
		// There is nothing to comment on, and complaining about deleted code
		// is the kind of noise that gets a bot switched off.
		return ReasonDeleted, true
	case len(f.CommentableLines()) == 0:
		return ReasonNoChanges, true
	}
	return "", false
}

// fitEntry costs an entry and, when its content will not fit, narrows that
// content to the widest window around the changes that does. The diff itself is
// never trimmed: it is the thing being reviewed.
//
// Two limits bind, for different reasons, and each is measured in the unit it
// names. maxBytes is review.max_file_bytes and bounds how much FILE is
// understood: it is compared against the file's own bytes, both for the whole
// file and for the bytes a window retains of it. budget bounds what is sent and
// is compared against the rendered entry, line numbering and headings included,
// because that is the text the provider receives. Neither is allowed
// to answer "no content at all" while a narrower window would have satisfied
// it. The held prompt fragment needs no separate cap: it is the thing budget
// measures.
//
// The returned reason is empty when nothing was given up; otherwise it names
// the limit an operator would have to raise to get more context, and says what
// the file was left with. e.Truncated distinguishes the two outcomes: windowed
// (context reduced) from dropped (diff only).
func fitEntry(e *Entry, budget, maxBytes int, estimator *llms.TokenEstimator) string {
	if !e.HasContent() {
		e.Tokens = estimator.EstimateTokens(Render(*e))
		return ""
	}

	// The byte cap is read before the whole file is costed, because it alone
	// can force a window and the cost of the whole file is then irrelevant.
	// Rendering and estimating it anyway was work that scaled with the largest
	// file in the diff and was bounded by nothing: a 64 MiB file spent 632 MiB
	// of allocation computing a number that was thrown away, to produce a
	// 29 KiB window.
	//
	// maxBytes <= 0 is read as "no cap" rather than "cap of zero". Validation
	// rejects a non-positive value, but a hand-built Config reaches here too,
	// and the wrong reading would strip every file's content in silence.
	bound := ReasonTooLarge
	if maxBytes <= 0 || len(e.Content) <= maxBytes {
		bound = ReasonOverBudget
		e.Tokens = estimator.EstimateTokens(Render(*e))
		if e.Tokens <= budget {
			return ""
		}
	}

	win := newWindower(e.Content, e.File)

	// probe reports whether a width fits, and records which limit turned it down.
	// The last refusal is the narrowest one, so bound ends up naming the knob
	// that stopped the window from being wider, not merely the one that rejected
	// the whole file. Naming the wrong one sends an operator to a setting they
	// can raise without anything changing, which the reason strings exist to
	// prevent.
	probe := func(width int) (text string, tokens int, ok bool) {
		text, srcBytes, elided := win.render(width)
		if !elided {
			return "", 0, false
		}
		if maxBytes > 0 && srcBytes > maxBytes {
			bound = ReasonTooLarge
			return "", 0, false
		}

		// Costed on a copy: a width that turns out not to fit must leave the
		// entry exactly as it was.
		trial := *e
		trial.Content, trial.Truncated, trial.ContextLines = text, true, width
		if tokens = estimator.EstimateTokens(Render(trial)); tokens > budget {
			bound = ReasonOverBudget
			return "", 0, false
		}
		return text, tokens, true
	}

	// Widen from the floor rather than narrow from the ceiling. Both find the
	// same width, but this way every render is at most twice the size of the
	// window that ends up shipping, where starting at the ceiling means rendering
	// something close to the whole file first, the cost the byte cap was just
	// moved above to avoid.
	var (
		fit    int
		over   int
		text   string
		tokens int
	)
	// The start is a floor, not the floor: a file whose edits all point past
	// the end of the content it was fetched with keeps nothing until the
	// window is wide enough to reach back into it.
	start := max(minContextLines, win.floor())
	if ceiling := win.ceiling(); ceiling >= start {
		for width := start; ; width = min(width*2, ceiling) {
			t, n, ok := probe(width)
			if !ok {
				over = width
				break
			}
			fit, text, tokens = width, t, n
			if width == ceiling {
				break
			}
		}

		// Doubling only locates the answer within a factor of two, and the half
		// it leaves behind is context the reviewer could have had and did not.
		for range windowBisectSteps {
			if fit == 0 || over-fit < 2 {
				break
			}

			mid := fit + (over-fit)/2
			t, n, ok := probe(mid)
			if !ok {
				over = mid
				continue
			}
			fit, text, tokens = mid, t, n
		}
	}

	if fit > 0 {
		e.Content, e.Truncated, e.ContextLines, e.Tokens = text, true, fit, tokens
		return windowedReason(bound, fit)
	}

	// Not even the narrowest useful window fits. Drop file context and review the
	// diff alone rather than send a request the provider will reject, a rejected
	// request loses every file in its batch, so an over-budget send is worse than
	// a thinner review. Recorded, because an entry that reads as fully attached
	// while carrying nothing is the same silent zero the Degraded list exists to
	// prevent.
	e.Content = ""
	e.Truncated = false
	e.ContextLines = 0
	e.Tokens = estimator.EstimateTokens(Render(*e))
	return diffOnlyReason(bound)
}

// batch groups entries under both a per-request file count and a token budget.
//
// An entry that alone exceeds the budget still gets its own batch: dropping it
// would silently skip a reviewable file, and an over-budget request that the
// provider rejects is a visible, diagnosable failure instead.
//
// Which of the two limits binds is decided entirely by file size, because
// fitEntry has already sized every entry against the whole request budget on
// its own. Measured by TestPackingTableReportsBudgetUse at the shipped 60k budget and 6 files
// per request, by the row names it prints: "6 tiny" (20-line files) fills a
// request to 4.9% of budget and "6 small (200L)" to 36.1%, both split only by
// the file ceiling, while "6 big (2000L)" costs 33,116 tokens a file and takes
// a request each without ever reaching that ceiling. Packing by count is the
// deliberate half (fewer files per request means more attention each), but the
// consequence is that max_files_per_request is a ceiling large files cannot
// reach and the budget is a limit small ones cannot approach. Re-measure there
// before retuning either, and take each number from the row that is named:
// 4.9% belongs to "6 tiny", and "6 small (200L)" is the row that measures
// 36.1%.
func batch(entries []Entry, maxFiles, budget int) []Batch {
	var (
		batches []Batch
		current Batch
	)

	flush := func() {
		if len(current.Entries) > 0 {
			batches = append(batches, current)
			current = Batch{}
		}
	}

	for _, e := range entries {
		overFiles := len(current.Entries) >= maxFiles
		overBudget := len(current.Entries) > 0 && current.Tokens+e.Tokens > budget

		if overFiles || overBudget {
			flush()
		}

		current.Entries = append(current.Entries, e)
		current.Tokens += e.Tokens
	}
	flush()

	return batches
}

// RenderDiffOnly renders an entry's header and diff without the file body or
// related context: what a classifier reads, since the change is the diff.
func RenderDiffOnly(e Entry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "### File: %s\n", promptSafe(e.File.Path))
	stats := e.File.Stats()
	fmt.Fprintf(&b, "Change: %s (+%d/-%d)\n\n```diff\n", e.File.Kind, stats.Added, stats.Removed)
	b.WriteString(e.File.String())
	b.WriteString("```\n")
	return b.String()
}

// Render formats one entry for a prompt: the change itself, then the file it
// lives in. Line numbers are included throughout, since a finding is only
// actionable if the model can cite where it belongs.
func Render(e Entry) string {
	if e.SourceOnly {
		var b strings.Builder
		fmt.Fprintf(&b, "### Supporting source: %s\nFindings here are summary evidence, not inline comments.\n", promptSafe(e.File.Path))
		for _, instruction := range e.Instructions {
			fmt.Fprintf(&b, "Path instruction: %s\n", promptSafe(instruction))
		}
		fmt.Fprintf(&b, "\n```\n%s```\n", numberLines(e.Content))
		return b.String()
	}
	var b strings.Builder

	fmt.Fprintf(&b, "### File: %s\n", promptSafe(e.File.Path))
	if e.File.OldPath != "" && e.File.OldPath != e.File.Path {
		fmt.Fprintf(&b, "Renamed from: %s\n", promptSafe(e.File.OldPath))
	}

	stats := e.File.Stats()
	fmt.Fprintf(&b, "Change: %s (+%d/-%d)\n\n", e.File.Kind, stats.Added, stats.Removed)

	if len(e.Instructions) > 0 {
		b.WriteString("Repository instructions for this path:\n")
		for _, ins := range e.Instructions {
			fmt.Fprintf(&b, "- %s\n", promptSafe(ins))
		}
		b.WriteByte('\n')
	}

	b.WriteString("#### Diff\n\n```diff\n")
	b.WriteString(e.File.String())
	b.WriteString("```\n")

	// A file the change adds is already whole in its own diff: every line is
	// an addition, and the diff renders each with its new-file line number, so
	// the full-file section that follows would be the same content a second
	// time in a different costume. It was 43,569 of 100,070 prompt characters
	// on the three-file fixture in #81.
	//
	// Content itself is kept rather than dropped at assembly, because related
	// context parses it for the imports a new file brings in, which is where
	// that context is worth most.
	//
	// Only when the whole file is there. A window is a window even of an
	// addition, and saying so is the point of the heading it carries.
	wholeAddition := e.File != nil && e.File.Kind == diff.ChangeAdded && !e.Truncated
	if e.HasContent() && !wholeAddition {
		if e.Truncated {
			// The width is stated because it tells the model how much of the
			// file it is not seeing. Without it, a window reads like a whole
			// file, and "this helper is never called" is a confident finding
			// drawn from the part that happened to be elided.
			//
			// "an edit" is exact, and had to be earned: the window is built
			// around removal sites as well as added lines. Built on additions
			// alone, a pure-deletion hunk's site was elided while this heading
			// told the model it was seeing the regions around every edit.
			fmt.Fprintf(&b, "\n#### File after the change (only the regions within %d lines of an edit; the rest of the file is not shown)\n\n", e.ContextLines)
		} else {
			b.WriteString("\n#### Full file after the change\n\n")
		}

		b.WriteString("```\n")
		// A windowed body is already numbered, because its elision markers
		// occupy no line number.
		if e.Truncated {
			b.WriteString(e.Content)
		} else {
			b.WriteString(numberLines(e.Content))
		}
		b.WriteString("```\n")
	}

	if len(e.Related) > 0 {
		// Stated as context and not as a file under review, twice: the heading
		// says so, and the anchoring rule already confines findings to the
		// paths listed for the batch. A finding placed on one of these files
		// is dropped by the anchor filter, so the model is told not to try.
		var defs, callers []Related
		for _, r := range e.Related {
			if r.Calls != "" {
				callers = append(callers, r)
			} else {
				defs = append(defs, r)
			}
		}
		if len(defs) > 0 {
			b.WriteString("\n#### Definitions this change uses, from files it does not touch\n\n")
			b.WriteString("Context only. These files are not under review: judge the change by them, but do not report findings on them.\n\n")
			for _, r := range defs {
				b.WriteString(renderRelated(r))
			}
		}
		if len(callers) > 0 {
			// Callers are the one place a changed contract shows its cost.
			// The instruction names the check because the model otherwise
			// reads them as more of the same context.
			b.WriteString("\n#### Callers of what this change redefines, from files it does not touch\n\n")
			b.WriteString("Context only. These files are not under review and are unchanged: they still assume the old behaviour. Check each against the new definition it calls, and report any break on the changed line that causes it, not on the caller. A constant listed with a caller's name is one that caller passes, shown so its value is known.\n\n")
			for _, r := range callers {
				b.WriteString(renderRelated(r))
			}
		}
	}

	return b.String()
}

// numberLines prefixes each line with its 1-based number so the model can cite
// a line it sees in the full file rather than only lines present in the diff.
func numberLines(content string) string {
	var b strings.Builder

	lines := strings.Split(content, "\n")
	// A trailing newline produces a final empty element that is not a line.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	for i, line := range lines {
		fmt.Fprintf(&b, "%6d  %s\n", i+1, line)
	}
	return b.String()
}

// RenderBatch renders the source and task metadata sent together in one call.
func RenderBatch(b Batch) string {
	var out strings.Builder
	if b.Assessment != "" {
		out.WriteString(promptSafe(b.Assessment))
		out.WriteString("\n\n")
	}
	for _, entry := range b.Entries {
		out.WriteString(Render(entry))
		out.WriteByte('\n')
	}
	return out.String()
}
