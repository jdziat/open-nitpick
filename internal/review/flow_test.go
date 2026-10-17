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
