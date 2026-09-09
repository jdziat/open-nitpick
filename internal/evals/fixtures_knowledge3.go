package evals

import "github.com/jdziat/open-nitpick/internal/config"

// A mutable default argument is one object shared by every call.
func knowMutableDefaultFixture() Fixture {
	return Fixture{
		Name: "know-py-mutable-default",
		Base: map[string]string{"basket/cart.py": `"""Shopping baskets."""


def total(items):
    return sum(i.price for i in items)
`},
		Head: map[string]string{"basket/cart.py": `"""Shopping baskets."""


def total(items):
    return sum(i.price for i in items)


def add_item(item, basket=[]):
    """Add an item and return the basket."""
    basket.append(item)
    return basket
`},
		Defects: []Defect{{
			Path: "basket/cart.py", Line: 8,
			Keywords: []string{"mutable default", "default argument", "evaluated once",
				"shared between calls", "same list", "accumulat"},
			Class:        config.ClassCorrectness,
			WantSeverity: config.SeverityError,
			Why:          "the default list is created once at definition and shared by every call that omits it",
		}},
	}
}

// The control: None with an in-body default.
func knowCleanNoneDefaultFixture() Fixture {
	return Fixture{
		Name: "know-py-clean-none-default",
		Base: map[string]string{"basket/cart.py": `"""Shopping baskets."""


def total(items):
    return sum(i.price for i in items)
`},
		Head: map[string]string{"basket/cart.py": `"""Shopping baskets."""


def total(items):
    return sum(i.price for i in items)


def add_item(item, basket=None):
    """Add an item and return the basket."""
    if basket is None:
        basket = []
    basket.append(item)
    return basket
`},
	}
}

// set -e does not fail a pipeline whose last command succeeds.
func knowPipefailFixture() Fixture {
	return Fixture{
		Name: "know-sh-pipeline-masks-failure",
		Base: map[string]string{"scripts/install.sh": `#!/usr/bin/env bash
set -e

echo "installing"
`},
		Head: map[string]string{"scripts/install.sh": `#!/usr/bin/env bash
set -e

echo "installing"
curl -fsSL "https://example.invalid/tool.tar.gz" | tar -xz -C /usr/local/bin
echo "done"
`},
		Defects: []Defect{{
			Path: "scripts/install.sh", Line: 5,
			Keywords: []string{"pipefail", "exit status of the last", "last command in the pipeline",
				"masked", "download fail", "curl fail"},
			Class:        config.ClassCorrectness,
			WantSeverity: config.SeverityError,
			Why:          "without pipefail the pipeline's status is tar's, so a failed download is a successful step",
		}},
	}
}

// The control: the same pipeline with pipefail set.
func knowCleanPipefailSetFixture() Fixture {
	return Fixture{
		Name: "know-sh-clean-pipefail",
		Base: map[string]string{"scripts/install.sh": `#!/usr/bin/env bash
set -e

echo "installing"
`},
		Head: map[string]string{"scripts/install.sh": `#!/usr/bin/env bash
set -euo pipefail

echo "installing"
curl -fsSL "https://example.invalid/tool.tar.gz" | tar -xz -C /usr/local/bin
echo "done"
`},
	}
}

// Writing to a nil map panics; reading one does not.
func knowNilMapWriteFixture() Fixture {
	return Fixture{
		Name: "know-go-nil-map-write",
		Base: map[string]string{"go.mod": knowGoMod, "cache/store.go": `package cache

// Store holds values by key.
type Store struct {
	values map[string]string
}

// New returns a ready Store.
func New() *Store {
	return &Store{values: map[string]string{}}
}

// Get returns a value, and false when it is absent.
func (s *Store) Get(k string) (string, bool) {
	v, ok := s.values[k]
	return v, ok
}
`},
		Head: map[string]string{"go.mod": knowGoMod, "cache/store.go": `package cache

// Store holds values by key.
type Store struct {
	values map[string]string
}

// New returns a ready Store.
func New() *Store {
	return &Store{values: map[string]string{}}
}

// FromSnapshot rebuilds a Store from a saved list of keys.
func FromSnapshot(keys []string) *Store {
	s := &Store{}
	for _, k := range keys {
		s.values[k] = ""
	}
	return s
}

// Get returns a value, and false when it is absent.
func (s *Store) Get(k string) (string, bool) {
	v, ok := s.values[k]
	return v, ok
}
`},
		Defects: []Defect{{
			Path: "cache/store.go", Line: 16,
			Keywords: []string{"nil map", "assignment to entry in nil map",
				"not initialis", "not initializ", "never made", "zero value of a map"},
			Class:        config.ClassCorrectness,
			WantSeverity: config.SeverityCritical,
			SeverityNote: "critical rather than error: the second construction path panics on its first write, in every caller that takes it",
			Why:          "FromSnapshot leaves values nil and then writes to it",
		}},
	}
}

// The control: the second path makes the map too.
func knowCleanMapMadeFixture() Fixture {
	return Fixture{
		Name: "know-go-clean-map-made",
		Base: map[string]string{"go.mod": knowGoMod, "cache/store.go": `package cache

// Store holds values by key.
type Store struct {
	values map[string]string
}

// New returns a ready Store.
func New() *Store {
	return &Store{values: map[string]string{}}
}

// Get returns a value, and false when it is absent.
func (s *Store) Get(k string) (string, bool) {
	v, ok := s.values[k]
	return v, ok
}
`},
		Head: map[string]string{"go.mod": knowGoMod, "cache/store.go": `package cache

// Store holds values by key.
type Store struct {
	values map[string]string
}

// New returns a ready Store.
func New() *Store {
	return &Store{values: map[string]string{}}
}

// FromSnapshot rebuilds a Store from a saved list of keys.
func FromSnapshot(keys []string) *Store {
	s := New()
	for _, k := range keys {
		s.values[k] = ""
	}
	return s
}

// Get returns a value, and false when it is absent.
func (s *Store) Get(k string) (string, bool) {
	v, ok := s.values[k]
	return v, ok
}
`},
	}
}
