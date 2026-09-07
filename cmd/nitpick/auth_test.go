package main

import (
	"bytes"
	"errors"
	"flag"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/llm"
	llms "github.com/nocturnium/llm-go-sdk/v6"
	"github.com/zalando/go-keyring"
)

// clearKeystore empties the mock keystore TestMain installed, so each test
// starts from nothing rather than from what an earlier one stored.
func clearKeystore(t *testing.T) {
	t.Helper()
	for _, p := range llms.RegisteredProviders() {
		_ = keyring.Delete(llm.KeyringService, p)
	}
}

func TestAuthStoresACredentialWhereAReviewLooksForIt(t *testing.T) {
	clearKeystore(t)
	var out bytes.Buffer

	if err := runAuth([]string{"set", "synthetic"}, strings.NewReader("syn_abc123\n"), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "synthetic") {
		t.Errorf("output does not name the provider: %s", out.String())
	}

	// The point of the command: what it wrote is what resolution reads with
	// nothing configured.
	if !llm.HasCredential("synthetic") {
		t.Error("the stored credential is not where resolution looks for it")
	}
	if got, err := keyring.Get(llm.KeyringService, "synthetic"); err != nil || got != "syn_abc123" {
		t.Errorf("stored %q, err = %v", got, err)
	}
}

// The credential is read from standard input rather than an argument, so it
// does not reach the shell's history or the process table.
func TestAuthTakesTheCredentialFromStandardInputAndTrimsIt(t *testing.T) {
	clearKeystore(t)
	var out bytes.Buffer

	if err := runAuth([]string{"set", "openai"}, strings.NewReader("  sk-trailing  \n"), &out); err != nil {
		t.Fatal(err)
	}
	if got, _ := keyring.Get(llm.KeyringService, "openai"); got != "sk-trailing" {
		t.Errorf("stored %q; a pasted credential's whitespace must not become part of the token", got)
	}

	var empty bytes.Buffer
	if err := runAuth([]string{"set", "openai"}, strings.NewReader("\n"), &empty); err == nil {
		t.Error("an empty credential was stored")
	}
}

// A typo becomes a secret nothing ever reads, so it is refused here.
func TestAuthRefusesAProviderTheRegistryDoesNotKnow(t *testing.T) {
	clearKeystore(t)
	var out bytes.Buffer

	err := runAuth([]string{"set", "sinthetic"}, strings.NewReader("k\n"), &out)
	if err == nil {
		t.Fatal("a misspelled provider was accepted")
	}
	if !strings.Contains(err.Error(), "nitpick providers") {
		t.Errorf("the refusal does not say how to find the right name: %v", err)
	}
}

func TestAuthDeleteRemovesTheCredential(t *testing.T) {
	clearKeystore(t)
	var out bytes.Buffer

	if err := runAuth([]string{"set", "groq"}, strings.NewReader("gsk_1\n"), &out); err != nil {
		t.Fatal(err)
	}
	if err := runAuth([]string{"delete", "groq"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	if llm.HasCredential("groq") {
		t.Error("the credential survived deletion")
	}
}

// list answers which providers are configured and never what the secret is.
func TestAuthListNamesProvidersAndNotSecrets(t *testing.T) {
	clearKeystore(t)
	var out bytes.Buffer

	if err := runAuth([]string{"list"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no credentials") {
		t.Errorf("an empty keystore did not say so: %s", out.String())
	}

	out.Reset()
	if err := runAuth([]string{"set", "mistral"}, strings.NewReader("secret_value\n"), &out); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runAuth([]string{"list"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "mistral") {
		t.Errorf("list does not name a configured provider: %s", out.String())
	}
	if strings.Contains(out.String(), "secret_value") {
		t.Errorf("list printed the credential: %s", out.String())
	}
}

// Bare "nitpick auth" is a usage error, not a help request.
//
// main maps flag.ErrHelp to exit 0, so returning it here would let a wrapper
// running "nitpick auth" with no subcommand see success and no stored
// credential. Bare "nitpick" exits 64 for the same mistake.
func TestBareAuthIsAUsageErrorRatherThanHelp(t *testing.T) {
	var out bytes.Buffer
	err := runAuth(nil, nil, &out)
	if err == nil {
		t.Fatal("bare auth reported success")
	}
	if errors.Is(err, flag.ErrHelp) {
		t.Error("bare auth returned flag.ErrHelp, which main exits 0 on")
	}
	if !strings.Contains(err.Error(), "set") {
		t.Errorf("the error does not name the commands: %v", err)
	}
}
