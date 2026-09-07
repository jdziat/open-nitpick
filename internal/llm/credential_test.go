package llm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

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
//
// The values go in files the script reads rather than into the script's own
// text. Embedded, a value containing a quote, a dollar or a backtick would
// break the script or be expanded by it, and the next caller would debug a
// shell rather than the code under test.
func printer(t *testing.T, stdout, stderr string, exit int) []string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the script this test writes is a shell script")
	}
	dir := t.TempDir()
	outFile := filepath.Join(dir, "out")
	errFile := filepath.Join(dir, "err")
	for path, content := range map[string]string{outFile: stdout, errFile: stderr} {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	script := filepath.Join(dir, "cred.sh")
	body := "#!/bin/sh\ncat " + outFile + "\ncat " + errFile + " >&2\nexit " + strconv.Itoa(exit) + "\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return []string{script}
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

	// A keystore that ANSWERS, so this proves the empty variable is reported
	// rather than that the keystore happened to fail too. With the stub above
	// still erroring, the assertion would pass for the wrong reason.
	stubKeyring(t, map[string]string{KeyringService + "/openai": "from_the_default_lookup"}, nil)

	if _, _, err := resolve(t, config.ModelSpec{Provider: "openai", APIKeyEnv: "NOT_SET"}, nil); err == nil {
		t.Error("an empty named environment variable was ignored in favour of the default lookup")
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
		CredentialCommand: printer(t, "op_secret\n", "", 0),
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
	if runtime.GOOS == "windows" {
		// echo is a cmd.exe builtin with no echo.exe on PATH, so exec fails
		// and the test would report the failure rather than the behaviour.
		t.Skip("this test runs echo, which is not a program on Windows")
	}
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

// What bounding a credential command's output actually guarantees, in the two
// shapes a flood comes in.
//
// Not that the first line is recovered from a killed command: a command that
// fails yields an error and no key, deliberately, because a partial read of a
// secret is not a secret. The guarantees are that a command which floods and
// exits has its output capped, and that one which never stops returns instead
// of hanging or growing the heap until the timeout.
func TestACredentialCommandThatFloodsIsBounded(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the scripts this test writes are shell scripts")
	}
	dir := t.TempDir()

	write := func(name, body string) []string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
		return []string{path}
	}

	t.Run("floods and exits: the output is capped", func(t *testing.T) {
		argv := write("burst.sh", "#!/bin/sh\nhead -c 400000 /dev/zero | tr '\\0' 'a'\n")

		key, ok, err := resolveCredential(context.Background(),
			config.ModelSpec{Provider: "synthetic", CredentialCommand: argv}, nil)
		if err != nil || !ok {
			t.Fatalf("ok = %v, err = %v", ok, err)
		}
		if len(key) > maxCredentialBytes {
			t.Errorf("kept %d bytes, want at most %d", len(key), maxCredentialBytes)
		}
		if len(key) == 0 {
			t.Error("the cap discarded everything")
		}
	})

	t.Run("never stops: the call returns anyway", func(t *testing.T) {
		argv := write("flood.sh", "#!/bin/sh\nprintf 'thekey\\n'\nexec yes padding\n")

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		type result struct {
			key string
			err error
		}
		// On a channel rather than through captured variables, so the timeout
		// path cannot read what the goroutine is still writing, and nothing
		// touches t once this function has returned.
		done := make(chan result, 1)
		go func() {
			key, _, err := resolveCredential(ctx,
				config.ModelSpec{Provider: "synthetic", CredentialCommand: argv}, nil)
			done <- result{key, err}
		}()

		var got result
		select {
		case got = <-done:
		case <-time.After(30 * time.Second):
			t.Fatal("a command that never stops was waited on rather than bounded")
		}

		if got.err == nil {
			t.Fatalf("a killed command reported success, key = %q", got.key)
		}
		if got.key != "" {
			t.Errorf("key = %q; a failed command must yield nothing", got.key)
		}
	})
}

// A named keystore entry is bounded like the default one. A locked keyring
// waits on an unlock prompt with no timeout of its own, and a review that
// hangs before its first request is the same outcome either way.
func TestANamedKeystoreEntryIsBoundedToo(t *testing.T) {
	prev := keyringGet
	release := make(chan struct{})

	// The lookup gives up while the stub is still inside the call, so the
	// package variable must not be restored until that call has returned.
	// Restoring it while the leaked goroutine is still reading it is a write
	// racing a read, which -race fails on.
	// The stub is called once and closes exited on its way out, so cleanup can
	// wait for that one call to return before restoring the package variable.
	// A WaitGroup does not work here: its Add would run inside the goroutine,
	// after Wait may already have been reached.
	exited := make(chan struct{})
	t.Cleanup(func() {
		close(release)
		select {
		case <-exited:
		case <-time.After(10 * time.Second):
			t.Error("the keystore stub never returned")
		}
		keyringGet = prev
	})

	keyringGet = func(string, string) (string, error) {
		defer close(exited)
		<-release // never answers within the test
		return "", nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, _, err := resolveCredential(ctx,
			config.ModelSpec{Provider: "synthetic", APIKeyKeyring: "vault/synthetic"}, nil)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a keystore that never answered was read as a success")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("a named keystore lookup was waited on rather than bounded")
	}
}
