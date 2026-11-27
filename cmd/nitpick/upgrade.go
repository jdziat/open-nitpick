package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/nitpick-oss/nitpick/internal/upgrade"
)

// runUpgrade replaces this binary with a release. It runs only when invoked:
// nothing here checks for a newer version in the background.
func runUpgrade(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	var (
		pin   string
		check bool
	)
	fs.StringVar(&pin, "version", "", "install this release, such as v2.10.0, instead of the latest; may be older")
	fs.BoolVar(&check, "check", false, "report whether a newer release exists and install nothing")
	fs.Usage = func() {
		_, _ = fmt.Fprint(fs.Output(), `Usage: nitpick upgrade [flags]

Replaces this binary with the latest release. The download is checked against
the release's checksums.txt and its Sigstore signature, and nothing is written
beside the binary until both pass, so a refused upgrade leaves it as it was.

Flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("nitpick upgrade takes no arguments, got %q", fs.Arg(0))
	}

	target, err := installedBinary()
	if err != nil {
		return err
	}
	_, err = upgrade.Run(ctx, upgrade.Config{
		ReleaseBase: os.Getenv(upgrade.EnvReleaseBase),
		LatestBase:  os.Getenv(upgrade.EnvLatestBase),
		Client:      &http.Client{Timeout: 10 * time.Minute},
		NewVerifier: func(context.Context) (upgrade.Verifier, error) {
			// Fetched here, after a download whose checksum matched, so a check
			// or an early refusal never needs the Sigstore trusted root.
			trusted, err := upgrade.FetchTrustedRoot()
			if err != nil {
				return nil, err
			}
			return upgrade.NewSigstoreVerifier(trusted), nil
		},
		GOOS:    runtime.GOOS,
		GOARCH:  runtime.GOARCH,
		Current: version,
		Target:  target,
		Version: pin,
		Check:   check,
		Out:     out,
	})
	return err
}

// installedBinary is the file to replace: this executable, with symbolic links
// followed, because a link to the binary is not the thing a package manager or
// an install script put there.
func installedBinary() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("find this binary: %w", err)
	}
	real, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("find this binary: %w", err)
	}
	if real == "" {
		return "", errors.New("find this binary: empty path")
	}
	return real, nil
}

// removeStaleUpgrade deletes the previous binary a Windows upgrade moved aside.
// Only Windows leaves one, and only there is it looked for.
func removeStaleUpgrade() {
	if runtime.GOOS != "windows" {
		return
	}
	if exe, err := os.Executable(); err == nil {
		upgrade.RemoveStale(exe)
	}
}
