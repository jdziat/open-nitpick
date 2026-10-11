package review

import (
	"context"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/llm"
)

// splittable reports whether a batch can be reviewed as two smaller ones.
// A design batch is one indivisible assessment, and a single file has
// nothing to halve.
func splittable(b bundle.Batch) bool {
	return b.DesignTask == "" && len(b.Entries) > 1
}

// halves cuts a batch where its estimated tokens cross the midpoint, keeping
// both sides non-empty.
func halves(b bundle.Batch) (bundle.Batch, bundle.Batch) {
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
