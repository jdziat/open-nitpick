package review

import (
	"context"
	"strings"
	"sync"
	"testing"

	llms "github.com/nocturnium/llm-go-sdk/v6"

	"github.com/jdziat/open-nitpick/v2/internal/config"
	"github.com/jdziat/open-nitpick/v2/internal/llm"
	"github.com/jdziat/open-nitpick/v2/internal/vcs"
)

// namedLLM answers every review with one finding titled after itself, so a
// test can read which model reviewed which file off the findings.
type namedLLM struct {
	name    string
	mu      sync.Mutex
	prompts []string
}

func (n *namedLLM) GenerateContent(_ context.Context, msgs []llms.Message, _ ...llms.CallOption) (*llms.Response, error) {
	n.mu.Lock()
	n.prompts = append(n.prompts, msgs[len(msgs)-1].Content)
	n.mu.Unlock()
	user := msgs[len(msgs)-1].Content
	switch {
	case strings.Contains(user, "Classify the following change"):
		kinds := `[]`
		if strings.Contains(user, "password") {
			kinds = `["security"]`
		}
		return &llms.Response{Content: `{"kinds":` + kinds + `}`}, nil
	case strings.Contains(user, "findings were reported"):
		// Triage: keep everything, in order.
		var b strings.Builder
		b.WriteString(`{"summary":"walkthrough.","findings":[`)
		first := true
		for _, line := range strings.Split(user, "\n") {
			if !strings.Contains(line, " — ") {
				continue
			}
			if !first {
				b.WriteString(",")
			}
			first = false
			num := strings.TrimSpace(strings.SplitN(line, ".", 2)[0])
			b.WriteString(`{"number":` + num + `,"path":"` + pathOf(line) + `","line":1,"severity":"warning","class":"correctness","title":"` + titleOf(line) + `","rationale":"kept"}`)
		}
		b.WriteString(`]}`)
		return &llms.Response{Content: b.String()}, nil
	}
	path := "unknown"
	for _, line := range strings.Split(user, "\n") {
		if strings.HasPrefix(line, "### File: ") {
			path = strings.TrimPrefix(line, "### File: ")
			break
		}
	}
	return &llms.Response{Content: `{"findings":[{"path":"` + path + `","line":1,"severity":"warning","class":"correctness","title":"seen by ` + n.name + `","rationale":"the change on line 1 breaks the caller, demonstrated by the test at line 1"}]}`}, nil
}

func pathOf(line string) string {
	i := strings.Index(line, "] ")
	j := strings.Index(line, ":1 — ")
	if i < 0 || j < 0 {
		return "unknown"
	}
	return line[i+2 : j]
}

func titleOf(line string) string {
	_, after, _ := strings.Cut(line, " — ")
	return after
}

func (n *namedLLM) Stream(context.Context, []llms.Message, ...llms.CallOption) (<-chan llms.StreamChunk, error) {
	return nil, nil
}
func (n *namedLLM) Provider() llms.Provider { return "fake" }
func (n *namedLLM) Model() string           { return n.name }

const routedDiff = `diff --git a/auth.go b/auth.go
new file mode 100644
--- /dev/null
+++ b/auth.go
@@ -0,0 +1,2 @@
+package auth
+var password = "hunter2"
diff --git a/web.ts b/web.ts
new file mode 100644
--- /dev/null
+++ b/web.ts
@@ -0,0 +1,1 @@
+export const x = 1;
diff --git a/main.py b/main.py
new file mode 100644
--- /dev/null
+++ b/main.py
@@ -0,0 +1,1 @@
+x = 1
`

func TestRoutesAndEnsemblesChooseTheReviewerPerBatch(t *testing.T) {
	cfg := config.Defaults()
	cfg.Models.Default = config.ModelSpec{Provider: "fake", Model: "cheap"}
	cfg.Models.Triage = &config.ModelSpec{Model: "triager"}
	cfg.Models.Router = &config.ModelSpec{Model: "router"}
	cfg.Models.Routes = []config.Route{
		{Name: "security", Match: config.RouteMatch{Kinds: []string{config.KindSecurity}}, Review: &config.ModelSpec{Model: "strong"}},
		{Name: "typescript", Match: config.RouteMatch{Languages: []string{"typescript"}}, Review: &config.ModelSpec{Model: "ts-expert"}, Ensemble: []config.ModelSpec{}},
	}
	cfg.Models.Ensemble = []config.ModelSpec{{Model: "second"}}
	cfg.Review.MaxFilesPerRequest = 1
	cfg.Review.MinSeverity = config.SeverityNit

	built := map[string]*namedLLM{}
	build := func(spec config.ModelSpec) (*llm.Client, error) {
		m := &namedLLM{name: spec.Model}
		built[spec.Model] = m
		return llm.NewClientForTest(m, spec), nil
	}
	roles := &llm.Roles{Build: build}
	for _, role := range []struct {
		dst  **llm.Client
		spec config.ModelSpec
	}{
		{&roles.Review, cfg.Models.ResolveModel(config.RoleReview)},
		{&roles.Triage, cfg.Models.ResolveModel(config.RoleTriage)},
	} {
		c, _ := build(role.spec)
		*role.dst = c
	}
	router, _ := cfg.Models.ResolveRouter()
	roles.Router, _ = build(router)

	engine := &Engine{Config: cfg, Roles: roles, Provider: &stubProvider{diff: routedDiff}}
	report, err := engine.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}

	// Who reviewed what, from the findings' sources.
	seen := map[string][]string{}
	for _, f := range report.Findings {
		seen[f.Path] = append(seen[f.Path], f.Source)
	}
	want := map[string][]string{
		"auth.go": {"fake/second", "fake/strong"}, // security route + global ensemble
		"web.ts":  {"fake/ts-expert"},             // typescript route, ensemble removed by the route
		"main.py": {"fake/cheap", "fake/second"},  // default + global ensemble
	}
	for path, models := range want {
		got := seen[path]
		sortStrings(got)
		if strings.Join(got, ",") != strings.Join(models, ",") {
			t.Errorf("%s reviewed by %v, want %v", path, got, models)
		}
	}

	if len(report.Routes) != 3 {
		t.Fatalf("routes recorded = %d, want 3: %+v", len(report.Routes), report.Routes)
	}
	for _, d := range report.Routes {
		switch d.Files[0] {
		case "auth.go":
			if d.Route != "security" || strings.Join(d.Kinds, ",") != "security" {
				t.Errorf("auth.go decision = %+v", d)
			}
		case "web.ts":
			if d.Route != "typescript" || len(d.Ensemble) != 0 {
				t.Errorf("web.ts decision = %+v", d)
			}
		case "main.py":
			if d.Route != "" || d.Reviewer != "fake/cheap" {
				t.Errorf("main.py decision = %+v", d)
			}
		}
	}

	// The router saw diffs only, never a full file section.
	for _, p := range built["router"].prompts {
		if strings.Contains(p, "Full file after the change") {
			t.Error("the router was sent a full file; it classifies the diff")
		}
	}
}

func sortStrings(s []string) {
	for i := range s {
		for j := i + 1; j < len(s); j++ {
			if s[j] < s[i] {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
}
