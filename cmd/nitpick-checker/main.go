// Command nitpick-checker is the trusted entrypoint of the execution image.
package main

import (
	"context"
	"flag"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/jdziat/open-nitpick/internal/execution"
)

func main() {
	head := flag.String("head", "", "pinned source commit")
	digest := flag.String("sha256", "", "pinned archive hash")
	kind := flag.String("kind", "", "trusted check profile")
	packagePath := flag.String("package", "", "package scope")
	flag.Parse()
	if flag.NArg() != 0 {
		os.Exit(75)
	}
	file, err := os.Open("/input/snapshot.tar")
	if err != nil {
		os.Exit(75)
	}
	raw, readErr := io.ReadAll(io.LimitReader(file, (32<<20)+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		os.Exit(75)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := execution.RunGuest(ctx, execution.Snapshot{Head: *head, SHA256: *digest, Archive: raw}, execution.Check{Kind: *kind, Package: *packagePath}, "/work", os.Stdout)
	stop()
	os.Exit(code)
}
