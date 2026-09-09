//go:build eval

package knowledge

import (
	"context"
	"os"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/llm"
)

func newLiveEmbedder(t *testing.T) Embedder {
	t.Helper()
	e, err := llm.BuildEmbedder(context.Background(), config.ModelSpec{
		Provider: os.Getenv("EMBED_PROVIDER"),
		Model:    os.Getenv("EMBED_MODEL"),
	})
	if err != nil {
		t.Fatalf("BuildEmbedder: %v", err)
	}
	return e
}
