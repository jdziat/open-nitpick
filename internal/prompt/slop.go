package prompt

// SlopGuidance is the prompt layer for the slop class: generated-looking
// code that costs a reader, defined as things a reader can check on the
// line, never as a feeling about the author. Each rule carries the shape
// that is a finding and the lookalike that is not, because the class is
// worthless if it fires on ordinary code. See docs/plan-full-review.md.
func SlopGuidance() string {
	return `## The slop class

This review ALSO reports findings of class ` + "`slop`" + `: code that reads as generated
and left unread, where the cost to the next reader can be named. Each rule
below says what counts and what does not. Report a slop finding only when
the line matches a rule as written; file it at ` + "`nit`" + ` unless a rule says
otherwise, and say which rule in the rationale.

1. **A comment that restates the line below it.** ` + "`// increment i`" + ` above
   ` + "`i++`" + `, ` + "`# return the result`" + ` above ` + "`return result`" + `. Not a finding: a comment
   that says why, names a constraint, or documents a contract a caller needs.
2. **A comment or docstring that describes behaviour the code does not
   have.** A docstring promising a retry the body does not do; a comment
   naming a parameter the function no longer takes. File at ` + "`warning`" + `: a
   reader who trusts it is misled. Not a finding: an accurate comment that is
   merely long.
3. **Dead code left beside its replacement.** An old implementation kept
   under a new name and never called; a commented-out block with its live
   version beneath it. Not a finding: an unused import or variable a linter
   already reports, which is evidence for this class, not a second finding.
4. **A check against a condition the types exclude.** ` + "`if x != nil`" + ` on a
   value type, ` + "`if d != 0 || d == 0`" + `, a length check on a value that
   cannot be negative. Not a finding: a check that guards a real input.
5. **An error swallowed and carried on from.** ` + "`except Exception: pass`" + `, a
   ` + "`recover()`" + ` that prints and continues, an error assigned to ` + "`_`" + ` on a call
   whose failure matters. File at ` + "`warning`" + `. Not a finding: an error
   deliberately ignored with a comment saying why, or recorded and re-raised.
6. **Generic naming where the file's own vocabulary has a specific word.**
   ` + "`data`, `result`, `helper`, `utils2`, `ProcessData`" + ` in a file whose other
   names say ` + "`order`, `invoice`, `tenant`" + `. Not a finding: a generic name in
   generic code (a container, a codec).
7. **Boilerplate repeated three or more times where the language has the
   abstraction.** The same five lines for three fields where a loop, a helper
   or a generic would do. Not a finding: two occurrences, or three that
   differ in a way the abstraction would hide.
8. **Prose that addresses the reader as a chat reply.** "Sure! Here's",
   "Note that", "This function will", "I hope this helps", in a comment or
   docstring. Not a finding: ordinary documentation in the second person
   where the codebase writes that way.
9. **A test that asserts nothing, or only that the code ran.** A test whose
   body calls the function and checks no result; ` + "`assert True`" + `; a test that
   asserts on its own fixture. File at ` + "`warning`" + `. Not a finding: a test
   whose assertion is the absence of a panic, when it says so.

The controls for this class are ordinary, human-written files. A slop
finding on one of those is the failure this class is measured against, so
when a line could be read either way, it is not slop.
`
}
