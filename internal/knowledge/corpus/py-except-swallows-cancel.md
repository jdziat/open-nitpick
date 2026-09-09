---
title: In asyncio, a bare except catches CancelledError and breaks cancellation
languages: [python]
source: https://docs.python.org/3/library/asyncio-task.html#task-cancellation
checked: 2026-09-08
---
Since Python 3.8 `asyncio.CancelledError` inherits from `BaseException`, not
`Exception`, which was done so that `except Exception` would stop swallowing
it. A bare `except:` still catches it, and so does `except BaseException`.

A coroutine that catches cancellation and continues has made cancellation
advisory. The task that awaited it hangs, a timeout does not time out, and
shutdown waits for work that was told to stop. The documentation is explicit
that cancellation should be propagated: catching it to run cleanup is fine only
where the handler re-raises.

What to look for: a bare `except:` inside an `async def`, or a handler that
catches `CancelledError` without a `raise` on every path. In a `finally` doing
cleanup, an `await` that can itself be cancelled is the second-order version of
the same problem.
