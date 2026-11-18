package config

// Learnings controls repository references. Repository is required for local
// reviews, where a forge cannot supply the repository identity.
type Learnings struct {
	// Enabled includes accepted repository references in review prompts.
	Enabled bool `yaml:"enabled"`
	// Repository is the operator-authorized repository identity bound to each reference.
	Repository string `yaml:"repository"`
	// MaxTokens caps the complete rendered section. Zero selects the built-in
	// cap of 2,000 tokens; it does not mean unbounded.
	MaxTokens int `yaml:"max_tokens"`
}
