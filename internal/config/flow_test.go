package config

import (
	"strings"
	"testing"
	"time"
)

func TestFlowDefaultsAreDisabledAndBounded(t *testing.T) {
	cfg := Defaults()
	if cfg.Flow.Mode != FlowOff {
		t.Fatalf("mode = %q, want off", cfg.Flow.Mode)
	}
	if cfg.Flow.MaxFiles != 2000 || cfg.Flow.MaxBytes != 32<<20 ||
		cfg.Flow.MaxDepthCallers != 8 || cfg.Flow.MaxDepthCallees != 4 ||
		cfg.Flow.MaxNodes != 500 || cfg.Flow.MaxEdges != 1000 || cfg.Flow.MaxFlows != 3 ||
		cfg.Flow.Timeout != 15*time.Second {
		t.Fatalf("unexpected flow defaults: %+v", cfg.Flow)
	}
}

func TestFlowConfigLoadsAndValidates(t *testing.T) {
	t.Setenv("LLM_PROVIDER", "synthetic")
	t.Setenv("LLM_MODEL", "flow-test")
	root := writeConfig(t, `flow:
  mode: auto
  max_files: 10
  max_bytes: 1000
  max_depth_callers: 2
  max_depth_callees: 3
  max_nodes: 20
  max_edges: 30
  max_flows: 2
  timeout: 2s
  include_unchanged: true
  entrypoints: [cmd.main]
  exclude: ["**/vendor/**"]
  build_tags: [integration]
`)
	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Flow.Mode != FlowAuto || cfg.Flow.MaxFiles != 10 || !cfg.Flow.IncludeUnchanged {
		t.Fatalf("flow config not applied: %+v", cfg.Flow)
	}
}

func TestFlowConfigRejectsInvalidValues(t *testing.T) {
	cases := []string{
		"flow:\n  mode: sometime\n",
		"flow:\n  max_nodes: 0\n",
		"flow:\n  timeout: 0s\n",
		"flow:\n  exclude: ['']\n",
		"flow:\n  entrypoints: ['']\n",
		"flow:\n  build_tags: ['']\n",
	}
	for _, body := range cases {
		_, err := Load(writeConfig(t, body))
		if err == nil {
			t.Errorf("Load(%q) succeeded", strings.TrimSpace(body))
		}
	}
}
