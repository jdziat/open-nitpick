---
title: A database/sql rows loop that does not check rows.Err silently truncates
languages: [go]
source: https://pkg.go.dev/database/sql#Rows.Err
checked: 2026-09-08
---
`for rows.Next()` ends on two conditions that look identical from inside the
loop: the result set finished, or the iteration failed. A connection dropped
mid-stream, a query cancelled, a scan error at the driver level all stop
`Next` from returning true.

Without `rows.Err()` after the loop, a partial result is indistinguishable from
a complete one, and the caller reports success over the rows that happened to
arrive. This is the failure mode where a report is short by an amount nobody
notices, rather than one that errors.

The same applies to `bufio.Scanner`, whose `Scan` ends on error and success
alike and whose `Err` is checked just as rarely.

What to look for: a `rows.Next()` or `scanner.Scan()` loop whose closing brace
is followed by a return of the accumulated results, with no `Err()` between.
