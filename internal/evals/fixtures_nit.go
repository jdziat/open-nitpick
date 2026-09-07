package evals

import "github.com/jdziat/open-nitpick/internal/config"

// nitFixtures are the corpus's nit-level plants.
//
// The note behind it is in docs/measurement.md#nitfixtures.
func nitFixtures() []Fixture {
	return []Fixture{
		redundantSnapshotCopyNitFixture(),
		redundantSortNitFixture(),
		sortedForMinNitFixture(),
		duplicateTestCaseNitFixture(),
		defensiveCopyOfLocalNitFixture(),
	}
}

// redundantSnapshotCopyNitFixture copies a slice that is already a copy,
// across
// two files.
//
// The note behind it is in docs/measurement.md#redundantsnapshotcopynitfixture.
func redundantSnapshotCopyNitFixture() Fixture {
	return Fixture{
		Name: "cross-file-copy-nit",
		Base: map[string]string{
			"store/store.go": `package store

import "sync"

// Event is one thing that happened.
type Event struct {
	ID   string
	Kind string
}

// Store holds the events recorded so far.
type Store struct {
	mu     sync.Mutex
	events []Event
}

// Add records an event.
func (s *Store) Add(e Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
}
`,
		},
		Head: map[string]string{
			"store/store.go": `package store

import "sync"

// Event is one thing that happened.
type Event struct {
	ID   string
	Kind string
}

// Store holds the events recorded so far.
type Store struct {
	mu     sync.Mutex
	events []Event
}

// Add records an event.
func (s *Store) Add(e Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
}

// Snapshot returns the events recorded so far.
//
// The returned slice is a fresh one the caller owns. It is built under the lock
// and shares no backing array with the store, so a caller may keep it, reorder
// it, or hand it on without coordinating with Add.
func (s *Store) Snapshot() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]Event, len(s.events))
	copy(out, s.events)
	return out
}
`,
			"report/summary.go": `package report

import "example.com/app/store"

// Summary is what the dashboard renders.
type Summary struct {
	Events []store.Event
	Total  int
}

// Build reads the store and returns the summary for this request.
func Build(s *store.Store) Summary {
	events := s.Snapshot()

	// Keep our own copy so nothing the store does later can change what this
	// request already rendered.
	own := make([]store.Event, len(events))
	copy(own, events)

	return Summary{Events: own, Total: len(own)}
}
`,
		},
		Extra: map[string]string{
			"go.mod": "module example.com/app\n\ngo 1.25\n",
		},
		Defects: []Defect{{
			Path: "report/summary.go",
			Line: 17, // the make() that allocates the second slice
			// Not "copy": the change contains `copy(own, events)` and
			// store.go's own `copy(out, s.events)`, so any comment that quotes
			// either line would score as a detection of a redundancy it never
			// noticed. Not "race", "concurrent" or "stale" either: those are
			// the words of the objection this fixture exists to refuse.
			Keywords: []string{
				"already returns a copy", "already a copy", "already returns a fresh",
				"already returns its own", "copy of a copy", "copies it again",
				"copied twice", "second copy", "redundant copy", "unnecessary copy",
				"needless copy", "no need to copy", "allocates a second slice",
			},
			// resource is the least-wrong box, exactly as it is for
			// capacity-hint-nit: the class means "leaks and unbounded growth"
			// and this is one bounded allocation that is freed with the
			// request. The closed set has no home for work that is merely
			// wasted, which is why nothing scores a model against Class.
			Class:        config.ClassResource,
			WantSeverity: config.SeverityNit,
			SeverityNote: "nit under \"minor and optional\" — " +
				"Snapshot's contract is in the same change, so the second slice defends against nothing " +
				"and no input makes Build answer differently. Not info: \"a defensible concern the author " +
				"should consciously accept or reject\" needs a trade-off to weigh, and a copy that buys " +
				"nothing is not a position anyone holds. Not warning: there is no \"genuine hazard under " +
				"plausible conditions\" — the hazard the comment names is the one Snapshot already " +
				"removed. It shares the resource class with multi-defect's descriptor leak (error) and " +
				"retry-no-backoff (warning) and differs from both in that nothing accumulates: the extra " +
				"slice dies with the request.",
			Why: "Snapshot already returns a slice the caller owns, so copying it again allocates and fills a second slice of the same length on every request",
		}},
	}
}

