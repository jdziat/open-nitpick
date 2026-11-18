package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

type fixCheckProvider struct {
	vcs.Provider
	head       string
	sourceHead string
}

func (p *fixCheckProvider) ListSnapshotDir(_ context.Context, ref vcs.Ref, _ string) ([]string, error) {
	p.sourceHead = ref.Head
	return []string{"go.mod", "main.go"}, nil
}
func (p *fixCheckProvider) FileContent(_ context.Context, _ vcs.Ref, file string) ([]byte, error) {
	if file == "go.mod" {
		return []byte("module example\ngo 1.25\n"), nil
	}
	return []byte("package example\n"), nil
}
func (p *fixCheckProvider) PullRequest(_ context.Context, ref vcs.Ref) (*vcs.PullRequest, error) {
	if ref.Head != "" {
		return &vcs.PullRequest{HeadSHA: ref.Head}, nil
	}
	return &vcs.PullRequest{HeadSHA: p.head}, nil
}

func TestFixChecksBindEditsAndRefuseRequiredUnavailableResults(t *testing.T) {
	head := strings.Repeat("a", 40)
	provider := &fixCheckProvider{head: head}
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	settings := config.Execution{Enabled: true, Image: "sha256:" + strings.Repeat("a", 64), Runtime: "runc", TrustedCorpus: true, RiskAccepted: true, RequiredForFix: true, Checks: []config.ExecutionCheck{{Kind: "go_build", Package: "."}}}
	edits := []vcs.FileEdit{{Path: "main.go", Content: []byte("package example\nconst Fix=true\n")}}
	for _, test := range []struct {
		name    string
		code    string
		moved   bool
		allowed bool
		want    string
	}{{"pass", "0", false, true, "candidate **checks passed**"}, {"fail", "1", false, false, "candidate **checks failed**"}, {"unavailable", "125", false, false, "unavailable/incomplete"}, {"stale", "0", true, false, "unavailable/incomplete"}} {
		t.Run(test.name, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(dir, "docker"), []byte("#!/bin/sh\nif [ \"$1\" = run ]; then exit "+test.code+"; fi\nexit 0\n"), 0700); err != nil {
				t.Fatal(err)
			}
			provider.head = head
			if test.moved {
				provider.head = strings.Repeat("b", 40)
			}
			body, allowed := checkFix(t.Context(), provider, vcs.Ref{Head: head}, settings, edits)
			if allowed != test.allowed || !strings.Contains(body, test.want) || provider.sourceHead != head {
				t.Fatalf("wrong publish gate: allowed=%v body=%s source=%s", allowed, body, provider.sourceHead)
			}
		})
	}
}
