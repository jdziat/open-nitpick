package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The tells pass needs no model: a repository with one prose file and one
// source file is scored, with each tell named, counted and given a fix.
func TestSlopWithoutAModelScoresTheTells(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	for _, args := range [][]string{{"init", "-q", "-b", "main"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	files := map[string]string{
		"README.md":     "# x\n\nA fast, reliable, and secure tool — it genuinely works.\n",
		"a.go":          "package a\n\nvar counter int\n\n// Sure! Here's the counter.\nfunc F() {\n\t// increment the counter\n\tcounter++\n}\n",
		"vendor/v.md":   "vendored — ignored\n",
		".nitpick.yaml": "models:\n  default:\n    provider: openrouter\n    model: t/m\nreview:\n  ignore: [\"vendor/**\"]\n",
	}
	for p, body := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, p), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	res, err := slopScore(context.Background(), &reviewFlags{repo: root}, nil, 0, true, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if res.ModelRan || res.Findings != nil {
		t.Errorf("a model ran: %+v", res)
	}
	var rules []string
	for _, tell := range res.Tells {
		rules = append(rules, tell.Rule)
		if tell.Path == "vendor/v.md" {
			t.Errorf("an ignored file was scanned: %+v", tell)
		}
	}
	got := strings.Join(rules, ",")
	for _, want := range []string{"em-dash", "filler-qualifier", "triplet-rhythm", "chat-prose", "restating-comment"} {
		if !strings.Contains(got, want) {
			t.Errorf("tells lack %s: %s", want, got)
		}
	}
	if res.TellsPerKLOC == 0 || len(res.Recommendations) == 0 || res.Recommendations[0] == "" {
		t.Errorf("score or recommendations missing: %+v", res)
	}
	text := res.Text()
	for _, want := range []string{"Tells (no model)", "README.md:3", "Model pass not run", "Recommendations"} {
		if !strings.Contains(text, want) {
			t.Errorf("text lacks %q:\n%s", want, text)
		}
	}
	raw, err := json.Marshal(res)
	if err != nil || !strings.Contains(string(raw), `"tells_per_kloc"`) {
		t.Errorf("json = %s, %v", raw, err)
	}
}