// redundantSortNitFixture sorts an array the callee already sorted, in
// TypeScript.
//
// The note behind it is in docs/measurement.md#redundantsortnitfixture.
func redundantSortNitFixture() Fixture {
	return Fixture{
		Name: "cross-file-sort-nit",
		Base: map[string]string{
			"src/members.ts": `export interface Member {
  id: string;
  displayName: string;
  role: string;
}

const byDisplayName = (a: Member, b: Member): number =>
  a.displayName.localeCompare(b.displayName);

// listMembers returns a team's members.
export function listMembers(members: Member[]): Member[] {
  return [...members].sort(byDisplayName);
}
`,
		},
		Head: map[string]string{
			"src/members.ts": `export interface Member {
  id: string;
  displayName: string;
  role: string;
}

const byDisplayName = (a: Member, b: Member): number =>
  a.displayName.localeCompare(b.displayName);

// listMembers returns a team's members, sorted by display name.
//
// The spread copies before sorting, so the caller is handed an array of its own
// that is already in order, and the roster's own storage is never reordered.
export function listMembers(members: Member[]): Member[] {
  return [...members].sort(byDisplayName);
}
`,
			// rosterHeading is filler, and it is deliberately after the plant so
			// the defect stays on line 6. It is here because the file was 12
			// lines long and the plant sits at line 6, which put every line of
			// it within noiseTolerance of the plant: a reviewer commenting on
			// every single line was charged NO noise, because each comment was
			// "near" the defect by virtue of the file being short.
			// TestTheNoiseToleranceIsPinnedByTheCorpus calls that vacuous and
			// fails on it, correctly, the NOISE column measures nothing on a
			// fixture smaller than its own radius. Nothing here is a second
			// defect: the singular case is handled, and no keyword of the plant
			// appears, so a finding about this function cannot score as a
			// detection of the redundant sort.
			"src/roster.ts": `import { Member, listMembers } from "./members";

// renderRoster returns one line per member, in alphabetical order.
export function renderRoster(members: Member[]): string[] {
  const listed = listMembers(members);
  const ordered = [...listed].sort((a, b) =>
    a.displayName.localeCompare(b.displayName),
  );

  return ordered.map((m) => m.displayName + " (" + m.role + ")");
}

// rosterHeading is the line shown above the roster.
export function rosterHeading(teamName: string, count: number): string {
  if (count === 1) {
    return teamName + ": 1 member";
  }

  return teamName + ": " + count + " members";
}
`,
		},
		Extra: map[string]string{
			"tsconfig.json": "{\n  \"compilerOptions\": {\n    \"target\": \"ES2022\",\n    \"strict\": true\n  }\n}\n",
		},
		Defects: []Defect{{
			Path: "src/roster.ts",
			Line: 6, // the spread-and-sort of an array that arrives sorted
			// Not "sort" alone: it is on the changed line, in the callee, and
			// in the doc comment, so the in-place-mutation objection this
			// fixture is built to refuse would quote it and score. Not
			// "comparator" or "duplicate": the duplicated-comparator finding is
			// a different, weaker claim whose fix keeps the redundant sort.
			Keywords: []string{
				"already sorted", "already in order", "already returns them sorted",
				"already returns a sorted", "already returns a fresh", "already returns its own",
				"sorted twice", "sorts them again", "sorted again", "second sort",
				"redundant sort", "unnecessary sort", "no need to sort", "sorts a second time",
			},
			Class:        config.ClassResource,
			WantSeverity: config.SeverityNit,
			SeverityNote: "nit under \"minor and optional\", which covers the spread on the same line as " +
				"the sort: both are work the render throws away. Not warning: sort " +
				"runs on an array listMembers already spread, so nothing shared is reordered and there " +
				"is no \"genuine hazard under plausible conditions\" to raise it for. Not info: a second " +
				"sort of an already-sorted array is not a concern \"the author should consciously accept " +
				"or reject\", because there is no reading of the contract under which it buys anything. " +
				"It sits with cross-file-copy-nit in the resource class at the same level for the same " +
				"reason: bounded work, freed immediately, no wrong answer.",
			Why: "listMembers already returns a fresh array sorted by display name, so renderRoster copies and re-sorts it once per render for an identical result",
		}},
	}
}

