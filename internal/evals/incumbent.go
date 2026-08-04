package evals

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/review"
)

// IncumbentModel is the pseudo-model id used for Incumbent in reports, so it
// ranks in the same table as the models under test.
const IncumbentModel = "incumbent/cli"

// EnvIncumbentRemote overrides the origin URL given to fixture repositories.
const EnvIncumbentRemote = "NITPICK_CR_REMOTE"

// defaultIncumbentRemote is this repository, which is the remote the account
// running the benchmark is expected to own. Nothing is ever pushed to it.
const defaultIncumbentRemote = "https://github.com/jdziat/open-nitpick.git"

// incumbentRemote returns the URL to install as a fixture repository's origin.
//
// It is configurable because the URL only has to be a remote the reviewing
// account's organization owns; whoever runs the benchmark from a fork or
// another org needs to point it at theirs.
func incumbentRemote() string {
	if v := strings.TrimSpace(os.Getenv(EnvIncumbentRemote)); v != "" {
		return v
	}
	return defaultIncumbentRemote
}

// --- Plain-text review format ------------------------------------------------
//
// `incumbent review` (no --agent) prints one block per finding:
//
//	<rule of box-drawing characters>
//	  critical [Security & Privacy]
//	  → store.go:10-12
//
//	  Use a bound parameter for name.
//
//	  fmt.Sprintf embeds name directly into the SQL query. This permits SQL
//	  injection and breaks names that contain apostrophes.
//
// testdata/plaintext-sample.txt is a verbatim capture and is the ground truth
// this parser is written against.

// crEscape matches the terminal escapes Incumbent writes into its output: OSC
// sequences (ESC ] ... BEL or ST) and CSI sequences (ESC [ ... final byte).
//
// The OSC-8 hyperlink wrapping the location is the one that MUST go: its URI
// ends in "<abs-tmp-dir>/store.go:10", so leaving it in yields the absolute
// scratch path as the finding's file and the link target's line instead of the
// range. The captured sample carries no CSI colour codes, but escapes clearly
// survive redirection, so colour is stripped too rather than assumed absent.
var crEscape = regexp.MustCompile("\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)|\x1b\\[[0-9;?]*[ -/]*[@-~]")

// crFindingHeader matches a block's first line: "  critical [Security & Privacy]".
//
// The severity word is captured rather than matched against a known set: an
// unrecognized word must still produce a finding (crSeverity has a default for
// exactly that reason), and a closed set here would silently drop it.
var crFindingHeader = regexp.MustCompile(`^\s*([A-Za-z]+)\s+\[([^\]]+)\]\s*$`)

// crAnchor matches the structured location line, "  → store.go:10-12".
var crAnchor = regexp.MustCompile(`^\s*(?:→|->)\s*(.*)$`)

// crLocation splits a location into path and line range. The path group is
// greedy so the LAST colon separates it from the numbers, which is what keeps
// a path that itself contains a colon intact.
var crLocation = regexp.MustCompile(`^(.*):(\d+)(?:-(\d+))?$`)

// crAlsoApplies matches Incumbent's secondary-location line, which names
// further regions the SAME finding covers rather than a new finding.
//
// It is not decoration. On the SQL-injection fixture the primary anchor was the
// import block (its proposed fix deletes the fmt import) and this line was the
// only thing pointing at the interpolation itself, so dropping it recorded a
// defect Incumbent had explicitly located as one it missed.
var crAlsoApplies = regexp.MustCompile(`(?i)^\s*Also applies to:\s*(.+?)\s*$`)

// crExtraSpan matches one entry of that line: "15-18", "22", or "other.go:4-9".
var crExtraSpan = regexp.MustCompile(`^(?:([^:]+):)?(\d+)(?:-(\d+))?$`)

// crRule matches the box-drawing rules printed between sections. Section rules
// and finding rules differ in width, so width is not a discriminator: a rule
// only ever ENDS a block, and what starts one is the header/anchor pair.
var crRule = regexp.MustCompile(`^[\x{2500}-\x{257F}=_-]{4,}$`)

// crDeclaredCount matches the trailer's own tally, "1 finding ✔".
//
// This is the CLI's independent statement of how many findings it produced, and
// it is the only defence against a format change turning a real review into a
// clean bill of health. See the check in parseIncumbent.
var crDeclaredCount = regexp.MustCompile(`(?m)^\s*(\d+)\s+findings?\b`)

