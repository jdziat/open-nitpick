package llm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/zalando/go-keyring"
)

// Where a model credential comes from, and in which order.
//
// A key in an environment variable is a key in the process table, in every
// child process, and in whatever CI writes its environment to. The operating
// system already has somewhere better, so the keystore is consulted without
// being asked: with nothing configured at all, a key stored under the default
// name below is found and used. That is the point of it. A resolution that
// needed a config key to switch on would leave the environment as the default
// path forever.
//
// Order is explicit sources before implicit ones:
//
//  1. credential_command, a program whose output is the key
//  2. api_key_keyring, a named secret in the operating system's keystore
//  3. api_key_env, a named environment variable
//  4. the keystore under the default name, service "open-nitpick", account
//     equal to the provider
//  5. nothing, leaving the SDK to read its own conventional variable
//
// The first three are what the operator wrote down for this model, so they
// come first and a failure in any of them is reported rather than skipped
// past. The fourth is a convenience, so a miss falls through in silence.

// KeyringService is the keystore service every default lookup uses.
const KeyringService = "open-nitpick"

// credentialTimeout bounds credential_command. A secret manager that prompts
// for a fingerprint takes seconds; one that hangs would otherwise hang the
// review before it had read a line of the diff.
const credentialTimeout = 2 * time.Minute

// keyringGet is the keystore read, replaced in tests.
var keyringGet = keyring.Get

// resolveCredential returns the credential for a model spec.
//
// ok is false when nothing supplied one and the SDK should resolve its own.
// An error means a source the operator NAMED could not be read, which is
// reported rather than fallen through: an operator who wrote down where their
// key lives has said they do not want the environment consulted instead.
func resolveCredential(ctx context.Context, spec config.ModelSpec, getenv func(string) string) (string, bool, error) {
	if getenv == nil {
		getenv = os.Getenv
	}

	if len(spec.CredentialCommand) > 0 {
		if strings.TrimSpace(spec.CredentialCommand[0]) == "" {
			return "", false, errors.New("credential_command names no program")
		}
		key, err := runCredentialCommand(ctx, spec.CredentialCommand)
		if err != nil {
			return "", false, err
		}
		return key, true, nil
	}

	if spec.APIKeyKeyring != "" {
		// The same rule the validator applies, restated rather than assumed.
		// A spec that reached here without going through Validate would
		// otherwise be looked up under an account the operator did not write.
		service, account, ok := strings.Cut(spec.APIKeyKeyring, "/")
		if !ok || strings.TrimSpace(service) == "" || strings.TrimSpace(account) == "" ||
			strings.Contains(account, "/") {
			return "", false, fmt.Errorf("api_key_keyring %q is not \"service/account\"", spec.APIKeyKeyring)
		}
		key, err := keyringGet(service, account)
		if err != nil {
			return "", false, fmt.Errorf("read %s from the keystore: %w", spec.APIKeyKeyring, err)
		}
		if strings.TrimSpace(key) == "" {
			return "", false, fmt.Errorf("the keystore entry %s is empty", spec.APIKeyKeyring)
		}
		return strings.TrimSpace(key), true, nil
	}

	if spec.APIKeyEnv != "" {
		if key := strings.TrimSpace(getenv(spec.APIKeyEnv)); key != "" {
			return key, true, nil
		}
		return "", false, fmt.Errorf("%s is empty", spec.APIKeyEnv)
	}

	// The default lookup. Every failure here is a miss, including a Linux
	// session with no D-Bus and a machine with no keystore at all, because
	// nobody asked for this one and the environment is still to be tried.
	if provider := strings.TrimSpace(spec.Provider); provider != "" {
		if key, err := keyringGet(KeyringService, provider); err == nil {
			if key = strings.TrimSpace(key); key != "" {
				return key, true, nil
			}
		}
	}

	// Nothing here supplied one, which is not a failure: the SDK still has its
	// own conventional variable to read, and that is the path every
	// configuration took before this file existed.
	return "", false, nil
}

// runCredentialCommand runs the command and takes its output as the key.
//
// No shell. The command is argv, so nothing in a config file is interpreted as
// a program by a shell that expands it. Standard error is captured for the
// error message and never for the key: a secret manager that prints a prompt
// or a warning there would otherwise become part of the credential.
func runCredentialCommand(ctx context.Context, argv []string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, credentialTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var stdout, stderr strings.Builder
	// A credential is one line. Bounding both pipes means a command that
	// streams, whether by fault or because it was pointed at the wrong thing,
	// is cut off rather than buffered into the heap until the timeout fires.
	cmd.Stdout = &limitedWriter{w: &stdout, left: maxCredentialBytes}
	cmd.Stderr = &limitedWriter{w: &stderr, left: maxCredentialBytes}

	err := cmd.Run()
	out := []byte(stdout.String())
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("credential_command %s: %w: %s", argv[0], err, oneLine(msg))
		}
		return "", fmt.Errorf("credential_command %s: %w", argv[0], err)
	}

	// The trailing newline every well-behaved command prints is not part of
	// the secret, and a bearer token with one in it fails as a puzzling 401.
	key := strings.TrimSpace(string(out))
	if key == "" {
		return "", fmt.Errorf("credential_command %s printed nothing", argv[0])
	}
	return key, nil
}

// maxCredentialBytes bounds what is kept from a credential command's output.
// A bearer token is a few hundred bytes; this leaves room for a long one and
// for a multi-line error on the other pipe.
const maxCredentialBytes = 64 << 10

// limitedWriter keeps the first left bytes and discards the rest, reporting
// success either way so the command is not killed by a broken pipe for
// printing more than was wanted.
type limitedWriter struct {
	w    *strings.Builder
	left int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.left > 0 {
		keep := p
		if len(keep) > l.left {
			keep = keep[:l.left]
		}
		if _, err := l.w.Write(keep); err != nil {
			return 0, err
		}
		l.left -= len(keep)
	}
	return len(p), nil
}

// oneLine flattens a message for a single-line error.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// keyringSet and keyringDelete are the keystore writes, replaced in tests.
var (
	keyringSet    = keyring.Set
	keyringDelete = keyring.Delete
)

// StoreCredential puts a provider's key in the operating system's keystore
// under the default name, where resolveCredential looks with nothing
// configured.
//
// It exists so that using the keystore needs no second tool. Telling someone
// their key belongs in a keystore and then handing them a `security
// find-generic-password` incantation for one platform is the manual wiring
// this was meant to remove.
func StoreCredential(provider, key string) error {
	provider, key = strings.TrimSpace(provider), strings.TrimSpace(key)
	if provider == "" {
		return errors.New("a provider is required")
	}
	if key == "" {
		return errors.New("the credential is empty")
	}
	if err := keyringSet(KeyringService, provider, key); err != nil {
		return fmt.Errorf("store the %s credential: %w", provider, err)
	}
	return nil
}

// DeleteCredential removes a provider's stored key. Removing one that is not
// there is reported rather than swallowed, since the reason to run this is to
// be sure a key is gone.
func DeleteCredential(provider string) error {
	if err := keyringDelete(KeyringService, strings.TrimSpace(provider)); err != nil {
		return fmt.Errorf("remove the %s credential: %w", provider, err)
	}
	return nil
}

// HasCredential reports whether a provider has a key stored under the default
// name. It never returns the key: the question a person asks of a list is
// which providers are configured, and answering it with the secret puts that
// secret on a terminal and in a scrollback buffer.
func HasCredential(provider string) bool {
	key, err := keyringGet(KeyringService, strings.TrimSpace(provider))
	return err == nil && strings.TrimSpace(key) != ""
}
