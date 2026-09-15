package flowhtml

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"

	"github.com/jdziat/open-nitpick/internal/prflow"
)

//go:embed assets_page.html
var pageTemplate string

//go:embed assets_style.css
var styleCSS string

//go:embed assets_app.js
var appJS string

var page = template.Must(template.New("flow").Parse(pageTemplate))

// Render writes a self-contained HTML document for one flow result. The output
// has no network dependency: styles, script, and diagrams are all inline, so
// the file works from disk, from a CI artifact, or behind an air gap.
//
// Rendering is deterministic. The same result and options always produce
// byte-identical output, which keeps a regenerated document diffable.
func Render(ctx context.Context, result prflow.Result, reader SourceReader, o Options) ([]byte, error) {
	doc := build(ctx, result, reader, o.withDefaults())
	nodeJSON, err := json.Marshal(viewNodes(doc))
	if err != nil {
		return nil, fmt.Errorf("encode flow nodes: %w", err)
	}
	startJSON, err := json.Marshal(startNode(doc))
	if err != nil {
		return nil, fmt.Errorf("encode flow entry point: %w", err)
	}
	var out bytes.Buffer
	data := struct {
		document
		CSS       template.CSS
		JS        template.JS
		NodeJSON  template.JS
		StartJSON template.JS
	}{
		document:  doc,
		CSS:       template.CSS(styleCSS),
		JS:        template.JS(appJS),
		NodeJSON:  template.JS(nodeJSON),
		StartJSON: template.JS(startJSON),
	}
	if err := page.Execute(&out, data); err != nil {
		return nil, fmt.Errorf("render flow document: %w", err)
	}
	return out.Bytes(), nil
}

// viewNode is the per-node payload the browser panel reads. Snippet HTML is
// produced by the Go highlighter, which escapes every byte of source it is
// given, so the panel can insert it without re-escaping markup it created.
type viewNode struct {
	ID       string    `json:"id"`
	Drawn    bool      `json:"drawn"`
	Label    string    `json:"label"`
	Kind     string    `json:"kind,omitempty"`
	State    string    `json:"state"`
	Changed  bool      `json:"changed"`
	Boundary bool      `json:"boundary,omitempty"`
	Reason   string    `json:"reason,omitempty"`
	Path     string    `json:"path"`
	Line     int       `json:"line"`
	Link     string    `json:"link,omitempty"`
	Snippet  string    `json:"snippet,omitempty"`
	Callers  []viewRef `json:"callers,omitempty"`
	Callees  []viewRef `json:"callees,omitempty"`
}

type viewRef struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	Kind       string `json:"kind"`
	Resolution string `json:"resolution,omitempty"`
}

func viewNodes(doc document) map[string]viewNode {
	out := make(map[string]viewNode, len(doc.Nodes))
	for _, node := range doc.Nodes {
		entry := viewNode{
			ID:       node.ID,
			Drawn:    node.Drawn,
			Label:    node.Label,
			Kind:     node.Kind,
			State:    node.State,
			Changed:  node.Changed,
			Boundary: node.Boundary,
			Reason:   node.Reason,
			Path:     node.SourcePath,
			Line:     node.SourceLine,
			Link:     node.Link,
			Callers:  refs(node.Callers),
			Callees:  refs(node.Callees),
		}
		if node.Snippet != nil {
			entry.Snippet = snippetHTML(*node.Snippet)
		}
		out[node.ID] = entry
	}
	return out
}

func refs(in []docRef) []viewRef {
	if len(in) == 0 {
		return nil
	}
	out := make([]viewRef, 0, len(in))
	for _, ref := range in {
		out = append(out, viewRef{ID: ref.ID, Label: ref.Label, Kind: ref.Kind, Resolution: ref.Resolution})
	}
	return out
}

// snippetHTML assembles one highlighted window. Line text is already escaped
// by highlightGo; only the line numbers are added here.
func snippetHTML(s snippet) string {
	var b bytes.Buffer
	for _, line := range s.Lines {
		class := "ln"
		if line.Focus {
			class = "ln focus"
		}
		fmt.Fprintf(&b, "<span class=\"%s\"><span class=\"num\">%d</span>%s</span>", class, line.Number, line.HTML)
	}
	return b.String()
}

// startNode picks the declaration the document opens on. A reader arrives
// asking what the change reached, so a changed declaration outranks context,
// one the change touched directly outranks a boundary, and a node with more
// relationships outranks a leaf. Among changed declarations a named function
// or method outranks an anonymous closure, which is harder to read in context.
// The empty string leaves nothing selected.
func startNode(doc document) string {
	best, bestScore := "", -1
	for _, node := range doc.Nodes {
		score := len(node.Callers) + len(node.Callees)
		if node.Changed {
			score += 1000
		}
		if node.Boundary {
			score -= 500
		}
		if node.Kind == "closure" {
			score -= 200
		}
		if score > bestScore {
			best, bestScore = node.ID, score
		}
	}
	return best
}