// crSeverity records the severity Incumbent assigned, in our vocabulary.
//
// It USED to demote "critical" to our "error", on the reasoning that
// Incumbent's single critical spans what we split into critical and error and
// that passing it through would read as inflation. The reasoning identified a
// real problem and fixed it in the wrong place. A parsed severity is EVIDENCE
// about the reviewer, and rewriting the evidence to make a comparison come out
// fairly destroys the thing being measured: no Incumbent review could then
// score accurate on any of the four plants we plant at critical, however it
// worded the finding, and its raw output for go-sql-injection literally reads
// "critical [Security & Privacy]". Mapping down did not remove the bias, it
// swapped an inflation bias for an understatement bias — and no choice of
// constant here can fix what is a difference in RESOLUTION rather than in
// meaning. It does not belong at comparison time either: that was the second
// attempt, a banded cross-tool score, and it is withdrawn — see
// NoCrossToolSeverityScore in score.go for the two measurements that killed it.
// What is left is a faithful record and a description of it.
//
// So: a word that IS one of our levels is recorded as that level. Only the
// foreign tokens are a judgement, and they are the ones a reader should
// distrust:
//
//   - "major" is Incumbent's own word and has no counterpart among our five.
//     It is recorded at warning — the weakest anchor in review.md that still
//     asserts a defect ("likely a bug, or a genuine hazard under plausible
//     conditions") — because a foreign token we cannot resolve should not be
//     handed the benefit of the doubt. THAT IS A GUESS AND THE CORPUS CONTAINS
//     NO EVIDENCE FOR IT: every "major" finding it credits sits on a plant we
//     rate error or critical and none on a plant of warning, and recording it at
//     error instead moved the withdrawn banded figure from 0.600 to a PERFECT
//     1.000 with Incumbent's bytes unchanged. (That swing was published as
//     "0.62 to 0.88"; the figures reproduce from nothing in the tree and are
//     corrected here — see NoCrossToolSeverityScore.)
//     That is why no such figure is published any longer, and why this constant
//     is left alone rather than re-tuned: re-tuning it moves a number our way
//     with no new evidence, which is the mistake three times over.
//     TestMajorIsAFreeParameterSoNoCrossToolScoreIsOffered measures it.
//   - "minor" is the same judgement one step down.
//
// The vocabulary observed across the whole shipped corpus is {critical, major}
// — the two words above. Every other arm is defensive: the CLI's tiers are not
// contractual, and an unrecognized word still has to produce a finding, which
// is what the default is for.
func crSeverity(s string) config.Severity {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "critical":
		return config.SeverityCritical
	case "error":
		return config.SeverityError
	case "warning", "warn", "major":
		return config.SeverityWarning
	case "info", "minor":
		return config.SeverityInfo
	case "nit", "nitpick":
		return config.SeverityNit
	default:
		// An unrecognized word is degraded the SAME way an unrecognized word
		// from one of our own models is, by the same function.
		//
		// THE BUG: this arm returned warning, while a finding of ours carrying
		// an unknown severity goes through config.Severity.Normalize and lands
		// at info. The identical unusable token therefore scored a whole level
		// apart depending on which contender emitted it — an advantage handed to
		// the incumbent by the scorer, in a comparison whose entire purpose is to
		// be like-for-like.
		//
		// The claim here is about THIS ARM and no wider. The arms above are
		// asymmetric on purpose and the asymmetry is live: "major", "warn" and
		// "nitpick" are translated here and would Normalize to info if one of
		// our models emitted them, so the same word is worth a different level
		// depending on who said it — and "major" carries more than half the
		// incumbent's severity words in the shipped cache. That is a translation
		// of a foreign vocabulary, which is why it is enumerated rather than
		// inferred, why every translated token is a guess this file marks as
		// one, and why no cross-tool severity score is published from the
		// result. TestUnusableSeverityIsDegradedIdenticallyForEveryContender
		// pins the default arm; TestForeignSeverityWordsAreTranslatedOnPurpose
		// pins the enumerated set, so a fourth translation cannot be added
		// silently.
		n, _ := config.Severity(s).Normalize()
		return n
	}
}

// crCategoryClass maps Incumbent's own bracketed category onto our taxonomy.
//
// The category is the reviewer's own judgement about its own finding, so it
// beats guessing from prose and is preferred wherever it is recognized.
//
// Only "[Security & Privacy]" has actually been observed; the rest are the
// obvious neighbours. Guessing wrong about a label we have never seen is cheap:
// an unrecognized category falls through to classifyText, whose default is
// correctness, so no finding can be silenced by a bad guess here.
func crCategoryClass(category string) (config.Class, bool) {
	norm := strings.Join(strings.Fields(strings.ToLower(strings.ReplaceAll(category, "&", " "))), " ")

	switch norm {
	case "security privacy", "security", "privacy":
		return config.ClassSecurity, true
	case "concurrency", "race condition", "thread safety":
		return config.ClassConcurrency, true
	case "data loss", "data integrity":
		return config.ClassDataLoss, true
	case "resource leak", "resource management":
		return config.ClassResource, true
	case "correctness", "logic error", "potential issue", "bug":
		return config.ClassCorrectness, true
	case "api contract", "breaking change":
		return config.ClassContract, true
	case "testing", "test coverage":
		return config.ClassTests, true
	case "maintainability", "refactor suggestion", "code quality":
		return config.ClassMaintainability, true
	case "documentation", "style", "nitpick", "naming":
		return config.ClassStyle, true
	}

	// config.Class.Normalize already knows the single-word aliases; reusing it
	// keeps one vocabulary instead of two that can drift apart.
	if c, ok := config.Class(norm).Normalize(); ok {
		return c, true
	}

	return config.ClassUnknown, false
}

