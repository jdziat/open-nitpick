package prompt

// SlopGuidance is the prompt layer for the slop class: generated-looking
// code that costs a reader, defined as things a reader can check on the
// line, never as a feeling about the author. Each rule carries the shape
// that is a finding and the lookalike that is not, because the class is
// worthless if it fires on ordinary code.
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
5. **An error swallowed and carried on from.** A handler is slop only when
   ALL three hold: the error is caught (` + "`except`, `catch`, `recover`, `_ = err`" + `),
   nothing is done with it (no log, no re-raise, no return, no wrap), and
   execution continues as if it had not happened. File at ` + "`warning`" + `. A
   handler that does ANY of these is not slop, whatever else it does: writes
   to a log, re-raises or returns the error, wraps it, stops the loop or the
   batch, or carries a comment saying why the error is ignored. Before
   filing, name which of the three conditions hold; if one does not, do not
   file.
6. **Generic naming where the file's own vocabulary has a specific word.**
   ` + "`data`, `result`, `helper`, `utils2`, `ProcessData`" + ` in a file whose other
   names say ` + "`order`, `invoice`, `tenant`" + `. Not a finding: a generic name in
   generic code (a container, a codec).
7. **Boilerplate repeated three or more times where the language has the
   abstraction.** The same five lines for three fields where a loop, a helper
   or a generic would do. Not a finding: two occurrences, or three that
   differ in a way the abstraction would hide.
8. **Prose that addresses the reader as a chat reply.** A comment is slop
   only when it contains a phrase that belongs to a conversation and not to
   the code: "Sure!", "Here's", "I hope this helps", "Let me know", "Note
   that this function will", "As you can see", "I've added", or an
   apology or a greeting. The person a comment is written in is NOT the
   test: a doc comment in the second person ("You get X, not Y") that
   names a return value, a constraint, a caller or an example is
   documentation, and is never slop under this rule. Before filing, quote
   the conversational phrase; if there is none, do not file.
9. **A test that asserts nothing, or only that the code ran.** A test whose
   body calls the function and checks no result; ` + "`assert True`" + `; a test that
   asserts on its own fixture. File at ` + "`warning`" + `. Not a finding: a test
   whose assertion is the absence of a panic, when it says so.
10. **A heading or card title that sells instead of naming.** "Linters as
    evidence, not noise", "A trust model, written down", "Measured, mistakes
    included". The tell is a slogan where a reader expects the name of what
    follows. Not a finding: a heading that names what the section holds
    ("Trust model", "Published measurements"), or a contrast whose negated half
    is a limit the reader would otherwise assume away ("Supports YAML, not
    TOML").
11. **Marketing words where a fact, a number or a command would do.** A word
    generated text reaches for (seamless, robust, comprehensive, powerful,
    leverage, streamline, empower), the shape "not just X but Y", or a
    sentence opened with "Overall", "In summary" or "Ultimately". Quote the
    word or the opener before filing. Not a finding: the same word inside a
    quoted name or an identifier, a claim with its number or command beside
    it, or a technical sense of the word ("a robust standard error").
12. **A blurb that retells the page it links to.** A README bullet, card or
    docs summary of more than two sentences that restates the linked page's
    counts, defaults and caveats instead of saying what the reader will find
    there and why to go. Name the page it duplicates before filing. Not a
    finding: the page itself, or a summary that adds a fact the page lacks.

The controls for this class are ordinary, human-written files. A slop
finding on one of those is the failure this class is measured against, so
when a line could be read either way, it is not slop.
`
}
