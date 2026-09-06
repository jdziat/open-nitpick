package config

import "time"

// DefaultIgnore excludes paths where review comments are noise: vendored and
// generated code, lockfiles, and fixtures. A repository can replace this list
// wholesale via review.ignore.
var DefaultIgnore = []string{
	"**/vendor/**",
	"**/node_modules/**",
	"**/testdata/**",
	"**/dist/**",
	"**/build/**",
	"**/*.pb.go",
	"**/*.gen.go",
	"**/*_generated.go",
	"**/*.min.js",
	"**/*.map",
	"**/go.sum",
	"**/package-lock.json",
	"**/pnpm-lock.yaml",
	"**/yarn.lock",
	"**/Cargo.lock",
	"**/poetry.lock",
	"**/*.snap",
	"**/*.svg",
	"**/*.png",
	"**/*.jpg",
	"**/*.gif",
	"**/*.ico",
	"**/*.pdf",
}

// DefaultLinters lists the runners enabled out of the box. Only those with
// something to read in the change are executed.
//
// eslint and semgrep are NOT here, and their absence is the whole point: both
// refuse to run without an operator configuration outside the repository —
// eslint because its config is JavaScript it would execute, semgrep because it
// has no default rule set — so neither can ever run under the shipped defaults.
//
// THE BUG THAT CAUSED: with all four listed, `mode: strict` failed EVERY review
// out of the box, on "linter semgrep is enabled but not available: not
// configured". Strict means "an analyzer I asked for did not run", and nobody
// asked for these two — the default list did. Listing an analyzer that cannot
// run also spent a line of the published roster, on every pull request forever,
// saying nothing; that is how a reader learns to skip the block where a real
// absence is announced.
//
// Naming either in linters.enabled still works and still means it: strict then
// does catch an operator who enabled one without configuring it.
var DefaultLinters = []string{"golangci-lint", "ruff"}

// The catalog analyzers (internal/linters) are not listed here because they
// are not ENABLED by default: they are auto-detected. Naming one in
// linters.enabled makes it a promise strict mode checks; leaving it to
// linters.auto_detect runs it when it is installed and skips it when it is
// not.

// Defaults returns the built-in configuration. The default model reads
// LLM_PROVIDER/LLM_MODEL-style environment configuration only after validation
// confirms a provider was supplied, so defaults intentionally leave the model
// unset: naming a model is the one thing a user must decide.
func Defaults() *Config {
	return &Config{
		Models: Models{
			Default: ModelSpec{
				StructuredOutput: StructuredAuto,

				// The timeout is generous on purpose, and max_tokens is
				// deliberately NOT set. A reasoning model spends its thinking
				// inside max_tokens on most providers and inside the wall
				// clock on all of them, and the two-minute, 8k-token defaults
				// this used to ship lost one review in five on glm-5.3-flash:
				// the schema-path answer came back truncated to prose, and
				// the JSON fallback then died on the HTTP timeout while the
				// model was still generating. Unset, an OpenAI-compatible
				// request carries no max_tokens and the model's own output
				// maximum applies — which is the only number that is not a
				// guess. The one provider whose SDK path substitutes a small
				// constant for "unset" is handled in llm.Client.CallOptions.
				Timeout: 10 * time.Minute,

				// Reviews should be reproducible. Left unset, providers apply
				// their own default (1.0 on Anthropic), and identical runs over
				// the same diff then disagree about both which findings exist
				// and how severe they are — which makes a severity gate a coin
				// flip.
				Temperature: ptr(0.0),
			},
		},
		Review: Review{
			MaxFiles:              60,
			TokenBudgetPerRequest: 60000,
			MaxFilesPerRequest:    6,
			Concurrency:           4,
			// Advisory by default. A reviewer that blocks merges on its first
			// false positive is a reviewer the team switches off — and this
			// one has not yet earned that trust. Opt in with fail_on.
			FailOn:           SeverityNone,
			MinSeverity:      SeverityInfo,
			Ignore:           append([]string(nil), DefaultIgnore...),
			IncludeFullFiles: true,
			MaxFileBytes:     256 * 1024,
			SkipGenerated:    true,
			Summary:          true,
			// On by default because it only does anything on a pull request
			// this tool has already reviewed, where the alternative is posting
			// the same findings again on every push.
			Incremental: true,
			// On: it reads only what the change already imports, and every
			// price in the model sweep was measured with it on (see
			// docs/findings.md). The caller walk is off: it reads files the
			// change never named, and an operator should choose that
			// knowing the fetch count and the measured gain.
			RelatedContext:        true,
			RelatedContextCallers: false,
			// Off until its controls are seen to be silent; see
			// docs/plan-full-review.md, section 2.
			Slop:                 false,
			RelatedContextTokens: 16000,
		},
		Persona: DefaultPersona(),
		// Stated rather than left to the zero value, because "off" here is a
		// decision with a reason: the pass is unmeasured and its risk is to
		// recall. See Validation.Enabled.
		Validation: Validation{Enabled: false},
		Linters: Linters{
			Enabled:          append([]string(nil), DefaultLinters...),
			Mode:             LinterAuto,
			Timeout:          2 * time.Minute,
			OnlyChangedLines: true,
			// No reduction by default: an analyzer that named a level we have
			// is reported at that level. Capping here by default would be the
			// old fold wearing a configuration key — the same silent policy for
			// every repository, just spelled differently. A team that does not
			// want semgrep, or a line in someone's .golangci.yml, deciding its
			// gate says so.
			MaxSeverity: SeverityCritical,
		},
	}
}

// ptr returns a pointer to v, for optional scalar defaults.
func ptr[T any](v T) *T { return &v }
