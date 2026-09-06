package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jdziat/open-nitpick/internal/modelid"
)

// runIdentifyModel says which corpus author a file's style is most similar
// to. It is offered for the one language the experiment cleared, answers
// "unknown" everywhere else and below the confidence floor, and says so:
// see docs/findings.md, "Which model wrote it".
func runIdentifyModel(args []string) error {
	fs := flag.NewFlagSet("identify-model", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: nitpick identify-model <file>...\n\nNames the model whose style each file is most similar to, from the experiment's corpus.\nTypeScript only; other languages and low-confidence answers are \"unknown\", with the reason.")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		fs.Usage()
		return fmt.Errorf("no file given")
	}
	for _, path := range fs.Args() {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		lang := languageOfExt(filepath.Ext(path))
		author, confidence, reason, err := modelid.Identify(string(data), lang)
		if err != nil {
			return err
		}
		if author == "unknown" {
			fmt.Printf("%s: unknown (%s)\n", path, reason)
			continue
		}
		fmt.Printf("%s: most similar to %s (confidence %.2f; a style match against a small corpus, not an attribution)\n", path, strings.ReplaceAll(author, "_", "/"), confidence)
	}
	return nil
}

func languageOfExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".ts", ".tsx":
		return "typescript"
	case ".go":
		return "go"
	case ".py":
		return "python"
	default:
		return strings.TrimPrefix(strings.ToLower(ext), ".")
	}
}
