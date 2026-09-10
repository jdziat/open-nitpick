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

	head := handWritten[:strings.Index(handWritten, BeginMarker)]
	tail := handWritten[strings.Index(handWritten, EndMarker)+len(EndMarker):]

	if !strings.HasPrefix(out, head) {
		t.Errorf("the text above the block changed:\n%q", out[:len(head)+40])
	}
	if !strings.HasSuffix(out, tail) {
		t.Errorf("the text below the block changed:\n%q", out[len(out)-len(tail)-40:])
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
