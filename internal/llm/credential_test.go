package llm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
)

// stubKeyring replaces the keystore for the duration of a test.
func stubKeyring(t *testing.T, entries map[string]string, err error) {
	t.Helper()
	prev := keyringGet
	t.Cleanup(func() { keyringGet = prev })
	keyringGet = func(service, account string) (string, error) {
		if err != nil {
			return "", err
		}
		v, ok := entries[service+"/"+account]
		if !ok {
			return "", errors.New("secret not found in keyring")
		}
		return v, nil
	}
}

// printer writes a script that prints what it is told, for credential_command.
func printer(t *testing.T, stdout, stderr string, exit int) []string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the script this test writes is a shell script")
	}
	path := filepath.Join(t.TempDir(), "cred.sh")
	body := "#!/bin/sh\nprintf '%s\\n' \"" + stdout + "\"\nprintf '%s' \"" + stderr + "\" >&2\nexit " +
		strings.TrimSpace(itoa(exit)) + "\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return []string{path}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	return string(rune('0' + n))
}

func resolve(t *testing.T, spec config.ModelSpec, env map[string]string) (string, bool, error) {
	t.Helper()
	return resolveCredential(context.Background(), spec, func(k string) string { return env[k] })
}

// With nothing configured, a key stored under the default name is found. This
// is the case the feature exists for: no config key switches it on.
func TestTheKeystoreIsConsultedWithNothingConfigured(t *testing.T) {
	stubKeyring(t, map[string]string{KeyringService + "/synthetic": "syn_from_keystore"}, nil)

	key, ok, err := resolve(t, config.ModelSpec{Provider: "synthetic"}, nil)
	if err != nil || !ok {
		t.Fatalf("ok = %v, err = %v", ok, err)
	}
	if key != "syn_from_keystore" {
		t.Errorf("key = %q", key)
	}
}

// A machine with no keystore, or a Linux session with no D-Bus, must not fail
// a review that was going to read the environment anyway.
func TestADefaultKeystoreMissFallsThroughSilently(t *testing.T) {
	stubKeyring(t, nil, errors.New("The name org.freedesktop.secrets was not provided by any .service files"))

	key, ok, err := resolve(t, config.ModelSpec{Provider: "synthetic"}, nil)
	if err != nil {
		t.Fatalf("an unavailable keystore failed the build: %v", err)
	}
	if ok || key != "" {
		t.Errorf("key = %q, ok = %v", key, ok)
	}
}

// A source the operator NAMED is different: a failure there is reported, not
// fallen past. Someone who wrote down where their key lives has said they do
// not want the environment used instead.
func TestANamedSourceThatFailsIsReported(t *testing.T) {
	stubKeyring(t, nil, errors.New("keyring locked"))

	_, _, err := resolve(t, config.ModelSpec{Provider: "synthetic", APIKeyKeyring: "vault/synthetic"}, nil)
	if err == nil {
		t.Fatal("a named keystore entry that could not be read was ignored")
	}
	if !strings.Contains(err.Error(), "vault/synthetic") {
		t.Errorf("the error does not name the entry: %v", err)
	}

	if _, _, err := resolve(t, config.ModelSpec{Provider: "openai", APIKeyEnv: "NOT_SET"}, nil); err == nil {
		t.Error("an empty named environment variable was ignored")
	}
}

func TestExplicitSourcesBeatTheDefaultLookup(t *testing.T) {
	stubKeyring(t, map[string]string{
		KeyringService + "/synthetic": "the_default",
		"vault/synthetic":             "the_named_entry",
	}, nil)

	spec := config.ModelSpec{Provider: "synthetic", APIKeyKeyring: "vault/synthetic"}
	if key, _, _ := resolve(t, spec, nil); key != "the_named_entry" {
		t.Errorf("key = %q, want the named entry to win", key)
	}

	spec = config.ModelSpec{Provider: "synthetic", APIKeyEnv: "MY_KEY"}
	if key, _, _ := resolve(t, spec, map[string]string{"MY_KEY": "from_env"}); key != "from_env" {
		t.Errorf("key = %q, want a named variable to beat the default lookup", key)
	}
}

func TestCredentialCommandOutputIsTheKeyAndBeatsEverything(t *testing.T) {
	stubKeyring(t, map[string]string{KeyringService + "/synthetic": "the_default"}, nil)

	spec := config.ModelSpec{
		Provider:          "synthetic",
		APIKeyEnv:         "MY_KEY",
		CredentialCommand: printer(t, "op_secret", "", 0),
	}
	key, ok, err := resolve(t, spec, map[string]string{"MY_KEY": "from_env"})
	if err != nil || !ok {
		t.Fatalf("ok = %v, err = %v", ok, err)
	}

	// The trailing newline a well-behaved command prints is not part of the
	// secret, and a bearer token carrying one fails as a puzzling 401.
	if key != "op_secret" {
		t.Errorf("key = %q, want the output trimmed", key)
	}
}

// Standard error is for the message, never for the key. A secret manager that
// prints a prompt there would otherwise put it in the bearer token.
func TestCredentialCommandFailureNamesWhatWentWrong(t *testing.T) {
	spec := config.ModelSpec{Provider: "synthetic", CredentialCommand: printer(t, "", "not signed in", 1)}

	_, _, err := resolve(t, spec, nil)
	if err == nil {
		t.Fatal("a failing credential_command was accepted")
	}
	if !strings.Contains(err.Error(), "not signed in") {
		t.Errorf("the error drops what the command said: %v", err)
	}
}

func TestACredentialCommandThatPrintsNothingIsAnError(t *testing.T) {
	spec := config.ModelSpec{Provider: "synthetic", CredentialCommand: printer(t, "", "", 0)}

	if _, _, err := resolve(t, spec, nil); err == nil {
		t.Fatal("a command that succeeded and printed nothing was read as a key")
	}
}

// The command is argv and is run without a shell, so nothing in a config file
// is interpreted as a program by an expansion.
func TestTheCredentialCommandIsNotRunThroughAShell(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "written-by-the-shell")
	spec := config.ModelSpec{
		Provider:          "synthetic",
		CredentialCommand: []string{"echo", "key; touch " + marker},
	}

	key, _, err := resolve(t, spec, nil)
	if err != nil {
		t.Fatal(err)
	}
	if key != "key; touch "+marker {
		t.Errorf("key = %q; the argument was split or expanded", key)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the argument was executed by a shell")
	}
}
