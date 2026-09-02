package review

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode"

	"github.com/jdziat/open-nitpick/internal/vcs"
)

// Fingerprint identifies a finding across runs of this tool on the same pull
// request, independently of the line it lands on.
//
// Lines move: a push that inserts a function above the defect shifts every
// finding below it, and a fingerprint that included the line would have every
// such finding re-posted as new. Wording moves too, but less, and a model at
// temperature zero handed the same file mostly titles the same defect the same
// way — so the fingerprint is the path, the class and the normalized title.
// The class is in it because two findings of different kinds can share a
// title's words ("unchecked error" as a correctness bug and as a style nit)
// and should not be taken for the same one.
func Fingerprint(f Finding) string {
	h := sha256.New()
	h.Write([]byte(f.Path))
	h.Write([]byte{0})
	h.Write([]byte(f.Class))
	h.Write([]byte{0})
	h.Write([]byte(fingerprintTitle(f.Title)))
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// fingerprintTitle folds a title to its letters and digits, lower-cased, so
// punctuation and spacing differences do not make two identical findings look
// new.
func fingerprintTitle(s string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
			continue
		}
		space = true
	}
	return b.String()
}

// priorLineTolerance is how far a previously posted comment may sit from a new
// finding of the same class on the same file and still be taken for the same
// finding. Two lines covers the ordinary case — a push that edits the lines
// beside the defect — without swallowing a distinct defect a screen away.
const priorLineTolerance = 2

// alreadyReported reports whether an earlier run posted this finding.
//
// Two ways to match, in order of confidence: the fingerprint, which survives a
// line shift; or the same class at the same place, which survives a rewording.
// A finding matched either way is withheld, and counted so the summary can say
// how many were.
func alreadyReported(f Finding, prior *vcs.PriorReview) bool {
	if prior == nil || len(prior.Comments) == 0 {
		return false
	}
	fp := Fingerprint(f)
	for _, c := range prior.Comments {
		if c.Fingerprint == fp {
			return true
		}
		if c.Path == f.Path && c.Class == f.Class && c.Line > 0 &&
			abs(c.Line-f.Line) <= priorLineTolerance {
			return true
		}
	}
	return false
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
