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
	Label   string   `json:"label"`
	Source  Location `json:"source"`
	Changed bool     `json:"changed"`
}

// Edge connects node indices and names the source that establishes the relation.
// Inferred connections must remain visually distinct from resolved connections.
type Edge struct {
	From     int      `json:"from"`
	To       int      `json:"to"`
	Label    string   `json:"label"`
	Source   Location `json:"source"`
	Inferred bool     `json:"inferred"`
}

// Graph contains one scoped flow and explicit limits on its interpretation.
type Graph struct {
	Nodes      []Node   `json:"nodes"`
	Edges      []Edge   `json:"edges"`
	Unresolved []string `json:"unresolved,omitempty"`
}

// Markdown renders a Mermaid flow and an evidence table. sourceBase must be
// an HTTPS URL ending at the immutable source revision, such as a GitHub blob URL.
func (g Graph) Markdown(sourceBase string) (string, error) {
	base, err := url.Parse(sourceBase)
	if err != nil || base.Scheme != "https" || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return "", fmt.Errorf("source base must be an HTTPS revision URL without credentials, query, or fragment")
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
		b.WriteString("Highlighted nodes changed. Dashed edges are inferred. Connections describe source relationships, not a runtime trace.\n\n```mermaid\nflowchart TD\n")
		for i, node := range g.Nodes {
			fmt.Fprintf(&b, "  n%d[\"%s\"]\n", i, escape(node.Label))
		}
		for _, edge := range g.Edges {
			arrow := "-->"
			if edge.Inferred {
				arrow = "-.->"
			}
			fmt.Fprintf(&b, "  n%d %s|\"%s\"| n%d\n", edge.From, arrow, escape(edge.Label), edge.To)
		}
		b.WriteString("  classDef changed fill:#fff3cd,stroke:#806000,color:#302500,stroke-width:3px\n")
		for i, node := range g.Nodes {
			if node.Changed {
				fmt.Fprintf(&b, "  class n%d changed\n", i)
			}
		}
		b.WriteString("```\n\n| Element | Source evidence |\n| --- | --- |\n")
		for i, node := range g.Nodes {
			fmt.Fprintf(&b, "| Node %d: %s | %s |\n", i, escape(node.Label), node.Source.link(base))
		}
		for i, edge := range g.Edges {
			fmt.Fprintf(&b, "| Edge %d: %s | %s |\n", i, escape(edge.Label), edge.Source.link(base))
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

func (l Location) validate() error {
	if l.Line < 1 || l.Path == "" || path.IsAbs(l.Path) || path.Clean(l.Path) != l.Path || l.Path == ".." || strings.HasPrefix(l.Path, "../") || strings.ContainsAny(l.Path, "\\\r\n\x00") {
		return fmt.Errorf("source location needs a relative repository path and positive line")
	}
	return nil
}

func (l Location) link(base *url.URL) string {
	target := *base
	target.Path = strings.TrimRight(base.Path, "/") + "/" + l.Path
	target.RawPath = ""
	target.Fragment = "L" + strconv.Itoa(l.Line)
	return "[" + escape(l.Path) + ":" + strconv.Itoa(l.Line) + "](<" + target.String() + ">)"
}

// Encode punctuation so repository text cannot become Mermaid or Markdown syntax.
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
