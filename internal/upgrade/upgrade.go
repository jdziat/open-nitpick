package upgrade

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/mod/semver"
)

// Where releases are served from, and the variables that point elsewhere: a
// mirror, or a test's fake release server. The install scripts read the same
// two. A mirror is not trusted with more than the files: what it serves still
// has to verify against the release workflow's signature.
const (
	DefaultReleaseBase = "https://github.com/nitpick-oss/nitpick/releases/download"
	DefaultLatestBase  = "https://github.com/nitpick-oss/nitpick/releases/latest/download"
	EnvReleaseBase     = "NITPICK_RELEASE_BASE"
	EnvLatestBase      = "NITPICK_LATEST_BASE"
	releasePage        = "https://github.com/nitpick-oss/nitpick/releases/tag/"
)

// Bounds on what is read from the network. An asset is tens of megabytes; the
// rest are a few kilobytes.
const (
	maxAsset = 400 << 20
	maxSmall = 1 << 20
)

// Config is one upgrade. Everything that touches the world is a field, so a
// test points it at a fake release server and a temporary directory.
type Config struct {
	ReleaseBase string // assets are <base>/<tag>/<asset>; default DefaultReleaseBase
	LatestBase  string // the latest release's checksums.txt is <base>/checksums.txt
	Client      *http.Client

	// NewVerifier builds the verifier, and is called only once an install has a
	// downloaded asset whose checksum matched. A check, or a refusal before
	// that, never needs the Sigstore trusted root.
	NewVerifier func(context.Context) (Verifier, error)

	GOOS, GOARCH string
	Current      string // the version the running binary reports
	Target       string // the running binary
	Version      string // a release to install instead of the latest; may be older
	Check        bool   // report, change nothing
	Out          io.Writer
	TempDir      string // where the download lands; default the system's
}

// Result says what happened, for the caller and for tests.
type Result struct {
	Current   string
	Latest    string // the release that was looked at: the latest, or the pinned one
	Available bool   // a newer release than Current exists
	Installed bool   // the binary was replaced
}

var versionPattern = regexp.MustCompile(`^v[0-9][A-Za-z0-9.+-]*$`)

// Run looks up a release and, unless Check is set, installs it over Target.
func Run(ctx context.Context, c Config) (Result, error) {
	c = c.withDefaults()
	res := Result{Current: c.Current}

	if c.Check && c.Version != "" {
		return res, errors.New("-check looks at the latest release and cannot be combined with -version")
	}
	if err := c.validateBases(); err != nil {
		return res, err
	}
	if _, err := assetFor("v0.0.0", c.GOOS, c.GOARCH); err != nil {
		return res, err
	}

	target := c.Version
	if target != "" {
		if !strings.HasPrefix(target, "v") {
			target = "v" + target
		}
		if !versionPattern.MatchString(target) {
			return res, fmt.Errorf("%q is not a release version; write it like v2.10.0", c.Version)
		}
	} else {
		latest, err := c.latest(ctx)
		if err != nil {
			return res, err
		}
		target = latest
	}
	res.Latest = target

	current := c.Current
	released := semver.IsValid(current)
	if c.Version == "" {
		switch {
		case !released && c.Check:
			c.say("nitpick %s is not a release build; the latest release is %s.\n", current, target)
			return res, nil
		case !released:
			return res, fmt.Errorf("this binary reports %q, which is not a release version, so there is nothing to compare; "+
				"run `nitpick upgrade -version %s` to replace it with that release", current, target)
		}
		cmp := semver.Compare(target, current)
		res.Available = cmp > 0
		switch {
		case cmp == 0:
			c.say("nitpick %s is up to date.\n", current)
			return res, nil
		case cmp < 0:
			c.say("nitpick %s is newer than the latest release, %s. Nothing to do.\n", current, target)
			return res, nil
		case c.Check:
			c.say("nitpick %s is installed; %s is available: %s%s\nRun `nitpick upgrade` to install it.\n", current, target, releasePage, target)
			return res, nil
		}
	} else {
		res.Available = released && semver.Compare(target, current) > 0
		if released && target == current {
			c.say("nitpick %s is already installed.\n", current)
			return res, nil
		}
	}

	if err := preflight(filepath.Dir(c.Target)); err != nil {
		return res, err
	}
	if err := c.install(ctx, target); err != nil {
		return res, err
	}
	res.Installed = true
	c.say("Upgraded nitpick %s to %s.\n%s%s\n", current, target, releasePage, target)
	return res, nil
}

func (c Config) withDefaults() Config {
	if c.ReleaseBase == "" {
		c.ReleaseBase = DefaultReleaseBase
	}
	if c.LatestBase == "" {
		c.LatestBase = DefaultLatestBase
	}
	if c.Client == nil {
		c.Client = &http.Client{}
	}
	if c.Out == nil {
		c.Out = io.Discard
	}
	return c
}

