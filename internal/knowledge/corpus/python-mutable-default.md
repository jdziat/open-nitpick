---
title: A Python default argument is evaluated once, so a mutable default is shared
languages: [python]
source: https://docs.python.org/3/reference/compound_stmts.html#function-definitions
checked: 2026-09-08
---
The reference is explicit: *"Default parameter values are evaluated from left
to right when the function definition is executed."* Once, at definition, not
per call.

So `def add(item, into=[])` has one list for the life of the process. Every call
that omits the argument mutates the same object, and the function accumulates
state across unrelated callers. The same holds for `{}`, for a `set()`, and for
anything constructed in the signature, including `datetime.now()`, which
freezes at import.

`None` with an in-body default is the idiom that does not have this property.

What to look for: a default that is a list, dict, set, or a call. A dataclass
field with a mutable default raises `ValueError` instead, which is the language
protecting against exactly this, and is a useful reminder that plain functions
are not protected.
