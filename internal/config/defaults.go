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

// DefaultLinters lists runners considered in auto mode. Only those actually
// detected in the repository are executed.
var DefaultLinters = []string{"golangci-lint", "ruff", "eslint", "semgrep"}

// Defaults returns the built-in configuration. The default model reads
// LLM_PROVIDER/LLM_MODEL-style environment configuration only after validation
// confirms a provider was supplied, so defaults intentionally leave the model
// unset: naming a model is the one thing a user must decide.
func Defaults() *Config {
	return &Config{
		Models: Models{
			Default: ModelSpec{
				StructuredOutput: StructuredAuto,
				Timeout:          2 * time.Minute,

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
		},
		Persona: DefaultPersona(),
		Linters: Linters{
			Enabled:          append([]string(nil), DefaultLinters...),
			Mode:             LinterAuto,
			Timeout:          2 * time.Minute,
			OnlyChangedLines: true,
		},
	}
}

// ptr returns a pointer to v, for optional scalar defaults.
func ptr[T any](v T) *T { return &v }
