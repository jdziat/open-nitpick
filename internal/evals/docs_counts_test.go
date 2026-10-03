package evals

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Published counts in the findings prose and README must match the evidence table.
func TestInstrumentBugCountIsTheTableRowCount(t *testing.T) {
	findings, err := os.ReadFile("../../docs/findings.md")
	if err != nil {
		t.Skip("docs/findings.md not present")
	}
	section := strings.SplitN(string(findings), "## What the instrument got wrong", 2)
	if len(section) != 2 {
		t.Fatal("findings.md has no 'What the instrument got wrong' section")
	}
	body := strings.SplitN(section[1], "\n## ", 2)[0]

	// The first table in the section: header, separator, then one row per
	// bug. The separator is recognised by its `|---` prefix, which is the
	// style findings.md uses; a `| --- |` separator would count as a row.
	rows := 0
	inTable := false
	for _, line := range strings.Split(body, "\n") {
		isRow := strings.HasPrefix(line, "|")
		switch {
		case isRow && !inTable:
			inTable = true
		case isRow && inTable:
			if !strings.HasPrefix(line, "|---") {
				rows++
			}
		case !isRow && inTable:
			inTable = false
		}
		if !inTable && rows > 0 {
			break
		}
	}
	if rows <= 0 {
		t.Fatalf("counted %d table rows", rows)
	}

	words := map[int]string{10: "Ten", 11: "Eleven", 12: "Twelve", 13: "Thirteen", 14: "Fourteen", 15: "Fifteen",
		16: "Sixteen", 17: "Seventeen", 18: "Eighteen", 19: "Nineteen", 20: "Twenty", 21: "Twenty-one", 22: "Twenty-two",
		23: "Twenty-three", 24: "Twenty-four", 25: "Twenty-five", 26: "Twenty-six", 27: "Twenty-seven", 28: "Twenty-eight",
		29: "Twenty-nine", 30: "Thirty"}
	word, ok := words[rows]
	if !ok {
		t.Fatalf("%d rows: extend the word table", rows)
	}
	if !strings.Contains(body, word+" measurement bugs have been found") {
		t.Errorf("findings.md prose does not say %q measurement bugs; the table has %d rows", word, rows)
	}

	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Skip("README.md not present")
	}
	// The README spells the figure out, in the sentence that sends a reader to
	// findings.md. Matching the phrase rather than the word is what makes a
	// stale number fail here instead of a missing sentence passing quietly.
	r := regexp.MustCompile(`the ([a-z-]+) instrument bugs found along the way`).FindStringSubmatch(string(readme))
	if r == nil {
		t.Fatal("README.md no longer states the instrument-bug count; drop this check or restore the sentence")
	}
	if !strings.EqualFold(r[1], word) {
		t.Errorf("README.md says %q instrument bugs; the table has %d rows", r[1], rows)
	}
}
