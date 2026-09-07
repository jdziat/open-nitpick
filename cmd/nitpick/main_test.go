package main

import (
	"os"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
)

// TestMain keeps this package's tests off the machine's own user-level
// configuration.
//
// Without it, a developer with a ~/.config/nitpick/config.yaml runs a
// different suite from everyone else: the round trip in init_test compares
// against built-in defaults, and a user-level file is by design not one.
func TestMain(m *testing.M) {
	_ = os.Setenv(config.EnvNoUserConfig, "1")
	os.Exit(m.Run())
}
