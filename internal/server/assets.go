package server

import (
	"embed"
	"encoding/json"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

// uiDist is the built UI: the style sheet and script with their content hashes
// in their names, and a manifest that says which is which. `make ui` writes it.
// A plain go build embeds only the placeholder, and the server then says the UI
// was not built.
//
//go:embed all:ui_dist
var uiDist embed.FS

type assets struct {
	fsys  fs.FS
	built bool
	css   string // the hashed file name, or ""
	js    string
	theme string          // the script that sets dark mode before the page paints; optional
	files map[string]bool // every name the manifest lists, and only those are served
}

// loadAssets reads the manifest. A missing or unreadable manifest, or one that
// names a file that is not there, means the UI was not built.
func loadAssets(override fs.FS) *assets {
	fsys := override
	if fsys == nil {
		sub, err := fs.Sub(uiDist, "ui_dist")
		if err != nil {
			return &assets{}
		}
		fsys = sub
	}
	raw, err := fs.ReadFile(fsys, "manifest.json")
	if err != nil {
		return &assets{}
	}
	var manifest map[string]string
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return &assets{}
	}
	a := &assets{fsys: fsys, files: map[string]bool{}}
	for logical, hashed := range manifest {
		if hashed == "" || strings.ContainsAny(hashed, "/\\") || hashed == "manifest.json" {
			return &assets{}
		}
		if _, err := fs.Stat(fsys, hashed); err != nil {
			return &assets{}
		}
		a.files[hashed] = true
		switch logical {
		case "app.css":
			a.css = hashed
		case "app.js":
			a.js = hashed
		case "theme.js":
			a.theme = hashed
		}
	}
	a.built = a.css != "" && a.js != ""
	return a
}

// serve answers GET /ui/assets/{name} for a file the manifest lists. The name is
// a content hash, so the answer never changes and may be cached for good.
func (a *assets) serve(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !a.built || !a.files[name] || path.Base(name) != name {
		http.NotFound(w, r)
		return
	}
	body, err := fs.ReadFile(a.fsys, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", mime.TypeByExtension(path.Ext(name)))
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	_, _ = w.Write(body)
}