// sortedForMinNitFixture orders a whole list to take one element from it.
//
// Python, so the corpus is not measuring a Go-shaped prompt, and a defect with
// no cross-file component: everything needed is on one line. sorted() builds a
// full copy of the list and orders all of it; min() with the same key walks it
// once and allocates nothing. The two agree on ties as well as on the answer,
// sorted() is stable, so [0] is the first minimum, which is what min() returns
// , so this is a pure cost with no behavioral difference to weigh.
//
// The false positive it invites is the empty-list objection: sorted(...)[0]
// raising IndexError is a real bug in the general case, and a reviewer that
// reaches for it here has not read the two lines above, which return None
// first. That guard is deliberate, and it is why no keyword contains "empty",
// "IndexError" or "sorted", the last of those is the changed line's own most
// typed token, and "this sorts the list and takes the first element, which
// raises IndexError when empty" must not score as a detection of a cost the
// comment never mentions. "min(" carries the parenthesis for the same reason:
// a finding about a missing minimum-length check should not match.
func sortedForMinNitFixture() Fixture {
	return Fixture{
		Name: "sorted-for-min-nit",
		Base: map[string]string{
			"sensors.py": `from dataclasses import dataclass


@dataclass
class Reading:
    """One temperature reading."""

    sensor: str
    celsius: float


def average(readings):
    """Return the mean temperature, or None when there are no readings."""
    if not readings:
        return None
    return sum(r.celsius for r in readings) / len(readings)
`,
		},
		Head: map[string]string{
			"sensors.py": `from dataclasses import dataclass


@dataclass
class Reading:
    """One temperature reading."""

    sensor: str
    celsius: float


def average(readings):
    """Return the mean temperature, or None when there are no readings."""
    if not readings:
        return None
    return sum(r.celsius for r in readings) / len(readings)


def coldest(readings):
    """Return the coldest reading, or None when there are no readings."""
    if not readings:
        return None
    return sorted(readings, key=lambda r: r.celsius)[0]
`,
		},
		Extra: map[string]string{
			"pyproject.toml": "[project]\nname = \"sensors\"\nversion = \"0.1.0\"\n",
		},
		Defects: []Defect{{
			Path: "sensors.py",
			Line: 23, // the sorted(...)[0]
			Keywords: []string{
				"min(", "single pass", "one pass", "whole list", "entire list",
				"n log n", "unnecessary sort", "needless sort", "without sorting",
				"no need to sort", "sorts every", "orders every",
			},
			Class:        config.ClassResource,
			WantSeverity: config.SeverityNit,
			SeverityNote: "nit under \"minor and optional\": sorted() copies the list and orders all of " +
				"it to read one element, which is a copy plus a sort that the call throws away. Not " +
				"error — \"a real bug that produces incorrect behavior on a reachable path\" — because " +
				"sorted() is stable and min() returns the first minimum too, so no input makes coldest " +
				"answer differently. Not warning: the list is one request's readings, the cost is bounded " +
				"by its length, and nothing degrades. Same level and same class as the other two " +
				"wasted-work plants; the descriptor leak that shares the class is an error because it " +
				"accumulates and this does not.",
			Why: "sorted() copies and orders the whole list to read its first element, where min() with the same key walks it once and copies nothing",
		}},
	}
}

// duplicateTestCaseNitFixture adds a table case that is already in the table.
//
// The note behind it is in docs/measurement.md#duplicatetestcasenitfixture.
func duplicateTestCaseNitFixture() Fixture {
	return Fixture{
		Name: "duplicate-test-case-nit",
		Base: map[string]string{
			"slug/slug.go": `package slug

import (
	"regexp"
	"strings"
)

var nonWord = regexp.MustCompile(` + "`" + `[^a-z0-9]+` + "`" + `)

// Slug returns s as a lowercase, hyphen-separated slug.
func Slug(s string) string {
	lowered := strings.ToLower(s)
	return strings.Trim(nonWord.ReplaceAllString(lowered, "-"), "-")
}
`,
			"slug/slug_test.go": `package slug

import "testing"

func TestSlug(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"lowercases", "Hello", "hello"},
		{"replaces spaces", "hello world", "hello-world"},
		{"strips punctuation", "hello, world!", "hello-world"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Slug(c.in); got != c.want {
				t.Errorf("Slug(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
`,
		},
		Head: map[string]string{
			"slug/slug.go": `package slug

import (
	"regexp"
	"strings"
)

var nonWord = regexp.MustCompile(` + "`" + `[^a-z0-9]+` + "`" + `)

// Slug returns s as a lowercase, hyphen-separated slug.
func Slug(s string) string {
	lowered := strings.ToLower(s)
	return strings.Trim(nonWord.ReplaceAllString(lowered, "-"), "-")
}
`,
			"slug/slug_test.go": `package slug

import "testing"

func TestSlug(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"lowercases", "Hello", "hello"},
		{"replaces spaces", "hello world", "hello-world"},
		{"strips punctuation", "hello, world!", "hello-world"},
		{"collapses runs of spaces", "hello   world", "hello-world"},
		{"trims a trailing space", "hello world ", "hello-world"},
		{"space becomes a hyphen", "hello world", "hello-world"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Slug(c.in); got != c.want {
				t.Errorf("Slug(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
`,
		},
		Defects: []Defect{{
			Path: "slug/slug_test.go",
			Line: 16, // the case that repeats line 12
			// The bare stems "duplicate", "duplicates" and "repeats" were here
			// and had to go. They admitted precisely the reviewer "identical
			// to" carries its preposition to exclude: five of the six cases
			// expect "hello-world", so "five of the six cases duplicate the
			// same expected output" and "the want column repeats hello-world"
			// were both credited with finding this plant while observing
			// something else entirely, and both sit inside anchorTolerance of
			// line 16 so distance does not exclude them either. The exclusion
			// was bought on one keyword and given back on three. What is left
			// names the CASE, not the column.
			Keywords: []string{
				"identical to", "duplicate of", "duplicates the case",
				"duplicates an earlier", "duplicates the previous",
				"repeats the case", "repeats an earlier", "repeated case",
				"same input", "same case", "already covered", "runs twice",
				"adds no coverage", "no new coverage",
				"no additional coverage", "exercises nothing new",
			},
			// tests is the only plant of its class in the corpus, so nothing
			// disagrees with it and no note is owed by the consistency check.
			// One is written anyway. A level with no reason recorded is a level
			// the next editor moves.
			Class:        config.ClassTests,
			WantSeverity: config.SeverityNit,
			SeverityNote: "nit under \"minor and optional\": the suite passes, and every branch of Slug the " +
				"table reached before it reaches still. Not warning — \"likely a bug, or a genuine hazard " +
				"under plausible conditions\" — because a repeated case hides nothing; the input it " +
				"covers is covered. Not info: a byte-identical case is not \"a defensible concern the " +
				"author should consciously accept or reject\", because no author would consciously keep " +
				"it. The stated cost is what makes it reportable at all — the table reads as six inputs " +
				"and is five.",
			Why: "the sixth case has the same input and expectation as the second, so it runs one assertion twice and adds no coverage",
		}},
	}
}

