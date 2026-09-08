---
title: A defer runs at function return, not at the end of the loop iteration
languages: [go]
source: https://go.dev/ref/spec#Defer_statements
checked: 2026-09-08
---
The specification: deferred calls run *"when the surrounding function
returns"*. Not when the block ends, not when the iteration ends.

A `defer f.Close()` inside a loop over ten thousand paths holds ten thousand
descriptors open until the function returns. On a typical soft limit of 1024
the failure is `too many open files`, and it arrives at whichever open happens
to be the 1025th, which is rarely the one at fault. The same shape holds for
`defer mu.Unlock()` in a loop, which deadlocks on the second iteration, and for
`defer tx.Rollback()`, which holds a transaction per iteration.

The fix is a function per iteration, so the defer has a scope that ends.

What to look for: any `defer` lexically inside a `for`, especially one
acquiring a bounded resource: a file, a lock, a connection, a transaction.
