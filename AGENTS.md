# Working in this repository

open-nitpick is a self-hosted, model-agnostic pull request reviewer. Everything
above the generated block is written by hand and no command rewrites it.

## Gates

A change passes all of these before it is pushed:

```bash
go test ./... -race          # internal/linters contends on golangci-lint's lock; use -p 1
go vet -tags=eval ./...      # the eval build tag has call sites a plain vet misses
golangci-lint run ./...
make docs                    # the configuration reference is generated
make agents                  # so is the block below
nitpick slop -no-model       # the prose tells, on the files you touched
./scripts/check-commits.sh   # Conventional Commits, 72 characters after the colon
```

## What this repository argues about itself

`docs/measurement.md` holds the rules a number here has to satisfy before it is
published. Two of them shape most of the code:

- **Rule 9**: a guard test fails under mutation. Break what it guards and watch
  it go red. A test that stays green is decoration, and this repository has
  shipped three that passed against the bug they named.
- **Rule 10**: silence needs proving. Zero findings and "nothing ran" are the
  same output and opposite facts, so any path that can return empty owns a test
  asserting it returns empty for the right reason.

`docs/findings.md` records what was measured and what it cost, including the
measurements that came out against the change being argued for. A claim about
quality names its number or is deleted.

## Comments

Say why the code is the way it is, not what the line does. The history of a fix
belongs in the commit that made it. `nitpick slop` will tell you when a comment
restates its line, narrates a changelog, or runs longer than the declaration it
documents.

<!-- nitpick:standards:begin -->

## Conventions, measured

Counted from this repository rather than asserted, so a rule here is one the
code demonstrates. The count after each is how many places were read and how
many of them follow it. Run `nitpick standards` to see every probe including
the contested ones, and `make agents` to regenerate this block.

- Wrap an error you are formatting into a new one with %w, not %v or %s. (199/199)
- Call t.Helper() at the top of a test helper. (134/134)
- Put context.Context first in the parameter list, named ctx. (220/221)
- Name a test after the behaviour it pins, in at least three words. (1178/1202)
- Return named results explicitly; do not use a bare return. (49/51)
- Open an exported declaration's doc comment with the declaration's own name. (694/731)

<!-- nitpick:standards:end -->
