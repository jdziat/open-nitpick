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

	"github.com/jdziat/open-nitpick/internal/config"
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
	// The version the linker set, where a message about what this build knows
	// can reach it. internal/config cannot see a package main variable.
	config.Version = version

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
	case "improve":
		err = runImproveCLI(ctx, os.Args[2:])
	case "slop":
		err = runSlop(ctx, os.Args[2:])
	case "respond":
		err = runRespond(ctx, os.Args[2:])
	case "mcp":
		err = runMCP(ctx, os.Args[2:])
	case "auth":
		err = runAuth(os.Args[2:], os.Stdin, os.Stdout)
	case "init":
		err = runInit(os.Args[2:], os.Stdout)
	case "explain-config":
		err = runExplainConfig(os.Args[2:])
	case "linters":
		err = runLinters()
	case "providers":
		err = runProviders()
	case "knowledge-index":
		err = runKnowledgeIndex(ctx, os.Args[2:], os.Stdout)
	case "config-reference":
		err = runConfigRef(os.Args[2:], os.Stdout)
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

// errIncomplete ends a run that produced output but did not finish.
//
// It exits 2 rather than 1 because it is not a statement about the code: the
// gate was never reached, and a caller that reads exit 1 as "the change has
// problems" would be told something nobody measured.
var errIncomplete = errors.New("the review did not complete; see the stages it reports")

func usage() {
	fmt.Fprint(os.Stderr, `nitpick: self-hosted, model-agnostic pull request review

Usage:
  nitpick review [flags]           Review a change
  nitpick full-review [flags] [path...]
                                   Review the whole tree, or the paths given, with a remediation plan
  nitpick repo-score [flags] [path...]
                                   The same, plus slop, bug and security findings per thousand lines, by language
  nitpick improve [flags]          The wider pass: the classes a normal review filters out
  nitpick slop [flags] [path...]   AI slop only: the tells without a model, the model's slop rules, a score, and fixes
  nitpick respond [flags]          Answer an @open-nitpick comment on a pull request (review again, resolve, or a question)
  nitpick mcp [flags]              Serve the review tools to an agent session over the Model Context Protocol (stdio)
  nitpick mcp install <client>     Register that server with an agent client (claude-code, cursor, opencode, codex, ...)
  nitpick init [flags]             Write a .nitpick.yaml for this repository
  nitpick auth set <provider>      Store a provider credential in the OS keystore
  nitpick explain-config [flags]   Show the resolved configuration and prompts
  nitpick providers                List available model providers
  nitpick linters                  List the deterministic analyzers and how each is configured
  nitpick config-reference         Print every configuration key, its type and its default
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
