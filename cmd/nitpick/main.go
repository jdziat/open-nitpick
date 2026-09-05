// Command nitpick reviews a pull request or a local change with a configured
// model.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

// version is set at build time via -ldflags.
var version = "dev"

// Exit codes. The distinction matters in CI: a review that found blocking
// issues is a different outcome from a review that could not run.
const (
	exitOK       = 0
	exitFindings = 1
	exitError    = 2
	exitUsage    = 64
)

func main() {
	os.Exit(run())
}

func run() int {
	if len(os.Args) < 2 {
		usage()
		return exitUsage
	}

	// Ctrl-C and CI cancellation should stop in-flight model calls rather than
	// leaving them to run to completion.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch cmd := os.Args[1]; cmd {
	case "review":
		err = runReview(ctx, os.Args[2:])
	case "full-review":
		err = runFullReview(ctx, os.Args[2:])
	case "repo-score":
		err = runRepoScore(ctx, os.Args[2:])
	case "identify-model":
		err = runIdentifyModel(os.Args[2:])
	case "explain-config":
		err = runExplainConfig(os.Args[2:])
	case "linters":
		err = runLinters()
	case "providers":
		err = runProviders()
	case "version", "--version", "-v":
		fmt.Println("nitpick", version)
		return exitOK
	case "help", "--help", "-h":
		usage()
		return exitOK
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		usage()
		return exitUsage
	}

	switch {
	case err == nil:
		return exitOK
	case errors.Is(err, flag.ErrHelp):
		// "nitpick review -h" printed usage; that is success, not the exit
		// code reserved for "the review could not run".
		return exitOK
	case errors.Is(err, errFindings):
		// The findings themselves were already reported.
		return exitFindings
	case errors.Is(err, context.Canceled):
		fmt.Fprintln(os.Stderr, "cancelled")
		return exitError
	default:
		fmt.Fprintln(os.Stderr, "error:", err)
		return exitError
	}
}

// errFindings signals that the review succeeded but found gating issues.
var errFindings = errors.New("findings at or above the configured threshold")

func usage() {
	fmt.Fprint(os.Stderr, `nitpick — self-hosted, model-agnostic pull request review

Usage:
  nitpick review [flags]           Review a change
  nitpick full-review [flags] [path...]
                                   Review the whole tree, or the paths given, with a remediation plan
  nitpick repo-score [flags] [path...]
                                   The same, plus slop, bug and security findings per thousand lines, by language
  nitpick identify-model <file>... Which model's style a file is most similar to (TypeScript only; see docs/findings.md)
  nitpick explain-config [flags]   Show the resolved configuration and prompts
  nitpick providers                List available model providers
  nitpick linters                  List the deterministic analyzers and how each is configured
  nitpick version                  Print the version

Run "nitpick <command> -h" for a command's flags.

Configuration is read from .nitpick.yaml at the repository root. With no config
file, LLM_PROVIDER and LLM_MODEL are used.
`)
}

// newLogger builds the run's logger. Human-readable by default; JSON when
// something is going to parse it.
func newLogger(verbose bool, format string) *slog.Logger {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}

	opts := &slog.HandlerOptions{Level: level}

	if strings.EqualFold(format, "json") {
		return slog.New(slog.NewJSONHandler(os.Stderr, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stderr, opts))
}
