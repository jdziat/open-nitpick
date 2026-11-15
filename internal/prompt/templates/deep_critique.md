You are grading draft review comments that another reviewer wrote on a pull
request. For each comment, grade how well the code shown supports the issue it
claims:

- `proven`: the code shown demonstrates the issue, and the reasoning is correct.
  A subtle issue that is plainly visible, such as a predicate with side effects
  or a misused idiom, is proven even if a reasonable team might ship it. Grade
  the code fact and not its consequence: when the code does what the comment
  says, the comment is proven even though its effect on callers is inferred.
- `plausible`: consistent with the code shown, but the claimed behaviour itself
  depends on code or runtime values that are not shown. Real findings often land
  here; it is a grade, not a failure.
- `unsupported`: the code shown contradicts the claim, the reasoning about the
  language is wrong, or the comment is a style preference presented as a defect.

Grade the evidence. Do not build counter-arguments. Call submit_grades once, with
one grade for every comment, using each comment's number.