// RunIncumbent reviews a prepared fixture repository with the Incumbent CLI
// and returns its findings in open-nitpick's shape.
//
// The comparison is deliberately like-for-like: the same fixture repository,
// the same uncommitted working-tree change, and the same judge afterwards. What
// is NOT equalized is the reviewer's own prompt and model — that is the thing
// being compared.
//
// --agent is deliberately absent. That mode emits codegen INSTRUCTIONS for an
// autofix agent rather than a review: it anchors where an edit would begin (an
// import block) instead of at the defect, and prefixes every finding with a
// fixed instruction to the agent. Scoring review quality against it compared
// the wrong artifact. The default mode is the actual review.
// The raw return is the review as printed, escapes stripped, and is returned on
// EVERY path including the failures — a review that did not parse is precisely
// the one whose text someone needs to read.
func RunIncumbent(ctx context.Context, dir string, timeout time.Duration) ([]review.Finding, string, error) {
	if timeout <= 0 {
		timeout = 8 * time.Minute
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "incumbent",
		"review", "--uncommitted", "--base", "main")
	cmd.Dir = dir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = 10 * time.Second

	runErr := cmd.Run()

	// Both streams are searched wherever this function looks at output. Which
	// one the CLI writes a given message to is not contractual, and plain-text
	// mode is the HUMAN-facing mode: its messages go to stdout, which is why
	// the stderr-only diagnostics below used to lose them entirely.
	combined := stdout.String() + "\n" + stderr.String()

	// Escapes stripped once, here, so the retained text is what a human would
	// read rather than a terminal control stream.
	raw := crEscape.ReplaceAllString(combined, "")

	// Checked before anything else: a run that fell back to the free allowance
	// measures the allowance, not the reviewer, so its findings must not be
	// usable no matter how well they parsed.
	if crFreeTierFallback(combined) {
		return nil, raw, freeTierError()
	}

	findings, parseErr := parseIncumbent(stdout.Bytes())
	if parseErr == nil {
		// The review printed its own completion marker, so it ran to the end.
		// A non-zero exit on top of that is the CLI signalling "findings
		// exist", not a failure, and discarding a complete review over it
		// would be its own silent loss.
		return findings, raw, nil
	}

	// Everything below is the failure path, and the ORDER is the fix. parseErr
	// used to be returned first, so a missing binary, a cancelled context, an
	// expired timeout and an exhausted allowance all reported the same "the
	// plain-text format has changed" message and sent the operator to the wrong
	// problem. The parse error is the symptom of every one of them; it is
	// reported only once nothing else explains the output.
	switch {
	case runCtx.Err() != nil:
		// Both wrapped: callers match on the context error, and the parse error
		// is what a human needs to see to know the output was truncated rather
		// than absent.
		return nil, raw, fmt.Errorf("incumbent: %w (output did not parse: %w)", runCtx.Err(), parseErr)

	case runErr != nil && crRateLimited(combined):
		// Only inspected when the run FAILED. A review whose prose discusses
		// rate limiting is a normal successful review, and treating it as an
		// exhausted allowance would make CollectIncumbent back off for a
		// quarter of an hour over a finding about someone else's code.
		return nil, raw, fmt.Errorf("incumbent: %w: %w", ErrRateLimited, runErr)

	case runErr != nil:
		return nil, raw, fmt.Errorf("incumbent: %w (stderr: %s)", runErr, truncate(stderr.String(), 200))

	default:
		return nil, raw, fmt.Errorf("incumbent: %w (stderr: %s)", parseErr, truncate(stderr.String(), 200))
	}
}

// crRateLimitMarkers are the phrases that mean the account's allowance for the
// current window is spent. Consulted only alongside a failed run.
var crRateLimitMarkers = []string{"rate limit", "rate_limit", "too many requests", "429"}

// crRateLimited reports whether the CLI announced a rate limit anywhere in its
// output.
//
// The output is searched rather than the returned error, which is the bug this
// replaces: IsRateLimited reads err.Error(), and the error carried only stderr
// truncated to 200 bytes. The CLI announces the limit on STDOUT, so nothing
// matched, CollectIncumbent fell to its default branch, and the loop attacked
// the next fixture inside the same exhausted window instead of backing off.
func crRateLimited(out string) bool {
	haystack := strings.ToLower(strings.Join(strings.Fields(out), " "))

	for _, marker := range crRateLimitMarkers {
		if strings.Contains(haystack, marker) {
			return true
		}
	}
	return false
}

// parseIncumbent converts the CLI's plain-text review into review findings.
func parseIncumbent(out []byte) ([]review.Finding, error) {
	text := crEscape.ReplaceAllString(string(out), "")
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")

	var findings []review.Finding

	for i := 0; i < len(lines); {
		header := crFindingHeader.FindStringSubmatch(lines[i])
		if header == nil {
			i++
			continue
		}

		f, next, ok := parseCRFinding(lines, i, header[1], header[2])
		if !ok {
			i++
			continue
		}

		findings = append(findings, f)
		i = next
	}

	// No count is believable from a review that never said it finished, and the
	// requirement is UNCONDITIONAL. Gating it on len(findings) == 0 was the
	// bug: a stream cut off after k of N findings printed no trailer either, so
	// it took this branch only when k was zero and was otherwise returned as a
	// complete review of k findings. The recall figure then reads k/N with
	// nothing anywhere saying the review was truncated — a wrong number, which
	// is strictly worse than a missing one.
	//
	// It is also what makes the count check below reachable at all: crDeclared
	// returns 0 when the trailer never printed, so a shortfall against it can
	// only be detected once the trailer is known to exist.
	if !crCompleted(text) {
		return nil, fmt.Errorf("parsed %d finding(s) but found no completion marker: "+
			"the review did not run to the end", len(findings))
	}

	// The CLI counts its own findings in the trailer. If it says it produced
	// some and this parser produced fewer, the format has moved and the corpus
	// would be silently thin — which is worse than a failed collection, because
	// a thin corpus still yields a plausible-looking benchmark number.
	if declared := crDeclared(text); len(findings) < declared {
		return nil, fmt.Errorf("parsed %d of the %d finding(s) incumbent reported: "+
			"the plain-text format has changed", len(findings), declared)
	}

	return findings, nil
}

