---
title: An HTTP response body must be closed even when the request returned an error status
languages: [go]
source: https://pkg.go.dev/net/http#Client.Do
checked: 2026-09-08
---
*"If the returned error is nil, the Response will contain a non-nil Body which
the user is expected to close."* The error being nil is the condition, and a
404 or a 500 is not an error: `err` is nil and the body is open.

So the common early return, checking `resp.StatusCode` and returning before the
`defer resp.Body.Close()` or without one at all, leaks a connection per failed
request. Under a retry loop against a failing endpoint that is a leak
proportional to the failure rate, which is the worst time for it.

The second half is less known: the connection is only reused if the body is
read to completion as well as closed. Closing an unread body discards the
connection, so a client that closes early loses keep-alive and opens a new
connection per request, which reads as a latency problem rather than a leak.

What to look for: a status-code check between `Do` and the `defer Close`, and
any path returning between them.
