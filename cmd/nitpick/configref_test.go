package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigReferenceSupportsStandaloneSourcesAndOptionalCommitDocs(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "config")
	if err := os.Mkdir(src, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "config.go"), []byte("package config\ntype Review struct {\n// MaxFiles carries the standalone source marker.\nMaxFiles int\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runConfigRef([]string{"-src", src}, &out); err != nil || !strings.Contains(out.String(), "standalone source marker") {
		t.Fatalf("standalone source was ignored or rejected: %v", err)
	}
	sibling := filepath.Join(root, "commits")
	if err := os.Mkdir(sibling, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sibling, "policy.go"), []byte("package commits\ntype Policy struct {\n// MaxDescriptionRunes carries the supplemental source marker.\nMaxDescriptionRunes int\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runConfigRef([]string{"-src", src}, &out); err != nil || !strings.Contains(out.String(), "standalone source marker") || !strings.Contains(out.String(), "supplemental source marker") {
		t.Fatalf("supplemental documentation was lost: %v", err)
	}
}
