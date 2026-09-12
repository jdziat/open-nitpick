package bundle

import (
	"fmt"
	"strings"
)

// SourceSpan names an inclusive range of original source lines.
type SourceSpan struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// SelectSourceSpans returns exact source bytes for ordered, disjoint ranges.
func SelectSourceSpans(content string, spans []SourceSpan) (string, error) {
	lines := strings.SplitAfter(content, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	var out strings.Builder
	prior := 0
	for _, span := range spans {
		if span.Start <= prior || span.End < span.Start || span.End > len(lines) {
			return "", fmt.Errorf("invalid source span %d:%d for %d lines", span.Start, span.End, len(lines))
		}
		for _, line := range lines[span.Start-1 : span.End] {
			out.WriteString(line)
		}
		prior = span.End
	}
	return out.String(), nil
}

func renderSourceSpans(entry Entry) string {
	var out strings.Builder
	fmt.Fprintf(&out, "### Selected supporting source: %s\nOnly the declared line ranges are shown. Findings here belong in the summary.\n", promptSafe(entry.File.Path))
	for _, instruction := range entry.Instructions {
		fmt.Fprintf(&out, "Path instruction: %s\n", promptSafe(instruction))
	}
	if _, err := SelectSourceSpans(entry.Content, entry.SourceSpans); err != nil {
		return out.String() + "Source ranges are unavailable.\n"
	}
	lines := strings.SplitAfter(entry.Content, "\n")
	for _, span := range entry.SourceSpans {
		fmt.Fprintf(&out, "\nLines %d-%d:\n```\n", span.Start, span.End)
		for index := span.Start - 1; index < span.End; index++ {
			fmt.Fprintf(&out, "%6d  %s", index+1, lines[index])
			if !strings.HasSuffix(lines[index], "\n") {
				out.WriteByte('\n')
			}
		}
		out.WriteString("```\n")
	}
	return out.String()
}

// ContainsSourceRange reports whether every requested line was supplied.
func ContainsSourceRange(spans []SourceSpan, start, end int) bool {
	if start < 1 || end < start {
		return false
	}
	next := start
	for _, span := range spans {
		if span.End < next {
			continue
		}
		if span.Start > next {
			return false
		}
		if span.End >= end {
			return true
		}
		next = span.End + 1
	}
	return false
}
