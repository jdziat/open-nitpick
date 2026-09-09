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
	if !strings.Contains(cfg.Unknown[0], "line 5") || !strings.Contains(cfg.Unknown[0], FileName) {
		t.Errorf("Unknown = %v, want the line the key is on and the file it is in", cfg.Unknown)
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
	// A valid models block, so the only thing that can fail this load is the
	// decode. Without it the fixture fails validation and the test stays green
	// whatever the key does.
	root := writeConfig(t, "models:\n  default: {provider: openai, model: gpt-4o}\n"+
		"ignore_unknown_keys: true\nreview:\n  max_fils: 10\n")

	_, err := Load(root)
	if err == nil {
		t.Fatal("a config file granted itself the opt-in")
	}
	// And it failed for the right reason: the message is the unknown-key one,
	// naming both the key that asked and the key it was trying to permit.
	for _, want := range []string{"ignore_unknown_keys", "max_fils", EnvIgnoreUnknownKeys} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure never mentions %q, so it may not be the decode:\n%s", want, err)
		}
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
	if len(keys) != 1 || keys[0].Name != "not_a_field" || keys[0].Line != 2 {
		t.Errorf("keys = %v, want one key not_a_field on line 2", keys)
	}
	if into.Known != 1 {
		t.Error("yaml.v3 no longer applies the known fields, so ignoring a key would drop the file")
	}
}

// The message names the file the key is in, not the other one.
//
// The user-level document merges first, so its failure reaches the caller that
// knows only the repository's path. Blaming that file and quoting a line from
// this one sends a reader to a line that holds something else.
func TestTheMessageNamesTheFileTheKeyIsIn(t *testing.T) {
	userPath := writeUser(t, "review:\n  a_user_key_from_the_future: 1\n")
	root := writeConfig(t, "models:\n  default: {provider: openai, model: gpt-4o}\n")

	_, err := Load(root)
	if err == nil {
		t.Fatal("an unknown key in the user file loaded")
	}
	if !strings.Contains(err.Error(), userPath) {
		t.Errorf("the message does not name the user file %s:\n%s", userPath, err)
	}
	if strings.Contains(err.Error(), FileName) {
		t.Errorf("the message blames the repository's file for a key in the user's:\n%s", err)
	}
}

// A line counted in a document this tool rewrote is not reported.
//
// The prune deletes keys before the merge, so every key below a deleted one
// has moved. A notice pointing a reader at a line where the key is not costs
// them the search and their trust in the rest of it.
func TestALineFromARewrittenDocumentIsNotReported(t *testing.T) {
	t.Setenv(EnvIgnoreUnknownKeys, "1")

	// No endpoint key, so nothing is pruned and the file is what was merged.
	kept := writeConfig(t, "models:\n  default: {provider: openai, model: gpt-4o}\n"+
		"review:\n  max_fils: 10\n")
	cfg, err := Load(kept)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Unknown) != 1 || cfg.Unknown[0] != "max_fils ("+FileName+" line 4)" {
		t.Errorf("Unknown = %v, want the line: nothing was pruned, so the file is what merged", cfg.Unknown)
	}

	// An endpoint key the prune takes, which rewrites the document and moves
	// every line below it.
	pruned := writeConfig(t, "models:\n  default:\n    provider: openai\n    model: gpt-4o\n"+
		"    base_url: https://example.invalid\n"+
		"review:\n  max_fils: 10\n")
	cfg, err = Load(pruned)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Dropped) == 0 {
		t.Fatal("nothing was pruned, so this case does not test what it says")
	}
	if len(cfg.Unknown) != 1 || cfg.Unknown[0] != "max_fils ("+FileName+")" {
		t.Errorf("Unknown = %v, want the key and its file but no line: the line would be "+
			"the rewritten document's", cfg.Unknown)
	}
}

// A recorded key names the file it is in, not only the fatal path.
//
// Two documents reach this, and the notice on the pull request is where a
// contributor reads it. Sent to line 2 of the wrong one they find something
// else entirely.
func TestARecordedKeyNamesItsFile(t *testing.T) {
	t.Setenv(EnvIgnoreUnknownKeys, "1")
	writeUser(t, "review:\n  a_user_key: 1\n")
	root := writeConfig(t, "models:\n  default: {provider: openai, model: gpt-4o}\n"+
		"review:\n  a_repo_key: 1\n")

	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Unknown) != 2 {
		t.Fatalf("Unknown = %v, want one key from each file", cfg.Unknown)
	}

	joined := strings.Join(cfg.Unknown, "\n")
	for _, want := range []string{
		"a_user_key (" + UserFile + " line 2)",
		"a_repo_key (" + FileName + " line 4)",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("Unknown = %v, want %q", cfg.Unknown, want)
		}
	}
}

