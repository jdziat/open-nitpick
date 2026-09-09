package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/knowledge"
	"github.com/jdziat/open-nitpick/internal/llm"
)

// runKnowledgeIndex embeds the corpus and writes the committed index.
//
// A command rather than a build step because embedding needs a credential and
// a network, and a build that needs either is one that fails on a machine
// without them. The file it writes is checked in and CI diffs it, so a corpus
// edited without regenerating fails the build that edited it.
func runKnowledgeIndex(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("knowledge-index", flag.ContinueOnError)
	fs.Usage = func() {
		_, _ = fmt.Fprint(fs.Output(), "Usage: nitpick knowledge-index [flags]\n\n"+
			"Embeds the knowledge corpus and writes the index the reviewer retrieves from.\n"+
			"Needs models.embed configured and that provider's credential.\n\n")
		fs.PrintDefaults()
	}
	cfgPath := fs.String("config", config.FileName, "configuration file to read models.embed from")
	out := fs.String("o", "internal/knowledge/index.json", "write the index here")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.LoadFile(*cfgPath)
	if err != nil {
		return err
	}
	spec, ok := cfg.Models.ResolveEmbed()
	if !ok {
		return fmt.Errorf("no models.embed is configured, so there is nothing to embed with")
	}

	embedder, err := llm.BuildEmbedder(ctx, spec)
	if err != nil {
		return err
	}
	entries, err := knowledge.Corpus()
	if err != nil {
		return err
	}

	ix, err := knowledge.Build(ctx, entries, embedder.Model(), embedder)
	if err != nil {
		return err
	}
	raw, err := knowledge.MarshalIndex(ix)
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, raw, 0o644); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(stdout, "wrote %s: %d entries, %d dimensions, %s\n",
		*out, len(ix.Vectors), ix.Dimensions, ix.Model)
	return nil
}
