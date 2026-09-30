package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestMissingModelRetainsValidatedDefaults(t *testing.T) {
	t.Setenv(EnvProvider, "")
	t.Setenv(EnvModel, "")
	t.Setenv(EnvNoUserConfig, "true")
	cfg, err := LoadFile(filepath.Join(t.TempDir(), FileName))
	var unresolved *UnresolvedModelError
	if !errors.As(err, &unresolved) || cfg != unresolved.Config {
		t.Fatalf("missing model recovery: cfg=%v err=%v", cfg, err)
	}
	if cfg.Review.Concurrency != Defaults().Review.Concurrency || cfg.Review.MaxFileBytes == 0 || cfg.Missing == "" {
		t.Fatal("model recovery discarded defaults or config provenance")
	}
}

func TestInvalidConfigIsNotRecoveredAsMissingModel(t *testing.T) {
	t.Setenv(EnvNoUserConfig, "true")
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte("models: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFile(path)
	var unresolved *UnresolvedModelError
	if cfg != nil || err == nil || errors.As(err, &unresolved) {
		t.Fatalf("invalid config recovery: cfg=%v err=%v", cfg, err)
	}
}

func TestInvalidUserConfigIsNotRecoveredAsMissingModel(t *testing.T) {
	dir := t.TempDir()
	user := filepath.Join(dir, "user.yaml")
	if err := os.WriteFile(user, []byte("review:\n  concurrency: 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvUserConfig, user)
	path := filepath.Join(dir, FileName)
	cfg, err := LoadFile(path)
	var unresolved *UnresolvedModelError
	if cfg != nil || err == nil || errors.As(err, &unresolved) {
		t.Fatalf("invalid user config recovery: cfg=%v err=%v", cfg, err)
	}
}

func TestPresentButModellessConfigIsRecoverable(t *testing.T) {
	t.Setenv(EnvNoUserConfig, "true")
	path := filepath.Join(t.TempDir(), FileName)
	// A repository config that names no model: valid apart from the missing
	// model, so fast-review's own fallback still applies.
	if err := os.WriteFile(path, []byte("review:\n  concurrency: 3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFile(path)
	var unresolved *UnresolvedModelError
	if !errors.As(err, &unresolved) || cfg != unresolved.Config {
		t.Fatalf("present modelless config: cfg=%v err=%v", cfg, err)
	}
	if cfg.Review.Concurrency != 3 {
		t.Fatalf("recovery dropped the file's settings: concurrency=%d", cfg.Review.Concurrency)
	}
}