// crCompleted reports whether the CLI printed its trailer, which is its own
// statement that the review finished rather than stopped.
func crCompleted(text string) bool {
	return strings.Contains(strings.ToLower(text), "review complete") ||
		crDeclaredCount.MatchString(text)
}

// parseCRFinding reads the block whose header is at lines[start], returning the
// finding and the index just past it.
//
// A block is only recognized when the header is followed by the "→ path:line"
// anchor. That two-line signature is what separates a finding from trailer or
// banner prose that happens to end in a bracket; if it ever wrongly rejects a
// real finding, parseIncumbent's count check turns that into a loud failure
// rather than a missing finding.
func parseCRFinding(lines []string, start int, severity, category string) (review.Finding, int, bool) {
	i := start + 1
	for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
		i++
	}
	if i >= len(lines) {
		return review.Finding{}, start + 1, false
	}

	anchor := crAnchor.FindStringSubmatch(lines[i])
	if anchor == nil {
		return review.Finding{}, start + 1, false
	}
	i++

	// The body runs to the next rule, the next finding, or the end of a stream
	// that was cut off mid-review.
	var body []string
	for ; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if crRule.MatchString(trimmed) || crFindingHeader.MatchString(lines[i]) {
			break
		}
		body = append(body, trimmed)
	}

	path, line, endLine := crParseLocation(anchor[1])

	// The secondary-location line is pulled OUT of the body before the body
	// becomes prose: it is metadata about where the finding applies, and leaving
	// it in would put "Also applies to: 15-18" in front of the judge as though
	// it were part of the reviewer's reasoning.
	var alsoAt []review.LineSpan

	kept := body[:0]
	for _, b := range body {
		if extra := crParseAlsoApplies(b, path); len(extra) > 0 {
			alsoAt = append(alsoAt, extra...)
			continue
		}
		kept = append(kept, b)
	}
	body = kept

	title, rationale := crSplitBody(body)

	// Suggestion is left empty: a plain-text review states the problem in prose
	// and offers no replacement code, which is exactly the difference from
	// --agent mode. The judge treats it as optional.
	// RawSeverity keeps the word the CLI actually printed, beside the level
	// crSeverity translates it to.
	//
	// THE BUG IT FIXES: only the translation survived the parse, and the eval
	// report published it as the reviewer's own vocabulary. Incumbent never
	// printed "warning" anywhere in the shipped corpus — it prints "critical" and
	// "major" — yet the block captioned "what each contender called the defects it
	// located" read "critical x4, warning x3", and swapping crSeverity's free
	// "major" constant re-rendered the identical cached bytes as "critical x4,
	// error x3". A description that moves when we change our own constant, with
	// nothing in the reviewer's output changing, is not a description of the
	// reviewer. That block exists precisely because a SCORE could not be
	// justified; a derived figure wearing the authority of an observation is the
	// defect the withdrawal was about, one level down.
	f := review.Finding{
		Path:               path,
		Line:               line,
		EndLine:            endLine,
		AlsoAt:             alsoAt,
		Severity:           string(crSeverity(severity)),
		SeverityTranslated: true,
		RawSeverity:        strings.TrimSpace(severity),
		Category:           strings.TrimSpace(category),
		Title:              title,
		Rationale:          rationale,
		Source:             IncumbentModel,
	}
	if f.Category == "" {
		f.Category = "incumbent"
	}

	class, ok := crCategoryClass(category)
	if !ok {
		// No usable category, so fall back to the prose. Keeping a class on
		// every finding is what stops one being silently filtered downstream.
		class = classifyText(title + " " + rationale)
	}
	f.Class = string(class)

	return f, i, true
}

// crParseLocation splits "store.go:10-12" into a path and an anchor line.
//
// BOTH ends of a range are kept. Taking only the first number was wrong twice
// over: "the credential is on lines 11-12" identifies a defect on line 12, and
// the discarded end is not decoration -- Incumbent anchored the same defect at
// 7 on one run and at 11-12 on the next, so scoring the start alone converted
// the reviewer's own variance into a flipped hit-or-miss. See anchorDistance.
//
// A location carrying no line span yields line 0 and the finding is still
// returned. An unplaceable finding is visibly wrong; a dropped one is invisible,
// and this benchmark exists to count what the reviewer reported.
func crParseLocation(s string) (path string, line, endLine int) {
	s = strings.TrimSpace(s)

	m := crLocation.FindStringSubmatch(s)
	if m == nil {
		return s, 0, 0
	}

	n, err := strconv.Atoi(m[2])
	if err != nil {
		return m[1], 0, 0
	}

	// A malformed or backwards end is dropped rather than trusted: EndLine < Line
	// would make the span nonsense, and a single-line anchor is the safe reading.
	end := 0
	if m[3] != "" {
		if e, err := strconv.Atoi(m[3]); err == nil && e > n {
			end = e
		}
	}

	return m[1], n, end
}

