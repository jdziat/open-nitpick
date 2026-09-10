package standards

import (
	"strings"
	"testing"
)

// measureOne runs one probe over one synthetic file.
func measureOne(t *testing.T, id, path, src string) Result {
	t.Helper()
	p, ok := Find(id)
	if !ok {
		t.Fatalf("no probe %q", id)
	}
	rep := Measure([]File{{Path: path, Src: []byte(src)}}, Options{})
	for _, r := range rep.Results {
		if r.ID == p.ID {
			return r
		}
	}
	t.Fatalf("probe %q produced no result", id)
	return Result{}
}

// want asserts both halves of the reading.
//
// Both, always. A probe that finds every violation and counts half the sites
// reports a share that is wrong in the direction nobody checks, which is how
// this package's first two measurements came out at 42% and 61% against true
// readings of 98% and 95%.
func want(t *testing.T, got Result, conforming, total int) {
	t.Helper()
	if got.Conforming != conforming || got.Total != total {
		t.Errorf("%s = %d/%d, want %d/%d; off=%v",
			got.ID, got.Conforming, got.Total, conforming, total, excerpts(got.Off))
	}
}

func excerpts(sites []Site) []string {
	out := make([]string, 0, len(sites))
	for _, s := range sites {
		out = append(out, s.Excerpt)
	}
	return out
}

// A doc comment is judged by its first line, and the tree's exported test
// functions are not judged at all.
//
// Both halves are regressions this package already shipped once. Reading the
// line directly above the declaration called 42% of this repository
// undocumented, because a multi-line comment ends on a line that does not
// repeat the name. Counting test functions called it 61%, because a test is
// exported and godoc documents no part of a test file. Neither bug touches
// which sites are reported as violations, so a test that checked only
// conformance would have passed against both.
func TestTheDocCommentProbeReadsTheFirstLineAndSkipsTests(t *testing.T) {
	const src = `package p

// Alpha does a thing.
func Alpha() {}

// Beta does a thing, and this second line of the comment
// says more about it without repeating the name.
func Beta() {}

// This one never names itself.
func Gamma() {}

func Delta() {}

// Epsilon is a type.
type Epsilon struct{}

// unexported is not API and is not a site.
func unexported() {}
`
	want(t, measureOne(t, "go-doc-comment-name", "p.go", src), 3, 5)

	// The same declarations in a test file are not sites at all.
	got := measureOne(t, "go-doc-comment-name", "p_test.go", src)
	if got.Total != 0 {
		t.Errorf("a test file contributed %d sites; godoc documents no part of one", got.Total)
	}
	if got.Standing != StandingUnseen {
		t.Errorf("standing = %q, want %q: nothing was looked at", got.Standing, StandingUnseen)
	}
}

// A doc comment sitting under a build directive still opens the prose.
func TestADirectiveDoesNotCountAsTheFirstLine(t *testing.T) {
	const src = `package p

//go:generate stringer -type=Kind

// Alpha does a thing.
func Alpha() {}
`
	want(t, measureOne(t, "go-doc-comment-name", "p.go", src), 1, 1)
}

// A comment opening with a longer word that merely starts with the name does
// not name the declaration.
func TestAPrefixIsNotTheName(t *testing.T) {
	const src = `package p

// Parser reads the header.
func Parse() {}

// Parse reads the header.
func Parse2() {}
`
	got := measureOne(t, "go-doc-comment-name", "p.go", src)
	want(t, got, 0, 2)
}

// fmt.Errorf is a site only when there is a cause in hand to lose.
//
// The denominator is the claim. A format call that builds a fresh error has no
// wrapped cause to drop, and counting it turns every error message in a
// repository into a violation of a rule about wrapping.
func TestTheWrapProbeCountsOnlyCallsCarryingAnError(t *testing.T) {
	const src = `package p

import "fmt"

func a(err error) error { return fmt.Errorf("read: %w", err) }
func b(err error) error { return fmt.Errorf("read: %v", err) }
func c(name string) error { return fmt.Errorf("no such user: %s", name) }
func d() error { return fmt.Errorf("nothing to do") }
func e(format string, err error) error { return fmt.Errorf(format, err) }
`
	got := measureOne(t, "go-error-wrap", "p.go", src)
	want(t, got, 1, 2)

	if len(got.Off) != 1 || !strings.Contains(got.Off[0].Excerpt, "%v") {
		t.Errorf("the reported violation is %v, want the %%v call", excerpts(got.Off))
	}
}

