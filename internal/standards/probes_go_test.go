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
