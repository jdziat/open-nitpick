package standards

import (
	"fmt"
	"strings"
	"testing"
)

// synthetic builds a report of n standards, all clearing any floor.
func synthetic(n int) Report {
	rep := Report{Floor: DefaultFloor()}
	for i := range n {
		rep.Results = append(rep.Results, Result{
			ID:         fmt.Sprintf("rule-%03d", i),
			Rule:       fmt.Sprintf("Do the thing numbered %d.", i),
			Conforming: 100 - i%5,
			Total:      100,
			Standing:   StandingStandard,
		})
	}
	return rep
}

// The block stays inside its budget, and says what it left out.
//
// Length is how a conventions file fails. Past a screen or two nobody reads to
// the end, and the rules that matter are diluted by the rules that were easy
// to write. A block that truncated in silence would read as the whole of what
// the repository decided.
func TestTheBlockIsBoundedAndSaysWhatItDropped(t *testing.T) {
	block := synthetic(200).Block()

	if got := strings.Count(block, "\n"); got > agentsMaxLines {
		t.Errorf("block is %d lines, over the %d-line budget", got, agentsMaxLines)
	}
	if !strings.Contains(block, "more measured standard(s) are not listed") {
		t.Error("the block dropped rules without saying so, so a truncated file reads as a complete one")
	}
	if !strings.HasSuffix(strings.TrimSpace(block), EndMarker) {
		t.Error("the block does not close its own marker")
	}

	// The dropped count is the real one, not a placeholder.
	listed := strings.Count(block, "\n- ")
	if !strings.Contains(block, fmt.Sprintf("%d more measured", 200-listed)) {
		t.Errorf("listed %d rules; the block does not report %d dropped", listed, 200-listed)
	}
}

// A small report is printed whole, with nothing about dropping.
func TestASmallReportIsNotTruncated(t *testing.T) {
	block := synthetic(3).Block()
	if n := strings.Count(block, "\n- "); n != 3 {
		t.Errorf("listed %d of 3 rules", n)
	}
	if strings.Contains(block, "not listed") {
		t.Error("a block that dropped nothing claims it dropped something")
	}
}

// A repository with no measurable convention says so.
//
// docs/measurement.md Rule 10, in the artifact rather than in the report. An
// empty block and a repository whose conventions were never measured look the
// same on the page, and only one of them is worth acting on.
func TestAnEmptyBlockSaysNothingCleared(t *testing.T) {
	block := Report{Floor: DefaultFloor()}.Block()
	if !strings.Contains(block, "clears the evidence floor") {
		t.Errorf("an empty block does not explain itself:\n%s", block)
	}
}

const handWritten = `# Working here

Run ` + "`make check`" + ` before you push. This paragraph is mine.

<!-- nitpick:standards:begin -->
stale content that should be replaced
<!-- nitpick:standards:end -->

## Notes

This tail is mine too, and includes a trailing thought.
`

// Everything outside the markers survives, byte for byte.
//
// The parts of an agent file worth having are the ones no probe can see: the
// build commands, the gates, the reason a package is laid out the way it is.
// A generator that reformats them, or trims a trailing line, is a generator
// people stop running.
func TestHandWrittenTextOutsideTheMarkersIsUntouched(t *testing.T) {
	out, err := Render(handWritten, synthetic(2))
	if err != nil {
		t.Fatal(err)
	}

	begin, end := strings.Index(handWritten, BeginMarker), strings.Index(handWritten, EndMarker)
	if begin < 0 || end < begin {
		t.Fatal("fixture must contain ordered markers")
	}
	head := handWritten[:begin]
	tail := handWritten[end+len(EndMarker):]

	if !strings.HasPrefix(out, head) {
		t.Errorf("the text above the block changed:\n%q", out)
	}
	if !strings.HasSuffix(out, tail) {
		t.Errorf("the text below the block changed:\n%q", out)
	}
	if strings.Contains(out, "stale content") {
		t.Error("the old block survived the regeneration")
	}
	if !strings.Contains(out, "Do the thing numbered 0.") {
		t.Error("the new block is not there")
	}

	// And running it again changes nothing, which is what the drift gate rests
	// on: a check that fails on a clean tree is a check people delete.
	again, err := Render(out, synthetic(2))
	if err != nil {
		t.Fatal(err)
	}
	if again != out {
		t.Error("a second regeneration produced different bytes, so the drift gate would never pass")
	}
}