// A context is a site wherever it appears, and only where it appears.
func TestTheContextProbeCountsOnlyFunctionsTakingOne(t *testing.T) {
	const src = `package p

import "context"

func a(ctx context.Context, n int) {}
func b(n int, ctx context.Context) {}
func c(n int) {}
func (t *T) d(ctx context.Context) {}

type T struct{}
`
	want(t, measureOne(t, "go-ctx-first-arg", "p.go", src), 2, 3)
}

// A grouped parameter list is read the same as a spelled-out one.
//
// This test claimed once to guard a walk over parameters rather than fields,
// and it could not: the two readings agree on every input. A context inside
// the first field means the first parameter is a context whatever else that
// field names, and a context in a later field is not first either way. The
// walk is over fields now, and this pins the answer rather than the mechanism.
func TestAGroupedParameterListIsReadLikeASpelledOutOne(t *testing.T) {
	const src = `package p

import "context"

func a(x, y int, ctx context.Context) {}
func b(ctx, other context.Context) {}
`
	want(t, measureOne(t, "go-ctx-first-arg", "p.go", src), 1, 2)
}

// Named results are the only place a naked return can be, and a closure's
// naked return belongs to the closure.
func TestTheNakedReturnProbeStopsAtAFunctionLiteral(t *testing.T) {
	const src = `package p

func a() (n int) { return 0 }
func b() (n int) { return }
func c() int { return 0 }
func d() (n int) {
	f := func() (m int) { return }
	_ = f
	return 1
}
`
	want(t, measureOne(t, "go-no-naked-return", "p.go", src), 2, 3)
}

// A test's name is counted in words, and TestMain is not a name anybody chose.
func TestTheTestNameProbeCountsWordsAndSkipsTestMain(t *testing.T) {
	const src = `package p

import (
	"os"
	"testing"
)

func TestParseRejectsEmptyInput(t *testing.T) {}
func TestParse(t *testing.T) {}
func TestMain(m *testing.M) { os.Exit(m.Run()) }
func helper(t *testing.T) { t.Helper() }
`
	want(t, measureOne(t, "go-test-name-sentence", "p_test.go", src), 1, 2)
}

// A run of capitals is one word.
func TestAnAcronymIsOneWord(t *testing.T) {
	for name, n := range map[string]int{
		"ParseRejectsEmptyInput": 4,
		"HTTPServerStarts":       3,
		"Parse":                  1,
		"":                       0,
	} {
		if got := words(name); got != n {
			t.Errorf("words(%q) = %d, want %d", name, got, n)
		}
	}
}

// A helper is a function in a test file that takes a *testing.T and is not
// itself a test.
func TestTheHelperProbeCountsHelpersOnly(t *testing.T) {
	const src = `package p

import "testing"

func marked(t *testing.T) { t.Helper() }
func unmarked(t *testing.T) { t.Log("x") }
func TestSomethingHappensHere(t *testing.T) {}
func noTesting(n int) {}
func blank(_ *testing.T) {}
`
	got := measureOne(t, "go-test-helper-marks", "p_test.go", src)
	want(t, got, 1, 2)

	// A non-test file has no helpers, whatever its signatures look like.
	if plain := measureOne(t, "go-test-helper-marks", "p.go", src); plain.Total != 0 {
		t.Errorf("a non-test file contributed %d helper sites", plain.Total)
	}
}

