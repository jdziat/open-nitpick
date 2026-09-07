package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jdziat/open-nitpick/internal/llm"
	llms "github.com/nocturnium/llm-go-sdk/v6"
)

// nitpick auth stores a provider credential in the operating system's
// keystore, where a review finds it with nothing configured.
//
// The alternative is an environment variable, which is a secret in the process
// table, in every child process, and in whatever the shell writes its history
// to. This command exists so that the better option needs no second tool and
// no per-platform incantation.

// runAuth dispatches the auth subcommands.
func runAuth(args []string, stdin io.Reader, out io.Writer) error {
	fs := flag.NewFlagSet("auth", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `Usage: nitpick auth <command> [provider]

  set <provider>      Read a credential from standard input and store it
  delete <provider>   Remove a stored credential
  list                Say which providers have one, never what it is

The credential is read from standard input rather than an argument, so it does
not reach the shell's history or the process table. Run "nitpick providers" for
the provider names.
`)
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	rest := fs.Args()
	if len(rest) == 0 {
		fs.Usage()
		return flag.ErrHelp
	}

	switch cmd := rest[0]; cmd {
	case "set":
		provider, err := authProvider(rest)
		if err != nil {
			return err
		}
		key, err := readCredential(stdin)
		if err != nil {
			return err
		}
		if err := llm.StoreCredential(provider, key); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "stored the %s credential in the keystore\n", provider)
		return nil

	case "delete":
		provider, err := authProvider(rest)
		if err != nil {
			return err
		}
		if err := llm.DeleteCredential(provider); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "removed the %s credential from the keystore\n", provider)
		return nil

	case "list":
		var stored []string
		for _, p := range llms.RegisteredProviders() {
			if llm.HasCredential(p) {
				stored = append(stored, p)
			}
		}
		if len(stored) == 0 {
			_, _ = fmt.Fprintln(out, "no credentials are stored in the keystore")
			return nil
		}
		_, _ = fmt.Fprintln(out, "Providers with a credential in the keystore:")
		for _, p := range stored {
			_, _ = fmt.Fprintf(out, "  %s\n", p)
		}
		return nil

	default:
		fs.Usage()
		return fmt.Errorf("unknown auth command %q", cmd)
	}
}

// authProvider takes the provider name and checks it against the registry, so
// a typo is caught here rather than becoming a stored secret nothing reads.
func authProvider(args []string) (string, error) {
	if len(args) < 2 || strings.TrimSpace(args[1]) == "" {
		return "", errors.New("a provider is required; run \"nitpick providers\" for the list")
	}
	provider := strings.TrimSpace(args[1])

	for _, p := range llms.RegisteredProviders() {
		if strings.EqualFold(p, provider) {
			return p, nil
		}
	}
	return "", fmt.Errorf("unknown provider %q; run \"nitpick providers\" for the list", provider)
}

// readCredential reads one line from standard input.
//
// One line, because a credential is one line and a paste that brought a
// trailing newline should not become a token that fails as a puzzling 401.
func readCredential(stdin io.Reader) (string, error) {
	if stdin == nil {
		stdin = os.Stdin
	}
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read the credential: %w", err)
	}
	key := strings.TrimSpace(line)
	if key == "" {
		return "", errors.New("no credential was given on standard input")
	}
	return key, nil
}
