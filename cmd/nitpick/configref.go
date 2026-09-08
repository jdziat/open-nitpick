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
	if err := fs.Parse(args); err != nil {
		return err
	}

	docs, err := docgen.ReadDocs(*src)
	if err != nil {
		return err
	}
	fields := docgen.Walk(config.Defaults(), docs)

	var b strings.Builder
	b.WriteString(header)
	for _, f := range fields {
		def := f.Default
		if def == "" {
			def = "none"
		}
		doc := f.Doc
		if doc == "" {
			doc = "Undocumented. A key with no sentence here has none in the source either."
		}
		fmt.Fprintf(&b, "| `%s` | %s | `%s` | %s |\n", f.Path, f.Type, def, cell(doc))
	}
	b.WriteString(footer)

	if *out == "" {
		_, err := io.WriteString(stdout, b.String())
		return err
	}
	return os.WriteFile(*out, []byte(b.String()), 0o644)
}

// cell keeps a table cell from ending the row it is in.
func cell(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "|", `\|`), "\n", " ")
}

const header = `# Configuration reference

Every key ` + "`.nitpick.yaml`" + ` accepts, generated from the configuration the
binary was built with. [Configuration](configuration.md) is the same settings
argued for rather than listed; this page is the index.

Regenerate with ` + "`nitpick config-reference -o docs/configuration-reference.md`" + `,
which ` + "`make docs`" + ` runs and CI checks. A key that reaches the loader
without a row here fails that check, so the two cannot drift apart quietly.

` + "`[]`" + ` marks a list whose entries carry the keys beneath it, and
` + "`<name>`" + ` a map whose keys you choose. A default of ` + "`none`" + ` means the
key is unset until you set it, which is not always the same as off: the prose
page says which.

Endpoint and credential keys (` + "`base_url`, `api_key_env`, `extra`," + `
` + "`allow_private_endpoint`, `api_key_keyring`, `credential_command`" + `) are
withheld from a repository's own file unless ` + "`NITPICK_TRUST_CONFIG_ENDPOINTS=1`" + `
is set. See [Trust model](trust-model.md).

| Key | Type | Default | What it does |
|---|---|---|---|
`

const footer = `
Anything not listed is not a key. The loader rejects an unrecognised one rather
than ignoring it, so a typo fails the run instead of silently doing nothing.
`
