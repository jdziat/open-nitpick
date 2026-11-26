package installscript

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// repoRoot is the directory holding go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test file")
	}
	dir := filepath.Dir(file)
	for range 6 {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("no go.mod above the test file")
	return ""
}

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// target is one os/arch pair a release is built for.
type target struct{ os, arch string }

func (t target) String() string { return t.os + "/" + t.arch }

var (
	targetsLine = regexp.MustCompile(`(?m)^TARGETS\s*:=\s*(.+)$`)
	distName    = regexp.MustCompile(`-o "dist/([^"]+)"`)
)

// releaseTargets reads the targets the Makefile builds.
func releaseTargets(t *testing.T) []target {
	t.Helper()
	m := targetsLine.FindStringSubmatch(readRepoFile(t, "Makefile"))
	if m == nil {
		t.Fatal("no TARGETS line in the Makefile")
	}
	var out []target
	for _, f := range strings.Fields(m[1]) {
		os, arch, ok := strings.Cut(f, "/")
		if !ok {
			t.Fatalf("target %q is not os/arch", f)
		}
		out = append(out, target{os, arch})
	}
	return out
}

// assetName is what the Makefile calls the binary built for tg, derived from the
// Makefile's own template and not copied here, so a change of naming there
// changes it here.
func assetName(t *testing.T, version string, tg target) string {
	t.Helper()
	mk := readRepoFile(t, "Makefile")
	m := distName.FindStringSubmatch(mk)
	if m == nil {
		t.Fatal("no dist output name in the Makefile")
	}
	ext := ""
	if tg.os == "windows" {
		if !strings.Contains(mk, "ext=.exe") {
			t.Fatal("the Makefile no longer gives windows binaries .exe: update the install scripts")
		}
		ext = ".exe"
	}
	r := strings.NewReplacer(
		"$(BINARY)", "nitpick",
		"$(VERSION)", version,
		"$${os}", tg.os,
		"$${arch}", tg.arch,
		"$$ext", ext,
	)
	name := r.Replace(m[1])
	if strings.ContainsAny(name, "$(){}") {
		t.Fatalf("the Makefile's binary name %q uses something the test does not understand", m[1])
	}
	return name
}

// fakeRelease serves one or more releases the way the install script fetches
// them: <base>/<tag>/<file>, plus /latest/<file> for the latest release's files.
type fakeRelease struct {
	srv    *httptest.Server
	mu     sync.Mutex
	files  map[string][]byte // "<tag>/<file>" to body
	latest string
	log    []string
}

func newFakeRelease(t *testing.T) *fakeRelease {
	t.Helper()
	f := &fakeRelease{files: map[string][]byte{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.log = append(f.log, r.URL.Path)
		latest := f.latest
		var key string
		switch {
		case strings.HasPrefix(r.URL.Path, "/dl/"):
			key = strings.TrimPrefix(r.URL.Path, "/dl/")
		case strings.HasPrefix(r.URL.Path, "/latest/") && latest != "":
			// The files of whichever release is the latest, as GitHub redirects
			// releases/latest/download/<file> to them.
			key = latest + "/" + strings.TrimPrefix(r.URL.Path, "/latest/")
		}
		body, ok := f.files[key]
		f.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeRelease) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.log...)
}

func (f *fakeRelease) put(tag, name string, body []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[tag+"/"+name] = body
}

func (f *fakeRelease) drop(tag, name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.files, tag+"/"+name)
}

