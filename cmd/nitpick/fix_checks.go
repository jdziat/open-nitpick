package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/nitpick-oss/nitpick/internal/config"
	"github.com/nitpick-oss/nitpick/internal/execution"
	"github.com/nitpick-oss/nitpick/internal/vcs"
)

func checkFix(ctx context.Context, provider vcs.Provider, ref vcs.Ref, settings config.Execution, edits []vcs.FileEdit) (string, bool) {
	if !settings.Enabled {
		return "", true
	}
	unavailable := func(err error) (string, bool) {
		// Error details can contain repository-controlled paths and host diagnostics.
		// Keep them out of the forge; stderr remains the operator's receipt.
		fmt.Fprintf(os.Stderr, "fix checks unavailable: %v\n", err)
		return "\n\nSelected checks: unavailable/incomplete. No execution claim.\n", !settings.RequiredForFix
	}
	runner, err := execution.NewRunner(settings.Policy())
	if err != nil {
		return unavailable(err)
	}
	snapshot, err := execution.ReadSnapshot(ctx, provider, ref)
	if err != nil {
		return unavailable(err)
	}
	patch := map[string][]byte{}
	for _, edit := range edits {
		if _, duplicate := patch[edit.Path]; duplicate {
			return unavailable(fmt.Errorf("duplicate proposed file"))
		}
		patch[edit.Path] = edit.Content
	}
	var body strings.Builder
	fmt.Fprintf(&body, "\n\nSelected checks at `%s` (runtime `%s`, image `%s`):\n", ref.Head, settings.Policy().Runtime, settings.Image)
	passed := true
	for _, profile := range settings.Checks {
		check := execution.Check{Kind: profile.Kind, Package: profile.Package}
		comparison, err := runner.RunPatch(ctx, snapshot, patch, check)
		if err != nil {
			return unavailable(err)
		}
		currentRef := ref
		currentRef.Head = ""
		current, err := provider.PullRequest(ctx, currentRef)
		if err != nil {
			return unavailable(err)
		}
		if current == nil {
			return unavailable(fmt.Errorf("provider returned no current head"))
		}
		if err := runner.VerifyPatch(comparison, snapshot, patch, check, current.HeadSHA); err != nil {
			return unavailable(err)
		}
		fmt.Fprintf(&body, "\n- `%s %s`: baseline **%s**, candidate **%s**; patch `%s`, source `%s`, policy `%s`, candidate output `%s`.\n", check.Kind, check.Package, comparison.Baseline.Status, comparison.Candidate.Status, comparison.PatchSHA256, snapshot.SHA256, comparison.Candidate.PolicySHA256, comparison.Candidate.OutputSHA256)
		passed = passed && comparison.Baseline.Status == execution.Passed && comparison.Candidate.Status == execution.Passed
	}
	body.WriteString("\nThese results cover the selected checks only; they do not prove a semantic repair.\n")
	return body.String(), passed || !settings.RequiredForFix
}
