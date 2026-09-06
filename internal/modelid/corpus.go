// Package modelid is the model-identification experiment of
// docs/plan-full-review.md, section 4: whether the model that wrote a file
// can be told from cheap stylistic features. It is an experiment first; the
// command that would use it is built only if the experiment passes.
package modelid

import "strings"

// Language is one language the corpus is written in.
type Language struct {
	Name string
	Ext  string
}

// Languages are the three the corpus covers.
var Languages = []Language{{"go", ".go"}, {"python", ".py"}, {"typescript", ".ts"}}

// Tasks are the programs every model writes in every language. They are
// small, ordinary, and varied enough that a classifier cannot learn the task
// instead of the author: the experiment splits train and test by task.
var Tasks = []string{
	"An LRU cache with a fixed capacity, get and put operations, and eviction of the least recently used entry.",
	"A parser for a subset of CSV: quoted fields, escaped quotes, and a configurable delimiter, returning rows of strings.",
	"A token-bucket rate limiter with a capacity and a refill rate per second, safe to call from concurrent code where the language has concurrency.",
	"A retry helper that calls a function up to N times with exponential backoff and jitter, and returns the last error when all attempts fail.",
	"A slugify function that lowercases text, replaces runs of non-alphanumerics with single hyphens, trims hyphens, and handles Unicode letters.",
	"A word-frequency counter that reads text, normalises case and punctuation, and returns the top K words with their counts.",
	"A binary search tree with insert, search, delete and in-order traversal.",
	"A loader for a JSON configuration file with defaults, environment-variable overrides, and validation of required keys.",
	"A priority queue backed by a binary heap, with push, pop and peek, ordered by a caller-supplied comparison.",
	"A date-range iterator that yields each day between two dates inclusive, with an option to skip weekends.",
	"A minimal in-memory URL shortener: shorten returns a short code, resolve returns the original URL, with expiry.",
	"A matrix type with multiplication, transpose and a determinant for square matrices, with errors for shape mismatches.",
}

// Slug makes a model id safe as a directory name.
func Slug(model string) string {
	return strings.NewReplacer("/", "_", ":", "_", ".", "-").Replace(model)
}