// say writes progress. A writer that has gone away must not fail an upgrade.
func (c Config) say(format string, args ...any) { _, _ = fmt.Fprintf(c.Out, format, args...) }

// validateBases refuses a plain-HTTP release source unless it is on this
// machine. The version to install is read from it, and over a network that
// can be rewritten, the way to be served an older release is to be served an
// older release's checksums.
func (c Config) validateBases() error {
	for _, raw := range []string{c.ReleaseBase, c.LatestBase} {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return fmt.Errorf("%q is not a release URL", raw)
		}
		if u.Scheme == "https" {
			continue
		}
		host := u.Hostname()
		if u.Scheme == "http" && (host == "localhost" || host == "127.0.0.1" || host == "::1") {
			continue
		}
		return fmt.Errorf("%q is not served over HTTPS; refusing to read a release from it", raw)
	}
	return nil
}

// supported lists the platforms a release is built for.
var supported = map[string]bool{
	"linux/amd64": true, "linux/arm64": true,
	"darwin/amd64": true, "darwin/arm64": true,
	"windows/amd64": true,
}

// assetFor is the file a release publishes for a platform.
func assetFor(version, goos, goarch string) (string, error) {
	if !supported[goos+"/"+goarch] {
		return "", fmt.Errorf("there is no release build for %s/%s", goos, goarch)
	}
	name := fmt.Sprintf("nitpick_%s_%s_%s", version, goos, goarch)
	if goos == "windows" {
		name += ".exe"
	}
	return name, nil
}

// latest finds the newest release from its checksums.txt, which names every
// asset with its version, and not from the GitHub API, which limits an
// unauthenticated address to 60 requests an hour.
func (c Config) latest(ctx context.Context) (string, error) {
	sums, err := c.get(ctx, c.LatestBase+"/checksums.txt", maxSmall)
	if err != nil {
		return "", fmt.Errorf("find the latest release: %w", err)
	}
	ext := ""
	if c.GOOS == "windows" {
		ext = ".exe"
	}
	pattern := regexp.MustCompile(`^[0-9a-fA-F]{64}\s+\*?nitpick_(v[A-Za-z0-9.+-]+?)_` +
		regexp.QuoteMeta(c.GOOS+"_"+c.GOARCH+ext) + `\s*$`)
	for _, line := range strings.Split(string(sums), "\n") {
		if m := pattern.FindStringSubmatch(strings.TrimRight(line, "\r")); m != nil && versionPattern.MatchString(m[1]) {
			return m[1], nil
		}
	}
	return "", fmt.Errorf("the latest release has no build for %s/%s", c.GOOS, c.GOARCH)
}

// install downloads, verifies and swaps in one release.
func (c Config) install(ctx context.Context, version string) error {
	asset, err := assetFor(version, c.GOOS, c.GOARCH)
	if err != nil {
		return err
	}
	work, err := os.MkdirTemp(c.TempDir, "nitpick-upgrade-")
	if err != nil {
		return fmt.Errorf("make a place to download to: %w", err)
	}
	defer func() { _ = os.RemoveAll(work) }()

	c.say("Downloading %s.\n", asset)
	downloaded := filepath.Join(work, asset)
	digest, err := c.download(ctx, c.ReleaseBase+"/"+version+"/"+asset, downloaded)
	if err != nil {
		return fmt.Errorf("download %s: %w", asset, err)
	}

	sums, err := c.get(ctx, c.ReleaseBase+"/"+version+"/checksums.txt", maxSmall)
	if err != nil {
		return fmt.Errorf("download checksums.txt for %s: %w", version, err)
	}
	want, ok := checksumFor(sums, asset)
	if !ok {
		return fmt.Errorf("checksums.txt does not list %s; nothing was installed", asset)
	}
	if got := hex.EncodeToString(digest); got != want {
		return fmt.Errorf("checksum mismatch for %s (expected %s, got %s); nothing was installed", asset, want, got)
	}

	bundle, err := c.get(ctx, c.ReleaseBase+"/"+version+"/"+asset+".sigstore.json", maxSmall)
	if err != nil {
		return fmt.Errorf("download the signature bundle for %s: %w; nothing was installed", asset, err)
	}
	if c.NewVerifier == nil {
		return errors.New("no signature verifier is configured; nothing was installed")
	}
	verifier, err := c.NewVerifier(ctx)
	if err != nil {
		return fmt.Errorf("%w; nothing was installed", err)
	}
	if err := verifier.Verify(bundle, digest); err != nil {
		return fmt.Errorf("%s: %w; nothing was installed", asset, err)
	}
	c.say("Verified the checksum and the Sigstore signature.\n")

	staged, err := stage(downloaded, c.Target)
	if err != nil {
		return err
	}
	if err := swap(staged, c.Target, c.GOOS); err != nil {
		_ = os.Remove(staged) // best effort: the swap's error is the one to report
		return err
	}
	return nil
}

