---
title: time.After in a loop holds its timer until it fires, however the loop exits
languages: [go]
classes: [resource]
source: https://pkg.go.dev/time#After
checked: 2026-09-08
---
The documentation states it: *"The underlying Timer is not recovered by the
garbage collector until the timer fires."*

In a `select` inside a `for`, every iteration that takes a different branch
leaves behind a timer that stays live for its whole duration. A loop selecting
on a channel and `time.After(time.Hour)` accumulates one hour-long timer per
message. At a few thousand messages a second, that is millions of live timers
and the memory they hold, and the leak looks like ordinary heap growth with no
allocation site that explains it.

`time.NewTimer` with an explicit `Stop`, or one timer reset outside the loop,
does not have this property.

Note the version boundary: Go 1.23 changed timers so an unreferenced one can be
collected before firing. Code targeting an older toolchain, which `go.mod`
states, still has the old behaviour.

What to look for: `time.After` inside a `for` or a `select` that runs more than
once, especially with a duration longer than the loop's period.
