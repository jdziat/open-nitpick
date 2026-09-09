//go:build eval

package llm

import (
	"context"
	"os"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
)

// A live check that a provider can actually embed, run by hand.
func TestLiveEmbed(t *testing.T) {
	provider, model := os.Getenv("EMBED_PROVIDER"), os.Getenv("EMBED_MODEL")
	if provider == "" || model == "" {
		t.Skip("set EMBED_PROVIDER and EMBED_MODEL")
	}
	e, err := BuildEmbedder(context.Background(), config.ModelSpec{Provider: provider, Model: model})
	if err != nil {
		t.Fatalf("BuildEmbedder: %v", err)
	}
	v, err := e.Embed(context.Background(), []string{"hello", "goodbye"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	t.Logf("vectors=%d dims=%d", len(v), len(v[0]))
}
