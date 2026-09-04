# Contributing

Before opening a pull request:

```bash
make lint      # go vet, gofmt, golangci-lint
go test ./...  # no credentials needed
```

The eval battery (`go test -tags eval`, `make quick`, `make benchmark-multifile`)
spends real provider credit and is not run in CI. If a change touches a
prompt, a fixture, or a keyword, read `docs/measurement.md` first: it lists
the rules a number has to satisfy before it is worth recording, and
`internal/evals/promptcollision_test.go` will refuse a prompt that quotes a
planted keyword back.

What a good change looks like here: the smallest diff that fixes one thing,
a comment that says why rather than what, and a test that fails without it.
Claims in comments must point at something the build runs
(`internal/evals/claims_test.go` enforces this).

Measured claims in `docs/` carry their run logs' numbers and their caveats
at the point of the claim. A change that improves a number should add the
before and after, the run count, and what was not controlled for.
