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
// to. It is offered for the languages the experiment cleared, answers
// "unknown" everywhere else and below the confidence floor, and says so:
// see docs/findings.md, "Which model wrote it" and "Fingerprints".
func runIdentifyModel(args []string) error {
	fs := flag.NewFlagSet("identify-model", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: nitpick identify-model <file>...\n\nNames the model whose style each file is most similar to, from the experiment's corpus.\nGo, Python and TypeScript; other languages and low-confidence answers are \"unknown\", with the reason.")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		fs.Usage()
		return fmt.Errorf("no file given")
	}
	ids, err := identifyFiles(fs.Args())
	if err != nil {
		return err
	}
	for _, id := range ids {
		if id.Author == "unknown" {
			fmt.Printf("%s: unknown (%s)\n", id.Path, id.Reason)
			continue
		}
		fmt.Printf("%s: most similar to %s (margin %.2f over the next; %s)\n", id.Path, id.Author, id.Margin, identifyCaveat)
	}
	return nil
}

// identifyCaveat travels with every answer, on the command line and over MCP.
const identifyCaveat = "a style match against a small corpus of six models, not an attribution"

// identification is one file's answer.
type identification struct {
	Path   string  `json:"path"`
	Author string  `json:"author" jsonschema:"the corpus author the file is most similar to, as provider/model, or unknown"`
	Margin float64 `json:"margin" jsonschema:"the gap between the best and second-best similarity, relative to the best"`
	Reason string  `json:"reason,omitempty" jsonschema:"why the answer is unknown, when it is"`
}

// identifyFiles answers for each path. Shared by the command and the server.
func identifyFiles(paths []string) ([]identification, error) {
	var out []identification
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		author, confidence, reason, err := modelid.Identify(string(data), languageOfExt(filepath.Ext(path)))
		if err != nil {
			return nil, err
		}
		out = append(out, identification{Path: path, Author: strings.ReplaceAll(author, "_", "/"), Margin: confidence, Reason: reason})
	}
	return out, nil
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
