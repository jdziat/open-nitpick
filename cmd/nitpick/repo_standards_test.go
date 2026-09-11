package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/standards"
)

type repoStandardsSource struct {
	observation bool
	unavailable bool
	seen        []string
}

func (s *repoStandardsSource) Name() string { return "fixture" }
func (s *repoStandardsSource) Observe(_ context.Context, _ string, files []standards.File) ([]standards.Observation, standards.Coverage, error) {
	if s.unavailable {
		return nil, standards.Coverage{Why: "fixture analyzer missing"}, nil
	}
	coverage := standards.Coverage{Ran: true, Files: map[string]int{}}
	for _, f := range files {
		s.seen = append(s.seen, f.Path)
		coverage.Files[f.Path] = 1
	}
	if s.observation {
		return []standards.Observation{{Rule: "ruff.F401", Path: "app.py", Line: 1}}, coverage, nil
	}
	return nil, coverage, nil
}

func TestRepoStandardsChecksWholeTreeAndDistinguishesMissingAnalyzers(t *testing.T) {
	for _, tc := range []struct {
		name           string
		dirty, missing bool
		want           error
	}{
		{"clean", false, false, nil}, {"violation", true, false, errFindings}, {"unavailable", false, true, errIncomplete},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			write(t, repo, "app.py", "import os\n")
			write(t, repo, ".nitpick.yaml", "models:\n  default:\n    provider: invalid-model-provider\n")
			source := &repoStandardsSource{observation: tc.dirty, unavailable: tc.missing}
			var output bytes.Buffer
			err := repoStandardsCommand(context.Background(), []string{"-repo", repo, "-json", "-check"}, &output, func(cfg *config.Config) standards.Source {
				if strings.Join(cfg.Linters.Enabled, ",") != "ruff" {
					t.Fatalf("selected %v for Python", cfg.Linters.Enabled)
				}
				return source
			})
			if !errors.Is(err, tc.want) {
				t.Fatalf("error=%v, want %v", err, tc.want)
			}
			var result RepoStandardsResult
			if err := json.Unmarshal(output.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if len(result.Sources) != 1 || result.Sources[0].Coverage.Ran == tc.missing {
				t.Fatalf("coverage=%+v", result.Sources)
			}
			if !tc.missing && (len(source.seen) != 2 || !strings.Contains(strings.Join(source.seen, ","), "app.py")) {
				t.Fatalf("source did not receive whole tree: %v", source.seen)
			}
			if len(result.Report.Unprobed) == 0 {
				t.Fatal("Python was presented as probed")
			}
		})
	}
}

func TestRepoStandardsEnforcesObservedConventionsWithoutPromotingProposals(t *testing.T) {
	repo := t.TempDir()
	var code strings.Builder
	code.WriteString("package p\nimport \"fmt\"\n")
	for i := range 20 {
		format := "%w"
		if i == 0 {
			format = "%v"
		}
		code.WriteString("func F" + string(rune('a'+i)) + "(err error) error { return fmt.Errorf(\"read: " + format + "\", err) }\n")
	}
	write(t, repo, "app.go", code.String())
	var output bytes.Buffer
	err := runRepoStandards(context.Background(), []string{"-repo", repo, "-no-linters", "-check", "-json"}, &output)
	if !errors.Is(err, errFindings) {
		t.Fatalf("established wrapping convention did not gate: %v", err)
	}
	var result RepoStandardsResult
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(result.Recommendations, "\n"), "Enforce go-error-wrap") {
		t.Fatalf("recommendations=%v", result.Recommendations)
	}
	write(t, repo, ".nitpick.yaml", "standards:\n  min_share: 1\n")
	output.Reset()
	if err := runRepoStandards(context.Background(), []string{"-repo", repo, "-no-linters", "-check"}, &output); err != nil {
		t.Fatalf("contested convention incorrectly gated: %v", err)
	}
	if !strings.Contains(output.String(), "Consider go-error-wrap") {
		t.Fatal("contested rule not proposed")
	}
}

func TestRepoStandardsRejectsEmptyAndMistypedChecks(t *testing.T) {
	for _, args := range [][]string{{"-check", "-no-linters"}, {"-linters", "rufff"}, {"unexpected"}, {"-no-linters", "-linters", "ruff"}} {
		var output bytes.Buffer
		if err := runRepoStandards(context.Background(), append([]string{"-repo", t.TempDir()}, args...), &output); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestRepoStandardsRunsRealLinterAdapter(t *testing.T) {
	repo := t.TempDir()
	write(t, repo, "app.py", "import os\n")
	toolDir := t.TempDir()
	tool := filepath.Join(toolDir, "ruff")
	body := "#!/bin/sh\nprintf '%s\\n' '[{\"filename\":\"app.py\",\"location\":{\"row\":1,\"column\":1},\"code\":\"F401\",\"message\":\"unused import\"}]'\nexit 1\n"
	if err := os.WriteFile(tool, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", toolDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	var output bytes.Buffer
	err := runRepoStandards(context.Background(), []string{"-repo", repo, "-linters", "ruff", "-check", "-json"}, &output)
	if !errors.Is(err, errFindings) {
		t.Fatalf("linter violation not enforced: %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "F401") {
		t.Fatal("linter rule missing from evidence")
	}
	if err := os.WriteFile(tool, []byte("#!/bin/sh\nprintf '[]\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err := runRepoStandards(context.Background(), []string{"-repo", repo, "-linters", "ruff", "-check"}, &output); err != nil {
		t.Fatalf("clean analyzer did not pass: %v\n%s", err, output.String())
	}
}
