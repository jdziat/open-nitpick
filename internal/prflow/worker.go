package prflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/build"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jdziat/open-nitpick/internal/diff"
)

const (
	workerResponseLimit = 2 << 20
	workerInputLimit    = 64 << 20
)

var errWorkerResponseLimit = errors.New("flow worker response exceeds 2 MiB")

// AnalyzeIsolated runs flow extraction in a short-lived child process. The
// parent materializes the immutable snapshot first, so the child receives only
// bounded source bytes and cannot read the review's working tree or execute a
// repository package driver. A deadline kills and reaps the child promptly.
func AnalyzeIsolated(parent context.Context, req Request, executable string) (Result, error) {
	o := req.Options.withDefaults()
	finish := func(result *Result) { finalizeResult(result, o) }
	ctx, cancel := context.WithTimeout(parent, o.Timeout)
	defer cancel()
	base := Result{Version: 1, Status: StatusFailed, Base: req.Base, Head: req.Head}
	files, omissions, err := materialize(ctx, req, o)
	if err != nil {
		base.Status = StatusUnavailable
		base.Explanation = err.Error()
		base.Coverage.Omissions = omissions
		finish(&base)
		return base, nil
	}
	discovered, moduleOmissions := discoverModules(files)
	modules := append(append([]Module(nil), req.Modules...), discovered...)
	base.Coverage.Files = len(files)
	for _, file := range files {
		base.Coverage.Bytes += len(file.Content)
	}
	base.Coverage.Omissions = append(base.Coverage.Omissions, omissions...)
	base.Coverage.Omissions = append(base.Coverage.Omissions, moduleOmissions...)
	if base.Head.SHA == "" {
		base.Head.Snapshot = snapshotDigest(files)
	}
	if ctx.Err() != nil {
		base.Status = StatusPartial
		base.Explanation = "flow worker timed out before analysis"
		base.Coverage.Omissions = append(base.Coverage.Omissions, Omission{Reason: "timeout", Count: 1})
		finish(&base)
		return base, nil
	}

	payload, err := json.Marshal(workerRequest{Base: req.Base, Head: req.Head, Files: wireFiles(files), Changes: req.Changes, Modules: modules, Options: o})
	if err != nil {
		return base, fmt.Errorf("encode flow worker request: %w", err)
	}
	if len(payload) > workerInputLimit {
		base.Status = StatusPartial
		base.Explanation = "flow worker request exceeded input limit"
		base.Coverage.Omissions = append(base.Coverage.Omissions, Omission{Reason: "worker_input_limit", Count: 1})
		finish(&base)
		return base, nil
	}
	if executable == "" {
		executable = os.Args[0]
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return base, fmt.Errorf("resolve flow worker executable: %w", err)
	}
	tempDir, err := os.MkdirTemp("", "nitpick-flow-worker-")
	if err != nil {
		return base, fmt.Errorf("create flow worker directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()
	requestPath := filepath.Join(tempDir, "request.json")
	if err := os.WriteFile(requestPath, payload, 0o600); err != nil {
		return base, fmt.Errorf("write flow worker request: %w", err)
	}
	requestFile, err := os.Open(requestPath)
	if err != nil {
		return base, fmt.Errorf("open flow worker request: %w", err)
	}
	defer func() { _ = requestFile.Close() }()

	workerCtx, cancelWorker := context.WithCancel(ctx)
	defer cancelWorker()
	cmd := exec.CommandContext(workerCtx, executable, "__flow-worker")
	configureWorkerProcess(cmd)
	cmd.Cancel = func() error {
		killWorkerProcess(cmd)
		return nil
	}
	cmd.Dir = tempDir
	cmd.Stdin = requestFile
	cmd.Stdout = &limitedWriter{limit: workerResponseLimit, cancel: cancelWorker}
	var stderr bytes.Buffer
	cmd.Stderr = &limitedWriter{limit: 64 << 10, dst: &stderr, cancel: cancelWorker}
	cmd.WaitDelay = 250 * time.Millisecond
	cmd.Env = cleanWorkerEnv(tempDir)
	err = cmd.Run()
	stdout, tooLarge := cmd.Stdout.(*limitedWriter).Bytes()
	if ctx.Err() != nil {
		base.Status = StatusPartial
		base.Explanation = "flow worker timed out"
		base.Coverage.Omissions = append(base.Coverage.Omissions, Omission{Reason: "timeout", Count: 1})
		finish(&base)
		return base, nil
	}
	if tooLarge {
		return base, errWorkerResponseLimit
	}
	if err != nil {
		if text := stderr.String(); text != "" {
			return base, fmt.Errorf("flow worker: %w: %s", err, text)
		}
		return base, fmt.Errorf("flow worker: %w", err)
	}
	var result Result
	if err := json.Unmarshal(stdout, &result); err != nil {
		return base, fmt.Errorf("decode flow worker result: %w", err)
	}
	result.Coverage.Omissions = append(result.Coverage.Omissions, omissions...)
	if reducesCoverage(omissions) {
		if result.Status == StatusComplete || result.Status == StatusNoFlow || result.Status == StatusSkipped {
			result.Status = StatusPartial
		}
	}
	if hasOmission(omissions, "source_unavailable") && len(files) == 0 {
		result.Status = StatusUnavailable
		result.Explanation = "selected Go source could not be read"
	}
	finish(&result)
	return result, nil
}

func cleanWorkerEnv(tempDir string) []string {
	toolRoot := build.Default.GOROOT
	if _, err := os.Stat(filepath.Join(toolRoot, "bin", "go")); err != nil {
		if goPath, err := exec.LookPath("go"); err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			command := exec.CommandContext(ctx, goPath, "env", "GOROOT")
			command.Dir = tempDir
			command.Env = []string{"PATH=" + filepath.Dir(goPath), "GOTOOLCHAIN=local", "GOENV=off"}
			if output, err := command.Output(); err == nil {
				toolRoot = strings.TrimSpace(string(output))
			}
			cancel()
		}
	}
	cache := os.Getenv("GOCACHE")
	if cache == "" {
		if userCache, err := os.UserCacheDir(); err == nil {
			cache = filepath.Join(userCache, "go-build")
		}
	}
	return []string{
		"PATH=" + filepath.Join(toolRoot, "bin"),
		"GOROOT=" + toolRoot,
		"HOME=" + tempDir,
		"USERPROFILE=" + tempDir,
		"GOCACHE=" + cache,
		"GOPATH=" + filepath.Join(tempDir, "gopath"),
		"GOENV=off",
		"GOTOOLCHAIN=local",
		"GOPROXY=off",
		"GOSUMDB=off",
		"GOWORK=off",
		"GOFLAGS=-mod=readonly",
		"CGO_ENABLED=0",
		"GOPACKAGESDRIVER=off",
	}
}

type workerRequest struct {
	Base    Revision     `json:"base"`
	Head    Revision     `json:"head"`
	Files   []workerFile `json:"files"`
	Changes diff.Files   `json:"changes,omitempty"`
	Modules []Module     `json:"modules,omitempty"`
	Options Options      `json:"options"`
}

// RunWorker decodes one bounded request from r, analyzes only the supplied
// immutable files, and writes one versioned result to w. It is the child-side
// entrypoint used by the nitpick __flow-worker command.
func RunWorker(ctx context.Context, r io.Reader, w io.Writer) error {
	payload, err := io.ReadAll(io.LimitReader(r, workerInputLimit+1))
	if err != nil {
		return fmt.Errorf("read flow worker request: %w", err)
	}
	if len(payload) > workerInputLimit {
		return errors.New("flow worker request exceeds 64 MiB")
	}
	var request workerRequest
	if err := json.Unmarshal(payload, &request); err != nil {
		return fmt.Errorf("decode flow worker request: %w", err)
	}
	result, err := Analyze(ctx, Request{Base: request.Base, Head: request.Head, Files: sourceFiles(request.Files), Changes: request.Changes, Modules: request.Modules, Options: request.Options})
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(w)
	if err := encoder.Encode(result); err != nil {
		return fmt.Errorf("encode flow worker result: %w", err)
	}
	return nil
}

// workerFile is deliberately separate from SourceFile so the wire format can
// include the unexported deleted-symbol set without making it part of the
// public result schema.
type workerFile struct {
	Path           string   `json:"path"`
	ComparePath    string   `json:"compare_path,omitempty"`
	Content        []byte   `json:"content"`
	Package        string   `json:"package,omitempty"`
	ChangedLines   []int    `json:"changed_lines,omitempty"`
	Removed        bool     `json:"removed,omitempty"`
	RemovedSymbols []string `json:"removed_symbols,omitempty"`
}

func wireFiles(files []SourceFile) []workerFile {
	out := make([]workerFile, 0, len(files))
	for _, file := range files {
		wire := workerFile{Path: file.Path, ComparePath: file.comparePath, Content: append([]byte(nil), file.Content...), Package: file.Package, ChangedLines: append([]int(nil), file.ChangedLines...), Removed: file.Removed}
		for symbol := range file.removedSymbols {
			wire.RemovedSymbols = append(wire.RemovedSymbols, symbol)
		}
		sort.Strings(wire.RemovedSymbols)
		out = append(out, wire)
	}
	return out
}

func sourceFiles(wire []workerFile) []SourceFile {
	out := make([]SourceFile, 0, len(wire))
	for _, file := range wire {
		removed := map[string]bool{}
		for _, symbol := range file.RemovedSymbols {
			removed[symbol] = true
		}
		if len(removed) == 0 {
			removed = nil
		}
		out = append(out, SourceFile{Path: file.Path, comparePath: file.ComparePath, Content: append([]byte(nil), file.Content...), Package: file.Package, ChangedLines: append([]int(nil), file.ChangedLines...), Removed: file.Removed, removedSymbols: removed})
	}
	return out
}

type limitedWriter struct {
	limit  int
	dst    *bytes.Buffer
	cancel context.CancelFunc
	data   bytes.Buffer
	full   bool
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if w.full {
		return 0, errWorkerResponseLimit
	}
	remaining := w.limit - w.data.Len()
	if len(p) > remaining {
		if remaining > 0 {
			_, _ = w.data.Write(p[:remaining])
			if w.dst != nil {
				_, _ = w.dst.Write(p[:remaining])
			}
		}
		w.full = true
		if w.cancel != nil {
			w.cancel()
		}
		return remaining, errWorkerResponseLimit
	}
	_, _ = w.data.Write(p)
	if w.dst != nil {
		_, _ = w.dst.Write(p)
	}
	return len(p), nil
}

func (w *limitedWriter) Bytes() ([]byte, bool) { return w.data.Bytes(), w.full }
