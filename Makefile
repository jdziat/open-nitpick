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

# Cross-compiled release binaries, named the way action.yml downloads them:
#   dist/nitpick_<version>_<os>_<arch>[.exe]  plus dist/checksums.txt
# The release workflow and CI both run this target, so a binary built for a
# pull request is byte-for-byte the process a release uses.
TARGETS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64

.PHONY: dist
dist:
	rm -rf dist && mkdir -p dist
	@for target in $(TARGETS); do \
		os=$${target%/*}; arch=$${target#*/}; ext=""; \
		[ "$$os" = windows ] && ext=.exe; \
		echo "building $$os/$$arch"; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' \
			-o "dist/$(BINARY)_$(VERSION)_$${os}_$${arch}$$ext" $(PKG) || exit 1; \
	done
	cd dist && sha256sum -- * > checksums.txt
	@ls -1 dist

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

# The documentation site: README.md as the guide page, docs/*.md as their own
# pages, and website/ for what only the site has (landing page, styling).
# Staged into .website/ so every relative link in the repository resolves on
# the site unchanged. Needs mkdocs-material (pip install mkdocs-material).
.PHONY: docs docs-serve docs-reference
# The configuration reference is generated from the configuration, so a key
# the loader accepts and the docs never mention cannot survive a build.
docs-reference:
	go run ./cmd/nitpick config-reference -o docs/configuration-reference.md

