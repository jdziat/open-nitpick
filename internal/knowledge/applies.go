package knowledge

import (
	"fmt"
	"go/version"
	"strings"
)

// Constraint is one clause of an entry's `applies:` line: a named thing, a
// comparison, and a version to compare against.
type Constraint struct {
	// Name is what the version belongs to, as gomod.Versions keys it. Only
	// "go" is answerable today, and a clause naming anything else is parsed
	// and then never decides anything, which is deliberate: an entry may
	// state a constraint this tool cannot yet resolve without that silently
	// removing the entry.
	Name string

	// Op is one of >=, >, <, <=, ==.
	Op string

	// Version is the right-hand side, as it is written in go.mod: "1.23".
	Version string
}

// parseApplies reads an `applies:` line: clauses separated by commas, all of
// which must hold.
//
// No `or`. A rule that applies under either of two unrelated versions is two
// rules or a rule stated badly, and a grammar with precedence in it is a
// grammar somebody has to debug from a corpus file.
func parseApplies(id, value string) ([]Constraint, error) {
	var out []Constraint
	for _, clause := range strings.Split(value, ",") {
		clause = strings.TrimSpace(clause)
		if clause == "" {
			continue
		}
		fields := strings.Fields(clause)
		if len(fields) != 3 {
			return nil, fmt.Errorf("knowledge: %s: applies: %q is not `name op version`", id, clause)
		}
		c := Constraint{Name: strings.ToLower(fields[0]), Op: fields[1], Version: fields[2]}
		switch c.Op {
		case ">=", ">", "<", "<=", "==":
		default:
			return nil, fmt.Errorf("knowledge: %s: applies: %q is not one of >= > < <= ==", id, c.Op)
		}
		if !version.IsValid("go" + c.Version) {
			return nil, fmt.Errorf("knowledge: %s: applies: %q is not a version", id, c.Version)
		}
		out = append(out, c)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("knowledge: %s: applies: is empty; leave the key out instead", id)
	}
	return out, nil
}

// AppliesTo reports whether an entry's constraints hold for the versions a
// repository declares.
//
// An unknown version keeps the entry. The filter drops an entry whose claim is
// about a version this repository does not target, and a repository nothing is
// known about is not one of those. Silencing on ignorance would make a missing
// go.mod look like a corpus that had nothing to say, which is the failure the
// whole knowledge stage is measured against.
func (e Entry) AppliesTo(versions map[string]string) bool {
	for _, c := range e.Applies {
		declared, known := versions[c.Name]
		if !known {
			continue
		}
		if !c.holds(declared) {
			return false
		}
	}
	return true
}

// holds compares a declared version against one clause.
//
// go/version, the same comparison the linter roster makes, so "1.9" is below
// "1.23" rather than above it the way a string compare would have it. A
// declared version this package cannot parse keeps the entry, for AppliesTo's
// reason.
func (c Constraint) holds(declared string) bool {
	got, want := "go"+declared, "go"+c.Version
	if !version.IsValid(got) || !version.IsValid(want) {
		return true
	}
	cmp := version.Compare(version.Lang(got), version.Lang(want))
	switch c.Op {
	case ">=":
		return cmp >= 0
	case ">":
		return cmp > 0
	case "<":
		return cmp < 0
	case "<=":
		return cmp <= 0
	case "==":
		return cmp == 0
	}
	return true
}

// ForVersions keeps the entries whose constraints hold.
func ForVersions(entries []Entry, versions map[string]string) []Entry {
	if len(versions) == 0 {
		return entries
	}
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		if e.AppliesTo(versions) {
			out = append(out, e)
		}
	}
	return out
}
