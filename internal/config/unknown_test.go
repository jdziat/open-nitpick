package config

import (
	"bytes"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The default did not move: a key this build does not have fails the run.
//
// TestUnknownFieldIsRejected covers the same ground from the other side. This
// one names the message, because the message is the part of the default that
// changed: it says which key, which line and what to do, where yaml's own said
// "field max_fils not found in type config.Review".
func TestAnUnknownKeyStillFailsAndTheMessageSaysWhatToDo(t *testing.T) {
	root := writeConfig(t, "review:\n  max_fils: 10\n")

	Version = "v1.8.0"
	t.Cleanup(func() { Version = "" })

	_, err := Load(root)
	if err == nil {
		t.Fatal("an unknown key loaded; the default must stay fatal")
	}
	for _, want := range []string{"max_fils", "line 2", "v1.8.0", EnvIgnoreUnknownKeys} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the message never says %q:\n%s", want, err)
		}
	}
	if strings.Contains(err.Error(), "config.Review") {
		t.Errorf("the message names a Go type, which is not the reader's vocabulary:\n%s", err)
	}
}

// With the opt-in set, the key is ignored, recorded, and the rest of the file
// still applies.
//
// The last part is what makes this an ignored key rather than an abandoned
// file: yaml.v3 records the unknown field and carries on, so everything the
// document did set is already in the config by the time the error is read.
func TestAnIgnoredKeyLeavesTheRestOfTheFileInForce(t *testing.T) {
	root := writeConfig(t, "models:\n  default: {provider: openai, model: gpt-4o}\n"+
		"review:\n  concurrency: 7\n  max_fils: 10\n")
	t.Setenv(EnvIgnoreUnknownKeys, "1")

	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Unknown) != 1 || !strings.Contains(cfg.Unknown[0], "max_fils") {
		t.Errorf("Unknown = %v, want the one key that was ignored", cfg.Unknown)
	}
	if !strings.Contains(cfg.Unknown[0], "line 5") {
		t.Errorf("Unknown = %v, want the line the key is on", cfg.Unknown)
	}
	if cfg.Review.Concurrency != 7 {
		t.Errorf("concurrency = %d, want 7: the keys the file did set must still apply",
			cfg.Review.Concurrency)
	}
}

// Anything that is not purely unknown keys stays fatal, opt-in or not.
//
// Such a decode applied some of the document and skipped some, and nothing
// here knows which. Continuing would review under a config this tool cannot
// describe, which is worse than the outage the opt-in exists to prevent.
func TestTheOptInDoesNotSwallowATypeError(t *testing.T) {
	// A valid models block in every case, so the only thing that can fail the
	// load is the decode. Without it these fixtures fail validation instead and
	// the test passes whatever the classification does.
	const models = "models:\n  default: {provider: openai, model: gpt-4o}\n"

	for name, body := range map[string]string{
		"a type error alone":               "review:\n  concurrency: \"lots\"\n",
		"a type error beside unknown keys": "review:\n  concurrency: \"lots\"\n  max_fils: 10\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := writeConfig(t, models+body)
			t.Setenv(EnvIgnoreUnknownKeys, "1")

			if _, err := Load(root); err == nil {
				t.Fatal("loaded; a decode this tool cannot describe must not be ignored")
			}
		})
	}

	// And the control: the same file without the type error does load, so the
	// case above is the decode failing rather than the fixture.
	t.Run("the unknown key alone is ignored", func(t *testing.T) {
		root := writeConfig(t, models+"review:\n  max_fils: 10\n")
		t.Setenv(EnvIgnoreUnknownKeys, "1")

		if _, err := Load(root); err != nil {
			t.Fatalf("Load: %v", err)
		}
	})
}

// The opt-in is read from the environment and nowhere else.
//
// A config file that could switch off the check on its own keys is the one
// thing this must not be, which is why it is not a config key.
func TestTheOptInIsNotAConfigKey(t *testing.T) {
	root := writeConfig(t, "ignore_unknown_keys: true\nreview:\n  max_fils: 10\n")

	if _, err := Load(root); err == nil {
		t.Fatal("a config file granted itself the opt-in")
	}
}

// The message this build reads is still the message yaml.v3 writes.
//
// Everything above turns on parsing gopkg.in/yaml.v3's KnownFields text
// (decode.go:944). Being wrong about it is silent in both directions: a
// wording this stops recognising turns every ignored key fatal again, and one
// it recognises too broadly hides a type error as a key nobody has. A
// dependency bump that changes it fails here.
func TestTheUnknownFieldMessageIsStillYAMLsOwn(t *testing.T) {
	var into struct {
		Known int `yaml:"known"`
	}
	dec := yaml.NewDecoder(bytes.NewReader([]byte("known: 1\nnot_a_field: 2\n")))
	dec.KnownFields(true)
	err := dec.Decode(&into)

	keys, only := unknownFields(err)
	if !only {
		t.Fatalf("yaml.v3 no longer reports an unknown field the way this package reads it: %v", err)
	}
	if len(keys) != 1 || keys[0] != "not_a_field (line 2)" {
		t.Errorf("keys = %v, want [\"not_a_field (line 2)\"]", keys)
	}
	if into.Known != 1 {
		t.Error("yaml.v3 no longer applies the known fields, so ignoring a key would drop the file")
	}
}