// A file written before this existed keeps everything and gains a block.
func TestAFileWithNoMarkersKeepsItsContent(t *testing.T) {
	const prior = "# Notes\n\nSomething a person wrote.\n"
	out, err := Render(prior, synthetic(1))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, prior) {
		t.Errorf("the prior content was not preserved:\n%s", out)
	}
	if !strings.Contains(out, BeginMarker) {
		t.Error("no block was added")
	}
}

// Half a marker pair is refused rather than guessed at.
//
// Guessing where the block was meant to end means overwriting prose nobody can
// get back, and the two ways to be wrong are not symmetric: refusing costs a
// person one minute, and guessing costs them whatever they had written.
func TestABrokenMarkerPairIsRefused(t *testing.T) {
	for name, body := range map[string]string{
		"no end":   "# T\n" + BeginMarker + "\nrules\n",
		"no begin": "# T\nrules\n" + EndMarker + "\n",
		"reversed": "# T\n" + EndMarker + "\nrules\n" + BeginMarker + "\n",
	} {
		if _, err := Render(body, synthetic(1)); err == nil {
			t.Errorf("%s: accepted, and a regeneration would have eaten the file", name)
		}
	}
}

// A file with no content at all gets a scaffold that admits it is one.
func TestANewFileSaysTheProseIsNotGenerated(t *testing.T) {
	out, err := Render("", synthetic(1))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Nothing here is regenerated") {
		t.Error("the scaffold does not say which half a person owns")
	}
	if !strings.Contains(out, BeginMarker) || !strings.Contains(out, EndMarker) {
		t.Error("the scaffold has no managed block")
	}
}

// A rule that falls below the floor is retired out loud.
//
// Regeneration removes a rule the moment the code stops following it, which is
// the property this design rests on. Removing it in silence is the same
// property with the reader taken out: `make agents` deletes the line, CI is
// green because the author regenerated, and a convention erodes with nobody
// told. The block names what has sites and no longer clears the floor.
func TestARetiredRuleIsNamedRatherThanDeletedInSilence(t *testing.T) {
	rep := synthetic(2)
	rep.Results = append(rep.Results, Result{
		ID: "rule-fallen", Rule: "Do the fallen thing.",
		Conforming: 60, Total: 100, Standing: StandingContested,
	})

	block := rep.Block()
	if strings.Contains(block, "Do the fallen thing.") {
		t.Error("a contested probe was published as a rule")
	}
	if !strings.Contains(block, "rule-fallen") {
		t.Errorf("a probe that stopped clearing the floor vanished without a word:\n%s", block)
	}
	if !strings.Contains(block, "do not clear the floor") {
		t.Error("the block does not say why the probe is not a rule")
	}

	// A probe that was never seen at all is not a retirement.
	quiet := synthetic(1)
	quiet.Results = append(quiet.Results, Result{ID: "rule-quiet", Standing: StandingUnseen})
	if strings.Contains(quiet.Block(), "rule-quiet") {
		t.Error("a probe with no sites was reported as having failed the floor")
	}
}

// The evidence is banded, and the band still falls when the share does.
//
// An exact count moves whenever anybody adds a function, so a gated artifact
// carrying one fails CI on nearly every pull request touching Go. Bucketing
// buys that back, and it is only worth having if a convention the code stops
// following still loses its rule.
func TestTheBandSurvivesChurnAndStillFalls(t *testing.T) {
	for _, tc := range []struct {
		conforming, total int
		want              string
	}{
		{1205, 1205, "every site of 1000+ places"},
		{1181, 1205, "98%+ of 1000+ places"},
		{1182, 1206, "98%+ of 1000+ places"},
		{49, 51, "95%+ of 50+ places"},
		{91, 100, "90%+ of 100+ places"},
		{86, 100, "85%+ of 100+ places"},
	} {
		got := band(Result{Conforming: tc.conforming, Total: tc.total})
		if got != tc.want {
			t.Errorf("band(%d/%d) = %q, want %q", tc.conforming, tc.total, got, tc.want)
		}
	}

	// The two readings a day apart render the same, which is the point.
	if band(Result{Conforming: 1181, Total: 1205}) != band(Result{Conforming: 1182, Total: 1206}) {
		t.Error("one added function moved the band, so the gated file still churns")
	}
	// And a real fall moves it.
	if band(Result{Conforming: 1181, Total: 1205}) == band(Result{Conforming: 1100, Total: 1205}) {
		t.Error("a share falling from 98% to 91% left the band unchanged")
	}
}