// checksumFor reads the digest checksums.txt lists for exactly this asset. The
// name is compared as written, case included, so a checksum for some other
// file cannot stand in for it.
func checksumFor(sums []byte, asset string) (string, bool) {
	line := regexp.MustCompile(`^([0-9a-fA-F]{64})\s+\*?(\S.*?)\s*$`)
	for _, l := range strings.Split(string(sums), "\n") {
		if m := line.FindStringSubmatch(strings.TrimRight(l, "\r")); m != nil && m[2] == asset {
			return strings.ToLower(m[1]), true
		}
	}
	return "", false
}

// get reads a small file from the release source.
func (c Config) get(ctx context.Context, rawURL string, limit int64) ([]byte, error) {
	resp, err := c.do(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s is larger than the %d bytes expected", rawURL, limit)
	}
	return data, nil
}

// download writes an asset to dst and returns its SHA-256 as it goes.
func (c Config) download(ctx context.Context, rawURL, dst string) ([]byte, error) {
	resp, err := c.do(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxAsset+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return nil, err
	}
	if n > maxAsset {
		return nil, fmt.Errorf("the download is larger than the %d bytes expected", int64(maxAsset))
	}
	if n == 0 {
		return nil, errors.New("the download is empty")
	}
	return h.Sum(nil), nil
}

// do makes one GET and returns the response only when it is a 200; the caller
// closes the body.
func (c Config) do(ctx context.Context, rawURL string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "nitpick-upgrade")
	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("%s: %s", rawURL, resp.Status)
	}
	return resp, nil
}

// preflight proves the directory can be written before anything is downloaded,
// so a binary somebody else owns is refused in a second and not after fifty
// megabytes. The message names the directory, because "permission denied" does
// not say whose it is.
func preflight(dir string) error {
	f, err := os.CreateTemp(dir, ".nitpick-upgrade-check-*")
	if err != nil {
		// Only the reason: the OS error repeats the path of the file it tried
		// to make, and the directory is named once below, as the thing to fix.
		var pathErr *fs.PathError
		if errors.As(err, &pathErr) {
			err = pathErr.Err
		}
		return fmt.Errorf("cannot write to %s: %w\n"+
			"nitpick is installed where this account cannot change it. If a package manager put it there, "+
			"upgrade it with that package manager. If go install did, run go install again. "+
			"Otherwise run this with permission to write to %s, or reinstall into a directory you own",
			dir, err, dir)
	}
	name := f.Name()
	closeErr := f.Close()
	if err := os.Remove(name); err != nil {
		return err
	}
	return closeErr
}

// stage copies the verified download beside the binary it will replace, under a
// name no one runs, with the old binary's permissions. A rename is only atomic
// within one filesystem, which is why it is here and not in the temporary
// directory.
func stage(downloaded, target string) (string, error) {
	mode := os.FileMode(0o755)
	if info, err := os.Stat(target); err == nil {
		mode = info.Mode().Perm() | 0o111
	}
	src, err := os.Open(downloaded)
	if err != nil {
		return "", err
	}
	defer func() { _ = src.Close() }()
	dst, err := os.CreateTemp(filepath.Dir(target), ".nitpick-upgrade-*")
	if err != nil {
		return "", fmt.Errorf("cannot write to %s: %w", filepath.Dir(target), err)
	}
	name := dst.Name()
	_, err = io.Copy(dst, src)
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(name, mode)
	}
	if err != nil {
		_ = os.Remove(name)
		return "", fmt.Errorf("stage the new binary in %s: %w", filepath.Dir(target), err)
	}
	return name, nil
}

// staleSuffix is what the running executable is renamed to on Windows.
const staleSuffix = ".old"

// swap puts the staged file where the binary is.
//
// Elsewhere a rename over a running executable is fine and atomic. Windows
// refuses to replace or delete a running .exe but allows renaming it, so it is
// moved aside, the new one takes its name, and the old one is deleted by
// RemoveStale the next time nitpick starts, when nothing is running it. An
// earlier upgrade's leftover is replaced by that rename.
func swap(staged, target, goos string) error {
	if goos != "windows" {
		if err := os.Rename(staged, target); err != nil {
			return fmt.Errorf("replace %s: %w", target, err)
		}
		return nil
	}
	old := target + staleSuffix
	if err := os.Rename(target, old); err != nil {
		return fmt.Errorf("move %s aside: %w", target, err)
	}
	if err := os.Rename(staged, target); err != nil {
		if rerr := os.Rename(old, target); rerr != nil {
			return fmt.Errorf("install %s failed (%w) and the old binary could not be put back (%w); it is at %s", target, err, rerr, old)
		}
		return fmt.Errorf("install %s: %w", target, err)
	}
	return nil
}

// RemoveStale deletes the binary an earlier upgrade moved aside. It runs at
// start-up and ignores every error: the file may still be a running process, in
// which case the next start tries again.
func RemoveStale(target string) {
	_ = os.Remove(target + staleSuffix)
}
