package linters

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/standards"
)

func TestLanguageConventionLintersRejectViolationsAndAcceptCleanFiles(t *testing.T) {
	for _, tc := range []struct{ tool, path, dirty, clean, localConfig, localBody string }{
		{"ruff", "app.py", "class bad_name:\n    def FetchUser(self): return 1\n", "class Client:\n    def fetch_user(self): return 1\n", "ruff.toml", "[lint]\nselect = []\n"},
		{"eslint", "app.js", "class bad_name {}\nconst same = 1 == '1';\n", "class Client {}\nconst same = 1 === '1';\n", "eslint.config.mjs", "export default [];\n"},
		{"pmd", "Client.java", "package com.Example;\npublic class bad_name {}\n", "package com.example;\npublic class Client {}\n", "ruleset.xml", "invalid project rules\n"},
		{"rubocop", "app.rb", "class Bad_Name\n  def FetchUser; end\nend\n", "class Client\n  def fetch_user; end\nend\n", ".rubocop.yml", "AllCops:\n  DisabledByDefault: true\n"},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			if _, err := exec.LookPath(tc.tool); err != nil {
				if os.Getenv("NITPICK_REQUIRE_LANGUAGE_LINTERS") == "1" {
					t.Fatal(err)
				}
				t.Skip(tc.tool + " is not installed")
			}
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, tc.localConfig), []byte(tc.localBody), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg := config.Defaults()
			cfg.Linters.Enabled = []string{tc.tool}
			cfg.Linters.AutoDetect = new(bool)
			cfg.Linters.Mode = config.LinterStrict
			cfg.Linters.OnlyChangedLines = false
			source := NewStandardsSource(cfg, nil)
			for _, content := range []struct {
				body  string
				dirty bool
			}{{tc.dirty, true}, {tc.clean, false}} {
				if err := os.WriteFile(filepath.Join(root, tc.path), []byte(content.body), 0o600); err != nil {
					t.Fatal(err)
				}
				found, cov, err := source.Observe(context.Background(), root, []standards.File{{Path: tc.path, Src: []byte(content.body)}})
				if err != nil || !cov.Ran || cov.Why != "" || len(cov.Files) != 1 {
					t.Fatalf("coverage=%+v err=%v", cov, err)
				}
				if content.dirty && len(found) < 2 || !content.dirty && len(found) != 0 {
					t.Errorf("dirty=%t observations=%+v", content.dirty, found)
				}
				if content.dirty {
					for _, rule := range map[string][]string{
						"ruff": {"N801", "N802"}, "eslint": {"eqeqeq", "no-restricted-syntax"},
						"pmd": {"ClassNamingConventions", "PackageCase"}, "rubocop": {"Naming/MethodName", "Naming/ClassAndModuleCamelCase"},
					}[tc.tool] {
						present := false
						for _, obs := range found {
							present = present || strings.Contains(obs.Rule, rule)
						}
						if !present {
							t.Errorf("missing %s in %v", rule, found)
						}
					}
				}
				for _, obs := range found {
					if obs.Path != tc.path || obs.Line < 1 {
						t.Errorf("location: %+v", obs)
					}
				}
			}
			if cfg.Linters.RuffConfig != "" || cfg.Linters.ESLintConfig != "" || cfg.Linters.Configs[tc.tool] != "" {
				t.Error("source mutated caller configuration")
			}
		})
	}
}

func TestLanguageConventionConfigsKeepOperatorChoicesAndCleanUp(t *testing.T) {
	root := t.TempDir()
	cfg := config.Defaults()
	cfg.Linters.Enabled = []string{"ruff", "eslint", "rubocop", "pmd"}
	cfg.Linters.RuffConfig = "/operator/ruff.toml"
	cfg.Linters.Configs = map[string]string{"pmd": "/operator/pmd.xml"}
	original := cfg.Linters.Configs
	cleanup, err := languageConventionConfigs(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if cfg.Linters.RuffConfig != "/operator/ruff.toml" || cfg.Linters.Configs["pmd"] != "/operator/pmd.xml" {
		t.Fatal("operator choice replaced")
	}
	if len(original) != 1 {
		t.Fatal("caller map mutated")
	}
	generated := cfg.Linters.ESLintConfig
	if strings.HasPrefix(generated, root+string(filepath.Separator)) {
		t.Fatal("generated config is inside repository")
	}
	if _, err := os.Stat(generated); err != nil {
		t.Fatal(err)
	}
	cleanup()
	if _, err := os.Stat(generated); !os.IsNotExist(err) {
		t.Fatalf("config remains after cleanup: %v", err)
	}
}

func TestLanguageConventionSetupFailuresReportIncompleteCoverage(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, root)
	}
	cfg := config.Defaults()
	cfg.Linters.Enabled = []string{"ruff"}
	cfg.Linters.GolangciConfig = "/operator/golangci.yml"
	found, coverage, err := NewStandardsSource(cfg, nil).Observe(context.Background(), root, nil)
	if err != nil || coverage.Ran || coverage.Why == "" || len(found) != 0 {
		t.Fatalf("found=%v coverage=%+v err=%v", found, coverage, err)
	}
}

func TestLanguageConventionRubySyntaxErrorsCannotReportClean(t *testing.T) {
	if _, err := exec.LookPath("rubocop"); err != nil {
		if os.Getenv("NITPICK_REQUIRE_LANGUAGE_LINTERS") == "1" {
			t.Fatal(err)
		}
		t.Skip("rubocop is not installed")
	}
	root := t.TempDir()
	cfg := config.Defaults()
	cfg.Linters.Enabled = []string{"rubocop"}
	cfg.Linters.AutoDetect = new(bool)
	cfg.Linters.Mode = config.LinterStrict
	cfg.Linters.OnlyChangedLines = false
	for _, opener := range []string{"<<DOC", "<<-DOC", "<<~DOC"} {
		body := []byte("puts " + opener + "\nclass Fake_End; end\n")
		if err := os.WriteFile(filepath.Join(root, "app.rb"), body, 0o600); err != nil {
			t.Fatal(err)
		}
		found, cov, err := NewStandardsSource(cfg, nil).Observe(context.Background(), root, []standards.File{{Path: "app.rb", Src: body}})
		if err != nil || !cov.Ran || len(cov.Files) != 1 {
			t.Fatalf("coverage=%+v err=%v", cov, err)
		}
		syntax := false
		for _, obs := range found {
			syntax = syntax || strings.Contains(obs.Rule, "Lint/Syntax")
		}
		if !syntax {
			t.Errorf("%s: missing syntax finding: %+v", opener, found)
		}
	}
}
