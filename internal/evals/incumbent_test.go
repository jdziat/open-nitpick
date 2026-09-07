package evals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/review"
)

// The two rule widths Incumbent prints: 40 columns between sections, 72 around
// a finding. Both are U+2500, and neither width is load-bearing for the parser.
const (
	crSectionRule = "────────────────────────────────────────"
	crFindingRule = "────────────────────────────────────────────────────────────────────────"
)

// completed appends the trailer the CLI prints when a review runs to the end.
//
// The fragments below exercise the finding parser, and without a trailer each
// one is indistinguishable from a stream that was cut off, which parseIncumbent
// now refuses outright rather than returning as a short review. Writing the
// trailer here rather than dropping the requirement keeps the fragments honest:
// they are complete reviews that happen to be small.
func completed(t *testing.T, n int, body string) string {
	t.Helper()

	noun := "findings"
	if n == 1 {
		noun = "finding"
	}
	return body + "\n" + crSectionRule + "\nReview complete\n" +
		fmt.Sprintf("%d %s ✔\n", n, noun)
}

// readSample loads the verbatim capture of `incumbent review`, escapes and all.
func readSample(t *testing.T) string {
	t.Helper()

	data, err := os.ReadFile("testdata/plaintext-sample.txt")
	if err != nil {
		t.Fatalf("read sample: %v", err)
	}
	if !strings.Contains(string(data), "\x1b]8;;") {
		t.Fatal("the sample no longer contains an OSC-8 hyperlink; it is not the capture this parser is written against")
	}
	return string(data)
}

