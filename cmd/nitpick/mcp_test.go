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

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcpSession connects a client to the server over in-memory transports.
func mcpSession(t *testing.T, root string) *mcp.ClientSession {
	t.Helper()
	server := newMCPServer(root, slog.New(slog.NewTextHandler(io.Discard, nil)))
	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	session, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestMCPServerListsItsTools(t *testing.T) {
	session := mcpSession(t, t.TempDir())
	res, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
		if tool.Description == "" || tool.InputSchema == nil {
			t.Errorf("tool %s has no description or schema", tool.Name)
		}
	}
	want := "ai_slop,code_smell,explain_config,full_review,repo_score,review"
	got := strings.Join(sortedStrings(names), ",")
	if got != want {
		t.Errorf("tools = %s, want %s", got, want)
	}
	// Every tree tool and the review carry a structured output schema, so a
	// client can rely on the finding fields.
	for _, tool := range res.Tools {
		if tool.Name == "explain_config" {
			continue
		}
		schema, _ := json.Marshal(tool.OutputSchema)
		if !strings.Contains(string(schema), `"findings"`) {
			t.Errorf("tool %s output schema lacks findings: %s", tool.Name, schema)
		}
	}
}

func TestMCPExplainConfigReadsTheRepository(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".nitpick.yaml"), []byte("models:\n  default:\n    provider: openrouter\n    model: test/model\nreview:\n  fail_on: error\n  ignore: [\"vendor/**\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	session := mcpSession(t, root)
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "explain_config", Arguments: map[string]any{"path": "vendor/x.go"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("explain_config errored: %s", textOf(res))
	}
	text := textOf(res)
	for _, want := range []string{"Config source:", "fail_on      error", "ignored by review.ignore"} {
		if !strings.Contains(text, want) {
			t.Errorf("explain_config output lacks %q:\n%s", want, text)
		}
	}
}

// A review with no model configured is an error that names the missing
// setting; the server must not print anything to stdout on the way, since
// stdout is the protocol.
func TestMCPReviewWithoutAModelIsAnErrorNotAPanic(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LLM_PROVIDER", "")
	t.Setenv("LLM_MODEL", "")
	t.Setenv("OPENROUTER_API_KEY", "")
	t.Setenv("LLM_API_KEY", "")
	session := mcpSession(t, root)
	for _, name := range []string{"review", "ai_slop"} {
		res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: map[string]any{}})
		if err != nil {
			t.Fatal(err)
		}
		if !res.IsError {
			t.Errorf("%s without a model did not error: %s", name, textOf(res))
		}
	}
}

func TestReviewTextOrdersBySeverityAndCarriesTheGate(t *testing.T) {
	text := reviewText(ReviewOut{
		Summary: "Walkthrough.", Files: 2, FailOn: "warning", Failed: true,
		Findings: []Finding{
			{Path: "b.go", Line: 3, Severity: "nit", Class: "style", Title: "Naming", Rationale: "r"},
			{Path: "a.go", Line: 9, Severity: "error", Class: "correctness", Title: "Nil deref", Rationale: "r", Suggestion: "x := y\nreturn x"},
			{Path: "go.mod", Line: 1, Severity: "warning", Class: "security", Title: "CVE-1", Rationale: "r", Source: "osv-scanner(CVE-1)"},
		},
		Counts: map[string]int{"nit": 1, "error": 1, "warning": 1},
	})
	if !strings.HasPrefix(text, "Walkthrough.\n\n2 file(s) reviewed, 3 finding(s) (1 error, 1 warning, 1 nit); gate warning failed.") {
		t.Errorf("header:\n%s", text)
	}
	if strings.Index(text, "Nil deref") > strings.Index(text, "CVE-1") || strings.Index(text, "CVE-1") > strings.Index(text, "Naming") {
		t.Errorf("not ordered by severity:\n%s", text)
	}
	if !strings.Contains(text, "reported by osv-scanner(CVE-1)") || !strings.Contains(text, "suggestion:\n    x := y\n    return x") {
		t.Errorf("source or suggestion missing:\n%s", text)
	}
}

func textOf(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func sortedStrings(s []string) []string {
	out := append([]string(nil), s...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// The citation reaches the text half of the result, not only the JSON.
//
// A client reading the text sees why a finding was removed, and the entry that
// reason rests on is part of it. Rendering it on one half only makes the
// documented promise true for half the consumers.
func TestReviewTextCarriesTheCitation(t *testing.T) {
	text := reviewText(ReviewOut{
		Withheld: []Withheld{{
			Path: "a.go", Line: 4, Title: "Deferred close in a loop",
			Expert: "resource", Reason: "the loop body returns", Cited: "go-defer-in-loop",
		}},
	})
	// The whole line, so the id cannot run into the reason: a reader has to be
	// able to see where the id ends.
	if !strings.Contains(text, "(resource: the loop body returns) (citing go-defer-in-loop)") {
		t.Errorf("the citation did not render after the reason:\n%s", text)
	}

	// And a withheld finding with no citation reads cleanly.
	plain := reviewText(ReviewOut{
		Withheld: []Withheld{{Path: "a.go", Line: 4, Title: "T", Expert: "resource", Reason: "why"}},
	})
	if !strings.Contains(plain, "(resource: why)") {
		t.Errorf("an uncited withheld finding did not render cleanly:\n%s", plain)
	}
}