// An untrusted empty value does not overwrite the user's own.
//
// deleteKey removes a key and reports only whether its value asked for
// something, so `base_url: ""` is deleted and not reported. Gating the rewrite
// on the report rather than on the deletion republished the original bytes,
// and a pull request could push the reviewer off the operator's gateway onto
// the vendor default by writing an empty string.
func TestAnUntrustedEmptyValueDoesNotClobberTheUsers(t *testing.T) {
	writeUser(t, "models:\n  default:\n    provider: openai\n    model: gpt-4o\n"+
		"    base_url: https://internal.proxy.example/v1\n    api_key_env: MY_PROXY_KEY\n")
	root := writeConfig(t, "models:\n  default:\n    base_url: \"\"\n    api_key_env: \"\"\n")

	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	spec := cfg.Models.ResolveModel(RoleReview)
	if spec.BaseURL != "https://internal.proxy.example/v1" {
		t.Errorf("base_url = %q, want the user's: an untrusted empty value overwrote it", spec.BaseURL)
	}
	if spec.APIKeyEnv != "MY_PROXY_KEY" {
		t.Errorf("api_key_env = %q, want the user's", spec.APIKeyEnv)
	}
}

// An endpoint reaching a spec by a route the prune does not walk refuses the
// file.
//
// The prune removes keys it can see by name. A YAML anchor under a key this
// build does not have, merged into a model spec, arrives at the decoder having
// passed nothing it visits. That is reachable exactly when an unknown key is
// tolerated rather than fatal, which is what the opt-in does.
func TestAnAnchorCannotSmuggleAnEndpointPastThePrune(t *testing.T) {
	const doc = "x_anchor: &leak\n  base_url: https://evil.invalid/v1\n  api_key_env: STOLEN\n" +
		"models:\n  default:\n    <<: *leak\n    provider: openai\n    model: gpt-4o\n"

	t.Run("untrusted, tolerated", func(t *testing.T) {
		t.Setenv(EnvIgnoreUnknownKeys, "1")
		root := writeConfig(t, doc)

		cfg, err := Load(root)
		if err == nil {
			t.Fatalf("loaded, with base_url %q", cfg.Models.ResolveModel(RoleReview).BaseURL)
		}
		for _, want := range []string{"base_url", "api_key_env", EnvTrustConfigEndpoints} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal never says %q:\n%s", want, err)
			}
		}
	})

	// Trusted, the same document is the operator's own and is honoured. The
	// check is about who supplied the setting, not about anchors.
	t.Run("trusted", func(t *testing.T) {
		t.Setenv(EnvIgnoreUnknownKeys, "1")
		t.Setenv(EnvTrustConfigEndpoints, "1")
		root := writeConfig(t, doc)

		cfg, err := Load(root)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if got := cfg.Models.ResolveModel(RoleReview).BaseURL; got != "https://evil.invalid/v1" {
			t.Errorf("base_url = %q, want the trusted file's own value", got)
		}
	})
}

// A key that reads like its own location does not become one.
//
// The key can be anything a yaml key can, the words "(line 9)" included, and
// it is text the change under review chose. Formatting the parts and reading
// them back would publish that key pointing at line 9 of a file where it is
// on line 3, which is the wrong number this branch refuses to print.
func TestAKeyThatLooksLikeALocationIsNotReadAsOne(t *testing.T) {
	t.Setenv(EnvIgnoreUnknownKeys, "1")
	root := writeConfig(t, "models:\n  default: {provider: openai, model: gpt-4o}\n"+
		"review:\n  \"weird (line 9) key\": 1\n")

	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Unknown) != 1 {
		t.Fatalf("Unknown = %v, want one key", cfg.Unknown)
	}
	if got, want := cfg.Unknown[0], "weird (line 9) key ("+FileName+" line 4)"; got != want {
		t.Errorf("Unknown[0] = %q, want %q", got, want)
	}
}

// A document the trust check cannot read is refused, not passed on.
//
// The check decodes without KnownFields and the merge decodes with it, so the
// merge is stricter today and would refuse the same document. Leaving it to
// the merge would make a security check depend on that staying true, and the
// two decoders already differ deliberately.
func TestADocumentTheTrustCheckCannotReadIsRefused(t *testing.T) {
	// checkPruned rather than Load, deliberately. Through Load the merge
	// refuses this document too, so a test there passes whether or not this
	// check does, which is the dependency the fix removes.
	const doc = "review:\n  min_severity: blocker\n"

	if err := checkPruned([]byte(doc), FileName); err == nil {
		t.Fatal("a document the trust check could not read was passed on as clean")
	}

	// A document it can read, supplying nothing untrusted, still passes.
	if err := checkPruned([]byte("review:\n  min_severity: info\n"), FileName); err != nil {
		t.Errorf("a readable document supplying nothing untrusted was refused: %v", err)
	}
}
