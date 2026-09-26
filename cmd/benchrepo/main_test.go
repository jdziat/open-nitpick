package main

import (
	"testing"

	"github.com/jdziat/open-nitpick/internal/evals"
)

func TestPresentationDoesNotPanicOnAnEmptyTitle(t *testing.T) {
	// "-nit" reduces to an empty title. Mutation: indexing title[:1] panics.
	title, body := presentation(evals.Fixture{Name: "-nit"})
	if title != "-nit" || body == "" {
		t.Fatalf("title=%q body=%q", title, body)
	}
}
