---
title: JSON.parse returns any, so a typed binding checks nothing at runtime
languages: [typescript]
source: https://www.typescriptlang.org/docs/handbook/2/everyday-types.html#any
checked: 2026-09-08
---
`const user: User = JSON.parse(body)` type-checks because `JSON.parse` returns
`any`, and `any` is assignable to everything. No property is verified, no
narrowing happens, and the annotation is a comment the compiler agrees with.

Downstream code then reads `user.id.toString()` on undefined, at a line that
looks correct and is nowhere near the parse. Where the JSON came off the
network, its shape is whatever the sender chose, so this is also the boundary
where a schema belongs.

`unknown` instead of `any` forces the check the annotation was pretending to
be, and a validator (zod, valibot, a hand-written type guard) is what makes the
type true.

Note that `strict` does not help here: this is `any` behaving as specified.

What to look for: a type annotation on the result of `JSON.parse`,
`response.json()`, or any `as User` on parsed input.
