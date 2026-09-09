---
title: A Context belongs in a function's first argument, not in a struct field
languages: [go]
classes: [contract]
source: https://pkg.go.dev/context
checked: 2026-09-08
---
The package documentation is direct: *"Do not store Contexts inside a struct
type; instead, pass a Context explicitly to each function that needs it."*

A stored context is captured once, at construction, and every later call uses
that one. Cancellation and deadlines then belong to whoever built the struct
rather than to whoever is calling, so a per-request deadline cannot be applied
to a long-lived client, and cancelling one caller cancels every other caller
sharing it.

The two exceptions worth recognising rather than flagging: a struct that exists
to carry one request's state and dies with it, and a type implementing an
interface that cannot take a context, where the field is the documented
workaround and the comment says so.

What to look for: `ctx context.Context` as a struct field, and methods that use
`c.ctx` rather than an argument.
