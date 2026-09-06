# How a review runs

```
diff → select and batch files → review each batch → triage → render → publish
```

- **Select** drops ignored, binary, deleted, and generated files. A review that
  quietly skipped half the diff would otherwise look identical to a clean one, so
  the exclusions are reported, with one deliberate exception and two places to
  look. *Files not reviewed* lists them all except the ones matching your ignore
  list, because `go mod vendor` is hundreds of files and those patterns are your
  own. An ignored file is still not silent where it matters: a changed `.go` file
  an analyzer would otherwise have read is named under *Analyzed less than it ran
  over*, and a change where **everything** was set aside opens by saying that
  nothing in it was reviewed.
- **Batch** groups files under a token budget, attaching whole file contents
  where they fit and a window around the changes where they do not.
- **Review** runs batches concurrently. One failed batch is logged and skipped;
  *every* batch failing is an error rather than a "no issues found".
- **Triage** merges duplicates across batches, drops unsupported findings, and
  writes the walkthrough. It may reword and merge, but it cannot invent findings
  for files nobody reported on.
- **Anchor** snaps near-miss line numbers onto real changed lines and drops
  findings that cannot be placed, so comments land where they belong. An
  *analyzer* finding dropped here is counted and published rather than discarded
  quietly; see the discard block above.
