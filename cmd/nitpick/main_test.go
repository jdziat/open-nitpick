package main

import (
	"os"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/zalando/go-keyring"
)

// TestMain keeps this package's tests off the machine's own user-level
// configuration.
//
// Without it, a developer with a ~/.config/nitpick/config.yaml runs a
// different suite from everyone else: the round trip in init_test compares
// against built-in defaults, and a user-level file is by design not one.
func TestMain(m *testing.M) {
	_ = os.Setenv(config.EnvNoUserConfig, "1")

	// keyring.MockInit swaps a process-global provider and has no restore, so
	// it is done once here rather than by whichever test reaches it first.
	// Called per test it would leave the mock installed for everything after
	// it and leave everything before it talking to the developer's real
	// keystore.
	keyring.MockInit()

	os.Exit(m.Run())
}
