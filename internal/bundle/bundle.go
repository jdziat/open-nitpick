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
	"strings"
	"unicode/utf8"

	llms "github.com/nocturnium/llm-go-sdk"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// ContentFetcher reads a file's full contents at the reviewed revision.
type ContentFetcher func(ctx context.Context, path string) ([]byte, error)

// Entry is one file prepared for review.
type Entry struct {
	File *diff.File

	// Content is the full new-side file, empty when it was unavailable,
	// too large, binary, or excluded by the token budget.
	Content string

	// Truncated marks that Content is a window around the changed hunks
	// rather than the whole file.
	Truncated bool

	// Instructions are the configured path-scoped prompts that apply here.
	Instructions []string

	// Tokens is the estimated cost of rendering this entry.
	Tokens int
}

// HasContent reports whether full-file context is attached.
func (e *Entry) HasContent() bool { return e.Content != "" }

// Batch is a group of entries reviewed in one model call.
type Batch struct {
	Entries []Entry

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

	// Skipped records files that were excluded and why. Reporting these
	// matters: a review that silently ignored half the diff looks identical
	// to one that found nothing wrong.
	Skipped []Skip
}

// Skip records one excluded file.
type Skip struct {
	Path   string
	Reason string
}

// Files returns every file across all batches.
func (p *Plan) Files() int {
	n := 0
	for _, b := range p.Batches {
		n += len(b.Entries)
	}
	return n
}

// Reasons to skip a file.
const (
	ReasonIgnored     = "matched an ignore pattern"
	ReasonBinary      = "binary file"
	ReasonDeleted     = "file was deleted"
	ReasonNoChanges   = "no added lines to comment on"
	ReasonGenerated   = "generated file"
	ReasonFileLimit   = "exceeded review.max_files"
	ReasonUnavailable = "contents could not be read"
	ReasonTooLarge    = "exceeded review.max_file_bytes"
	ReasonNotText     = "not valid UTF-8 text"
)

// Assemble selects reviewable files, attaches their contents, and groups them
// into batches.
//
// Fetch may be nil, in which case entries carry only their diffs. A fetch
// failure for one file downgrades that file to diff-only rather than failing
// the run, since a partial review is far more useful than none.
func Assemble(ctx context.Context, cfg *config.Config, files diff.Files, fetch ContentFetcher) (*Plan, error) {
	if cfg == nil {
		return nil, errors.New("bundle: nil config")
	}

	plan := &Plan{}
	estimator := llms.DefaultTokenEstimator()

	selected := make([]*diff.File, 0, len(files))
	for _, f := range files {
		if reason, ok := skipReason(cfg, f); ok {
			plan.Skipped = append(plan.Skipped, Skip{Path: f.Path, Reason: reason})
			continue
		}

		if len(selected) >= cfg.Review.MaxFiles {
			plan.Skipped = append(plan.Skipped, Skip{Path: f.Path, Reason: ReasonFileLimit})
			continue
		}
		selected = append(selected, f)
	}

	entries := make([]Entry, 0, len(selected))
	for _, f := range selected {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		entry := Entry{
			File:         f,
			Instructions: cfg.InstructionsFor(f.Path),
		}

		if cfg.Review.IncludeFullFiles && fetch != nil {
			content, skip := fetchContent(ctx, cfg, fetch, f)
			switch {
			case skip != "":
				// Not fatal: review the diff without the full file.
				plan.Skipped = append(plan.Skipped, Skip{Path: f.Path, Reason: skip})

			case cfg.Review.SkipGenerated && isGenerated(content):
				// Generated files are dropped entirely rather than reviewed
				// diff-only: commenting on output nobody edits by hand is
				// pure noise.
				plan.Skipped = append(plan.Skipped, Skip{Path: f.Path, Reason: ReasonGenerated})
				continue

			default:
				entry.Content = content
			}
		}

		fitEntry(&entry, cfg.Review.TokenBudgetPerRequest, estimator)
		entries = append(entries, entry)
	}

	plan.Batches = batch(entries, cfg.Review.MaxFilesPerRequest, cfg.Review.TokenBudgetPerRequest)
	return plan, nil
}

// fetchContent reads a file's contents, applying the size and encoding limits.
// The returned reason is non-empty when full content was not attached.
func fetchContent(ctx context.Context, cfg *config.Config, fetch ContentFetcher, f *diff.File) (content, reason string) {
	data, err := fetch(ctx, f.Path)
	switch {
	case errors.Is(err, vcs.ErrNotFound):
		return "", ReasonUnavailable
	case err != nil:
		return "", fmt.Sprintf("%s: %v", ReasonUnavailable, err)
	}

	if len(data) > cfg.Review.MaxFileBytes {
		return "", ReasonTooLarge
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
	case len(f.ChangedLines()) == 0:
		return ReasonNoChanges, true
	}
	return "", false
}

// fitEntry costs an entry and, when it alone would blow the per-request
// budget, replaces its full content with a window around the changes. The diff
// itself is never trimmed: it is the thing being reviewed.
func fitEntry(e *Entry, budget int, estimator *llms.TokenEstimator) {
	e.Tokens = estimator.EstimateTokens(Render(*e))
	if e.Tokens <= budget || !e.HasContent() {
		return
	}

	windowed, elided := window(e.Content, e.File)
	if !elided {
		return
	}

	e.Content = windowed
	e.Truncated = true
	e.Tokens = estimator.EstimateTokens(Render(*e))

	// Still too large even windowed: drop full-file context and review the
	// diff alone rather than sending a request the provider will reject.
	if e.Tokens > budget {
		e.Content = ""
		e.Truncated = false
		e.Tokens = estimator.EstimateTokens(Render(*e))
	}
}

// batch groups entries under both a per-request file count and a token budget.
//
// An entry that alone exceeds the budget still gets its own batch: dropping it
// would silently skip a reviewable file, and an over-budget request that the
// provider rejects is a visible, diagnosable failure instead.
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

// Render formats one entry for a prompt: the change itself, then the file it
// lives in. Line numbers are included throughout, since a finding is only
// actionable if the model can cite where it belongs.
func Render(e Entry) string {
	var b strings.Builder

	fmt.Fprintf(&b, "### File: %s\n", e.File.Path)
	if e.File.OldPath != "" && e.File.OldPath != e.File.Path {
		fmt.Fprintf(&b, "Renamed from: %s\n", e.File.OldPath)
	}

	stats := e.File.Stats()
	fmt.Fprintf(&b, "Change: %s (+%d/-%d)\n\n", e.File.Kind, stats.Added, stats.Removed)

	if len(e.Instructions) > 0 {
		b.WriteString("Repository instructions for this path:\n")
		for _, ins := range e.Instructions {
			fmt.Fprintf(&b, "- %s\n", ins)
		}
		b.WriteByte('\n')
	}

	b.WriteString("#### Diff\n\n```diff\n")
	b.WriteString(e.File.String())
	b.WriteString("```\n")

	if e.HasContent() {
		if e.Truncated {
			b.WriteString("\n#### File after the change (regions around the edits)\n\n")
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
