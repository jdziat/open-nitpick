//go:build eval

package evals

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"
)

// TestProbeModel runs the selected fixtures against the selected models and
// prints every failure with the raw responses that preceded it, for
// diagnosing a model that loses reviews in the battery.
func TestProbeModel(t *testing.T) {
	opts, err := OptionsFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(os.Getenv(EnvFixtures)) == "" {
		opts.Fixtures = Fixtures()
	}
	var logs strings.Builder
	opts.Log = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	for _, m := range opts.Models {
		for _, f := range opts.Fixtures {
			for i := range max(opts.Runs, 1) {
				logs.Reset()
				res := Run(context.Background(), m, f, i, opts)
				if res.Err == nil {
					t.Logf("%s %s run %d: ok, %d finding(s), %d call(s)", m.ID, f.Name, i, len(res.Report.Findings), res.Usage.Calls())
					continue
				}
				t.Logf("%s %s run %d: FAILED: %v", m.ID, f.Name, i, res.Err)
				for line := range strings.SplitSeq(logs.String(), "\n") {
					if strings.Contains(line, "level=ERROR") || strings.Contains(line, "level=WARN") {
						t.Log("  " + line)
					}
				}
				for j, r := range res.Responses {
					s := r
					if len(s) > 600 {
						s = s[:600] + "…"
					}
					t.Logf("  response %d: %q", j, s)
				}
			}
		}
	}
}
