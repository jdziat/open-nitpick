---
title: A held std Mutex guard across an await point blocks the whole executor thread
languages: [rust]
classes: [concurrency]
source: https://docs.rs/tokio/latest/tokio/sync/struct.Mutex.html
checked: 2026-09-08
---
`std::sync::Mutex` blocks the operating system thread. In an async runtime the
thread is an executor worker shared by many tasks, so blocking it stalls every
other task scheduled on it, not only the one holding the lock.

Holding the guard across `.await` is the acute case: the task yields while
holding a blocking lock, another task on the same worker takes the lock and
blocks the thread, and the first task cannot be polled to release it. That is a
deadlock reachable with two tasks and one lock.

Tokio's own documentation names the tradeoff: its async `Mutex` is slower and
is the right choice when the guard must be held across an await, while the std
one is correct for short critical sections with no await inside.

Note the compiler will often, not always, catch this through `Send` on the
returned future; a `spawn_local` or a single-threaded runtime removes that
protection.

What to look for: a `.lock().unwrap()` on `std::sync::Mutex` whose guard is
still live at an `.await`.
