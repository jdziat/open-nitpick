package llm

import (
	"context"
	"fmt"

	llms "github.com/nocturnium/llm-go-sdk/v6"

	"github.com/jdziat/open-nitpick/internal/config"
)

// Embedding clients.
//
// Separate from Client because an embedder is not a chat model and shares
// almost nothing with one: no structured output, no stall loop, no fallback,
// no roles. What it does share is provider construction and credential
// resolution, which is why it lives here rather than in internal/knowledge.
//
// Not every provider can do this. The SDK reports embedding support per
// provider and several this tool otherwise recommends do not have it, synthetic
// among them, so a repository embedding needs a second provider and says so.

// Embedder wraps an SDK client that can embed.
type Embedder struct {
	llm   llms.Embedder
	spec  config.ModelSpec
	model string
}

// BuildEmbedder constructs an embedding client from a spec.
//
// It fails at construction rather than at the first request when the provider
// cannot embed, so a misconfiguration is a message naming the provider instead
// of an error in the middle of a review.
func BuildEmbedder(ctx context.Context, spec config.ModelSpec) (*Embedder, error) {
	if err := validateProvider(spec.Provider); err != nil {
		return nil, err
	}

	cfg, err := providerConfig(ctx, spec)
	if err != nil {
		return nil, err
	}
	client, err := llms.New(spec.Provider, cfg)
	if err != nil {
		return nil, fmt.Errorf("build embedder %s/%s: %w", spec.Provider, spec.Model, err)
	}

	e, ok := llms.AsEmbedder(client)
	if !ok {
		return nil, fmt.Errorf("provider %q cannot produce embeddings; "+
			"name a provider that can under models.embed", spec.Provider)
	}
	return &Embedder{llm: e, spec: spec, model: spec.Provider + "/" + spec.Model}, nil
}

// Model names the provider and model, which is what an index records.
func (e *Embedder) Model() string { return e.model }

// Embed returns one vector per text, in order.
func (e *Embedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	resp, err := e.llm.Embed(ctx, texts, llms.WithEmbedModel(e.spec.Model))
	if err != nil {
		return nil, err
	}
	if len(resp.Embeddings) != len(texts) {
		return nil, fmt.Errorf("embed: asked for %d vectors and got %d", len(texts), len(resp.Embeddings))
	}

	// By Index rather than by position. The API documents the field, and a
	// provider that returns them out of order would otherwise attach every
	// vector to the wrong entry, which is a failure no test downstream of here
	// could distinguish from a bad embedding model.
	out := make([][]float32, len(texts))
	for _, emb := range resp.Embeddings {
		if emb.Index < 0 || emb.Index >= len(out) {
			return nil, fmt.Errorf("embed: vector index %d is outside the %d texts sent", emb.Index, len(out))
		}
		if out[emb.Index] != nil {
			return nil, fmt.Errorf("embed: two vectors claim index %d", emb.Index)
		}
		out[emb.Index] = emb.Vector
	}
	for i, v := range out {
		if v == nil {
			return nil, fmt.Errorf("embed: no vector for text %d", i)
		}
	}
	return out, nil
}
