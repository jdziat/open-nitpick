package review

import (
	"context"
	"fmt"
	"strings"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/flow"
	"github.com/jdziat/open-nitpick/internal/standards"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// flowListMax bounds each list under the diagram. The diagram holds at most
// 30 nodes, but a dense one has several times that many arrows, and the
// walkthrough shares a comment with every finding.
const flowListMax = 40

// flowDiagram builds the call-flow diagram for review.flow. The engineering
// profile has already frozen the repository's Go source, so it is reused; any
// other profile reads it once here. A repository that cannot be read gives no
// diagram, which the walkthrough leaves out rather than drawing from a
// partial tree.
func (e *Engine) flowDiagram(ctx context.Context, ref vcs.Ref, files diff.Files, execution *DesignExecution) *flow.Diagram {
	if !e.Config.Review.Flow {
		return nil
	}
	var sources []standards.File
	if execution != nil {
		sources = execution.Sources
	} else {
		changed := make([]string, 0, len(files))
		for _, file := range files {
			if !e.Config.Ignored(file.Path) && !file.Binary {
				changed = append(changed, file.Path)
			}
		}
		fetch := func(ctx context.Context, name string) ([]byte, error) {
			return e.Provider.FileContent(ctx, ref, name)
		}
		view := bundle.CaptureDesignSources(ctx, e.Config, changed, fetch, bundle.ListerFrom(e.Provider, ref), nil, bundle.SourceLimits{Paths: 4096, Bytes: 32 << 20})
		if len(view.Errors) > 0 {
			e.log().Info("flow diagram skipped", "reason", view.Errors[0])
			return nil
		}
		for name, src := range view.Content {
			sources = append(sources, standards.File{Path: name, Src: src})
		}
	}
	return flow.Build(sources, files)
}

// flowNotice renders the diagram as a collapsed block. It is not gated on
// review.summary: the opt-in is review.flow, and turning the prose off does
// not ask for the diagram to go too.
func flowNotice(report *Report) string {
	d := report.Flow
	if d == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n<details>\n<summary>Flow around %s</summary>\n\n", flowHeadline(d.Changed))
	b.WriteString("```mermaid\n" + d.Mermaid + "```\n\n")
	b.WriteString("Solid arrows are calls by name or through an import. Dashed arrows labelled *inferred* match a method by name alone, so they can point at another type's method; those labelled *unresolved* match several methods and are not guessed. A green box is a registered route; the numbers on its arrows are the order its handlers run in. Pink boxes are calls that write to a database or the filesystem.\n\n")
	if len(d.Calls) > 0 {
		b.WriteString("Calls:\n\n")
		for i, c := range d.Calls {
			if i == flowListMax {
				fmt.Fprintf(&b, "- and %d more\n", len(d.Calls)-flowListMax)
				break
			}
			fmt.Fprintf(&b, "- %s to %s (%s), %s\n", inline(c.From), inline(c.To), c.Kind, sourceLink(report, c.Path, c.Line, fmt.Sprintf("%s:%d", c.Path, c.Line)))
		}
		b.WriteString("\n")
	}
	if len(d.Locations) > 0 {
		b.WriteString("Declarations:\n\n")
		for i, l := range d.Locations {
			if i == flowListMax {
				fmt.Fprintf(&b, "- and %d more\n", len(d.Locations)-flowListMax)
				break
			}
			fmt.Fprintf(&b, "- %s, %s\n", inline(l.Label), sourceLink(report, l.Path, l.Line, fmt.Sprintf("%s:%d", l.Path, l.Line)))
		}
		b.WriteString("\n")
	}
	b.WriteString("Built from Go source by name, without type checking. Calls through interfaces or tables are not drawn, so a missing arrow is not proof of no call.")
	if d.Truncated {
		fmt.Fprintf(&b, " The diagram stops at %d nodes.", d.Nodes)
	}
	for _, limit := range d.Limits {
		b.WriteString(" " + inline(limit))
	}
	b.WriteString("\n\n</details>\n")
	return b.String()
}

func flowHeadline(changed []string) string {
	const shown = 3
	if len(changed) <= shown {
		return inline(strings.Join(changed, ", "))
	}
	return fmt.Sprintf("%s and %d more", inline(strings.Join(changed[:shown], ", ")), len(changed)-shown)
}
