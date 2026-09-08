package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/config/docgen"
)

// runConfigRef writes the configuration reference: every key the loader
// accepts, its type, its shipped default and one sentence about it.
//
// It is a command rather than a checked-in file because a checked-in file is
// a promise someone has to keep by hand. `make docs` regenerates it and CI
// fails on a diff, so a key added without a sentence about it fails the build
// that adds it.
func runConfigRef(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("config-reference", flag.ContinueOnError)
	src := fs.String("src", "internal/config", "the package the documentation is read from")
	out := fs.String("o", "", "write here instead of standard output")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: nitpick config-reference [flags]\n\n"+
			"Writes every key .nitpick.yaml accepts, with its type, its shipped default and one\n"+
			"sentence about it, read from the source of the package -src names. It reads that\n"+
			"package from disk, so it runs from a checkout.\n\nFlags:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	docs, values, err := docgen.ReadDocs(*src)
	if err != nil {
		return err
	}
	fields := docgen.Walk(config.Defaults(), docs, values)

	// One entry per key, under a heading of its own name.
	//
	// Six four-column tables were tried and replaced. A table is the obvious
	// shape for a reference and it was the wrong one here: 199 keys carried 7
	// anchors between them, so no key could be linked or searched to; the
	// widest column was the one a reader came for, and below 1366px it went off
	// the right edge behind a horizontal scrollbar that sat thousands of pixels
	// down the page; and on paper the key column took 44% of the width and left
	// the sentence 29 characters a line.
	//
	// A heading per key costs a long table of contents. That is what an index
	// is, and it is the affordance a 199-key reference exists to provide.
	var b strings.Builder
	b.WriteString(header)
	group := ""
	for _, f := range fields {
		if g := topLevel(f.Path); g != group {
			group = g
			fmt.Fprintf(&b, "\n## %s\n", group)
		}
		def := f.Default
		if def == "" {
			def = "none"
		}
		doc := f.Doc
		if doc == "" {
			doc = "Undocumented. A key with no sentence here has none in the source either."
		}
		fmt.Fprintf(&b, "\n### `%s`\n\n%s, default `%s`.\n%s\n", f.Path, f.Type, def, doc)
	}
	b.WriteString(footer)

	if *out == "" {
		_, err := io.WriteString(stdout, b.String())
		return err
	}
	return os.WriteFile(*out, []byte(b.String()), 0o644)
}

// topLevel is the key a path hangs from: the section it is listed under.
func topLevel(path string) string {
	if i := strings.IndexAny(path, ".["); i >= 0 {
		return path[:i]
	}
	return path
}

const header = `# Configuration reference

Every key ` + "`.nitpick.yaml`" + ` accepts, generated from the configuration the
binary was built with. [Configuration](configuration.md) is the same settings
argued for rather than listed; this page is the index.

Regenerate with ` + "`nitpick config-reference -o docs/configuration-reference.md`" + `,
which ` + "`make docs`" + ` runs and CI checks. That check diffs this file against
what the generator produces now, so a key the generator reaches cannot drift
from its row. It says nothing about a key the generator never walks to, and
nothing fails when it stops short. One field it cannot walk into is
` + "`fallback`" + `, a model block inside a model block: walking it does not
terminate, so it is emitted as a ` + "`same keys as …`" + ` row naming the block
whose keys it repeats.

` + "`[]`" + ` marks a list whose entries carry the keys beneath it,
` + "`<name>`" + ` a map whose keys you choose, and ` + "`same keys as …`" + ` a block
that repeats the keys listed under the path it names. A default of
` + "`none`" + ` means the key is unset until you set it, which is not always the
same as off: the prose page says which.

Every model block overlays ` + "`models.default`" + `. A role, a route or an
ensemble entry sets only what differs, and a key it leaves out is served by the
default's value, so that is what the Default column carries for those rows
rather than the zero of the field's type.

Endpoint and credential keys (` + "`base_url`, `api_key_env`, `extra`," + `
` + "`allow_private_endpoint`, `api_key_keyring`, `credential_command`" + `) are
withheld from a repository's own file unless ` + "`NITPICK_TRUST_CONFIG_ENDPOINTS=1`" + `
is set, for every role. So is ` + "`persona.custom`" + `. See
[Trust model](trust-model.md).
`

const footer = `
Anything not listed is not a key. The loader rejects an unrecognised one rather
than ignoring it, so a typo fails the run instead of silently doing nothing.
`
