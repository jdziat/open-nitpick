BINARY := nitpick
PKG    := ./cmd/nitpick
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: all
all: check build

.PHONY: build
build:
	go build -ldflags '$(LDFLAGS)' -o bin/$(BINARY) $(PKG)

.PHONY: install
install:
	go install -ldflags '$(LDFLAGS)' $(PKG)

.PHONY: test
test:
	go test ./...

# The engine reviews batches concurrently against a shared client, so the race
# detector is not optional here.
.PHONY: race
race:
	go test -race ./...

.PHONY: cover
cover:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

.PHONY: lint
lint:
	go vet ./...
	@test -z "$$(gofmt -l .)" || { echo "gofmt needed:"; gofmt -l .; exit 1; }
	@if command -v golangci-lint >/dev/null; then \
		golangci-lint run; \
	else \
		echo "golangci-lint not installed; skipping"; \
	fi

.PHONY: check
check: lint race

# Review this repository's own uncommitted changes.
.PHONY: review
review: build
	./bin/$(BINARY) review -v

.PHONY: clean
clean:
	rm -rf bin coverage.out

# --- Evaluation against real models -----------------------------------------
# Calls real endpoints and costs money, so it is excluded from `make check`.
# Requires OPENROUTER_API_KEY, read from .env if present.
#
#   make eval
#   make eval MODELS=openai/gpt-4o-mini
#   make eval RUNS=3
#   make eval FIXTURES=go-nil-deref,clean-refactor
#   make eval CAPTURE=testdata/responses
MODELS   ?=
RUNS     ?=
FIXTURES ?=
CAPTURE  ?=
AXIS     ?=
JUDGE    ?=

.PHONY: eval
eval:
	NITPICK_EVAL_MODELS='$(MODELS)' \
	NITPICK_EVAL_RUNS='$(RUNS)' \
	NITPICK_EVAL_FIXTURES='$(FIXTURES)' \
	NITPICK_EVAL_CAPTURE='$(CAPTURE)' \
	go test -tags=eval -count=1 -timeout=60m -v -run 'TestPrompts|TestPlanted|TestKeywords' ./internal/evals/

# Compare persona variants, judged by a strong model standing in for a senior
# human reviewer. AXIS=nitpick (default) or AXIS=voice.
.PHONY: tune
tune:
	NITPICK_EVAL_MODELS='$(MODELS)' \
	NITPICK_EVAL_FIXTURES='$(FIXTURES)' \
	NITPICK_EVAL_AXIS='$(AXIS)' \
	NITPICK_EVAL_JUDGE='$(JUDGE)' \
	go test -tags=eval -count=1 -timeout=45m -v -run TestTunePersona ./internal/evals/

# Rank every model in the battery by JUDGED quality, not keyword recall.
.PHONY: judge-models
judge-models:
	NITPICK_EVAL_MODELS='$(MODELS)' \
	NITPICK_EVAL_FIXTURES='$(FIXTURES)' \
	NITPICK_EVAL_JUDGE='$(JUDGE)' \
	go test -tags=eval -count=1 -timeout=90m -v -run TestJudgeModels ./internal/evals/

# Head-to-head against Incumbent on identical fixtures, same judge.
# Requires the incumbent CLI, authenticated: incumbent auth login
.PHONY: benchmark
benchmark:
	NITPICK_EVAL_MODELS='$(MODELS)' \
	NITPICK_EVAL_FIXTURES='$(FIXTURES)' \
	NITPICK_EVAL_JUDGE='$(JUDGE)' \
	go test -tags=eval -count=1 -timeout=90m -v -run TestBenchmarkAgainstIncumbent ./internal/evals/

# Collect Incumbent reviews one fixture at a time, caching each.
# The free CLI allowance is small; re-run until nothing is outstanding.
.PHONY: collect-incumbent
collect-incumbent:
	NITPICK_EVAL_FIXTURES='$(FIXTURES)' \
	go test -tags=eval -count=1 -timeout=120m -v -run TestCollectIncumbent ./internal/evals/