func (f *fakeRelease) setLatest(tag string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.latest = tag
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// fakeBinary is a stand-in for the nitpick binary that says which release it
// came from.
func fakeBinary(version string) []byte {
	return []byte("#!/bin/sh\necho nitpick " + version + "\n")
}

// publish adds a release for every Makefile target, with a checksums.txt that
// is correct, and returns the binary it built for each target.
func (f *fakeRelease) publish(t *testing.T, version string) map[target][]byte {
	t.Helper()
	built := map[target][]byte{}
	var lines strings.Builder
	for _, tg := range releaseTargets(t) {
		name := assetName(t, version, tg)
		body := append(fakeBinary(version), []byte("# "+tg.String()+"\n")...)
		built[tg] = body
		f.put(version, name, body)
		f.put(version, name+".sigstore.json", []byte(`{"bundle":"x"}`))
		fmt.Fprintf(&lines, "%s  %s\n", sum(body), name)
	}
	f.put(version, "checksums.txt", []byte(lines.String()))
	return built
}

// opts says how one run of a script is set up.
type opts struct {
	rel     *fakeRelease
	sysname string // what uname -s says
	machine string // what uname -m says
	version string // VERSION, when set
	dir     string // INSTALL_DIR; a fresh directory when empty
	path    []string
	// tools names the real programs the script may find. Nil means the usual
	// set; an empty non-nil slice means none.
	tools []string
	// fake maps a program name to the body of a shell script standing in for it.
	fake  map[string]string
	stdin string
	// extraEnv is added to the environment as KEY=VALUE.
	extraEnv []string
	// script is the file to run; install.sh when empty.
	script string
}

type result struct {
	code           int
	stdout, stderr string
	dir            string
}

func (r result) out() string { return r.stdout + r.stderr }

var baseTools = []string{"awk", "sed", "head", "tr", "mktemp", "rm", "mkdir", "cp", "chmod", "mv"}

// checksumTool is whichever of the two programs the script accepts this machine has.
func checksumTool(t *testing.T) string {
	t.Helper()
	for _, n := range []string{"sha256sum", "shasum"} {
		if _, err := exec.LookPath(n); err == nil {
			return n
		}
	}
	t.Skip("neither sha256sum nor shasum is installed")
	return ""
}

func defaultTools(t *testing.T) []string {
	t.Helper()
	return append(append([]string(nil), baseTools...), "curl", checksumTool(t))
}

// toolbox builds a directory holding only the programs a case allows, so that
// "cosign is not installed" is a fact about the PATH and not about the machine.
func toolbox(t *testing.T, o opts) string {
	t.Helper()
	dir := t.TempDir()
	tools := o.tools
	if tools == nil {
		tools = defaultTools(t)
	}
	for _, name := range tools {
		real, err := exec.LookPath(name)
		if err != nil {
			t.Skipf("%s is not installed", name)
		}
		if err := os.Symlink(real, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	uname := fmt.Sprintf("#!/bin/sh\ncase \"$1\" in -m) echo %q ;; *) echo %q ;; esac\n", orDefault(o.machine, "x86_64"), orDefault(o.sysname, "Linux"))
	write(t, filepath.Join(dir, "uname"), uname)
	for name, body := range o.fake {
		write(t, filepath.Join(dir, name), "#!/bin/sh\n"+body)
	}
	return dir
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil { //nolint:gosec // a test script must be executable
		t.Fatal(err)
	}
}

// run executes the install script and reports what it did.
func run(t *testing.T, o opts) result {
	t.Helper()
	bin := toolbox(t, o)
	dir := o.dir
	if dir == "" {
		dir = filepath.Join(t.TempDir(), "bin")
	}
	script := o.script
	if script == "" {
		script = filepath.Join(repoRoot(t), "website", "install.sh")
	}
	pathParts := append([]string{bin}, o.path...)
	env := []string{
		"PATH=" + strings.Join(pathParts, string(os.PathListSeparator)),
		"HOME=" + t.TempDir(),
		"TMPDIR=" + t.TempDir(),
		"INSTALL_DIR=" + dir,
	}
	if o.rel != nil {
		env = append(env, "NITPICK_RELEASE_BASE="+o.rel.srv.URL+"/dl", "NITPICK_LATEST_BASE="+o.rel.srv.URL+"/latest")
	}
	if o.version != "" {
		env = append(env, "VERSION="+o.version)
	}
	env = append(env, o.extraEnv...)

	var cmd *exec.Cmd
	if o.stdin != "" {
		cmd = exec.Command("/bin/sh")
		cmd.Stdin = strings.NewReader(o.stdin)
	} else {
		cmd = exec.Command("/bin/sh", script)
	}
	cmd.Env = env
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	err := cmd.Run()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("running the script: %v", err)
		}
		code = ee.ExitCode()
	}
	return result{code: code, stdout: so.String(), stderr: se.String(), dir: dir}
}

// installed lists what is in the install directory, which must be nothing when
// an install is refused: not the binary, and not a temporary file either.
func installed(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