docs: docs-reference
	rm -rf .website && mkdir -p .website/docs
	cp website/index.md .website/index.md
	# Rejected logo concepts are not documentation and were reachable in
	# production until this line. logo.jpg joined them: 94 KB of the published
	# site that no page, template or stylesheet names.
	cp -r website/assets .website/assets && rm -rf .website/assets/logo-candidates .website/assets/logo.jpg
	printf -- '---\ntitle: Guide\n---\n' > .website/guide.md
	# The heading is on line 3, under the centred logo, so the address this
	# substitution used to carry never matched and the Guide's h1 was the site's
	# own name, two inches under the header that already says it. README.md has
	# exactly one line reading `# open-nitpick`.
	# The logo and the three badges are deleted with it. A 112px centred mark
	# 150px below the header's own mark and wordmark, over a left-aligned h1,
	# over three shields, is README furniture: it makes the second page a
	# four-minute reader reaches read as a rehosted README, and the License
	# badge is the third statement of what the footer already carries as
	# "Apache-2.0". README.md on GitHub keeps all four lines.
	sed -E 's/^# open-nitpick$$/# Guide/; /^<p align="center"><img src="website\/assets\/logo\.svg"/d; /^\[!\[/d; /^Documentation: <https:\/\/jdziat\.github\.io/d; /^The same documents are published at/d' README.md >> .website/guide.md
	cp docs/*.md .website/docs/
	cp docs/configuration-reference.md .website/docs/configuration-reference.md
	# SECURITY.md sits at the repository root, so its links are docs/-relative;
	# staged beside the pages it points at, that prefix has to go.
	sed -E 's#\]\(docs/#](#g' SECURITY.md > .website/docs/security.md
	# ../README.md resolves for someone reading docs/ in the repository and
	# names no file on the site, where the same document is staged as guide.md.
	# Written the other way round it is the repository copy that breaks, and
	# these pages are read in both places.
	for f in .website/docs/*.md; do sed -E -e 's#\]\(\.\./(internal|cmd|action|\.github)/#](https://github.com/jdziat/open-nitpick/blob/main/\1/#g' -e 's#\]\(\.\./README\.md#](../guide.md#g' "$$f" > "$$f.tmp" && mv "$$f.tmp" "$$f"; done
	sed -E 's#\]\((internal|cmd|action|\.github)/#](https://github.com/jdziat/open-nitpick/blob/main/\1/#g' .website/guide.md > .website/guide.md.tmp && mv .website/guide.md.tmp .website/guide.md
	mkdocs build

docs-serve: docs
	mkdocs serve -a 127.0.0.1:8321 -w README.md -w docs -w website -w mkdocs.yml

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
#
# FIXTURES also reaches the HELD-OUT corpus (evals.HeldOutFixtures), which the
# default run deliberately excludes. It exists to check once, at the end, that a
# tuning gain generalizes. Every run of it against a prompt still being tuned
# converts it into training data and there is no way to un-spend it:
#
#   make eval FIXTURES=$(HELD_OUT)
#
# A name that resolves to nothing is now an error rather than a silent fallback
# to the tuning corpus, and every table prints which corpus it measured.
#
# Every fixture in evals.Fixtures(), for `make quick`; TestTheMakefileNamesTheWholeTuningCorpus pins it.
TUNING := go-nil-deref,go-sql-injection,go-hardcoded-secret,python-command-injection,clean-refactor,style-only,multi-defect,capacity-hint-nit,ts-unbounded-memo-key,go-cancel-goroutine-leak,python-timing-unsafe-hmac,cross-file-copy-nit,sorted-for-min-nit,kotlin-widened-input,php-forbidden-vs-404,go-package-singleton

# Every fixture in evals.CallerFixtures(): the change is the contract and the
# file it breaks is untouched. TestTheMakefileNamesTheWholeCallersCorpus pins it.
# Every fixture in evals.SlopFixtures(): planted/control pairs for the slop
# class. Run with NITPICK_EVAL_SLOP=1 (make eval-slop), or the plants are in a
# class the review never asks for.
SLOP := go-slop-restating-comments,go-clean-why-comments,python-slop-swallowed-exception,python-clean-logged-and-reraised,ts-slop-chat-prose,ts-clean-doc-comment,go-slop-type-excluded-check,go-clean-real-guard,python-slop-test-asserts-nothing,python-clean-test-asserts

CALLERS := go-error-identity-changed,go-clean-wrapped-sentinel,go-return-units-changed,python-precondition-added,python-clean-precondition-satisfied,ts-return-units-changed

# Every fixture in evals.KnowledgeFixtures(): six plants whose defect needs one
# specific fact, each paired with a control whose code attracts the same entry.
# The pairs are the measurement: a gain on the plants alone would not separate
# retrieval working from a reviewer reporting whatever it was shown.
KNOWLEDGE := know-go-defer-in-loop,know-go-clean-defer-scoped,know-go-time-after-leak,know-go-clean-timer-reset,know-go-rows-err-unchecked,know-go-clean-rows-err-checked,know-py-mutable-default,know-py-clean-none-default,know-sh-pipeline-masks-failure,know-sh-clean-pipefail,know-go-nil-map-write,know-go-clean-map-made

# Every fixture in evals.InfoFixtures(): the band no reviewer had located.
INFO := info-go-timeout-halved,info-python-pin-loosened,info-ts-any-widening,info-go-context-string-key,info-java-mutable-constant,info-sql-column-unindexed,info-bash-hardcoded-region,info-python-print-diagnostics,info-ts-magic-duration,info-go-close-error-on-write,info-clean-go-named-constant,info-clean-python-logging

# This list must name EVERY fixture in evals.HeldOutFixtures. A name missing
# from it is not an error — it is a shorter held-out run reporting a
# generalization number over a subset, with nothing on the table saying which
# fixtures were left out. TestTheMakefileSpendsTheWholeHeldOutCorpus compares
# the two and fails when they drift.
HELD_OUT := contract-break,data-loss-migration,ts-unawaited-async,timezone-boundary,clean-sql-allowlist,removed-guard,retry-no-backoff,csharp-client-per-request,bash-fixed-temp-path,cross-file-sort-nit,duplicate-test-case-nit,defensive-copy-nit,rust-crate-for-one-call,ruby-default-page-size

MODELS   ?=
RUNS     ?=
FIXTURES ?=
CAPTURE  ?=
AXIS     ?=
JUDGE    ?=

# JUDGE2 adds a SECOND judge, and every judged figure in the resulting table is
# then printed with the disagreement between the two as ONE value:
#
#   make judge-models JUDGE2=default
#   make benchmark    JUDGE2=default
#   make tune         JUDGE2=default
#
# `default` resolves to evals.SecondJudgeModel — x-ai/grok-4.5, a vendor NO
# contender in the battery shares. It is spelled `default` rather than written
# out here on purpose: a judge named in this file is a judge no test can see, and
# the suite recomputes the vendor check from DefaultModels on every run. Any
# other value is used as given, and is checked and reported the same way.
#
# WHY IT EXISTS. The judge is openai/gpt-5.6-terra and the battery contains three
# OpenAI contenders — gpt-5.6-luna, gpt-5.4, and gpt-5.6-terra, WHICH IS THE
# JUDGE. GRADE, PREC, MISSED, WORTH, J-INFL, J-UNDER and SIGNAL are all one
# vendor's opinion of three of its own models, one of which is the grader. Separately, the same cached findings scored 3.66, 3.90, 3.95 and
# 3.98 across four runs at temperature 0, and no column anywhere said so. One
# number from one judge cannot express either problem; two numbers that disagree
# express both, and the size of the disagreement is the reader's confidence
# interval.
#
# WHAT IT COSTS. Judging, and nothing else. The second judge is handed the
# findings the first judge was just shown, in the same positions, through the
# same re-judge path `make rejudge` uses — no review is run a second time. On the
# persona axis that is one judgement per VARIANT, because each nitpick level is a
# different finding list and each voice variant is a different review.
#
# UNSET IS SUPPORTED AND IS NOT SILENT. Every judged figure then renders `X+?`,
# where `?` is a disagreement that was NOT MEASURED rather than one measured at
# zero, and the table says above itself that it is a single judge and names the
# contenders that judge shares a vendor with.
JUDGE2   ?=

# DUMP writes every judged finding to a JSON Lines file: the finding, the
# judge's verdict and reasoning, and the severity the fixture planted. The
# tables say how many findings were inflated; this says which ones, which is
# what a prompt change has to be aimed at.
#
#   make benchmark DUMP=/tmp/findings.jsonl
#
# It TRUNCATES the file it is given. Use a different path for a held-out run
# than for the tuning loop: a held-out dump carries defect_why, which is the
# planted defect's own prose, and the documented workflow is to read the dump
# and edit the prompt. Records from the held-out corpus carry "held_out":true so
# a file that ended up mixed can still be filtered.
#
# SETTING IT IS OPTIONAL FOR `make benchmark` AND `make judge-models`, which both
# retain their findings either way: with DUMP unset each writes
# internal/evals/.eval-runs/<battery>-<corpus>-<utc>-<pid>.jsonl and logs the
# path. That default is created exclusively and refuses rather than overwriting,
# and it is written under a .partial suffix that Close renames away, so a file
# under the final name is one whose run finished. Both are covered because both
# print the judged table and both can be pointed at the held-out corpus —
# `make judge-models FIXTURES=$(HELD_OUT)` is the invocation that makes the
# second one a spend.
# The reason it is a default at all: the held-out battery that produced the Rule
# 14 evidence ran with this unset, so its findings were never written down and
# two of the four pre-registered conditions needed a re-run of a corpus that is
# spent once. RECALL, NOISE, ANCHOR and L/DEF recompute from a retained file with
# no judge and no network; GRADE, MISSED and SIGNAL still need `make rejudge`.
DUMP     ?=
TIMEOUT  ?=

# REJUDGE re-scores an ALREADY-COLLECTED dump with a different judge, running
# no review at all:
#
#   make rejudge REJUDGE=/tmp/findings.jsonl JUDGE=anthropic/claude-opus-5
#
# The judge is an OpenAI model and three contenders are OpenAI models, one of
# them the judge's own pro variant, so precision and every J-* column rest on a
# vendor scoring its own family. Changing the judge by re-running `make
# benchmark` would change the findings AND the judge together; this judges the
# SAME recorded findings twice, which changes exactly one thing. The report
# prints both rankings with the judge's vendor cohort separated out.
#
# BASELINE names the judge that produced the dump, for the cohort split and the
# report header. The dump records verdicts, not who made them, so this is an
# assertion; it defaults to evals.DefaultJudgeModel.
#
# Pointing JUDGE at the SAME id as BASELINE is the other measurement: identical
# input judged twice by one model is that model's own variance, which is the
# noise floor any vendor comparison has to clear.
#
# DUMP is deliberately NOT forwarded here. It names a file to WRITE and the
# writer truncates on open, so a re-judge that inherited it could be handed the
# file it is reading.
#
# A dump collected before a fixture's source was edited is REFUSED, by design:
# the dump names its fixture and the re-judge resolves that name against the
# corpus as it stands now, so an edited Head would move the change under review
# as well as the judge — the one confound this target exists to eliminate.
# Re-collect the dump rather than working around it.
REJUDGE  ?=
BASELINE ?=

.PHONY: eval
eval:
	$(if $(MODELS),NITPICK_EVAL_MODELS='$(MODELS)') \
	$(if $(RUNS),NITPICK_EVAL_RUNS='$(RUNS)') \
	$(if $(FIXTURES),NITPICK_EVAL_FIXTURES='$(FIXTURES)') \
	$(if $(CAPTURE),NITPICK_EVAL_CAPTURE='$(CAPTURE)') \
	$(if $(RELATED),NITPICK_EVAL_RELATED_CONTEXT='$(RELATED)') \
	go test -tags=eval -count=1 -timeout=60m -v -run 'TestPrompts|TestPlanted|TestKeywords' ./internal/evals/

# Compare persona variants, judged by a strong model standing in for a senior
# human reviewer. AXIS=nitpick (default) or AXIS=voice.
.PHONY: tune
tune:
	$(if $(MODELS),NITPICK_EVAL_MODELS='$(MODELS)') \
	$(if $(FIXTURES),NITPICK_EVAL_FIXTURES='$(FIXTURES)') \
	$(if $(AXIS),NITPICK_EVAL_AXIS='$(AXIS)') \
	$(if $(JUDGE),NITPICK_EVAL_JUDGE='$(JUDGE)') \
	$(if $(JUDGE2),NITPICK_EVAL_JUDGE2='$(JUDGE2)') \
	$(if $(DUMP),NITPICK_EVAL_DUMP='$(DUMP)') \
	$(if $(TIMEOUT),NITPICK_EVAL_TIMEOUT='$(TIMEOUT)') \
	go test -tags=eval -count=1 -timeout=45m -v -run TestTunePersona ./internal/evals/

# Rank every model in the battery by JUDGED quality, not keyword recall.
.PHONY: judge-models
judge-models:
	$(if $(MODELS),NITPICK_EVAL_MODELS='$(MODELS)') \
	$(if $(FIXTURES),NITPICK_EVAL_FIXTURES='$(FIXTURES)') \
	$(if $(JUDGE),NITPICK_EVAL_JUDGE='$(JUDGE)') \
	$(if $(JUDGE2),NITPICK_EVAL_JUDGE2='$(JUDGE2)') \
	$(if $(DUMP),NITPICK_EVAL_DUMP='$(DUMP)') \
	$(if $(TIMEOUT),NITPICK_EVAL_TIMEOUT='$(TIMEOUT)') \
	go test -tags=eval -count=1 -timeout=90m -v -run TestJudgeModels ./internal/evals/

# Head-to-head against Incumbent on identical fixtures, same judge.
# Requires the incumbent CLI, authenticated: incumbent auth login
.PHONY: benchmark
benchmark:
	$(if $(MODELS),NITPICK_EVAL_MODELS='$(MODELS)') \
	$(if $(FIXTURES),NITPICK_EVAL_FIXTURES='$(FIXTURES)') \
	$(if $(JUDGE),NITPICK_EVAL_JUDGE='$(JUDGE)') \
	$(if $(JUDGE2),NITPICK_EVAL_JUDGE2='$(JUDGE2)') \
	$(if $(RUNS),NITPICK_EVAL_RUNS='$(RUNS)') \
	$(if $(DUMP),NITPICK_EVAL_DUMP='$(DUMP)') \
	$(if $(TIMEOUT),NITPICK_EVAL_TIMEOUT='$(TIMEOUT)') \
	go test -tags=eval -count=1 -timeout=90m -v -run TestBenchmarkAgainstIncumbent ./internal/evals/

# Re-judge findings that were already collected, with a different judge.
# Runs no review: every finding comes out of the dump, in its recorded position.
.PHONY: rejudge
rejudge:
	$(if $(REJUDGE),NITPICK_EVAL_REJUDGE_DUMP='$(REJUDGE)') \
	$(if $(JUDGE),NITPICK_EVAL_JUDGE='$(JUDGE)') \
	$(if $(BASELINE),NITPICK_EVAL_BASELINE_JUDGE='$(BASELINE)') \
	go test -tags=eval -count=1 -timeout=90m -v -run TestRejudgeDump ./internal/evals/

# The iteration model. Cheap enough to run the whole tuning corpus for a few
# cents, so a prompt or analyzer change can be measured before it is committed
# rather than after; docs/findings.md records how it compares with the default
# reviewer. Override with QUICK=<openrouter id>.
QUICK ?= z-ai/glm-5.3-flash

# One cheap pass over the tuning corpus and the multi-file corpus, judge-free,
# with related context off and on. What to run after editing a prompt.
.PHONY: quick
quick:
	$(MAKE) benchmark-multifile MODELS='$(QUICK)' RUNS='$(or $(RUNS),1)' FIXTURES='$(or $(FIXTURES),$(TUNING))'
	$(MAKE) benchmark-multifile MODELS='$(QUICK)' RUNS='$(or $(RUNS),1)'

# Judge-free head-to-head on the MULTI-FILE corpus: every model with related
# context off and on, against every incumbent with a cached or collectable
# review (Incumbent).
# FIXTURES= points it at any other corpus, e.g. the tuning corpus, to see what
# related context costs where the defect is in the diff.
# The full-review acceptance: the fixture repository in
# internal/evals/fixtures_fullreview.go reviewed whole. One model, one run.
.PHONY: eval-fullreview
eval-fullreview:
	$(if $(MODELS),NITPICK_EVAL_MODELS='$(MODELS)') \
	go test -tags=eval -count=1 -timeout=45m -v -run 'TestFullReviewFixture|TestRepoScoreFixture' ./internal/evals/

# The slop corpus, judge-free, with review.slop on: recall over the plants
# and, above all, silence on the controls.
.PHONY: eval-slop
eval-slop:
	NITPICK_EVAL_SLOP=1 $(MAKE) benchmark-multifile FIXTURES='$(SLOP)' MODELS='$(or $(MODELS),z-ai/glm-5.3-flash)' RUNS='$(or $(RUNS),1)'

.PHONY: benchmark-multifile
benchmark-multifile:
	$(if $(MODELS),NITPICK_EVAL_MODELS='$(MODELS)') \
	$(if $(FIXTURES),NITPICK_EVAL_FIXTURES='$(FIXTURES)') \
	$(if $(RUNS),NITPICK_EVAL_RUNS='$(RUNS)') \
	$(if $(TIMEOUT),NITPICK_EVAL_TIMEOUT='$(TIMEOUT)') \
	$(if $(ENGINE_LOG),NITPICK_EVAL_ENGINE_LOG='$(ENGINE_LOG)') \
	go test -tags=eval -count=1 -timeout=300m -v -run TestBenchmarkMultiFile ./internal/evals/
