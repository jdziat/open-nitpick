package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// commentEvent writes an issue_comment payload for a pull request and returns
// the arguments that point `nitpick respond` at it.
func commentEvent(t *testing.T, body string) []string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"issue": map[string]any{"number": 7, "pull_request": map[string]any{}},
		"comment": map[string]any{
			"id": 11, "body": body, "author_association": "MEMBER",
			"user": map[string]any{"login": "maintainer"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cfg := "models:\n  default: {provider: openrouter, model: m}\n"
	if err := os.WriteFile(filepath.Join(dir, ".nitpick.yaml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "event.json")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	return []string{"-repo", dir, "-event", path, "-event-name", "issue_comment", "-owner", "o", "-repo-name", "r"}
}

// `nitpick respond` returns quietly for a comment that addresses nobody, and
// goes on to the forge for one that does. With no token the forge step fails,
// which is the proof the command got past the parser: the two outcomes differ
// only by whether the comment was read as a command.
func TestRespondReadsASlashCommandAsAnAddressedComment(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	for body, addressed := range map[string]bool{
		"/nitpick review":                    true,
		"thanks\n/nitpick why is this bad":   true,
		"@nitpick-review review":             true,
		"@open-nitpick review":               true,
		"looks good":                         false,
		"> /nitpick fix":                     false,
		"```\n/nitpick fix\n```":             false,
		"see github.com/nitpick-oss/nitpick": false,
	} {
		err := runRespond(context.Background(), commentEvent(t, body))
		if addressed && err == nil {
			t.Errorf("%q returned quietly; the comment was not read as addressed", body)
		}
		if !addressed && err != nil {
			t.Errorf("%q reached the forge (%v); it addresses nobody", body, err)
		}
	}
}
