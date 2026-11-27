// Command uibuild writes the nitpick server's UI into the directory the server
// embeds: each source file under a name that carries a hash of its content, and a
// manifest saying which is which. It has no dependencies and needs no Node.
//
//	go run ./internal/server/uibuild
//
// A plain go build does not run it, and the server it produces says the UI was
// not built. The release image does.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// assets are the sources the UI is made of, by their logical names.
var assets = []string{"app.css", "app.js"}

func main() {
	src := flag.String("src", "internal/server/ui_src", "directory holding the UI sources")
	dst := flag.String("dst", "internal/server/ui_dist", "directory the server embeds")
	flag.Parse()
	if err := build(*src, *dst); err != nil {
		fmt.Fprintln(os.Stderr, "uibuild:", err)
		os.Exit(1)
	}
}

// build writes the hashed assets and the manifest into dst. Hashed files from an
// earlier build are removed, so the directory holds one build and nothing else.
// The manifest is written last: a build that fails partway leaves no manifest,
// and the server then reports the UI as not built instead of serving half of it.
func build(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	if err := removeOldBuild(dst); err != nil {
		return err
	}
	manifest := map[string]string{}
	for _, name := range assets {
		body, err := os.ReadFile(filepath.Join(src, name))
		if err != nil {
			return err
		}
		if len(body) == 0 {
			return fmt.Errorf("%s is empty", name)
		}
		sum := sha256.Sum256(body)
		ext := filepath.Ext(name)
		hashed := strings.TrimSuffix(name, ext) + "." + hex.EncodeToString(sum[:6]) + ext
		if err := os.WriteFile(filepath.Join(dst, hashed), body, 0o644); err != nil {
			return err
		}
		manifest[name] = hashed
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dst, "manifest.json"), append(raw, '\n'), 0o644)
}

// removeOldBuild deletes what a build writes and nothing else: the manifest and
// files named like a hashed asset. The placeholder that keeps the directory in
// the repository stays.
func removeOldBuild(dst string) error {
	entries, err := os.ReadDir(dst)
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			return errors.New(dst + " holds a directory, which a build does not make")
		}
		if name == "manifest.json" || isHashedAsset(name) {
			if err := os.Remove(filepath.Join(dst, name)); err != nil {
				return err
			}
		}
	}
	return nil
}

func isHashedAsset(name string) bool {
	for _, logical := range assets {
		ext := filepath.Ext(logical)
		base := strings.TrimSuffix(logical, ext)
		if rest, ok := strings.CutPrefix(name, base+"."); ok {
			if hash, ok := strings.CutSuffix(rest, ext); ok && len(hash) == 12 && isHex(hash) {
				return true
			}
		}
	}
	return false
}

func isHex(s string) bool {
	_, err := hex.DecodeString(s)
	return err == nil
}
