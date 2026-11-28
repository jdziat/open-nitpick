// Command uibuild takes the output of the UI's Vite build and puts it where the
// nitpick server embeds it: every file under its own name, which already carries
// a hash of its content, and a manifest saying which is which.
//
//	cd ui && npm ci && npm run build
//	go run ./internal/server/uibuild
//
// A plain go build does not run it, and the server it produces says the UI was
// not built. The release image does both steps.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func main() {
	src := flag.String("src", "ui/dist", "directory the Vite build wrote")
	dst := flag.String("dst", "internal/server/ui_dist", "directory the server embeds")
	flag.Parse()
	if err := build(*src, *dst); err != nil {
		fmt.Fprintln(os.Stderr, "uibuild:", err)
		os.Exit(1)
	}
}

// entries are the files the server's page loads by their logical names. The page
// is rendered by the server, not by Vite, so it needs to know which hashed file
// each one is.
var entries = []struct {
	logical  string
	re       *regexp.Regexp
	optional bool
}{
	{"app.js", regexp.MustCompile(`^app\.[A-Za-z0-9_-]+\.js$`), false},
	{"app.css", regexp.MustCompile(`^app\.[A-Za-z0-9_-]+\.css$`), false},
	{"theme.js", regexp.MustCompile(`^theme\.[A-Za-z0-9_-]+\.js$`), true},
}

// safeName is a name the server may serve from one flat directory: no path, no
// leading dot.
var safeName = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]*$`)

// keep is what stays in the embed directory across builds: the placeholders that
// keep it in the repository.
var keep = map[string]bool{".gitignore": true, "README.md": true}

// build copies the Vite output into dst and writes the manifest last, so a build
// that fails partway leaves no manifest and the server reports the UI as not
// built instead of serving half of it.
func build(src, dst string) error {
	files, err := readOutput(src)
	if err != nil {
		return err
	}
	manifest := map[string]string{}
	for _, e := range entries {
		var found []string
		for name := range files {
			if e.re.MatchString(name) {
				found = append(found, name)
			}
		}
		switch {
		case len(found) == 1:
			manifest[e.logical] = found[0]
		case len(found) == 0 && e.optional:
		default:
			return fmt.Errorf("%s: want exactly one file for %s, found %d", src, e.logical, len(found))
		}
	}
	for name, body := range files {
		if strings.HasSuffix(name, ".css") {
			if err := checkCSS(name, body); err != nil {
				return err
			}
		}
		if _, isEntry := entryOf(manifest, name); !isEntry {
			manifest[name] = name
		}
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	if err := removeOldBuild(dst); err != nil {
		return err
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dst, name), body, 0o644); err != nil {
			return err
		}
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dst, "manifest.json"), append(raw, '\n'), 0o644)
}

func entryOf(manifest map[string]string, name string) (string, bool) {
	for logical, file := range manifest {
		if file == name && logical != name {
			return logical, true
		}
	}
	return "", false
}

// readOutput reads the Vite output. index.html is Vite's page, which the server
// does not use, so it is left out. Anything that is not a plain file with a plain
// name is refused: the server serves one flat directory.
func readOutput(src string) (map[string][]byte, error) {
	list, err := os.ReadDir(src)
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	for _, e := range list {
		name := e.Name()
		if e.IsDir() {
			return nil, fmt.Errorf("%s holds a directory %s: the build is expected to be flat", src, name)
		}
		if name == "index.html" {
			continue
		}
		if !safeName.MatchString(name) || name == "manifest.json" {
			return nil, fmt.Errorf("%s: %q is not a name the server can serve", src, name)
		}
		body, err := os.ReadFile(filepath.Join(src, name))
		if err != nil {
			return nil, err
		}
		if len(body) == 0 {
			return nil, fmt.Errorf("%s is empty", name)
		}
		files[name] = body
	}
	return files, nil
}

var (
	dataURL   = regexp.MustCompile(`(?i)url\(\s*["']?data:`)
	remoteURL = regexp.MustCompile(`(?i)(@import\s+(url\()?\s*["']?(https?:)?//|url\(\s*["']?(https?:)?//)`)
)

// checkCSS refuses what the page's Content-Security-Policy would block in the
// browser: a data: URL or a remote one. The build would otherwise pass and the
// page would show a missing image or font with nothing to say why.
func checkCSS(name string, body []byte) error {
	switch {
	case dataURL.Match(body):
		return fmt.Errorf("%s has a data: URL, which the page's policy does not allow; set assetsInlineLimit to 0", name)
	case remoteURL.Match(body):
		return fmt.Errorf("%s loads something from another origin, which the page's policy does not allow", name)
	}
	return nil
}

// removeOldBuild deletes the files of an earlier build and nothing else.
func removeOldBuild(dst string) error {
	list, err := os.ReadDir(dst)
	if err != nil {
		return err
	}
	for _, e := range list {
		if e.IsDir() {
			return errors.New(dst + " holds a directory, which a build does not make")
		}
		if keep[e.Name()] {
			continue
		}
		if err := os.Remove(filepath.Join(dst, e.Name())); err != nil {
			return err
		}
	}
	return nil
}
