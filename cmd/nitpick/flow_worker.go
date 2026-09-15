package main

import (
	"context"
	"os"

	"github.com/jdziat/open-nitpick/internal/prflow"
)

// runFlowWorker is intentionally hidden from the user-facing command list.
// The parent flow pass invokes it in a clean, private working directory with
// a bounded JSON request; it never reads the repository on its own.
func runFlowWorker(ctx context.Context) error {
	return prflow.RunWorker(ctx, os.Stdin, os.Stdout)
}
