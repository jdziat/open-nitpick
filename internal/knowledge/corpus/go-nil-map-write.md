---
title: Reading a nil map is fine and writing to one panics
languages: [go]
classes: [correctness]
source: https://go.dev/ref/spec#Map_types
checked: 2026-09-08
---
The zero value of a map is nil. Reading from it returns the zero value and
`len` returns 0, so a nil map behaves like an empty one right up until a write,
which panics with `assignment to entry in nil map`.

That asymmetry is why the bug survives review and testing. A struct with a map
field that some paths populate and others leave zero reads correctly everywhere
and panics on the first write down the path nobody exercised, often in
production, often in a handler.

`make(map[K]V)` in the constructor, or a nil check before the write.

What to look for: a map field on a struct with more than one construction path,
a map returned from a function that can return early, and any `m[k] = v` where
`m` came from a parameter or a field rather than a `make` in view.
