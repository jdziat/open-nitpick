# How a review runs

Seven stages take a diff to posted comments. Each one below says what it drops
or rewrites and what the run reports when it does, because a review that
quietly skipped part of a change reads exactly like one that found nothing in
it.

```
diff → select and batch files → review each batch → triage → anchor → render → publish
```

## Select

Select drops ignored, binary, deleted, and generated files. A review that
quietly skipped half the diff would otherwise look identical to a clean one, so
the exclusions are reported, with one deliberate exception and two places to
look. *Files not reviewed* lists them all except the ones matching your ignore
list, because `go mod vendor` is hundreds of files and those patterns are your
own. An ignored file is still not silent where it matters: a changed `.go` file
an analyzer would otherwise have read is named under *Analyzed less than it ran
over*, and a change where **everything** was set aside opens by saying that
nothing in it was reviewed.

## Batch

Batch groups files under a token budget, attaching whole file contents where
they fit and a window around the changes where they do not.

## Review

Review runs batches concurrently. One failed batch is logged and skipped;
*every* batch failing is an error rather than a "no issues found".

## Triage

Triage merges duplicates across batches, drops unsupported findings, and writes
the walkthrough. It may reword and merge, but it cannot invent findings for
files nobody reported on.

## Anchor

Anchor snaps near-miss line numbers onto real changed lines and drops findings
that cannot be placed, so comments land where they belong. An *analyzer*
finding dropped here is counted and published rather than discarded quietly,
under *Analyzed less than it ran over*.

## Render

Render turns surviving findings into comments: a title, the rationale, and a
suggestion where one applies as a `suggestion` block the forge can commit. The
walkthrough and the file table become the summary comment.

## Publish

Publish posts them. On a re-run it posts only what is new, edits what moved, and
resolves the threads whose findings the change has since fixed;
`review.incremental` and `review.resolve_superseded` govern both. A run that
reviewed only part of the diff still publishes, and says so: the summary names
the batches that failed and counts only the files it reviewed, because a
partial review that reads like a complete one is the failure mode this pipeline
is built to avoid.
