# Application flows

`nitpick flow` shows the static Go call paths affected by a change. It needs no
model configuration and makes no model calls. The graph and its source table
describe relationships in the selected source, not runtime order or proof that
every effect has been found.

```bash
nitpick flow -mode on
nitpick flow -mode on -base main -head feature -include-unchanged
nitpick flow -mode on -entry main.run -format json -output flow.json
nitpick flow -mode on -format html -source -open -output flow.html
```

The default comparison is the tracked working tree against `HEAD`. With both
`-base` and `-head`, the command reads committed revisions. `-entry` accepts an
unambiguous package-qualified function, method, or repository-relative
`path/to/file.go:line`. The command writes to stdout unless `-output` names a
file; file output is replaced atomically. It never publishes a review.

## Including flows in reviews

Flow analysis is off by default. To include it in PR reviews, local output,
dry runs, and the Actions summary, set:

```yaml
flow:
  mode: auto
  include_unchanged: true
  exclude: ["**/*_test.go", "vendor/**"]
```

`auto` runs for changed Go files. `on` also reports when no eligible source was
selected. `off` skips the pass. Explicit CLI flags override configuration,
including `-include-unchanged=false` and zero caller/callee depth. All settings
are listed in the [configuration reference](configuration-reference.md#flow).

The pass sees the complete policy-filtered diff before incremental model review
narrows its input. Both review integration and the standalone command honor
`review.ignore` and `review.skip_generated`; generated files omitted by the
policy appear in coverage as `generated_file`. Disabling `review.summary` keeps the flow section visible.
Flow coverage does not change findings, the review gate, or incremental review
completion. Draft and skipped reviews do not run the pass.

## Reading the result

Solid arrows identify resolved calls, dashed arrows identify inferred callback
relationships, and crossed ends identify unresolved targets. Each diagram has
a text table with its nodes, edge endpoints, change states, and source evidence.
Separate collapsible sections keep disconnected flows readable.

PR evidence links use the pinned head repository and commit, including fork
heads. Removed declarations and their calls use the base revision. Local
working-tree evidence stays as `path:line`; JSON also records a digest of the
materialized source. Neither review output nor result JSON includes source
snippets.

| Status | Meaning |
| --- | --- |
| `complete` | The declared static scope resolved within its limits. |
| `partial` | A limit, unresolved target, or missing metadata reduced coverage. |
| `no_flow` | Eligible source was scanned but no reachable relationship was found. |
| `skipped` | The selected mode or language supplied nothing to analyze. |
| `unavailable` | Required source or an entrypoint could not be established. |
| `failed` | Analysis or safe rendering failed. |

An empty graph is not evidence that a change has no effect. Coverage counts and
omissions explain what ran and what could not be established. MCP review
results expose the same versioned data under `flow`.

## Browsing a flow as HTML

`-format html` writes a single self-contained page instead of Markdown or
JSON: a clickable SVG call graph per flow, a side panel with each
declaration's callers, callees, and source link, and a search box that
filters by symbol, path, or reason. The page has no external dependency — no
CDN script, no network call — so it opens from disk, from a CI artifact, or
behind an air gap.

```bash
nitpick flow -mode on -format html -output flow.html
nitpick flow -mode on -format html -source -open -output flow.html
```

`-source` embeds bounded source snippets for each declaration so the panel
shows code next to the graph; without it the panel links to `path:line` only.
A document built with `-source` says so in its header, since it then contains
repository source. `-open` hands the written file to the platform's default
browser opener once it is written. Rendering is deterministic: the same
result and options always produce byte-identical output, so a regenerated
document stays diffable.

A large flow is drawn as focus and context rather than all at once. Above
`-max-visible` nodes (24 by default) a diagram shows the changed declarations
and what they call directly, says how many nodes it left out, and keeps the
rest searchable through the filter box and the panel's caller and callee
links. Wide ranks wrap instead of running off the canvas, so labels stay at
their intended size and the view is panned and zoomed rather than shrunk.
Drag to pan, scroll to zoom, and use the Fit and 100% controls; inside a
diagram the arrow keys step between nodes, Enter opens one, and Escape
clears the selection. The detail panel on the right is resizable: drag the
handle on its left edge, or focus it and use the arrow keys; its width is
remembered across reloads and double-clicking the handle resets it.

## Scope and limits

The extractor indexes Go declarations and type information from the selected
snapshot. It follows callers toward command entrypoints and callees toward
operations such as filesystem writes, network calls, and SQL access. Unchanged
packages are available only with `include_unchanged`. Local module paths come
from the snapshot's `go.mod` files; missing dependencies, interface dispatch,
and unresolved function values remain visible boundaries. HTTP framework
adapters are follow-on work.

Production commands run parsing and type checking in a separate process with
a deadline. The worker does not run project generators, tests, package drivers,
or dependency downloads. It uses the installed Go toolchain and compiler cache
for standard-library metadata. Unsupported or missing metadata reduces coverage.

Defaults allow 2,000 materialized files, 32 MiB of source, 15 seconds, eight
caller levels, four callee levels, 500 nodes, 1,000 edges, and three inline flows.
Inline diagrams show at most 30 nodes and 50 edges per flow. Markdown is capped
at 48 KiB and result JSON at 2 MiB; limits are reported. Large reviews preserve
ordinary findings and report when flow evidence does not fit the review body.
Changed declarations take priority over unchanged context; hard graph and
publication caps can still omit roots, with explicit omission counts.
These defaults are bounds, not latency promises. Measurements and their scope
are in [findings](findings.md#flow-analysis-qualification).

Use `-max-files`, `-max-bytes`, `-max-nodes`, `-max-edges`, `-max-flows`,
`-max-depth-callers`, `-max-depth-callees`, and `-timeout` to adjust the bounds.
`-build-tag` and `-exclude` are repeatable. Start with a small scope, inspect its
coverage, and expand unchanged context when the surrounding call paths matter.
