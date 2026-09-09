package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

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
	dir := fs.String("d", "internal/knowledge/indexes", "write the index into this directory, named after the model")
	out := fs.String("o", "", "write the index to this exact path instead")
	provider := fs.String("provider", "", "override models.embed.provider")
	model := fs.String("model", "", "override models.embed.model")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.LoadFile(*cfgPath)
	if err != nil {
		return err
	}
	spec, ok := cfg.Models.ResolveEmbed()
	// The overrides exist so one checkout can build the index for every
	// shipped embedding model without editing the configuration it reviews
	// under. An operator building their own index names it under
	// review.knowledge_index; this is how the committed ones are regenerated.
	if *provider != "" {
		spec.Provider, ok = *provider, true
	}
	if *model != "" {
		spec.Model, ok = *model, true
	}
	if !ok {
		return fmt.Errorf("no models.embed is configured and no -provider/-model given, " +
			"so there is nothing to embed with")
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
	path := *out
	if path == "" {
		if err := os.MkdirAll(*dir, 0o755); err != nil {
			return err
		}
		path = filepath.Join(*dir, indexFileName(ix.Model))
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(stdout, "wrote %s: %d entries, %d dimensions, %s, corpus %s\n",
		path, len(ix.Vectors), ix.Dimensions, ix.Model, ix.Corpus)
	return nil
}

// indexFileName turns "synthetic/hf:nomic-ai/nomic-embed-text-v1.5" into a
// filename a person can recognise in a pull request.
//
// The name is a convenience only. Selection reads the model recorded inside
// the file, so a renamed or misnamed index cannot make a run query the wrong
// vectors.
func indexFileName(model string) string {
	name := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '.':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		default:
			return '-'
		}
	}, model)
	for strings.Contains(name, "--") {
		name = strings.ReplaceAll(name, "--", "-")
	}
	return strings.Trim(name, "-") + ".json"
}
