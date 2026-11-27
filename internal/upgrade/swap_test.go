package upgrade

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func contents(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Elsewhere the new file takes the name in one rename and nothing is left
// beside it.
func TestOutsideWindowsTheStagedFileTakesTheBinarysName(t *testing.T) {
	dir := t.TempDir()
	target, staged := filepath.Join(dir, "nitpick"), filepath.Join(dir, ".staged")
	writeFile(t, target, "old")
	writeFile(t, staged, "new")
	if err := swap(staged, target, "linux"); err != nil {
		t.Fatal(err)
	}
	if contents(t, target) != "new" {
		t.Error("the binary was not replaced")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("%d files left in the directory, want only the binary", len(entries))
	}
}

// On Windows the running executable cannot be replaced, so it is renamed aside,
// the new file takes its name, and RemoveStale deletes the old one later.
func TestOnWindowsTheOldBinaryIsMovedAsideAndRemovedLater(t *testing.T) {
	dir := t.TempDir()
	target, staged := filepath.Join(dir, "nitpick.exe"), filepath.Join(dir, ".staged")
	writeFile(t, target, "old")
	writeFile(t, staged, "new")
	if err := swap(staged, target, "windows"); err != nil {
		t.Fatal(err)
	}
	if contents(t, target) != "new" {
		t.Error("the new binary is not in place")
	}
	if got := contents(t, target+staleSuffix); got != "old" {
		t.Errorf("the old binary was not kept aside: %q", got)
	}
	RemoveStale(target)
	if _, err := os.Stat(target + staleSuffix); !os.IsNotExist(err) {
		t.Errorf("the old binary was not removed on the next start: %v", err)
	}
	if contents(t, target) != "new" {
		t.Error("RemoveStale touched the new binary")
	}
}

// A second upgrade while the first one's old file is still there replaces it.
func TestAnEarlierOldBinaryDoesNotBlockTheNextUpgrade(t *testing.T) {
	dir := t.TempDir()
	target, staged := filepath.Join(dir, "nitpick.exe"), filepath.Join(dir, ".staged")
	writeFile(t, target, "v2")
	writeFile(t, target+staleSuffix, "v1")
	writeFile(t, staged, "v3")
	if err := swap(staged, target, "windows"); err != nil {
		t.Fatal(err)
	}
	if contents(t, target) != "v3" || contents(t, target+staleSuffix) != "v2" {
		t.Errorf("target=%q old=%q", contents(t, target), contents(t, target+staleSuffix))
	}
}

// If the new file cannot be put in place the old binary comes back, so a failed
// Windows upgrade is not a missing nitpick.
func TestAFailedWindowsSwapPutsTheOldBinaryBack(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "nitpick.exe")
	writeFile(t, target, "old")
	err := swap(filepath.Join(dir, ".does-not-exist"), target, "windows")
	if err == nil {
		t.Fatal("a swap with nothing to install succeeded")
	}
	if contents(t, target) != "old" {
		t.Errorf("the old binary was not restored: %q", contents(t, target))
	}
	if _, err := os.Stat(target + staleSuffix); !os.IsNotExist(err) {
		t.Error("a restored upgrade left the old file behind")
	}
}

func TestRemovingAStaleBinaryThatIsNotThereIsQuiet(t *testing.T) {
	RemoveStale(filepath.Join(t.TempDir(), "nitpick.exe")) // must not panic or fail
}

// The helper this test file re-runs itself as: a process that holds its own
// executable open until its stdin closes.
func TestHelperProcessHoldsItsExecutable(t *testing.T) {
	if os.Getenv("NITPICK_UPGRADE_HELPER") != "1" {
		t.Skip("not the helper process")
	}
	if _, err := os.Stdout.WriteString("ready\n"); err != nil {
		t.Fatal(err)
	}
	// Returns when the parent closes stdin; the error is the end of input.
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}

// The case Windows is strict about: the binary being replaced is running. On
// Windows this fails for any swap that overwrites or deletes it; here it passes
// on every platform because the swap only renames.
func TestABinaryThatIsRunningCanBeReplaced(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	name := "nitpick"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	target := filepath.Join(dir, name)
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, data, 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(target, "-test.run=TestHelperProcessHoldsItsExecutable")
	cmd.Env = append(os.Environ(), "NITPICK_UPGRADE_HELPER=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	t.Cleanup(func() { _ = stdin.Close(); <-done })
	go func() { _ = cmd.Wait(); close(done) }()
	ready := make(chan string, 1)
	go func() { line, _ := bufio.NewReader(stdout).ReadString('\n'); ready <- line }()
	select {
	case line := <-ready:
		if line != "ready\n" {
			t.Fatalf("the helper said %q", line)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the helper never started")
	}

	staged := filepath.Join(dir, ".staged")
	writeFile(t, staged, "the new binary")
	if err := swap(staged, target, runtime.GOOS); err != nil {
		t.Fatalf("a running binary could not be replaced: %v", err)
	}
	if contents(t, target) != "the new binary" {
		t.Error("the new binary is not in place")
	}

	_ = stdin.Close()
	<-done
	RemoveStale(target)
	if _, err := os.Stat(target + staleSuffix); !os.IsNotExist(err) {
		t.Errorf("the old binary was not removable once it stopped: %v", err)
	}
}
