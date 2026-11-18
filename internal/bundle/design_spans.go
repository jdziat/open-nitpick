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
	regions, err := sourceSpanRegions(content, spans)
	if err != nil {
		return "", err
	}
	size := 0
	for _, region := range regions {
		size += region.end - region.start
	}
	var out strings.Builder
	out.Grow(size)
	for _, region := range regions {
		out.WriteString(content[region.start:region.end])
	}
	return out.String(), nil
}

type sourceRegion struct {
	start, end int
}

func sourceSpanRegions(content string, spans []SourceSpan) ([]sourceRegion, error) {
	if len(spans) == 0 {
		return nil, nil
	}
	lines := strings.Count(content, "\n")
	if len(content) > 0 && content[len(content)-1] != '\n' {
		lines++
	}
	prior := 0
	for _, span := range spans {
		if span.Start <= prior || span.End < span.Start || span.End > lines {
			return nil, fmt.Errorf("invalid source span %d:%d for %d lines", span.Start, span.End, lines)
		}
		prior = span.End
	}
	regions := make([]sourceRegion, len(spans))
	offset, line := 0, 1
	for i, span := range spans {
		for line < span.Start {
			offset += strings.IndexByte(content[offset:], '\n') + 1
			line++
		}
		start := offset
		for line <= span.End {
			next := strings.IndexByte(content[offset:], '\n')
			if next < 0 {
				offset = len(content)
			} else {
				offset += next + 1
			}
			line++
		}
		regions[i] = sourceRegion{start: start, end: offset}
	}
	return regions, nil
}

func renderSourceSpans(entry Entry) string {
	var out strings.Builder
	fmt.Fprintf(&out, "### Selected supporting source: %s\nOnly the declared line ranges are shown. Findings here belong in the summary.\n", promptSafe(entry.File.Path))
	for _, instruction := range entry.Instructions {
		fmt.Fprintf(&out, "Path instruction: %s\n", promptSafe(instruction))
	}
	regions, err := sourceSpanRegions(entry.Content, entry.SourceSpans)
	if err != nil {
		return out.String() + "Source ranges are unavailable.\n"
	}
	for i, span := range entry.SourceSpans {
		fmt.Fprintf(&out, "\nLines %d-%d:\n```\n", span.Start, span.End)
		region := regions[i]
		for number, offset := span.Start, region.start; offset < region.end; number++ {
			next := strings.IndexByte(entry.Content[offset:region.end], '\n')
			end := region.end
			if next >= 0 {
				end = offset + next + 1
			}
			fmt.Fprintf(&out, "%6d  %s", number, entry.Content[offset:end])
			if next < 0 {
				out.WriteByte('\n')
			}
			offset = end
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