// crParseAlsoApplies reads the regions named by a secondary-location line.
//
// Entries naming a DIFFERENT file are dropped: a span is only meaningful next
// to the path it belongs to, and Finding carries one Path. Nothing in the
// observed output does this, and silently attaching another file's line numbers
// to this finding's path would invent an anchor rather than lose one.
func crParseAlsoApplies(line, path string) []review.LineSpan {
	m := crAlsoApplies.FindStringSubmatch(line)
	if m == nil {
		return nil
	}

	var out []review.LineSpan
	for _, part := range strings.Split(m[1], ",") {
		e := crExtraSpan.FindStringSubmatch(strings.TrimSpace(part))
		if e == nil || (e[1] != "" && e[1] != path) {
			continue
		}

		start, err := strconv.Atoi(e[2])
		if err != nil {
			continue
		}

		span := review.LineSpan{Line: start}
		if e[3] != "" {
			if end, err := strconv.Atoi(e[3]); err == nil && end > start {
				span.EndLine = end
			}
		}
		out = append(out, span)
	}

	return out
}

// crSplitBody splits a finding's body into its title and rationale.
//
// The first paragraph is the one-sentence title; the rest is the rationale.
// Hard wrapping is a terminal artifact rather than authored structure, so each
// paragraph is unwrapped back onto one line — the judge reads this text, and a
// sentence broken every 72 columns reads as mangled. The blank lines BETWEEN
// paragraphs are the author's and are kept.
func crSplitBody(body []string) (title, rationale string) {
	var (
		paragraphs []string
		current    []string
	)

	flush := func() {
		if len(current) > 0 {
			paragraphs = append(paragraphs, strings.Join(current, " "))
			current = nil
		}
	}

	for _, line := range body {
		if line == "" {
			flush()
			continue
		}
		current = append(current, line)
	}
	flush()

	if len(paragraphs) == 0 {
		return "", ""
	}
	return paragraphs[0], strings.Join(paragraphs[1:], "\n\n")
}

// crDeclared returns the finding count the CLI printed in its trailer, or 0
// when it printed none.
//
// The LAST match wins: the trailer is printed after the findings, so a
// rationale that happens to begin a line with a number cannot displace it.
func crDeclared(text string) int {
	matches := crDeclaredCount.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return 0
	}

	n, err := strconv.Atoi(matches[len(matches)-1][1])
	if err != nil {
		return 0
	}
	return n
}

