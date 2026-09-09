---
title: Array.prototype.sort sorts in place and compares as strings by default
languages: [javascript, typescript]
source: https://tc39.es/ecma262/#sec-array.prototype.sort
checked: 2026-09-08
---
Two separate surprises in one method.

It mutates the receiver and returns the same array, so `const sorted =
items.sort()` leaves `items` sorted too. A function that sorts a parameter to
compute something has reordered its caller's data as a side effect, and where
that array is React state or a memoised value, the mutation is invisible until
something else reads it.

With no comparator, elements are converted to strings and compared by UTF-16
code unit. So `[1, 5, 10, 25].sort()` is `[1, 10, 25, 5]`. This is correct per
the specification and wrong in nearly every use.

`toSorted` returns a new array and is available from ES2023, which a project's
target says whether it can use.

What to look for: `.sort()` with no comparator on numbers, and `.sort()` on a
value the function did not create.
