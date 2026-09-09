---
title: errgroup.Group.Wait returns only the first error, so later failures are lost
languages: [go]
source: https://pkg.go.dev/golang.org/x/sync/errgroup
checked: 2026-09-08
---
`Wait` blocks until every goroutine returns and then reports **the first
non-nil error only**. Errors from the others are discarded, not joined.

Code that starts N independent jobs, collects `err := g.Wait()`, and reports
that one error is reporting one of N failures and silently dropping the rest.
Where each failure names a distinct resource, the operator sees one name and
retries one thing.

`WithContext` compounds it: the first error cancels the shared context, so the
remaining goroutines usually fail with `context.Canceled`, and those are the
errors being discarded. The visible error is the first one, which is correct,
but the log is then silent about how many others there were.

What to look for: a `g.Wait()` whose single error is passed upward as though it
described the whole batch, especially where the goroutines write to a shared
slice or map of results and nothing counts the failures.