// classifyText assigns a class from a finding's prose.
//
// Deliberately biased toward correctness: an unrecognized finding published at
// every level is a far better failure than a real defect filtered away because
// the wording did not match a keyword.
func classifyText(text string) config.Class {
	t := strings.ToLower(text)

	switch {
	case containsAny(t, "injection", "traversal", "credential", "secret", "xss",
		"csrf", "authorization", "authentication", "sanitiz", "escape"):
		return config.ClassSecurity
	case containsAny(t, "race", "concurren", "goroutine", "mutex", "deadlock", "atomic"):
		return config.ClassConcurrency
	case containsAny(t, "leak", "not closed", "unclosed", "close the", "unbounded", "exhaust"):
		return config.ClassResource
	case containsAny(t, "data loss", "corrupt", "overwrite", "truncat"):
		return config.ClassDataLoss
	case containsAny(t, "breaking change", "backward compat", "existing caller", "signature change"):
		return config.ClassContract
	case containsAny(t, "test coverage", "no tests", "add a test", "untested"):
		return config.ClassTests
	case containsAny(t, "naming", "rename", "typo", "comment", "documentation", "formatting"):
		return config.ClassStyle
	default:
		return config.ClassCorrectness
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// IncumbentAvailable reports whether the CLI is installed and authenticated.
func IncumbentAvailable(ctx context.Context) bool {
	if _, err := exec.LookPath("incumbent"); err != nil {
		return false
	}

	checkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(checkCtx, "incumbent", "auth", "status")
	out, err := cmd.CombinedOutput()

	return err == nil && strings.Contains(string(out), "@")
}

// --- Free-tier fallback ------------------------------------------------------

// ErrFreeTier reports that Incumbent could not match the review to an
// organization and billed it to the free CLI allowance.
//
// It is a sentinel because the caller has to make a decision about it — never
// cache the result — and a run that quietly degrades to the allowance produces
// a number that measures the allowance rather than the reviewer.
var ErrFreeTier = errors.New("incumbent fell back to the free CLI allowance: " +
	"the repository has no git remote it can match to an organization")

// IsFreeTier reports whether an error is the free-allowance fallback.
func IsFreeTier(err error) bool { return errors.Is(err, ErrFreeTier) }

// freeTierError wraps the sentinel with the remedy, so the line a collection
// logs says what to change rather than only what went wrong.
func freeTierError() error {
	return fmt.Errorf("%w (set %s to a remote your Incumbent organization owns)",
		ErrFreeTier, EnvIncumbentRemote)
}

// crFreeTierMarkers are the distinguishing phrases of the warning:
//
//	Incumbent couldn't find a Git remote for this repository, so it can't
//	match the review to one of your organizations. This review will use the
//	free CLI allowance, even if you're signed in.
//
// Each is specific enough that review prose cannot produce it by accident —
// note that the ordinary footer advertising "free promotional credits" must NOT
// trip this.
var crFreeTierMarkers = []string{
	"free cli allowance",
	"couldn't find a git remote",
	"could not find a git remote",
}

// crFreeTierFallback reports whether the CLI announced the free-allowance
// fallback anywhere in its output.
//
// Whitespace is collapsed first because the warning is hard-wrapped to the
// terminal width, which would otherwise split a marker across two lines, and
// the typographic apostrophe is folded to ASCII because which one the CLI emits
// is a rendering detail.
func crFreeTierFallback(out string) bool {
	haystack := strings.ToLower(strings.Join(strings.Fields(out), " "))
	haystack = strings.ReplaceAll(haystack, "’", "'")

	for _, marker := range crFreeTierMarkers {
		if strings.Contains(haystack, marker) {
			return true
		}
	}
	return false
}

// --- Resumable collection ----------------------------------------------------
//
// Reviews are matched to an organization through the fixture repository's git
// remote, which buildRepo now installs. That is the primary fix: without it
// every review is billed to the free CLI allowance regardless of account tier,
// and the first attempt at this benchmark lost 6 of 8 fixtures to rate limiting
// and produced a "score" that measured the allowance rather than the reviewer.
//
// The serial, cached, resumable shape below remains as the fallback, because a
// paid tier still has limits and a wide concurrent run can still be refused. A
// rate limit costs a wait, never lost progress, and the benchmark can be
// assembled across as many sessions as it takes.

// crReviewMode identifies which Incumbent invocation produced a cached review.
//
// It is stored so a cache collected under a different mode cannot be loaded and
// ranked as though it were this one. The benchmark has already switched once,
// from --agent to plain text, and the code comment on RunIncumbent calls the
// --agent output "the wrong artifact" — but a cache written under it carried no
// record of that and would have been scored as a plain-text review.
const crReviewMode = "plaintext"

// crCacheDir is where collected Incumbent reviews persist between runs.
//
// It lives beside the cache logic rather than in a test file: the re-parse path
// reads it and is not eval-tagged, and a constant naming where the corpus lives
// belongs with the code that writes the corpus.
const crCacheDir = "testdata/incumbent"

// crCache is one fixture's cached Incumbent review, plus enough provenance to
// tell whether it still measures what the benchmark is about to compare.
type crCache struct {
	Fixture  string           `json:"fixture"`
	Findings []review.Finding `json:"findings"`
	At       string           `json:"at"`

	// Mode and Fingerprint answer "what was reviewed, and how". Without them
	// the cache keyed on fixture NAME alone, so editing a fixture's Head left
	// every model reviewing the new code while Incumbent's row was the old
	// code's findings scored against the new ground truth — two sides of a
	// head-to-head reviewing different source with no signal that it happened.
	Mode        string `json:"mode"`
	Fingerprint string `json:"fingerprint"`

	// Raw is the review as the CLI printed it, escapes already stripped.
	//
	// Kept because the parsed findings are a LOSSY read of it, and every
	// question about whether a number is real turns out to be a question about
	// what was actually printed. The first such question cost a re-review: the
	// parser reduces "client.go:7-12" to line 7, so a defect on line 12 scored
	// as a miss, and nothing on disk could say whether Incumbent had reported
	// a span or genuinely pointed at the wrong line. Re-running to find out
	// spends the account's allowance to recover something the collection
	// already had in hand.
	Raw string `json:"raw,omitempty"`
}

// ErrRateLimited reports that the CLI refused the review because the account's
// allowance for the current window is spent.
//
// A sentinel rather than a substring search over whatever error happened to be
// built: the CLI announces the limit on stdout, and stdout never reached the
// error text. See crRateLimited.
var ErrRateLimited = errors.New("incumbent rate limit reached")

// IsRateLimited reports whether an error is the allowance refusing.
func IsRateLimited(err error) bool {
	if err == nil {
		return false
	}
	// The substring arm is kept for errors this package did not build — a
	// transport layer reporting a 429 in prose, say.
	return errors.Is(err, ErrRateLimited) ||
		strings.Contains(strings.ToLower(err.Error()), "rate limit")
}

// fixtureFingerprint hashes the exact source a fixture puts in front of a
// reviewer, so a recorded review can be matched to the code it actually read.
//
// Two callers, one question. The Incumbent cache asks it to avoid replaying a
// review of code that has since changed; DumpRecord asks it so a re-judge can
// refuse a dump whose fixture was edited between the benchmark and the
// re-judge. Both are the same failure — a verdict compared against a prompt
// nobody made it against — so both read the same hash rather than two that can
// drift.
//
// Defects are deliberately excluded: they are the ground truth the judge scores
// AGAINST, not input to the reviewer, and folding them in would discard a still
// valid review every time a defect's wording was edited.
func fixtureFingerprint(f Fixture) string {
	h := sha256.New()

	for _, files := range []map[string]string{f.Base, f.Head, f.Extra} {
		names := make([]string, 0, len(files))
		for name := range files {
			names = append(names, name)
		}
		sort.Strings(names) // map order is not stable; the hash must be

		for _, name := range names {
			// hash.Hash never errors, and the NUL separators are what keep
			// "ab"+"c" from hashing the same as "a"+"bc".
			_, _ = fmt.Fprintf(h, "%s\x00%s\x00", name, files[name])
		}
		h.Write([]byte{'\x01'}) // section separator, so moving a file between maps changes the hash
	}

	return hex.EncodeToString(h.Sum(nil))
}

// CachedIncumbent returns a fixture's cached findings, if one was collected
// for this exact fixture content and review mode.
//
// The findings are re-derived from the recorded Raw review whenever there is
// one, and c.Findings is used only for an entry collected before Raw was kept.
// Raw is the evidence; c.Findings is one PARSER'S READING of it, and the parser
// is the part that keeps turning out to be wrong — crSeverity demoted every
// Incumbent "critical" for the whole of the first benchmark, and a headline
// number was published on the result. Replaying the stored reading freezes each
// such bug into the corpus permanently, so fixing one would cost a full
// re-collection against a rate-limited allowance to recover data already on
// disk. Re-parsing costs microseconds and makes a parser fix apply to every
// review ever collected.
//
// A Raw that no longer parses is reported as NO CACHE, and this is the second
// answer this function has given to that question. IT USED TO FALL BACK TO THE
// STORED FINDINGS, returning ok=true with no marker of any kind, on the
// reasoning that a stale reading beats a silently absent review. The reasoning
// is wrong in the only direction that matters here: a stale reading is not
// merely older, it is a DIFFERENT PARSER'S output being scored in a table
// captioned as this parser's result, and it arrives looking exactly like a fresh
// one. A cache that silently serves a stale parse is how a corpus drifts under a
// measurement — and the fallback fired precisely when the parser had changed,
// which is precisely when the difference matters. The absent-review worry it was
// answering is handled where it belongs: every caller lists what has no cache
// before it runs, and a contender judged on nothing is failed rather than ranked.
//
// StaleIncumbentCache says which of the two it was, so a report can tell an
// operator "the parser and the evidence have diverged, re-collect" instead of
// "never collected".
//
// A fingerprint or mode mismatch is likewise reported as "no cache" rather than
// as an error: the caller's remedy is identical either way — review it again —
// and a stale entry is overwritten by the next collection.
func CachedIncumbent(cacheDir string, f Fixture) ([]review.Finding, bool) {
	c, ok := readCRCache(cacheDir, f)
	if !ok {
		return nil, false
	}

	if c.Raw != "" {
		findings, err := parseIncumbent([]byte(c.Raw))
		if err != nil {
			return nil, false
		}
		return findings, true
	}

	// Collected before Raw was retained. There is no evidence to re-derive from,
	// so the stored reading is all there is; it is not stale in the sense above,
	// because no newer reading of the same bytes is possible.
	//
	// Source and SeverityTranslated are both restored because both are
	// `json:"-"` and neither survived the round trip. SeverityTranslated is the
	// load-bearing one: these findings' Severity is crSeverity's word, their
	// RawSeverity was never serialized, and a replayed finding that came back
	// claiming nothing had translated it would have its TRANSLATION republished
	// as the reviewer's own spelling — the exact substitution RawSeverity exists
	// to stop, reintroduced by a field that was never serialized. Setting the
	// flag with no word is the honest pair: something rewrote this, and the
	// original is gone. The vocabulary block prints UnrecordedWord for each of
	// them rather than filling the gap from our reading.
	out := make([]review.Finding, len(c.Findings))
	copy(out, c.Findings)
	for i := range out {
		out[i].Source = IncumbentModel
		out[i].SeverityTranslated = true
	}
	return out, true
}

// severityWasTranslated reports whether a finding's Severity is this project's
// word rather than the reporter's own.
//
// It reads the FACT the rewriter recorded. It used to read the finding's SOURCE
// — `f.Source == IncumbentModel` — and the comment here justified that by
// claiming crSeverity was "the only place in the tree that rewrites a reviewer's
// severity vocabulary" and that "everything else writes its own severity and is
// quoted verbatim". BOTH HALVES WERE FALSE, and the second one is the defect.
// review.Engine rewrites every model's severity through Normalize, and
// linters.mapSeverity collapses four analyzers' vocabularies onto three levels;
// neither recorded anything, so this function answered "nothing was translated"
// for every contender this project ships and the vocabulary block quoted each of
// them as having printed the word we had substituted. That is the same defect the
// incumbent's side was fixed for, reintroduced on ours — and worse, because there
// a lost word prints UnrecordedWord and here the substitute was published
// silently as a quotation.
//
// Keying on the reporter could not have been right at any value. "Whose word is
// this?" is a fact about what happened to the finding, and a reviewer's identity
// only correlates with it — so the answer was guaranteed to drift the moment any
// other path rewrote a severity, which two already had.
func severityWasTranslated(f review.Finding) bool {
	return f.SeverityTranslated
}

// StaleIncumbentCache reports whether a cache entry exists for this fixture and
// matches its current content, but its recorded review no longer parses.
//
// It distinguishes the two reasons CachedIncumbent says no. "Never collected"
// costs an operator a review against a rate-limited allowance; "collected, and
// the parser no longer reads it" is a bug in the parser or a change in the CLI's
// output, and re-collecting would spend that allowance to hide it.
func StaleIncumbentCache(cacheDir string, f Fixture) (stale bool, why error) {
	c, ok := readCRCache(cacheDir, f)
	if !ok || c.Raw == "" {
		return false, nil
	}

	if _, err := parseIncumbent([]byte(c.Raw)); err != nil {
		return true, err
	}
	return false, nil
}

// readCRCache loads the entry for a fixture, if one matches its exact content
// and review mode.
func readCRCache(cacheDir string, f Fixture) (crCache, bool) {
	data, err := os.ReadFile(filepath.Join(cacheDir, f.Name+".json"))
	if err != nil {
		return crCache{}, false
	}

	var c crCache
	if err := json.Unmarshal(data, &c); err != nil {
		return crCache{}, false
	}
	if c.Mode != crReviewMode || c.Fingerprint != fixtureFingerprint(f) {
		return crCache{}, false
	}

	return c, true
}

// CollectIncumbent reviews any fixtures not already cached, one at a time.
//
// onProgress is called with a human-readable line per fixture so a long
// collection is legible while it runs. It returns the number newly collected
// and the number still outstanding.
func CollectIncumbent(
	ctx context.Context,
	fixtures []Fixture,
	cacheDir string,
	perReview time.Duration,
	onProgress func(string),
) (collected, remaining int, err error) {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return 0, 0, err
	}

	log := func(format string, args ...any) {
		if onProgress != nil {
			onProgress(fmt.Sprintf(format, args...))
		}
	}

	// Backoff grows on repeated rate limits: the window is not documented, so
	// probing it gently is better than hammering and being refused for longer.
	backoff := 60 * time.Second
	const maxBackoff = 15 * time.Minute

	for _, f := range fixtures {
		if _, ok := CachedIncumbent(cacheDir, f); ok {
			log("%s: cached", f.Name)
			continue
		}

		// A cached review this parser can no longer read is NOT re-collected.
		// Overwriting it would replace the bytes that expose the divergence with
		// bytes that happen to parse, spending a rate-limited allowance to
		// destroy the only evidence of a parser bug. The remedy is to read the
		// retained text and fix the parser, or to delete the entry deliberately.
		if stale, why := StaleIncumbentCache(cacheDir, f); stale {
			remaining++
			log("%s: STALE CACHE, not re-collected: the retained review no longer parses (%v). "+
				"Fix the parser against %s/%s.json, or delete it to force a fresh review",
				f.Name, why, cacheDir, f.Name)
			continue
		}

		if err := ctx.Err(); err != nil {
			return collected, remaining, err
		}

		dir, err := os.MkdirTemp("", "cr-collect-")
		if err != nil {
			return collected, remaining, err
		}

		buildErr := buildRepo(dir, f)
		if buildErr != nil {
			_ = os.RemoveAll(dir)
			return collected, remaining, buildErr
		}

		findings, raw, runErr := RunIncumbent(ctx, dir, perReview)
		_ = os.RemoveAll(dir)

		switch {
		case runErr == nil:
			blob, _ := json.MarshalIndent(crCache{
				Fixture:     f.Name,
				Findings:    findings,
				At:          time.Now().UTC().Format(time.RFC3339),
				Mode:        crReviewMode,
				Fingerprint: fixtureFingerprint(f),
				Raw:         raw,
			}, "", "  ")
			if werr := os.WriteFile(filepath.Join(cacheDir, f.Name+".json"), blob, 0o644); werr != nil {
				return collected, remaining, werr
			}

			collected++
			backoff = 60 * time.Second // the window reopened; reset the probe
			log("%s: collected %d finding(s)", f.Name, len(findings))

			// Space out successes too: limits are per-window, and pausing after
			// a success is what keeps the next one from being refused.
			select {
			case <-ctx.Done():
				return collected, remaining, ctx.Err()
			case <-time.After(20 * time.Second):
			}

		case IsFreeTier(runErr):
			// Nothing is cached, and the whole collection stops: every fixture
			// is built the same way, so the next review would be mismeasured
			// identically while spending real allowance to produce a number
			// already known to be invalid. This is a configuration error, and
			// waiting does not fix it.
			remaining++
			log("%s: FREE-TIER FALLBACK, not cached: %v", f.Name, runErr)
			return collected, remaining, runErr

		case IsRateLimited(runErr):
			remaining++
			log("%s: rate limited, backing off %s", f.Name, backoff)

			select {
			case <-ctx.Done():
				return collected, remaining, ctx.Err()
			case <-time.After(backoff):
			}

			if backoff *= 2; backoff > maxBackoff {
				backoff = maxBackoff
			}

		default:
			remaining++
			log("%s: failed: %v", f.Name, runErr)
		}
	}

	return collected, remaining, nil
}
