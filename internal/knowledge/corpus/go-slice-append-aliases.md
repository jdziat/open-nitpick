---
title: append may return a slice sharing the caller's backing array
languages: [go]
source: https://go.dev/blog/slices-intro
checked: 2026-09-08
---
`append` allocates a new array only when capacity is exhausted. Otherwise it
writes into the existing one and returns a slice over it.

So a function taking a slice and returning `append(s, x)` can overwrite an
element its caller still sees, whenever the caller's slice had spare capacity.
The classic shape is a sub-slice: `b := a[:2]` has the capacity of `a`, so
`append(b, v)` overwrites `a[2]`. Nothing about the call site says so, and it
is correct up until the day the caller's slice is created with `make([]T, n, m)`
rather than a literal.

Go 1.20's three-index `a[:2:2]` caps the capacity and makes the append copy,
which is the fix that reads as noise until you have met this.

What to look for: a function that appends to a slice parameter and returns it,
where the caller keeps using the original, and any `append` to a sub-slice of a
slice that outlives the call.
