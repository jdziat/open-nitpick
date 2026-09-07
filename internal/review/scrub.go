package review

import (
	"regexp"
	"strings"
)

// Scrubbing is the enforcement half of the voice layer in the prompt: a
// model told not to write em dashes still writes them, and a finding that
// reaches a pull request carrying the habits this tool reports in other
// people's code is the worst kind of finding. Only mechanical edits are
// made here, the ones a reader would make without thinking: punctuation a
// comma serves, words that carry nothing, and the chat wrapper around an
// answer. Anything needing judgement is left for the model and the prompt.

var (
	// An em dash, or an en dash between spaces, standing in for a comma.
	dashSeparator = regexp.MustCompile(`\s*[—]\s*|\s+–\s+`)
	// Filler that says nothing, with its leading space.
	fillerWord = regexp.MustCompile(`(?i)\s*\b(genuinely|honestly|actually|truly|simply|crucially|importantly|very|quite|somewhat)\b`)
	fillerLead = regexp.MustCompile(`(?i)^\s*(it'?s worth noting( that)?|note that|it should be noted( that)?)[,:]?\s*`)
	// The chat wrapper: an opener at the start, an offer at the end.
	chatOpen  = regexp.MustCompile(`(?i)^\s*(sure[!,.]?|certainly[!,.]?|great question[!.]?|of course[!,.]?|here'?s (the|a|an|your)\s+\w+[:.]?)\s*`)
	chatClose = regexp.MustCompile(`(?i)\s*(let me know if [^.!?]*[.!?]|hope (this|that) helps[.!]?|happy to help[.!]?|feel free to [^.!?]*[.!?])\s*$`)
	// Arrows standing in for a word, outside code spans.
	proseArrow = regexp.MustCompile(`\s+(→|->)\s+`)
	spaceRun   = regexp.MustCompile(`[ \t]{2,}`)
	spaceStop  = regexp.MustCompile(`\s+([,.;:!?])`)
)

// Scrub removes the mechanical tells from one piece of model-authored
// prose, leaving code spans and fenced blocks untouched. It reports
// whether anything changed, so a caller can count what the prompt did not
// prevent.
func Scrub(s string) (string, bool) {
	out := mapOutsideCode(s, func(text string) string {
		text = fillerLead.ReplaceAllString(text, "")
		// Openers stack, so strip until a pass removes nothing.
		for range 3 {
			stripped := chatOpen.ReplaceAllString(text, "")
			if stripped == text {
				break
			}
			text = stripped
		}
		text = chatClose.ReplaceAllString(text, "")
		text = dashSeparator.ReplaceAllString(text, ", ")
		text = proseArrow.ReplaceAllString(text, " to ")
		text = fillerWord.ReplaceAllString(text, "")
		text = spaceRun.ReplaceAllString(text, " ")
		text = spaceStop.ReplaceAllString(text, "$1")
		return text
	})
	out = strings.TrimSpace(out)
	// A sentence that began with a stripped word starts lowercase; the
	// first letter is raised back, and nothing else is touched.
	if out != "" && s != "" {
		r := []rune(out)
		if u := strings.ToUpper(string(r[0])); u != string(r[0]) && startsUpper(s) {
			out = u + string(r[1:])
		}
	}
	return out, out != strings.TrimSpace(s)
}

func startsUpper(s string) bool {
	t := strings.TrimSpace(s)
	if t == "" {
		return false
	}
	r := []rune(t)[0]
	return r >= 'A' && r <= 'Z'
}

// mapOutsideCode applies f to the parts of s that are prose, leaving
// fenced blocks and inline code spans exactly as the model wrote them: a
// dash inside code is code, and rewriting it changes a suggestion.
func mapOutsideCode(s string, f func(string) string) string {
	var b strings.Builder
	rest := s
	for {
		start := strings.Index(rest, "```")
		if start < 0 {
			break
		}
		end := strings.Index(rest[start+3:], "```")
		if end < 0 {
			break
		}
		end += start + 6
		b.WriteString(mapOutsideSpans(rest[:start], f))
		b.WriteString(rest[start:end])
		rest = rest[end:]
	}
	b.WriteString(mapOutsideSpans(rest, f))
	return b.String()
}

func mapOutsideSpans(s string, f func(string) string) string {
	parts := strings.Split(s, "`")
	for i := range parts {
		if i%2 == 0 {
			parts[i] = f(parts[i])
		}
	}
	return strings.Join(parts, "`")
}

// scrubOverruled scrubs the findings a reader still sees under "reported,
// then withheld", and the reason given for withholding them.
func scrubOverruled(overruled []Overruled) int {
	n := 0
	for i := range overruled {
		if t, changed := Scrub(overruled[i].Finding.Title); changed {
			overruled[i].Finding.Title, n = t, n+1
		}
		if r, changed := Scrub(overruled[i].Finding.Rationale); changed {
			overruled[i].Finding.Rationale, n = r, n+1
		}
		if r, changed := Scrub(overruled[i].Reason); changed {
			overruled[i].Reason, n = r, n+1
		}
	}
	return n
}

// scrubFindings scrubs every finding's title and rationale and returns how
// many pieces of text changed, for the log.
func scrubFindings(findings []Finding) int {
	n := 0
	for i := range findings {
		if t, changed := Scrub(findings[i].Title); changed {
			findings[i].Title, n = t, n+1
		}
		if r, changed := Scrub(findings[i].Rationale); changed {
			findings[i].Rationale, n = r, n+1
		}
	}
	return n
}
