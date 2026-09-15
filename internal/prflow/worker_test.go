package prflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRunWorkerRoundTripsBoundedSource(t *testing.T) {
	payload, err := json.Marshal(workerRequest{
		Files:   []workerFile{{Path: "p.go", Content: []byte("package p\nfunc Changed() {}\n"), ChangedLines: []int{2}}},
		Options: Options{Timeout: time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := RunWorker(context.Background(), bytes.NewReader(payload), &output); err != nil {
		t.Fatal(err)
	}
	var result Result
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Version != 1 || len(result.Nodes) != 1 || result.Nodes[0].Label != "p.Changed" {
		t.Fatalf("worker result = %+v", result)
	}
}

func TestAnalyzeIsolatedTimesOutAndReapsWorker(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell process-group test is Unix-specific")
	}
	dir := t.TempDir()
	executable := filepath.Join(dir, "sleep-worker")
	pidFile := filepath.Join(dir, "child.pid")
	script := "#!/bin/sh\n/bin/sleep 5 & child=$!\nprintf '%s' \"$child\" > " + pidFile + "\nwait\n"
	if err := os.WriteFile(executable, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resultCh := make(chan struct {
		result Result
		err    error
	}, 1)
	go func() {
		result, err := AnalyzeIsolated(ctx, Request{
			Files:   []SourceFile{{Path: "p.go", Content: []byte("package p\nfunc Changed() {}\n"), ChangedLines: []int{2}}},
			Options: Options{Timeout: 5 * time.Second},
		}, executable)
		resultCh <- struct {
			result Result
			err    error
		}{result, err}
	}()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(pidFile); err == nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := os.Stat(pidFile); err != nil {
		t.Fatal("worker did not record a child pid")
	}
	start := time.Now()
	cancel()
	var outcome struct {
		result Result
		err    error
	}
	select {
	case outcome = <-resultCh:
	case <-time.After(2 * time.Second):
		t.Fatal("worker was not reaped promptly")
	}
	if outcome.err != nil {
		t.Fatal(outcome.err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("worker was not reaped promptly: %s", elapsed)
	}
	if outcome.result.Status != StatusPartial || !hasOmission(outcome.result.Coverage.Omissions, "timeout") {
		t.Fatalf("timeout result = %+v", outcome.result)
	}
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		data, readErr := os.ReadFile(pidFile)
		if readErr == nil {
			pidText := strings.TrimSpace(string(data))
			if pidText == "" {
				time.Sleep(time.Millisecond)
				continue
			}
			pid, parseErr := strconv.Atoi(pidText)
			if parseErr != nil {
				t.Fatal(parseErr)
			}
			process, findErr := os.FindProcess(pid)
			if findErr != nil {
				return
			}
			if signalErr := process.Signal(syscall.Signal(0)); signalErr != nil {
				return
			}
			if stat, statErr := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat")); statErr == nil && strings.Contains(string(stat), ") Z ") {
				return
			}
			time.Sleep(10 * time.Millisecond)
			continue
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("worker grandchild is still running after timeout")
}

func TestAnalyzeIsolatedUsesCleanWorkerEnvironment(t *testing.T) {
	t.Setenv("LLM_API_KEY", "do-not-pass-this")
	t.Setenv("PATH", "/untrusted/bin")
	env := cleanWorkerEnv(t.TempDir())
	joined := strings.Join(env, "\n")
	for _, want := range []string{"GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "GOWORK=off", "GOFLAGS=-mod=readonly", "CGO_ENABLED=0", "GOPACKAGESDRIVER=off"} {
		if !strings.Contains(joined, want) {
			t.Errorf("worker environment missing %q: %s", want, joined)
		}
	}
	if strings.Contains(joined, "LLM_API_KEY") {
		t.Fatal("worker environment copied model credentials")
	}
	if strings.Contains(joined, "/untrusted/bin") {
		t.Fatal("worker environment copied the host PATH")
	}
}

func TestAnalyzeIsolatedBoundsWorkerResponse(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture requires Unix")
	}
	dir := t.TempDir()
	executable := filepath.Join(dir, "large-worker")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nprintf '%03000000d' 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := AnalyzeIsolated(context.Background(), Request{
		Files:   []SourceFile{{Path: "p.go", Content: []byte("package p\nfunc Changed() {}\n"), ChangedLines: []int{2}}},
		Options: Options{Timeout: time.Second},
	}, executable)
	if !errors.Is(err, errWorkerResponseLimit) {
		t.Fatalf("large worker response error = %v, want response limit", err)
	}
}

func TestAnalyzeIsolatedCountsModuleFailureOnce(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture requires Unix")
	}
	request := Request{Files: []SourceFile{
		{Path: "go.mod", Content: []byte("invalid module metadata")},
		{Path: "p.go", Content: []byte("package p\nfunc Changed(){ helper() }\nfunc helper(){}\n"), ChangedLines: []int{2}},
	}}
	expected, err := Analyze(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	response, err := json.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(t.TempDir(), "reply-worker")
	script := "#!/bin/sh\nprintf '%s' '" + strings.ReplaceAll(string(response), "'", "'\"'\"'") + "'\n"
	if err := os.WriteFile(executable, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	result, err := AnalyzeIsolated(context.Background(), request, executable)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, omission := range result.Coverage.Omissions {
		if omission.Reason == "module_metadata_invalid" {
			count += omission.Count
		}
	}
	if result.Status != StatusPartial || count != 1 {
		t.Fatalf("worker module coverage = %+v; want one invalid metadata omission", result.Coverage)
	}
}
