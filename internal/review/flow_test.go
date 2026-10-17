package review

import (
	"context"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/flow"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// listingProvider adds the directory listing the flow graph is read through.
type listingProvider struct{ *stubProvider }

func (p listingProvider) ListDir(_ context.Context, _ vcs.Ref, dir string) ([]string, error) {
	if dir != "" {
		return nil, vcs.ErrNotFound
	}
	var names []string
	for name := range p.content {
		names = append(names, name)
	}
	return names, nil
}

const flowDiffText = `diff --git a/app.go b/app.go
index 111..222 100644
--- a/app.go
+++ b/app.go
@@ -4,3 +4,4 @@
 func Run() { Get() }
 
 func Get() error {
+	_, err := os.ReadFile("x")
 	return nil
 }
`

const flowSource = "package app\n\nimport \"os\"\n\nfunc Run() { Get() }\n\nfunc Get() error {\n\t_, err := os.ReadFile(\"x\")\n\treturn err\n}\n"

func TestFlowIsOffByDefault(t *testing.T) {
	if config.Defaults().Review.Flow {
		t.Error("review.flow must ship off until its output has been measured on real reviews")
	}
}

func flowEngine(t *testing.T, on bool) *Engine {
	t.Helper()
	provider := listingProvider{&stubProvider{diff: flowDiffText, content: map[string]string{
		"go.mod": "module example.com/app\n",
		"app.go": flowSource,
	}}}
	return newEngine(t, &scriptedLLM{}, provider, func(cfg *config.Config) { cfg.Review.Flow = on })
}

func flowDiff(t *testing.T) diff.Files {
	t.Helper()
	files, err := diff.Parse([]byte(flowDiffText))
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestFlowDiagramReachesTheEnginePathOnlyWhenEnabled(t *testing.T) {
	got := flowEngine(t, true).flowDiagram(context.Background(), vcs.Ref{}, flowDiff(t), nil)
	if got == nil || !strings.Contains(got.Mermaid, "Run") || !strings.Contains(got.Mermaid, "os.ReadFile") {
		t.Fatalf("enabled flow lacks the caller or the effect: %+v", got)
	}
	if off := flowEngine(t, false).flowDiagram(context.Background(), vcs.Ref{}, flowDiff(t), nil); off != nil {
		t.Fatalf("a diagram was built with review.flow off: %+v", off)
	}
}

func TestFlowDiagramIsLeftOutWhenTheTreeCannotBeListed(t *testing.T) {
	engine := flowEngine(t, true)
	engine.Provider = engine.Provider.(listingProvider).stubProvider
	if got := engine.flowDiagram(context.Background(), vcs.Ref{}, flowDiff(t), nil); got != nil {
		t.Fatalf("a diagram was drawn from a tree that could not be listed: %+v", got)
	}
}

func TestFlowIsPublishedWithTheSummaryOff(t *testing.T) {
	report := &Report{Flow: &flow.Diagram{Mermaid: "flowchart LR\n  n0[\"a\"]\n", Changed: []string{"p.Get"}, Nodes: 1}}

	quiet := config.Defaults()
	quiet.Review.Summary = false
	got := Render(report, diff.Files{}, quiet).Summary
	if !strings.Contains(got, "```mermaid\nflowchart LR") || !strings.Contains(got, "Flow around p.Get") {
		t.Fatalf("summary lacks the fenced diagram:\n%s", got)
	}
	if got := Render(&Report{}, diff.Files{}, quiet).Summary; strings.Contains(got, "mermaid") {
		t.Fatalf("summary drew a diagram nobody asked for:\n%s", got)
	}
}

func TestFlowLinksCallsAndDeclarationsToTheReviewedRevision(t *testing.T) {
	d := &flow.Diagram{
		Mermaid: "flowchart LR\n", Changed: []string{"p.Get"}, Nodes: 2,
		Calls:     []flow.Call{{From: "p.Run", To: "p.Get", Kind: flow.Inferred, Path: "p/run.go", Line: 12}},
		Locations: []flow.Location{{Label: "p.Get", Path: "p/get.go", Line: 30}},
		Limits:    []string{"1 Go file(s) did not parse and are missing from the graph: p/x.go."},
	}
	report := &Report{Flow: d, PullRequest: &vcs.PullRequest{SourceBaseURL: "https://github.com/o/r/blob/abc123"}}
	got := flowNotice(report)
	for _, want := range []string{
		"https://github.com/o/r/blob/abc123/p/run.go#L12",
		"https://github.com/o/r/blob/abc123/p/get.go#L30",
		"p.Run to p.Get (inferred)",
		"did not parse",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("notice lacks %q:\n%s", want, got)
		}
	}

	report.PullRequest = nil
	if got := flowNotice(report); strings.Contains(got, "href") || !strings.Contains(got, "p/run.go:12") {
		t.Fatalf("without a browse URL the location must be plain text:\n%s", got)
	}

	d.Calls[0].Path = "../../etc/passwd"
	report.PullRequest = &vcs.PullRequest{SourceBaseURL: "https://github.com/o/r/blob/abc123"}
	if got := flowNotice(report); strings.Contains(got, "etc/passwd#") {
		t.Fatalf("a path that leaves the tree was linked:\n%s", got)
	}
}

func TestFlowListsStopBeforeTheyCrowdOutTheReview(t *testing.T) {
	d := &flow.Diagram{Mermaid: "flowchart LR\n", Changed: []string{"p.Get"}, Nodes: 2}
	for i := range flowListMax + 7 {
		d.Calls = append(d.Calls, flow.Call{From: "a", To: "b", Kind: flow.Resolved, Path: "p/p.go", Line: i + 1})
	}
	got := flowNotice(&Report{Flow: d})
	if strings.Count(got, "- a to b") != flowListMax || !strings.Contains(got, "- and 7 more") {
		t.Fatalf("call list was not capped at %d:\n%s", flowListMax, got)
	}
}
