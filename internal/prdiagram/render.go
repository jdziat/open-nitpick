// Package prdiagram renders source-linked application flows for pull requests.
package prdiagram

import (
	"fmt"
	"net/url"
	"path"
	"strconv"
	"strings"
)

// Location identifies evidence in a revision's source tree.
type Location struct {
	Path string `json:"path"`
	Line int    `json:"line"`
}

// Node is a step in a flow, with its declaration or call site as evidence.
type Node struct {
	ID      string   `json:"id,omitempty"`
	Label   string   `json:"label"`
	Source  Location `json:"source"`
	Changed bool     `json:"changed"`
	// State is the extractor's change state (added, modified, unchanged,
	// removed). It is rendered alongside the label so the diagram remains
	// understandable without colour or Mermaid support.
	State string `json:"state,omitempty"`
	// Boundary marks a node that represents an unresolved or external target.
	Boundary bool `json:"boundary,omitempty"`
	// Reason explains why a boundary exists.
	Reason string `json:"reason,omitempty"`
}

// Edge connects node indices and names the source that establishes the relation.
// Inferred connections must remain visually distinct from resolved connections.
type Edge struct {
	ID         string   `json:"id,omitempty"`
	From       int      `json:"from"`
	To         int      `json:"to"`
	Label      string   `json:"label"`
	Source     Location `json:"source"`
	Inferred   bool     `json:"inferred"`
	Unresolved bool     `json:"unresolved,omitempty"`
	// Removed selects the base revision for source evidence when this relation
	// belongs to a declaration that was removed by the change.
	Removed bool   `json:"removed,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// Graph contains one scoped flow and explicit limits on its interpretation.
type Graph struct {
	Nodes      []Node   `json:"nodes"`
	Edges      []Edge   `json:"edges"`
	Unresolved []string `json:"unresolved,omitempty"`
}

// Markdown renders a Mermaid flow and an evidence table. A nonempty sourceBase
// must be an immutable HTTPS revision URL, such as a GitHub blob URL. An empty
// sourceBase is for a local working tree and deliberately emits plain evidence.
func (g Graph) Markdown(sourceBase string) (string, error) {
	return g.MarkdownSources(sourceBase, "")
}

// MarkdownSources renders a graph using headBase for current evidence and
// baseBase for evidence belonging to removed declarations.
func (g Graph) MarkdownSources(headBase, baseBase string) (string, error) {
	// Empty bases retain plain path:line evidence (the local working-tree case).
	head, err := parseSourceBase(headBase)
	if err != nil {
		return "", err
	}
	base, err := parseSourceBase(baseBase)
	if err != nil {
		return "", err
	}
	for i, node := range g.Nodes {
		if err := node.Source.validate(); err != nil {
			return "", fmt.Errorf("node %d: %w", i, err)
		}
	}
	for i, edge := range g.Edges {
		if edge.From < 0 || edge.To < 0 || edge.From >= len(g.Nodes) || edge.To >= len(g.Nodes) {
			return "", fmt.Errorf("edge %d references an absent node", i)
		}
		if err := edge.Source.validate(); err != nil {
			return "", fmt.Errorf("edge %d: %w", i, err)
		}
	}
	var b strings.Builder
	b.WriteString("## Changed application flow\n\n")
	if len(g.Nodes) == 0 {
		b.WriteString("No flow was resolved in the supplied scope. This is not evidence that the change has no effect.\n")
	} else {
		b.WriteString("Highlighted nodes changed. Solid arrows are resolved, dashed arrows inferred, and crossed ends unresolved. Connections describe source relationships, not a runtime trace.\n\n```mermaid\nflowchart TD\n")
		for i, node := range g.Nodes {
			fmt.Fprintf(&b, "  n%d[\"%s\"]\n", i, escapeMermaid(node.displayLabel()))
		}
		for _, edge := range g.Edges {
			arrow := "-->"
			if edge.Inferred {
				arrow = "-.->"
			}
			if edge.Unresolved {
				arrow = "--x"
			}
			if edge.Label == "" {
				fmt.Fprintf(&b, "  n%d %s n%d\n", edge.From, arrow, edge.To)
			} else {
				fmt.Fprintf(&b, "  n%d %s|\"%s\"| n%d\n", edge.From, arrow, escapeMermaid(edge.Label), edge.To)
			}
		}
		b.WriteString("  classDef changed fill:#fff3cd,stroke:#806000,color:#302500,stroke-width:3px\n")
		for i, node := range g.Nodes {
			if node.Changed {
				fmt.Fprintf(&b, "  class n%d changed\n", i)
			}
		}
		b.WriteString("```\n\n| Element | Source evidence |\n| --- | --- |\n")
		for i, node := range g.Nodes {
			evidenceBase := head
			if node.State == "removed" {
				evidenceBase = base
			}
			fmt.Fprintf(&b, "| Node %d: %s | %s |\n", i, escape(node.displayLabel()), node.Source.link(evidenceBase))
		}
		for i, edge := range g.Edges {
			evidenceBase := head
			if edge.Removed {
				evidenceBase = base
			}
			label := edge.Label
			if edge.Reason != "" {
				label += " (" + edge.Reason + ")"
			}
			fmt.Fprintf(&b, "| Edge %d: node %d → node %d; %s | %s |\n", i, edge.From, edge.To, escape(label), edge.Source.link(evidenceBase))
		}
	}
	if len(g.Unresolved) > 0 {
		b.WriteString("\nUnresolved connections and coverage limits:\n\n")
		for _, reason := range g.Unresolved {
			fmt.Fprintf(&b, "- %s\n", escape(reason))
		}
	}
	return b.String(), nil
}

func parseSourceBase(sourceBase string) (*url.URL, error) {
	if sourceBase == "" {
		return nil, nil
	}
	parsed, err := url.Parse(sourceBase)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("source base must be an HTTPS revision URL without credentials, query, or fragment")
	}
	return parsed, nil
}

func (n Node) displayLabel() string {
	label := n.Label
	if n.State != "" {
		label += " (" + n.State + ")"
	}
	if n.Boundary {
		if n.Reason != "" {
			label += " (boundary: " + n.Reason + ")"
		} else {
			label += " (boundary)"
		}
	}
	return label
}

func (l Location) validate() error {
	if l.Line < 1 || l.Path == "" || path.IsAbs(l.Path) || path.Clean(l.Path) != l.Path || l.Path == ".." || strings.HasPrefix(l.Path, "../") || strings.ContainsAny(l.Path, "\\\r\n\x00") {
		return fmt.Errorf("source location needs a relative repository path and positive line")
	}
	return nil
}

func (l Location) link(base *url.URL) string {
	if base == nil {
		return escape(l.Path) + ":" + strconv.Itoa(l.Line)
	}
	target := *base
	target.Path = strings.TrimRight(base.Path, "/") + "/" + l.Path
	target.RawPath = ""
	target.Fragment = "L" + strconv.Itoa(l.Line)
	return "[" + escape(l.Path) + ":" + strconv.Itoa(l.Line) + "](<" + target.String() + ">)"
}

// escapeHTML encodes untrusted text for Markdown table cells and list items.
func escape(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == ' ' {
			b.WriteRune(r)
		} else {
			fmt.Fprintf(&b, "&#%d;", r)
		}
	}
	return b.String()
}

// escapeMermaid uses Mermaid entity syntax (without the HTML ampersand).
func escapeMermaid(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == ' ' {
			b.WriteRune(r)
		} else {
			fmt.Fprintf(&b, "#%d;", r)
		}
	}
	return b.String()

}