// TestParseRealSample pins every field against the captured review.
//
// It asserts a POPULATED parse rather than the absence of an error. The worst
// outcome this parser can have is returning zero findings with err == nil: the
// benchmark then reports Incumbent as having found nothing and the number
// looks plausible. See internal/llm/silentzero_test.go for the same treatment
// applied to model responses.
func TestParseRealSample(t *testing.T) {
	findings, err := parseIncumbent([]byte(readSample(t)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// Exactly one: the header block, the mascot banner, "Review complete",
	// "1 finding", "1 file reviewed" and the billing footer are not findings.
	if len(findings) != 1 {
		t.Fatalf("got %d finding(s), want exactly 1: %+v", len(findings), findings)
	}

	f := findings[0]

	// The OSC-8 URI ends in ".../cr-probe/store.go:10". Failing to strip the
	// hyperlink yields that absolute scratch path as the finding's file, and the
	// link's target line rather than the start of the range.
	if f.Path != "store.go" {
		t.Errorf("path = %q, want %q (the OSC-8 hyperlink was not stripped)", f.Path, "store.go")
	}
	if strings.Contains(f.Path+f.Title+f.Rationale, "vscode://") {
		t.Errorf("hyperlink target leaked into the finding: %+v", f)
	}

	// First number of the range: the defect is on line 10, not 12.
	if f.Line != 10 {
		t.Errorf("line = %d, want 10 (the first line of the 10-12 range)", f.Line)
	}

	// Recorded, not reinterpreted. Asserting `error` here matches a crSeverity
	// that demotes every Incumbent critical so its coarser vocabulary does not
	// read as inflation, and the effect is that no Incumbent review can score
	// accurate on a plant we planted critical, with a headline number published
	// on it. The vocabulary mismatch is not handled by
	// correcting it anywhere, the attempt to handle it at comparison time was
	// withdrawn too, see NoCrossToolSeverityScore. It is described rather than
	// scored, and the parser records what the reviewer said.
	if f.Severity != string(config.SeverityCritical) {
		t.Errorf("severity = %q, want %q: the review says \"critical [Security & Privacy]\", and a "+
			"parsed severity is evidence about the reviewer rather than a place to correct for "+
			"vocabulary", f.Severity, config.SeverityCritical)
	}
	if f.Class != string(config.ClassSecurity) {
		t.Errorf("class = %q, want %q from the [Security & Privacy] category", f.Class, config.ClassSecurity)
	}
	if f.Category != "Security & Privacy" {
		t.Errorf("category = %q, want Incumbent's own label", f.Category)
	}

	if f.Title != "Use a bound parameter for name." {
		t.Errorf("title = %q", f.Title)
	}

	// The rationale is hard-wrapped across three lines in the capture. Unwrapping
	// has to restore the sentence, otherwise the judge reads mangled prose.
	const wrapped = "This permits SQL injection and breaks names that contain apostrophes."
	if !strings.Contains(f.Rationale, wrapped) {
		t.Errorf("rationale = %q, want the hard wrap joined back into %q", f.Rationale, wrapped)
	}
	if strings.Contains(f.Rationale, "\n") {
		t.Errorf("rationale kept the terminal's line breaks: %q", f.Rationale)
	}

	if f.Source != IncumbentModel {
		t.Errorf("source = %q, want %q", f.Source, IncumbentModel)
	}
}

// TestParsePlainTextVariants covers the shapes a real collection runs into.
func TestParsePlainTextVariants(t *testing.T) {
	sample := readSample(t)

	// Two findings plus the trailer, used by the truncation case below.
	twoFindings := crFindingRule + `
  critical [Security & Privacy]
  → store.go:10-12

  Use a bound parameter for name.

  fmt.Sprintf embeds name directly into the SQL query.

` + crFindingRule + `
  warning [Concurrency]
  → worker.go:31-44

  Guard the shared counter.

  Two goroutines increment total without synchronization, so the
  final value depends on scheduling.

` + crSectionRule + `
Review complete
2 findings ✔
`

	// Cut mid-sentence in the second rationale: no trailer, no closing rule.
	cut := strings.Index(twoFindings, "final value depends")
	if cut < 0 {
		t.Fatal("truncation point not found; the fixture above changed")
	}
	truncated := twoFindings[:cut+len("final value")]

	cases := []struct {
		name string
		in   string
		want int
		// wantErr means the input must be REFUSED. A parse that succeeds with
		// fewer findings than the stream contained is the failure mode this
		// whole file exists to prevent, so "fewer, quietly" is not an option.
		wantErr bool
		check   func(*testing.T, []review.Finding)
	}{
		{
			name: "clean review with no findings",
			in: crSectionRule + `
Incumbent Review

Diff      : staged changes and tracked edits
Compare   : main → main
Directory : cr-probe
` + crSectionRule + `

(reviewer banner removed)


` + crSectionRule + `
Review complete
0 findings ✔

1 file reviewed:
- store.go
` + crSectionRule + `
`,
			want: 0,
		},
		{
			// The regression test for a wrong number rather than a missing one.
			// This input parses cleanly into two findings, and an earlier
			// version returned them: the completion check was gated on
			// len(findings) == 0, so a stream cut after k of N findings was
			// recorded as a complete review of k. Incumbent's recall on the
			// fixture then reads k/N, which is a result nobody can tell is
			// wrong. The declared-count check cannot save it either, a run
			// that died never printed the trailer that check reads.
			name:    "interrupted stream is refused, not silently thinned",
			in:      truncated,
			wantErr: true,
		},
		{
			name: "finding with no line span",
			in: completed(t, 1, crFindingRule+`
  warning [Best Practices]
  → config.go

  Prefer a typed constant here.

  A bare string is easy to mistype.
`),
			want: 1,
			check: func(t *testing.T, got []review.Finding) {
				if got[0].Path != "config.go" {
					t.Errorf("path = %q, want config.go", got[0].Path)
				}
				// Unplaceable, but reported: a dropped finding is invisible.
				if got[0].Line != 0 {
					t.Errorf("line = %d, want 0 when the anchor carries no numbers", got[0].Line)
				}
				if got[0].Title == "" {
					t.Error("title is empty; a missing line span must not cost the rest of the finding")
				}
			},
		},
		{
			name: "single line anchor with no range",
			in: completed(t, 1, crFindingRule+`
  critical [Security & Privacy]
  → store.go:7

  Shell out without quoting.

  The argument reaches sh unescaped.
`),
			want: 1,
			check: func(t *testing.T, got []review.Finding) {
				if got[0].Line != 7 {
					t.Errorf("line = %d, want 7", got[0].Line)
				}
			},
		},
		{
			name: "colour codes are stripped defensively",
			in: strings.Replace(sample, "critical [Security & Privacy]",
				"\x1b[1;31mcritical\x1b[0m \x1b[2m[Security & Privacy]\x1b[0m", 1),
			want: 1,
			check: func(t *testing.T, got []review.Finding) {
				if got[0].Severity != string(config.SeverityCritical) || got[0].Line != 10 {
					t.Errorf("CSI escapes changed the parse: %+v", got[0])
				}
				if strings.Contains(got[0].Category, "\x1b") {
					t.Errorf("escape survived into the category: %q", got[0].Category)
				}
			},
		},
		{
			name: "category is preferred over keyword guessing",
			in: completed(t, 1, crFindingRule+`
  critical [Security & Privacy]
  → auth.go:44

  Compare the token in constant time.

  The check returns as soon as two bytes differ, so response time
  reveals how much of the value was right.
`),
			want: 1,
			check: func(t *testing.T, got []review.Finding) {
				// The prose contains none of classifyText's security keywords, so
				// this only passes if the category decided the class.
				if guess := classifyText(got[0].Title + " " + got[0].Rationale); guess == config.ClassSecurity {
					t.Fatal("the prose now trips a security keyword; this case no longer proves anything")
				}
				if got[0].Class != string(config.ClassSecurity) {
					t.Errorf("class = %q, want security from the category", got[0].Class)
				}
			},
		},
		{
			name: "unrecognized category falls back to the prose",
			in: completed(t, 1, crFindingRule+`
  warning [Something We Have Never Seen]
  → pool.go:12

  Two goroutines share the counter.

  Neither takes the mutex, so the increment races.
`),
			want: 1,
			check: func(t *testing.T, got []review.Finding) {
				if got[0].Class != string(config.ClassConcurrency) {
					t.Errorf("class = %q, want concurrency from classifyText", got[0].Class)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseIncumbent([]byte(tc.in))

			if tc.wantErr {
				if err == nil {
					t.Fatalf("SILENTLY THINNED: %d finding(s) and no error, from a stream "+
						"that never finished: %+v", len(got), got)
				}
				return
			}

			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if len(got) != tc.want {
				t.Fatalf("got %d finding(s), want %d: %+v", len(got), tc.want, got)
			}
			for i, f := range got {
				if f.Path == "" || f.Title == "" {
					t.Errorf("finding %d parsed but is empty: %+v", i, f)
				}
			}
			if tc.check != nil {
				tc.check(t, got)
			}
		})
	}
}

// TestParseRefusesToUnderreport is the regression test for the failure that
// matters most: the format drifts, the parser matches nothing, and the
// benchmark records Incumbent as having found nothing at all.
//
// The CLI counts its own findings in the trailer, so any shortfall against that
// count is provably this parser's fault and has to be loud.
func TestParseRefusesToUnderreport(t *testing.T) {
	sample := readSample(t)

	// The header shape moves, exactly what a CLI release could do.
	drifted := strings.Replace(sample, "critical [Security & Privacy]",
		"critical <Security & Privacy>", 1)

	got, err := parseIncumbent([]byte(drifted))
	if err == nil {
		t.Fatalf("SILENT UNDERREPORT: parsed %d finding(s) from output declaring 1, with no error", len(got))
	}
	if !strings.Contains(err.Error(), "1 finding") {
		t.Errorf("error = %q, want it to name the count the CLI declared", err)
	}
}

// TestParseRefusesToInventACleanReview covers the other route to a silent zero:
// the stream stops before the review does, so an empty parse reads as "found
// nothing" when the truth is "never finished".
func TestParseRefusesToInventACleanReview(t *testing.T) {
	cases := map[string]string{
		"empty output": "",
		"banner only, killed before any finding": crSectionRule + `
Incumbent Review

Diff      : staged changes and tracked edits
` + crSectionRule + `
`,
		"a prompt where a review should be": "You are not signed in. Run: incumbent auth login\n",
	}

	for name, in := range cases {
		got, err := parseIncumbent([]byte(in))
		if err == nil {
			t.Errorf("%s: SILENT ZERO: %d finding(s) and no error, from output that never completed",
				name, len(got))
		}
	}
}

// TestDeclaredCountReadsTheTrailer pins the count the check above depends on.
func TestDeclaredCountReadsTheTrailer(t *testing.T) {
	cases := map[string]int{
		"Review complete\n1 finding ✔\n":                              1,
		"Review complete\n0 findings ✔\n\n1 file reviewed:\n- a.go\n": 0,
		"Review complete\n12 findings ✔\n":                            12,
		// No trailer at all: an interrupted stream must not fabricate a count
		// that then reads as an underreport.
		"  critical [Security & Privacy]\n  → a.go:1\n": 0,
	}

	for in, want := range cases {
		if got := crDeclared(in); got != want {
			t.Errorf("crDeclared(%q) = %d, want %d", in, got, want)
		}
	}
}

// TestFreeTierFallbackIsDetected asserts against the exact warning the CLI
// printed for a fixture repository with no remote.
//
// A run that degrades to the free allowance measures the allowance, not the
// reviewer, so it must never be mistaken for a valid result.
func TestFreeTierFallbackIsDetected(t *testing.T) {
	const warning = "Incumbent couldn't find a Git remote for this repository, so it can't " +
		"match the review to one of your organizations. This review will use the free CLI " +
		"allowance, even if you're signed in."

	// The same sentence as the terminal wraps it, plus the typographic
	// apostrophe a renderer may substitute.
	wrapped := "Incumbent couldn’t find a Git remote for this repository, so it\n" +
		"can’t match the review to one of your organizations. This review\n" +
		"will use the free CLI allowance, even if you’re signed in.\n"

	positive := map[string]string{
		"verbatim":                warning,
		"hard-wrapped and curly":  wrapped,
		"embedded in a real run":  readSample(t) + "\n" + warning,
		"only the remote clause":  "Incumbent couldn't find a Git remote for this repository.",
		"only the billing clause": "This review will use the free CLI allowance.",
	}
	for name, out := range positive {
		if !crFreeTierFallback(out) {
			t.Errorf("%s: free-tier fallback not detected", name)
		}
	}

	negative := map[string]string{
		// The ordinary footer of a perfectly good review. It says "free", it says
		// "credits", and it must not be confused for the fallback.
		"promotional credits footer": readSample(t),
		"clean review":               "Review complete\n0 findings\n",
		"empty":                      "",
	}
	for name, out := range negative {
		if crFreeTierFallback(out) {
			t.Errorf("%s: reported a free-tier fallback that is not there", name)
		}
	}
}

// TestFreeTierErrorIsRecognizable proves the sentinel survives wrapping, which
// is what CollectIncumbent's decision not to cache depends on.
func TestFreeTierErrorIsRecognizable(t *testing.T) {
	err := freeTierError()
	if !IsFreeTier(err) {
		t.Fatal("a wrapped ErrFreeTier must still be recognizable, or a degraded run gets cached")
	}
	if IsFreeTier(nil) {
		t.Error("nil is not a free-tier fallback")
	}
	if IsRateLimited(err) {
		t.Error("the free-tier fallback is not a rate limit; the two need different handling")
	}
}

// TestFixtureRepoHasOriginRemote is the regression test for defect 2: without a
// remote, Incumbent cannot match the review to an organization and bills every
// fixture to the free allowance.
func TestFixtureRepoHasOriginRemote(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}

	fixtures := Fixtures()
	if len(fixtures) == 0 {
		t.Fatal("no fixtures to build")
	}

	originOf := func(t *testing.T) string {
		t.Helper()

		dir := t.TempDir()
		if err := buildRepo(dir, fixtures[0]); err != nil {
			t.Fatalf("build repo: %v", err)
		}

		// The stored value, not `git remote get-url`: that command applies the
		// user's url.<base>.insteadOf rewrites, so on a machine that rewrites
		// https://github.com/ to git@github.com: it reports a URL this code never
		// wrote. What is under test is what buildRepo configured.
		cmd := exec.Command("git", "config", "--get", "remote.origin.url")
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git config --get remote.origin.url: %v\n%s", err, out)
		}
		return strings.TrimSpace(string(out))
	}

	t.Run("default", func(t *testing.T) {
		// Cleared explicitly: this subtest is about what buildRepo does with NO
		// override, and it failed for exactly the operator the override exists
		// for, someone benchmarking from a fork with NITPICK_CR_REMOTE
		// exported in their shell.
		t.Setenv(EnvIncumbentRemote, "")

		if got := originOf(t); got != defaultIncumbentRemote {
			t.Errorf("origin = %q, want %q", got, defaultIncumbentRemote)
		}
	})

	t.Run("configurable", func(t *testing.T) {
		const custom = "https://github.com/example/elsewhere.git"
		t.Setenv(EnvIncumbentRemote, custom)

		if got := originOf(t); got != custom {
			t.Errorf("origin = %q, want the %s override %q", got, EnvIncumbentRemote, custom)
		}
	})
}

// --- RunIncumbent ------------------------------------------------------------
//
// With nothing in this package calling RunIncumbent or CollectIncumbent, every
// guard inside them goes untested: deleting the free-tier check outright leaves
// the suite green. The tests below drive the real function through a shim
// on PATH, which is the only way to reach the branches that decide whether a
// number gets recorded at all.

// crShim installs a fake `incumbent` executable and points PATH at it.
func crShim(t *testing.T, script string) {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "incumbent")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatalf("write shim: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// crShimFinding prints one complete finding block, with no trailer. It is the
// output of a review that was killed after emitting its first finding.
const crShimFinding = `printf '%s\n' \
  '────────────────────────────────────────' \
  '  critical [Security & Privacy]' \
  '  → store.go:10-12' \
  '' \
  '  Use a bound parameter for name.' \
  '' \
  '  fmt.Sprintf embeds name directly into the SQL query.'
`

// TestRunIncumbentRefusesPartialReviews is the regression test for a partial
// review being cached forever as a complete one.
//
// A run that dies after k of N findings still parses into k findings. Returning
// those with a nil error made CollectIncumbent take its success branch and
// write them to testdata/incumbent/<fixture>.json, which every later benchmark
// then preferred over re-reviewing. The result is not a missing number, which
// is visible; it is a wrong one, which is not.
func TestRunIncumbentRefusesPartialReviews(t *testing.T) {
	cases := map[string]struct {
		script  string
		timeout time.Duration
		// wantIn is a substring the error must carry, so the operator is sent
		// to the real problem rather than to "the format changed".
		wantIn string
	}{
		"killed by the timeout": {
			script:  crShimFinding + "sleep 30\n",
			timeout: 2 * time.Second,
			wantIn:  "deadline exceeded",
		},
		"crashed after one finding": {
			script:  crShimFinding + "echo 'connection reset by peer' >&2\nexit 1\n",
			timeout: 10 * time.Second,
			wantIn:  "exit status 1",
		},
		"stream simply stopped": {
			script:  crShimFinding,
			timeout: 10 * time.Second,
			wantIn:  "did not run to the end",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			crShim(t, tc.script)

			got, _, err := RunIncumbent(context.Background(), t.TempDir(), tc.timeout)
			if err == nil {
				t.Fatalf("SILENT PARTIAL: %d finding(s) and no error from a review that never finished: %+v",
					len(got), got)
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("error = %q, want it to name %q", err, tc.wantIn)
			}
		})
	}
}

// TestRunIncumbentReturnsCompleteReviews is the other side: a review that
// printed its trailer must survive, including when the CLI exits non-zero to
// signal that it found something.
func TestRunIncumbentReturnsCompleteReviews(t *testing.T) {
	trailer := "printf '%s\\n' '────────────────────────────────' 'Review complete' '1 finding ✔'\n"

	for name, script := range map[string]string{
		"exit 0":                        crShimFinding + trailer,
		"exit 1 because findings exist": crShimFinding + trailer + "exit 1\n",
	} {
		t.Run(name, func(t *testing.T) {
			crShim(t, script)

			got, _, err := RunIncumbent(context.Background(), t.TempDir(), 10*time.Second)
			if err != nil {
				t.Fatalf("a review that printed its trailer must be kept: %v", err)
			}
			if len(got) != 1 {
				t.Fatalf("findings = %d, want 1: %+v", len(got), got)
			}
			if got[0].Line != 10 || got[0].Path != "store.go" {
				t.Errorf("finding = %+v, want store.go:10", got[0])
			}
		})
	}
}

// TestRunIncumbentDetectsFreeTierFallback pins the guard, which no test
// reached before: deleting it entirely from RunIncumbent left every test in
// this file passing.
//
// It also pins the ORDER. The warning is checked before parsing, because a
// free-allowance review parses perfectly well, and its findings measure the
// allowance rather than the reviewer, so they must not be usable.
func TestRunIncumbentDetectsFreeTierFallback(t *testing.T) {
	const warning = "Incumbent couldn't find a Git remote for this repository, so it can't " +
		"match the review to one of your organizations. This review will use the free CLI allowance."

	for name, script := range map[string]string{
		// On stdout, alongside a review that would otherwise be accepted.
		"announced with a complete review": "echo \"" + warning + "\"\n" + crShimFinding +
			"printf '%s\\n' 'Review complete' '1 finding ✔'\n",
		// On stderr, which is the other stream it can land on.
		"announced on stderr": "echo \"" + warning + "\" >&2\n" + crShimFinding +
			"printf '%s\\n' 'Review complete' '1 finding ✔'\n",
	} {
		t.Run(name, func(t *testing.T) {
			crShim(t, script)

			got, _, err := RunIncumbent(context.Background(), t.TempDir(), 10*time.Second)
			if !IsFreeTier(err) {
				t.Fatalf("err = %v (%d finding(s)); a degraded run must be refused, not scored", err, len(got))
			}
			if got != nil {
				t.Errorf("findings = %+v, want none: they measure the allowance", got)
			}
		})
	}
}

// TestRunIncumbentReportsRateLimitsFromEitherStream is the regression test for
// a backoff that could never fire.
//
// IsRateLimited reads err.Error(), and the error carried only stderr truncated
// to 200 bytes, but plain-text mode is the human-facing mode and announces the
// limit on STDOUT. So CollectIncumbent fell to its default branch and attacked
// the next fixture inside the same exhausted window, and the benchmark's own
// "never let an exhausted allowance masquerade as a low score" fallback was
// unreachable.
func TestRunIncumbentReportsRateLimitsFromEitherStream(t *testing.T) {
	const message = "Error: API rate limit exceeded. Retry after 3600s."

	for name, script := range map[string]string{
		"on stdout": "echo '" + message + "'\nexit 1\n",
		"on stderr": "echo '" + message + "' >&2\nexit 1\n",
		// Past the 200-byte stderr truncation, the other way the message is
		// lost.
		"on stdout behind a wall of noise": "head -c 4000 /dev/zero | tr '\\0' 'x'\necho\necho '" +
			message + "'\nexit 1\n",
	} {
		t.Run(name, func(t *testing.T) {
			crShim(t, script)

			_, _, err := RunIncumbent(context.Background(), t.TempDir(), 10*time.Second)
			if !IsRateLimited(err) {
				t.Fatalf("err = %v; an exhausted allowance must be recognizable, or the backoff never runs", err)
			}
			if IsFreeTier(err) {
				t.Error("a rate limit is not the free-tier fallback; the two get different handling")
			}
		})
	}
}

// TestRunIncumbentNamesTheRealFailure covers the failures that produce no
// conforming output at all.
//
// All of them used to report "the plain-text format has changed", because the
// parse error was returned before runErr and the context were consulted. That
// message sends whoever reads it to rewrite a parser over a missing binary.
func TestRunIncumbentNamesTheRealFailure(t *testing.T) {
	t.Run("cancelled context", func(t *testing.T) {
		crShim(t, "echo hi\n")

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, _, err := RunIncumbent(ctx, t.TempDir(), time.Second)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want it to wrap context.Canceled", err)
		}
	})

	t.Run("binary not installed", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())

		_, _, err := RunIncumbent(context.Background(), t.TempDir(), time.Second)
		if err == nil {
			t.Fatal("a missing CLI must be an error")
		}
		if !strings.Contains(err.Error(), "incumbent") || !strings.Contains(err.Error(), "not found") {
			t.Errorf("error = %q, want it to say the executable was not found", err)
		}
	})
}

