package linters

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nitpick-oss/nitpick/internal/diff"
	"github.com/nitpick-oss/nitpick/internal/review"
	"golang.org/x/mod/modfile"
)

// CheckpointKey identifies isolated native analyzers on a clean Git checkout.
// Scripts, external rule sets, workspaces and unsupported analyzers run fresh:
// their transitive inputs are not bounded by this identity.
func (s *Set) CheckpointKey(ctx context.Context, files diff.Files) string {
	if ctx.Err() != nil {
		return ""
	}
	status, err := s.checkpointCommand(ctx, "git", "status", "--porcelain=v1", "--untracked-files=all", "--ignored=matching")
	if err != nil || len(status) != 0 {
		return ""
	}
	head, err := s.checkpointCommand(ctx, "git", "rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	tracked, err := s.checkpointCommand(ctx, "git", "ls-files", "--stage", "-v", "-z")
	if err != nil {
		return ""
	}
	for _, entry := range strings.Split(string(tracked), "\x00") {
		if entry != "" && !strings.HasPrefix(entry, "H 100644 ") && !strings.HasPrefix(entry, "H 100755 ") {
			return ""
		}
	}
	paths := reviewablePaths(s.cfg, files)
	var tools []any
	var checkpointRuff []*ruff
	for _, runner := range s.runners {
		if err := runner.Detect(ctx, s.repoRoot, paths); err != nil {
			if !errors.Is(err, errNoTargets) {
				return ""
			}
			continue
		}
		var command string
		switch r := runner.(type) {
		case *golangciLint:
			if r.cfg.Ref != "" || r.cfg.Err != nil {
				return ""
			}
			goInputs, ok := s.checkpointGoInputs(ctx, tracked)
			if !ok {
				return ""
			}
			tools = append(tools, []any{"go inputs", goInputs, r.ForceGosec, r.BuildTags})
			command = "golangci-lint"
		case *ruff:
			if r.cfg.Ref != "" || r.cfg.Err != nil {
				return ""
			}
			command = "ruff"
			tools = append(tools, "ruff native cache disabled")
			checkpointRuff = append(checkpointRuff, r)
		default:
			return ""
		}
		binary, ok := checkpointExecutable(command)
		if !ok {
			return ""
		}
		tools = append(tools, []any{runner.Name(), binary})
	}
	if len(tools) == 0 {
		return ""
	}
	env := os.Environ()
	// Shell bookkeeping changes between compatible CLI invocations.
	env = slices.DeleteFunc(env, func(value string) bool { return strings.HasPrefix(value, "_=") || strings.HasPrefix(value, "SHLVL=") })
	slices.Sort(env)
	// Change this version when isolated commands or normalization change.
	data, err := json.Marshal([]any{"isolated-analyzers-v1", head, tracked, s.cfg, paths, tools, env})
	if err != nil || ctx.Err() != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	// A native .ruff_cache would dirty the checkout and defeat the snapshot.
	for _, r := range checkpointRuff {
		r.checkpoint = true
	}
	return hex.EncodeToString(sum[:])
}

// RestoreCheckpoint restores the accounting that belongs to reused findings.
func (s *Set) RestoreCheckpoint(statuses []review.LinterStatus, discarded []review.LinterDiscard, uncovered []review.LinterUncovered) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statuses = append([]review.LinterStatus(nil), statuses...)
	s.discarded = append([]review.LinterDiscard(nil), discarded...)
	s.uncovered = append([]review.LinterUncovered(nil), uncovered...)
}

func (s *Set) checkpointCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = s.repoRoot
	return cmd.Output()
}

func checkpointExecutable(name string) (string, bool) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", false
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", false
	}
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer func() { _ = f.Close() }()
	var header [4]byte
	if _, err := io.ReadFull(f, header[:]); err != nil {
		return "", false
	}
	native := string(header[:]) == "\x7fELF" || string(header[:2]) == "MZ"
	switch string(header[:]) {
	case "\xfe\xed\xfa\xce", "\xfe\xed\xfa\xcf", "\xce\xfa\xed\xfe", "\xcf\xfa\xed\xfe", "\xca\xfe\xba\xbe", "\xbe\xba\xfe\xca":
		native = true
	}
	if !native {
		return "", false
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", false
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		return "", false
	}
	return path + "\x00" + hex.EncodeToString(hash.Sum(nil)), true
}

func (s *Set) checkpointGoInputs(ctx context.Context, tracked []byte) ([]any, bool) {
	goBinary, ok := checkpointExecutable("go")
	if !ok {
		return nil, false
	}
	data, _, err := runCommand(ctx, s.repoRoot, s.repoRoot, "go", analyzerEnv(map[string]string{"GOTOOLCHAIN": "local"}), "env", "-json")
	var env map[string]string
	if err != nil || json.Unmarshal(data, &env) != nil || (env["GOWORK"] != "" && env["GOWORK"] != "off") {
		return nil, false
	}
	if env["CGO_ENABLED"] != "0" || env["GO111MODULE"] == "off" || env["GOMOD"] == "" || env["GOMOD"] == os.DevNull || env["GOCACHEPROG"] != "" {
		return nil, false
	}
	if driver := os.Getenv("GOPACKAGESDRIVER"); driver != "off" {
		if driver != "" {
			return nil, false
		}
		if discovered, _ := exec.LookPath("gopackagesdriver"); discovered != "" {
			return nil, false
		}
	}
	for _, flag := range strings.Fields(env["GOFLAGS"]) {
		if !strings.HasPrefix(flag, "-tags=") && flag != "-mod=readonly" && flag != "-mod=vendor" && flag != "-mod=mod" {
			return nil, false
		}
	}
	// This derived flag contains a new temporary build directory on each call.
	delete(env, "GOGCCFLAGS")
	moduleFiles := map[string]bool{}
	for _, entry := range strings.Split(string(tracked), "\x00") {
		_, path, _ := strings.Cut(entry, "\t")
		if filepath.Base(path) == "go.mod" {
			moduleFiles[filepath.Join(s.repoRoot, path)] = true
		}
	}
	foundModule := false
	for _, entry := range strings.Split(string(tracked), "\x00") {
		_, path, _ := strings.Cut(entry, "\t")
		if filepath.Base(path) != "go.mod" {
			continue
		}
		foundModule = true
		name := filepath.Join(s.repoRoot, path)
		data, err := os.ReadFile(name)
		if err != nil {
			return nil, false
		}
		mod, err := modfile.Parse(name, data, nil)
		if err != nil {
			return nil, false
		}
		for _, replace := range mod.Replace {
			if replace.New.Version != "" {
				continue
			}
			target := replace.New.Path
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(name), target)
			}
			if !moduleFiles[filepath.Join(target, "go.mod")] {
				return nil, false
			}
		}
	}
	return []any{goBinary, env}, foundModule
}
