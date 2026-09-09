---
title: time.After in a loop holds its timer until it fires, however the loop exits
languages: [go]
classes: [resource]
applies: go < 1.23
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

The version boundary is in the front matter: Go 1.23 changed timers so an
unreferenced one can be collected before firing, and the change is keyed to the
main module's `go` directive. A repository declaring 1.23 or later does not
have this behaviour, and does not get this entry.

One escape hatch is not read by that clause. `godebug asynctimerchan=1`, in
`go.mod` or as a directive, restores the old timers on a module that otherwise
declares 1.23 or later. Such a module has this leak and will not be shown this
entry, so a reviewer working on one has to know the rule already.

What to look for: `time.After` inside a `for` or a `select` that runs more than
once, especially with a duration longer than the loop's period.