// defensiveCopyOfLocalNitFixture copies a list that nothing else can reach, in
// Java.
//
// The note behind it is in docs/measurement.md#defensivecopyoflocalnitfixture.
func defensiveCopyOfLocalNitFixture() Fixture {
	return Fixture{
		Name: "defensive-copy-nit",
		Base: map[string]string{
			"src/main/java/com/example/report/Labels.java": `package com.example.report;

/** Builds the labels the dashboard shows for recorded events. */
public final class Labels {

    private Labels() {}

    /** Returns the label for one event id. */
    public static String forId(String id) {
        return "event:" + id;
    }
}
`,
		},
		Head: map[string]string{
			"src/main/java/com/example/report/Labels.java": `package com.example.report;

import java.util.ArrayList;
import java.util.Collections;
import java.util.List;

/** Builds the labels the dashboard shows for recorded events. */
public final class Labels {

    private Labels() {}

    /** Returns the label for one event id. */
    public static String forId(String id) {
        return "event:" + id;
    }

    /** Returns one label per event id, in the order the ids were given. */
    public static List<String> forIds(List<String> ids) {
        List<String> labels = new ArrayList<>(ids.size());
        for (String id : ids) {
            labels.add(forId(id));
        }

        // Hand back a copy so nothing the caller does can reach the list we
        // built above.
        return Collections.unmodifiableList(new ArrayList<>(labels));
    }
}
`,
		},
		Defects: []Defect{{
			Path: "src/main/java/com/example/report/Labels.java",
			Line: 26, // the copy taken of a list that never escaped
			// "unnecessary copy", "redundant copy", "needless copy" and "no need
			// to copy" were here and had to go, and their presence contradicted
			// this defect's own comment two paragraphs up: the keywords are
			// supposed to be built on reachability rather than on "copy", which
			// the change itself supplies. They credited the second false
			// positive that comment names and dismisses, "prefer List.copyOf",
			// which allocates the same second list and so has made none of the
			// escape judgement this plant is about. What is left cannot be
			// written without having looked at where labels goes.
			Keywords: []string{
				"never escapes", "does not escape", "cannot escape", "no other reference",
				"nothing else holds", "nothing else can reach", "already local",
				"not shared", "never leaves", "does not leave", "only reference",
				"second list", "extra list", "copies a list it just built",
				"copy is not needed", "copy adds nothing", "copy buys nothing",
			},
			Class:        config.ClassResource,
			WantSeverity: config.SeverityNit,
			SeverityNote: "nit under \"minor and optional\": " +
				"labels is local and unreachable after the return, so unmodifiableList over it is exactly " +
				"as immutable as unmodifiableList over a copy of it. Not warning: there is no \"genuine " +
				"hazard under plausible conditions\" — for the caller to mutate labels it would need a " +
				"reference the method never hands out. Not info: this is not a trade-off \"the author " +
				"should consciously accept or reject\", because the safety the comment claims is already " +
				"total without the copy. Same reading as cross-file-copy-nit, which is why both sit at " +
				"nit in a class that also holds an error and a warning.",
			Why: "labels never escapes forIds, so the ArrayList copied around it allocates and fills a second list of the same length on every call",
		}},
	}
}
