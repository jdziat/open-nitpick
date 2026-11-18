package execution

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestPatchEvidenceSeparatesBaselineFailureAndRejectsStaleEdits(t *testing.T) {
	runner := fixtureRunner(t)
	head := strings.Repeat("a", 40)
	base, err := NewSnapshot(head, map[string][]byte{"go.mod": []byte("module example\ngo 1.25\n"), "main.go": []byte("package example\n")})
	if err != nil {
		t.Fatal(err)
	}
	patch := map[string][]byte{"main.go": []byte("package example\nconst Fixed = true\n")}
	check := Check{Kind: "go_build", Package: "."}
	runs := 0
	runner.command = func(_ context.Context, args []string, _ io.Writer) (int, error) {
		if args[0] == "run" {
			runs++
			if runs == 1 {
				return 1, nil
			}
		}
		return 0, nil
	}
	result, err := runner.RunPatch(t.Context(), base, patch, check)
	if err != nil {
		t.Fatal(err)
	}
	if result.Baseline.Status != Failed || result.Candidate.Status != Passed || result.Candidate.SnapshotSHA256 == base.SHA256 {
		t.Fatalf("baseline was lost: %+v", result)
	}
	if err := runner.VerifyPatch(result, base, patch, check, head); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*Comparison){"swap": func(r *Comparison) { r.Baseline, r.Candidate = r.Candidate, r.Baseline }, "patch": func(r *Comparison) { r.PatchSHA256 = strings.Repeat("0", 64) }} {
		t.Run(name, func(t *testing.T) {
			changed := result
			change(&changed)
			if runner.VerifyPatch(changed, base, patch, check, head) == nil {
				t.Fatal("changed evidence accepted")
			}
		})
	}
	if runner.VerifyPatch(result, base, patch, check, strings.Repeat("b", 40)) == nil {
		t.Fatal("moved head accepted")
	}
	patch["main.go"] = []byte("package example\nconst Other = true\n")
	if runner.VerifyPatch(result, base, patch, check, head) == nil {
		t.Fatal("unexecuted edits accepted")
	}
}

func TestPassingBuildRefutesOnlyTheRegisteredBuildFailure(t *testing.T) {
	result := Result{Status: Passed, Check: Check{Kind: "go_build", Package: "internal/http"}}
	if !RefutesBuildFailure("go_build_failure", "internal/http/server.go", result) {
		t.Fatal("positive control did not refute selected build failure")
	}
	for _, kind := range []string{"behavioral_defect", "test_failure", "tagged_build_failure", ""} {
		if RefutesBuildFailure(kind, "internal/http/server.go", result) {
			t.Fatalf("build pass refuted %s", kind)
		}
	}
	for _, file := range []string{"internal/other/server.go", "internal/http/server_test.go", "README.md"} {
		if RefutesBuildFailure("go_build_failure", file, result) {
			t.Fatalf("build pass refuted out-of-scope %s", file)
		}
	}
	for _, status := range []Status{Unavailable, Failed} {
		result.Status = status
		if RefutesBuildFailure("go_build_failure", "internal/http/server.go", result) {
			t.Fatalf("%s refuted a claim", status)
		}
	}
}
