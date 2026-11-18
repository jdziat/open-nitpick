package execution

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func fixtureSnapshot(t *testing.T, headers ...*tar.Header) Snapshot {
	t.Helper()
	var out bytes.Buffer
	w := tar.NewWriter(&out)
	if len(headers) == 0 {
		headers = []*tar.Header{{Name: "go.mod", Mode: 0600, Size: 0, Typeflag: tar.TypeReg}}
	}
	for _, header := range headers {
		if err := w.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(out.Bytes())
	return Snapshot{Head: strings.Repeat("a", 40), SHA256: hex.EncodeToString(sum[:]), Archive: out.Bytes()}
}

func fixtureRunner(t *testing.T) *Runner {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte("#!/bin/sh\nexit 125\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	runner, err := NewRunner(Policy{Image: "example/checker@sha256:" + strings.Repeat("a", 64), RiskAccepted: true, TimeoutSeconds: 30, MemoryMiB: 1024, CPUs: 1, Pids: 64, DiskMiB: 128, MaxOutputBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	return runner
}

func TestExecutionRequiresPinnedSnapshotsAndIsolatedTrustedCommands(t *testing.T) {
	runner := fixtureRunner(t)
	var calls [][]string
	runner.command = func(_ context.Context, args []string, out io.Writer) (int, error) {
		calls = append(calls, slices.Clone(args))
		if args[0] == "run" {
			for _, required := range []string{"--runtime=runsc", "--network=none", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--user=65532:65532", "--log-driver=none", "--pull=never", "--pids-limit=64", "--memory=1024m", "--memory-swap=1024m"} {
				if !slices.Contains(args, required) {
					t.Errorf("required containment flag missing: %s", required)
				}
			}
			if !slices.Contains(args, "--entrypoint=/usr/local/bin/nitpick-checker") || strings.Contains(strings.Join(args, " "), "docker.sock") {
				t.Fatalf("untrusted entrypoint or host socket: %v", args)
			}
			_, err := out.Write([]byte("checks ran"))
			if err != nil {
				t.Fatal(err)
			}
		}
		return 0, nil
	}
	snapshot := fixtureSnapshot(t)
	result := runner.Run(t.Context(), snapshot, Check{Kind: "go_test", Package: "internal/http"})
	if result.Status != Passed || len(calls) != 2 || calls[1][0] != "rm" || calls[1][len(calls[1])-1] != result.JobID || result.SnapshotSHA256 != snapshot.SHA256 || result.PolicySHA256 == "" || result.OutputSHA256 == "" {
		t.Fatalf("unbound or uncleaned result: %+v calls=%v", result, calls)
	}
	check := Check{Kind: "go_test", Package: "internal/http"}
	if err := runner.VerifyResult(result, snapshot, check, snapshot.Head); err != nil {
		t.Fatalf("genuine result refused: %v", err)
	}
	tampered := result
	tampered.OutputSHA256 = strings.Repeat("0", 64)
	if err := runner.VerifyResult(tampered, snapshot, check, snapshot.Head); err == nil {
		t.Fatal("forged output evidence was accepted")
	}
	if err := runner.VerifyResult(result, snapshot, check, strings.Repeat("b", 40)); err == nil {
		t.Fatal("stale head passed publication guard")
	}
	if err := runner.VerifyResult(result, snapshot, Check{Kind: "go_build", Package: "."}, snapshot.Head); err == nil {
		t.Fatal("evidence for a different command was accepted")
	}
	for _, check := range []Check{{Kind: "shell", Package: "."}, {Kind: "go_test", Package: "../outside"}, {Kind: "go_test", Package: ".;curl attacker"}} {
		before := len(calls)
		if got := runner.Run(t.Context(), snapshot, check); got.Status != Unavailable || len(calls) != before {
			t.Fatalf("untrusted command ran: %+v", got)
		}
	}
	snapshot.SHA256 = strings.Repeat("0", 64)
	if got := runner.Run(t.Context(), snapshot, Check{Kind: "go_test", Package: "."}); got.Status != Unavailable || len(calls) != 2 {
		t.Fatalf("changed input ran: %+v", got)
	}
}

func TestExecutionCannotForgeSuccessOrHideFailedCleanup(t *testing.T) {
	for _, tc := range []struct {
		name    string
		exit    int
		cleanup int
		output  string
		want    Status
	}{
		{"failed check with success marker", 1, 0, `{"status":"checks passed"}`, Failed},
		{"runtime unavailable", 125, 0, "", Unavailable},
		{"dependencies unavailable", 75, 0, "", Unavailable},
		{"cleanup failed", 0, 1, "checks ran", Unavailable},
		{"output exceeded cap", 0, 0, strings.Repeat("x", 2048), Unavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := fixtureRunner(t)
			runner.command = func(_ context.Context, args []string, out io.Writer) (int, error) {
				if args[0] == "rm" {
					return tc.cleanup, nil
				}
				if _, err := out.Write([]byte(tc.output)); err != nil {
					t.Fatal(err)
				}
				return tc.exit, nil
			}
			if got := runner.Run(t.Context(), fixtureSnapshot(t), Check{Kind: "go_build", Package: "."}); got.Status != tc.want {
				t.Fatalf("untrusted output changed execution status: %+v", got)
			}
		})
	}
}

func TestExecutionCancellationStillCleansWithAnIndependentDeadline(t *testing.T) {
	runner := fixtureRunner(t)
	cleaned := false
	runner.command = func(ctx context.Context, args []string, _ io.Writer) (int, error) {
		if args[0] == "rm" {
			if ctx.Err() != nil {
				t.Fatal("cleanup inherited cancelled request")
			}
			cleaned = true
			return 0, nil
		}
		return -1, ctx.Err()
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	got := runner.Run(ctx, fixtureSnapshot(t), Check{Kind: "go_test", Package: "."})
	if !cleaned || got.Status != Unavailable || !strings.Contains(got.Reason, "cancelled") {
		t.Fatalf("cancelled job escaped cleanup: %+v cleaned=%v", got, cleaned)
	}
}

func TestSnapshotRejectsEscapesAndLinksBeforeWritingAnyFiles(t *testing.T) {
	for _, header := range []*tar.Header{
		{Name: "../escaped", Typeflag: tar.TypeReg},
		{Name: "/absolute", Typeflag: tar.TypeReg},
		{Name: "symlink", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"},
		{Name: "hardlink", Typeflag: tar.TypeLink, Linkname: "../escaped"},
		{Name: ".git/config", Typeflag: tar.TypeReg},
	} {
		snapshot := fixtureSnapshot(t, &tar.Header{Name: "safe.go", Typeflag: tar.TypeReg}, header)
		destination := t.TempDir()
		if err := ExtractSnapshot(snapshot, destination); err == nil {
			t.Fatalf("unsafe archive extracted: %+v", header)
		}
		entries, err := os.ReadDir(destination)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Fatal("snapshot was partially extracted before validation")
		}
	}
	destination := t.TempDir()
	if err := ExtractSnapshot(fixtureSnapshot(t), destination); err != nil {
		t.Fatalf("valid snapshot rejected: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destination, "go.mod")); err != nil {
		t.Fatal(err)
	}
}

func TestExecutionRefusesUnacceptedRiskAndMutableImages(t *testing.T) {
	for _, policy := range []Policy{{Image: "example/checker:latest", RiskAccepted: true}, {Image: "example/checker@sha256:" + strings.Repeat("a", 64)}} {
		if _, err := NewRunner(policy); err == nil {
			t.Fatalf("unsafe execution policy accepted: %+v", policy)
		}
	}
}
func runcPolicy(trusted bool) Policy {
	return Policy{Image: "example/checker@sha256:" + strings.Repeat("a", 64), Runtime: "runc", TrustedCorpus: trusted, RiskAccepted: true, TimeoutSeconds: 30, MemoryMiB: 1024, CPUs: 1, Pids: 64, DiskMiB: 128, MaxOutputBytes: 1024}
}

func TestSharedKernelRuntimeRequiresDeclaredTrustedCorpus(t *testing.T) {
	fixtureRunner(t)
	if _, err := NewRunner(runcPolicy(false)); err == nil {
		t.Fatal("runc accepted without a trusted-corpus declaration")
	}
	unknown := runcPolicy(true)
	unknown.Runtime = "kata"
	if _, err := NewRunner(unknown); err == nil {
		t.Fatal("unknown runtime accepted")
	}
	if _, err := NewRunner(runcPolicy(true)); err != nil {
		t.Fatalf("declared trusted corpus refused: %v", err)
	}
}

func TestRuncResultsNameTheirRuntimeAndCannotPassAsSandboxed(t *testing.T) {
	fixtureRunner(t)
	runc, err := NewRunner(runcPolicy(true))
	if err != nil {
		t.Fatal(err)
	}
	var flags []string
	runc.command = func(_ context.Context, args []string, _ io.Writer) (int, error) {
		if args[0] == "run" {
			flags = slices.Clone(args)
		}
		return 0, nil
	}
	snapshot := fixtureSnapshot(t)
	check := Check{Kind: "go_test", Package: "internal/http"}
	result := runc.Run(t.Context(), snapshot, check)
	if !slices.Contains(flags, "--runtime=runc") || slices.Contains(flags, "--runtime=runsc") || result.Runtime != "runc" {
		t.Fatalf("runtime not applied or not labelled: %v %+v", flags, result)
	}
	gvisor := fixtureRunner(t)
	if gvisor.policy.Runtime != "runsc" {
		t.Fatalf("default runtime is %q, want runsc", gvisor.policy.Runtime)
	}
	if err := gvisor.VerifyResult(result, snapshot, check, snapshot.Head); err == nil {
		t.Fatal("a runc result verified under a gVisor policy")
	}
}