// A file that does not parse is not a repository that abandoned its
// conventions.
func TestAFileThatDoesNotParseContributesNothing(t *testing.T) {
	got := measureOne(t, "go-doc-comment-name", "p.go", "package p\n\nfunc Alpha( {\n")
	if got.Total != 0 {
		t.Errorf("an unparseable file contributed %d sites", got.Total)
	}
}

// A block doc comment is a doc comment.
//
// `/* Alpha does a thing. */` is legal Go and was read as one token beginning
// with a slash, so every declaration documented that way counted as a
// violation. On a tree that prefers the block form the share would have been
// deflated by however much of it used the legal spelling this probe did not
// know about.
func TestABlockCommentIsReadLikeALineComment(t *testing.T) {
	const src = `package p

/* Alpha does a thing. */
func Alpha() {}

/*
Beta does a thing, and this comment
runs to a second line.
*/
func Beta() {}

/* This one never names itself. */
func Gamma() {}
`
	want(t, measureOne(t, "go-doc-comment-name", "p.go", src), 2, 3)
}

// A method on an unexported type is not something godoc renders.
//
// The probe's own Why is "godoc renders the comment as the entry for that
// name", and godoc renders nothing for a method hanging off a type the package
// does not export. These are interface adapters: Go documents the interface,
// not the adapter. Counting them produced 37 violations on this repository and
// not one of them was real, which is the fourth denominator error in this
// package and the fourth in the same direction.
func TestAMethodOnAnUnexportedTypeIsNotASite(t *testing.T) {
	const src = `package p

type held struct{}

func (h *held) Name() string { return "" }
func (h held) Diff() string { return "" }

// Exported is a type.
type Exported struct{}

// Name is documented.
func (e *Exported) Name() string { return "" }

func (e *Exported) Undocumented() string { return "" }

type box[T any] struct{ v T }

func (b *box[T]) Get() T { return b.v }
`
	// Two sites: the type Exported, and its two methods. The adapters on
	// `held` and on the generic `box` are not API.
	got := measureOne(t, "go-doc-comment-name", "p.go", src)
	want(t, got, 2, 3)
	for _, s := range got.Off {
		if strings.Contains(s.Excerpt, "held") || strings.Contains(s.Excerpt, "box") {
			t.Errorf("an unexported receiver was counted: %s", s.Excerpt)
		}
	}
}

// The wrap probe's population is the spellings its naming can see.
//
// This pins the denominator rather than the conformance, because the
// denominator is the claim. A count of 200/200 means 200 calls named in a way
// this can read, not 200 wrapping decisions audited, and the difference is the
// whole reason to write this down.
func TestTheWrapProbePopulationIsPinned(t *testing.T) {
	const src = `package p

import "fmt"

type box struct{ Err error }

func a(err error) error { return fmt.Errorf("op: %w", err) }
func b(err error) error { return fmt.Errorf("op: %v", err) }
func c(x box) error { return fmt.Errorf("op: %v", x.Err) }
func d(err error) error { return fmt.Errorf("op: %s", err.Error()) }
func e(readErr error) error { return fmt.Errorf("op: %v", readErr) }

func f(name string) error { return fmt.Errorf("no such user: %s", name) }
func g(cause error) error { return fmt.Errorf("op: %v", cause) }
func h(errs []error) error { return fmt.Errorf("op: %v", errs[0]) }
func i(x box) error { return fmt.Errorf("op: %v", trunc(x.Err)) }

func trunc(err error) string { return err.Error() }
`
	// The five it can see: a wraps, the other four do not. The four below the
	// blank line are invisible to it, and a call wrapped inside a function
	// whose own name says nothing is the shape that stays invisible.
	want(t, measureOne(t, "go-error-wrap", "p.go", src), 1, 5)
}

// A helper written against testing.TB is still a helper.
func TestATestingTBHelperIsASite(t *testing.T) {
	const src = `package p

import "testing"

func marked(tb testing.TB) { tb.Helper() }
func unmarked(tb testing.TB) { tb.Log("x") }
`
	want(t, measureOne(t, "go-test-helper-marks", "p_test.go", src), 1, 2)
}