// TestCacheIsRejectedWhenItMeasuredSomethingElse pins the provenance the cache
// carries.
//
// Keyed on fixture NAME alone, a cache collected before a fixture's Head was
// edited would still load: every model would review the new code while
// Incumbent's row was the old code's findings scored against the new ground
// truth, with nothing saying the two sides reviewed different source. The same
// applies across review modes. This benchmark has already switched from
// --agent to plain text once.
func TestCacheIsRejectedWhenItMeasuredSomethingElse(t *testing.T) {
	fx := Fixtures()[0]
	dir := t.TempDir()

	write := func(t *testing.T, c crCache) {
		t.Helper()
		blob, err := json.MarshalIndent(c, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, fx.Name+".json"), blob, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	findings := []review.Finding{{Path: "a.go", Line: 1, Title: "cached"}}

	write(t, crCache{Fixture: fx.Name, Findings: findings, Mode: crReviewMode, Fingerprint: fixtureFingerprint(fx)})
	if got, ok := CachedIncumbent(dir, fx); !ok || len(got) != 1 {
		t.Fatalf("a cache matching the fixture and the mode must load: ok=%v got=%+v", ok, got)
	}

	write(t, crCache{Fixture: fx.Name, Findings: findings, Mode: "agent", Fingerprint: fixtureFingerprint(fx)})
	if _, ok := CachedIncumbent(dir, fx); ok {
		t.Error("a review collected in another mode must not be ranked as this one")
	}

	// The fixture's source changes; the cache still names it.
	edited := fx
	edited.Head = map[string]string{"store.go": "package store // rewritten\n"}

	write(t, crCache{Fixture: fx.Name, Findings: findings, Mode: crReviewMode, Fingerprint: fixtureFingerprint(fx)})
	if _, ok := CachedIncumbent(dir, edited); ok {
		t.Error("a review of the old code must not be scored against the new code")
	}
}

// TestCachedReviewIsServedFromRawNotFromTheStoredParse pins which of the two
// things in a cache entry is the evidence.
//
// The stored findings are one parser's reading of the retained text, and the
// parser is the part that keeps turning out to be wrong: crSeverity demoted
// every Incumbent "critical" to our "error" for the whole of the first
// benchmark. If loading replayed the stored reading, that fix would have
// applied to nothing already collected. The shipped corpus would still be
// scored under the buggy parser, and correcting the published number would have
// meant buying fifteen reviews again against a rate-limited allowance to
// recover text already on disk.
//
// The last subtests pin the corollary the first version of this got wrong: when
// the retained text does not parse, the entry is refused rather than quietly
// served from the stored reading.
func TestCachedReviewIsServedFromRawNotFromTheStoredParse(t *testing.T) {
	fx := Fixtures()[0]

	raw := completed(t, 1, crFindingRule+`
  critical [Security & Privacy]
  → store.go:10-12

  Use a bound parameter for name.

  fmt.Sprintf embeds name directly into the SQL query.
`)

	// A stale reading of exactly that text: the severity the OLD crSeverity
	// would have recorded.
	stale := []review.Finding{{Path: "store.go", Line: 10, Severity: string(config.SeverityError),
		Title: "Use a bound parameter for name."}}

	write := func(t *testing.T, dir string, c crCache) {
		t.Helper()
		blob, err := json.MarshalIndent(c, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, fx.Name+".json"), blob, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	entry := crCache{Fixture: fx.Name, Mode: crReviewMode, Fingerprint: fixtureFingerprint(fx)}

	t.Run("raw wins over a stale stored parse", func(t *testing.T) {
		dir := t.TempDir()
		c := entry
		c.Findings, c.Raw = stale, raw
		write(t, dir, c)

		got, ok := CachedIncumbent(dir, fx)
		if !ok || len(got) != 1 {
			t.Fatalf("ok=%v got=%+v", ok, got)
		}
		if got[0].Sev() != config.SeverityCritical {
			t.Errorf("severity = %q, want %q: the recorded review says critical and the stored "+
				"reading says error, so a parser fix reaches the corpus only if raw decides",
				got[0].Sev(), config.SeverityCritical)
		}
	})

	t.Run("a pre-raw entry still loads", func(t *testing.T) {
		dir := t.TempDir()
		c := entry
		c.Findings = stale
		write(t, dir, c)

		got, ok := CachedIncumbent(dir, fx)
		if !ok || len(got) != 1 || got[0].Title != stale[0].Title {
			t.Errorf("ok=%v got=%+v: an entry collected before raw was retained has no other "+
				"reading available and must not be dropped", ok, got)
		}
	})

	// THE BUG, and this subtest used to assert it. When the retained review no
	// longer parsed, the loader served the STORED findings with ok=true and no
	// marker of any kind, on the reasoning that a stale reading beats an absent
	// review. That is wrong in the only direction that matters: the fallback
	// fires exactly when the parser and the evidence have diverged, the one
	// moment the difference is not cosmetic, and it hands a previous parser's
	// output to a table captioned as this parser's result, looking identical to a
	// fresh review. A cache that silently serves a stale parse is how a corpus
	// drifts under a measurement.
	//
	// The absent-review worry it was answering is real and is handled where it
	// belongs: callers list what has no usable cache before they run, and a
	// contender judged on nothing is failed rather than ranked.
	t.Run("unparseable raw is refused, and says why", func(t *testing.T) {
		dir := t.TempDir()
		c := entry
		// No trailer, so parseIncumbent refuses it as a truncated review.
		c.Findings, c.Raw = stale, "  critical [Security & Privacy]\n  → store.go:10\n\n  Cut off"
		write(t, dir, c)

		got, ok := CachedIncumbent(dir, fx)
		if ok {
			t.Errorf("ok=true got=%+v: this served a PREVIOUS parser's reading of bytes THIS parser "+
				"cannot read, with nothing to distinguish it from a fresh review", got)
		}

		// And the caller can tell "the parser and the evidence diverged" from
		// "never collected", because the two have opposite remedies: one is a bug
		// to fix, the other costs a review against a rate-limited allowance.
		stale, why := StaleIncumbentCache(dir, fx)
		if !stale || why == nil {
			t.Errorf("StaleIncumbentCache = %v, %v: a refusal that cannot say WHY sends an operator "+
				"to re-collect, which spends allowance to hide a parser bug", stale, why)
		}
	})

	t.Run("a healthy cache is not reported stale", func(t *testing.T) {
		dir := t.TempDir()
		c := entry
		c.Findings, c.Raw = stale, raw
		write(t, dir, c)

		if got, why := StaleIncumbentCache(dir, fx); got {
			t.Errorf("StaleIncumbentCache = true (%v) for a cache this parser reads fine; a staleness "+
				"report that cries wolf will be ignored when it matters", why)
		}
	})

	t.Run("an absent cache is not reported stale", func(t *testing.T) {
		// Nothing written. "Never collected" and "collected but unreadable" are
		// different problems and must not arrive as the same signal.
		if got, why := StaleIncumbentCache(t.TempDir(), fx); got {
			t.Errorf("StaleIncumbentCache = true (%v) with no cache at all", why)
		}
	})
}

// TestShippedCacheReflectsTheCurrentParser is the corpus-level half.
//
// Every published number about Incumbent is computed from these files, so
// "the parser was fixed" is only true of the benchmark if the fix reaches them.
// An entry with no raw retained would be served from whatever parser was
// current when it was collected, silently mixing two readings in one table.
func TestShippedCacheReflectsTheCurrentParser(t *testing.T) {
	for _, f := range AllFixtures() {
		blob, err := os.ReadFile(filepath.Join(crCacheDir, f.Name+".json"))
		if err != nil {
			continue // not yet collected; TestCollectIncumbent reports that
		}

		var c crCache
		if err := json.Unmarshal(blob, &c); err != nil {
			t.Errorf("%s: %v", f.Name, err)
			continue
		}
		if c.Raw == "" {
			t.Errorf("%s: no raw review retained, so this entry is frozen under whatever parser "+
				"collected it and cannot be corrected without spending the allowance again", f.Name)
			continue
		}

		served, ok := CachedIncumbent(crCacheDir, f)
		if !ok {
			continue // fingerprint mismatch; a different test's problem
		}

		fresh, err := parseIncumbent([]byte(c.Raw))
		if err != nil {
			t.Errorf("%s: retained raw no longer parses: %v", f.Name, err)
			continue
		}
		if len(served) != len(fresh) {
			t.Errorf("%s: served %d finding(s), a fresh parse of the same text yields %d",
				f.Name, len(served), len(fresh))
			continue
		}
		for i := range served {
			if served[i].Severity != fresh[i].Severity {
				t.Errorf("%s finding %d: served severity %q, current parser reads %q from the same "+
					"recorded review", f.Name, i, served[i].Severity, fresh[i].Severity)
			}
		}
	}
}

// TestFingerprintDistinguishesWhatTheReviewerSaw pins the hash's inputs.
func TestFingerprintDistinguishesWhatTheReviewerSaw(t *testing.T) {
	base := Fixture{
		Name:  "f",
		Base:  map[string]string{"a.go": "one", "b.go": "two"},
		Head:  map[string]string{"a.go": "three"},
		Extra: map[string]string{"c.go": "four"},
	}

	// Stable across calls: Go randomizes map iteration order, so a hash that
	// walked the maps unsorted would make cache entries miss at random. The
	// first value is captured before the loop so the comparison is not two
	// calls the compiler can fold together.
	first := fixtureFingerprint(base)
	for range 8 {
		if got := fixtureFingerprint(base); got != first {
			t.Fatalf("fingerprint changed between calls (%s then %s); map order leaked into the hash",
				first, got)
		}
	}

	changed := map[string]Fixture{}

	head := base
	head.Head = map[string]string{"a.go": "different"}
	changed["head content"] = head

	src := base
	src.Base = map[string]string{"a.go": "one", "b.go": "different"}
	changed["base content"] = src

	extra := base
	extra.Extra = map[string]string{"c.go": "different"}
	changed["extra content"] = extra

	// The same file bodies, moved between sections: the reviewer sees a
	// different diff even though the bytes are identical.
	moved := base
	moved.Base = map[string]string{"a.go": "one"}
	moved.Extra = map[string]string{"b.go": "two", "c.go": "four"}
	changed["a file moved from base to extra"] = moved

	for name, f := range changed {
		if fixtureFingerprint(f) == fixtureFingerprint(base) {
			t.Errorf("%s: fingerprint unchanged; a cache of the old source would still load", name)
		}
	}

	// Defects are ground truth for the judge, not input to the reviewer.
	// Rewording one must not throw away a still-valid review.
	reworded := base
	reworded.Defects = []Defect{{Path: "a.go", Line: 1}}
	if fixtureFingerprint(reworded) != fixtureFingerprint(base) {
		t.Error("defects changed the fingerprint; they are not part of what the reviewer read")
	}
}

// TestReparseCachedRaw re-derives every cached review from its retained raw
// text and rewrites the cache in place.
//
// This is what retaining raw bought. Both anchor fixes (keeping the end of a
// span, and reading the "Also applies to" line) changed how a review parses,
// and every cached entry predates them. Without the raw text the only way to
// apply a parser fix to an existing corpus is to buy the reviews again, which
// is how a benchmark ends up quietly scored under two different parsers.
//
// Guarded by NITPICK_REPARSE because it WRITES to testdata.
func TestReparseCachedRaw(t *testing.T) {
	if os.Getenv("NITPICK_REPARSE") == "" {
		t.Skip("set NITPICK_REPARSE=1 to re-derive cached reviews from their raw text")
	}

	entries, err := filepath.Glob(filepath.Join(crCacheDir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}

	for _, path := range entries {
		blob, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}

		var c crCache
		if err := json.Unmarshal(blob, &c); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if c.Raw == "" {
			t.Errorf("%s: no raw retained; it must be re-collected", c.Fixture)
			continue
		}

		found, err := parseIncumbent([]byte(c.Raw))
		if err != nil {
			t.Errorf("%s: re-parse failed: %v", c.Fixture, err)
			continue
		}
		if len(found) != len(c.Findings) {
			// Loud: a parser change that alters the finding COUNT is a different
			// event from one that refines an anchor, and silently accepting it
			// would let the corpus drift.
			t.Errorf("%s: re-parse yields %d finding(s), cache holds %d",
				c.Fixture, len(found), len(c.Findings))
		}

		c.Findings = found
		out, err := json.MarshalIndent(c, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, out, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: re-derived %d finding(s)", c.Fixture, len(found))
	}
}
