## Why

What problem does this solve? Link the issue if there is one.

## What changed

The change itself, in the order a reviewer should read it.

## How to verify

The command a reviewer can run, and what it should print. `go test ./...` is
the floor; name the specific test if you added one.

## Not in this PR

Anything deliberately left out, so a reviewer does not report it as missing.

---

- [ ] Commit subjects follow Conventional Commits, checked by `scripts/check-commits.sh`
- [ ] `go test ./...` passes
- [ ] Documentation updated if behavior or configuration changed
- [ ] Measurements updated in `docs/findings.md` if a number changed
