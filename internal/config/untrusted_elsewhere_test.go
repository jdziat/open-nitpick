package config

import (
	"strings"
	"testing"
)

// A repository file cannot grant itself the analyzers that run its own code,
// nor name a file for this process to open.
//
// Both were left unpruned on the argument written at Linters.GolangciConfig,
// that a value there can only name a file the change cannot write. Neither
// names a file: linters.trusted is a privilege grant, and knowledge_index is a
// path opened with a bare ReadFile.
func TestARepositoryCannotSupplyTheKeysThatNameNoFile(t *testing.T) {
	writeUser(t, "models:\n  default: {provider: openai, model: gpt-4o}\n"+
		"linters:\n  trusted: [phpstan]\n"+
		"review:\n  knowledge_index: /home/operator/index.json\n")
	root := writeConfig(t, "linters:\n  trusted: [clippy]\n"+
		"review:\n  knowledge_index: /tmp/attacker.json\n")

	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// The user's own survive; only the repository's are dropped.
	if got := cfg.Linters.Trusted; len(got) != 1 || got[0] != "phpstan" {
		t.Errorf("linters.trusted = %v, want the user file's own", got)
	}
	if cfg.Review.KnowledgeIndex != "/home/operator/index.json" {
		t.Errorf("knowledge_index = %q, want the user file's own", cfg.Review.KnowledgeIndex)
	}

	joined := strings.Join(cfg.Dropped, ", ")
	for _, want := range []string{"linters.trusted", "review.knowledge_index"} {
		if !strings.Contains(joined, want) {
			t.Errorf("Dropped = %v, want %s named", cfg.Dropped, want)
		}
	}
}

// The route the prune's walk cannot see is refused, as it is for base_url.
//
// A YAML anchor declared under one key and merged into another reaches the
// decoder having passed nothing the prune visits. checkPruned asks the decoded
// config instead, and these keys are on the list it reads.
func TestAnAnchorCannotSmuggleTheKeysThatNameNoFile(t *testing.T) {
	for name, doc := range map[string]string{
		"linters.trusted": "x: &leak\n  trusted: [clippy]\nlinters:\n  <<: *leak\n",
		"knowledge_index": "x: &leak\n  knowledge_index: /tmp/attacker.json\nreview:\n  <<: *leak\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(EnvIgnoreUnknownKeys, "1")
			root := writeConfig(t, "models:\n  default: {provider: openai, model: gpt-4o}\n"+doc)

			if _, err := Load(root); err == nil {
				t.Fatal("an anchor put the key on the config anyway")
			}
		})
	}
}

// A trusted file keeps both, since the check is about who supplied a setting.
func TestATrustedFileMaySupplyThem(t *testing.T) {
	t.Setenv(EnvTrustConfigEndpoints, "1")
	root := writeConfig(t, "models:\n  default: {provider: openai, model: gpt-4o}\n"+
		"linters:\n  trusted: [clippy]\n")

	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Linters.Trusted; len(got) != 1 || got[0] != "clippy" {
		t.Errorf("linters.trusted = %v, want the trusted file's own", got)
	}
	if len(cfg.Dropped) != 0 {
		t.Errorf("Dropped = %v, want nothing dropped from a trusted file", cfg.Dropped)
	}
}

// A semgrep registry reference has to look like one.
//
// internal/linters returns this value verbatim rather than resolving it, so it
// is the one analyzer config that reaches a command line with no path check.
func TestASemgrepRegistryReferenceHasAShape(t *testing.T) {
	for _, ok := range []string{"p/ci", "p/golang", "r/go.lang.security", "p/r2c-security-audit"} {
		if !SemgrepRegistryRef(ok) {
			t.Errorf("SemgrepRegistryRef(%q) = false, want a registry reference", ok)
		}
	}
	for _, bad := range []string{
		"p/../../etc/passwd",
		"p/",
		"p//etc",
		"p/-rules",
		"https://evil.invalid/rules.yml",
		"p/rules --config /etc/shadow",
		"p/rules\nmore",
		"x/rules",
	} {
		if SemgrepRegistryRef(bad) {
			t.Errorf("SemgrepRegistryRef(%q) = true, so it reaches semgrep unchecked", bad)
		}
	}
}
