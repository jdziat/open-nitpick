package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestMergeQueueActionReviewsTheEventRange(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Action runs a POSIX shell script")
	}
	data, err := os.ReadFile("../../action.yml")
	if err != nil {
		t.Fatal(err)
	}
	var action struct {
		Runs struct {
			Steps []struct {
				Name string
				Run  string
			}
		}
	}
	if err := yaml.Unmarshal(data, &action); err != nil {
		t.Fatal(err)
	}
	script := ""
	for _, step := range action.Runs.Steps {
		if step.Name == "Review" {
			script = step.Run
		}
	}
	if script == "" {
		t.Fatal("no review step")
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, "nitpick")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$CAPTURE\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, head := range []string{"beef0002", ""} {
		capture := filepath.Join(t.TempDir(), "args")
		cmd := exec.Command("bash", "-c", script)
		cmd.Env = append([]string{"PATH=" + dir + ":" + os.Getenv("PATH"), "EVENT_NAME=merge_group", "MERGE_BASE=beef0001", "MERGE_HEAD=" + head,
			"CAPTURE=" + capture, "GITHUB_WORKSPACE=" + dir, "GITHUB_OUTPUT=" + filepath.Join(dir, "output")},
			"NITPICK_CONFIG=", "NITPICK_FAIL_ON=", "NITPICK_INSTRUCTION=", "NITPICK_DRY_RUN=false", "NITPICK_SKIP_DRAFTS=false", "NITPICK_COMMAND=review", "PR_NUMBER=", "IS_FORK=false", "NITPICK_ARGS=")
		output, err := cmd.CombinedOutput()
		if head == "" {
			if err == nil {
				t.Fatal("missing merge-group SHA succeeded")
			}
			continue
		}
		if err != nil {
			t.Fatalf("review step: %v: %s", err, output)
		}
		args, err := os.ReadFile(capture)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(args), "-base\nbeef0001\n-head\nbeef0002\n") {
			t.Fatalf("queued range missing: %s", args)
		}
	}
}
