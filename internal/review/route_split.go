package review

import (
	"context"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/llm"
	llms "github.com/nocturnium/llm-go-sdk/v6"
)

// splittable reports whether a batch can be reviewed as two smaller ones.
// A design batch is one indivisible assessment. A lone file is divisible only
// along its hunks: with a single hunk the change itself is the smallest
// request there is.
func splittable(b bundle.Batch) bool {
	if b.DesignTask != "" {
		return false
	}
	switch len(b.Entries) {
	case 0:
		return false
	case 1:
		return hunkSplittable(b.Entries[0])
	}
	return true
}

// hunkSplittable reports whether an entry's diff has two hunks to divide.
// Supporting source carries no diff of its own, so there is nothing to cut.
func hunkSplittable(e bundle.Entry) bool {
	return !e.SourceOnly && len(e.SourceSpans) == 0 && e.File != nil && len(e.File.Hunks) > 1
}

// halves cuts a batch where its estimated tokens cross the midpoint, keeping
// both sides non-empty. A one-file batch is cut between its hunks instead.
func halves(b bundle.Batch) (bundle.Batch, bundle.Batch) {
	if len(b.Entries) == 1 && hunkSplittable(b.Entries[0]) {
		return halveHunks(b)
	}
	cut, sum := 0, 0
	for i, e := range b.Entries {
		if i > 0 && sum+e.Tokens > b.Tokens/2 {
			break
		}
		sum += e.Tokens
		cut = i + 1
	}
	if cut >= len(b.Entries) {
		cut = len(b.Entries) - 1
	}
	side := func(entries []bundle.Entry) bundle.Batch {
		n := bundle.Batch{Assessment: b.Assessment, Entries: entries}
		for _, e := range entries {
			n.Tokens += e.Tokens
		}
		return n
	}
	return side(b.Entries[:cut:cut]), side(b.Entries[cut:])
}

// halveHunks splits the diff of a lone file at the middle of its changed
// lines. Both halves keep the file's context and related definitions, so each
// still reads as a review of that file; they differ in how many changes the
// model has to answer for, which is what overran its output budget. Hunks are
// weighed by line count because a token estimate of the diff alone would
// need the rendered text of every candidate cut.
func halveHunks(b bundle.Batch) (bundle.Batch, bundle.Batch) {
	e := b.Entries[0]
	hunks := e.File.Hunks

	total := 0
	for _, h := range hunks {
		total += max(1, len(h.Lines))
	}
	cut, sum := 0, 0
	for i, h := range hunks {
		w := max(1, len(h.Lines))
		if i > 0 && sum+w > total/2 {
			break
		}
		sum += w
		cut = i + 1
	}
	if cut >= len(hunks) {
		cut = len(hunks) - 1
	}

	estimator := llms.DefaultTokenEstimator()
	side := func(part []diff.Hunk) bundle.Batch {
		file := *e.File
		file.Hunks = part
		entry := e
		entry.File = &file
		entry.Tokens = estimator.EstimateTokens(bundle.Render(entry))
		return bundle.Batch{Assessment: b.Assessment, Entries: []bundle.Entry{entry}, Tokens: entry.Tokens}
	}
	return side(hunks[:cut:cut]), side(hunks[cut:])
}

// reviewHalves reviews a batch the model could not answer as two smaller
// requests, splitting again where a half fails the same way. Either half
// failing fails the batch: a review that silently dropped half of its files
// would read as clean for them.
func (e *Engine) reviewHalves(ctx context.Context, c *llm.Client, prContext string, b bundle.Batch) ([]Finding, error) {
	left, right := halves(b)
	e.log().Warn("batch could not be answered whole; reviewing it in halves",
		"model", c.String(), "files", b.Paths(), "tokens", b.Tokens,
		"left_tokens", left.Tokens, "right_tokens", right.Tokens)

	base, err := e.reviewPromptFor(c)
	if err != nil {
		return nil, err
	}
	var all []Finding
	for _, half := range []bundle.Batch{left, right} {
		out, err := e.analyzeBatchWith(ctx, c, base, prContext, half, false)
		if err != nil && llm.ShouldEscalate(err) && splittable(half) {
			out, err = e.reviewHalves(ctx, c, prContext, half)
		}
		if err != nil {
			return nil, err
		}
		all = append(all, out...)
	}
	return all, nil
}
