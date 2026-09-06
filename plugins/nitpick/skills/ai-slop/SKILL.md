---
name: ai-slop
description: Find code that reads as generated and left unread, under nine rules a reader can check, using the nitpick MCP server's ai_slop tool. Use when asked whether code is AI slop, model-written, generated, or vibe-coded, to clean up code a model wrote, or to check your own generated code before committing it. Each finding names the rule it broke.
---

# AI slop

Call `ai_slop` from the nitpick server with `paths` naming what to check,
relative to the repository root (`repo` as an absolute path when the
session is not at the root). It runs two instruments. The tells need no
model and come back first: em dashes, en dashes as separators, arrows in
prose, filler qualifiers, chat prose, comments that restate their line,
doc comments longer than what they document, three adjectives of praise
in a row; each with its fix, counted per thousand lines. Then the model's
nine rules below, through the review engine, filtered to the slop class.
Pass `no_model: true` for the tells alone, which is free and instant; the
model's pass costs a full review of the paths, so name the paths rather
than the tree. To check work you produced in this session, pass the files
you wrote, with `no_model` first.

## The rules

Slop is defined as things a reader can check, not as a feeling. Each
finding names one of these:

1. A comment that restates the line below it.
2. A docstring or comment that describes behaviour the code does not have.
3. Dead code left beside its replacement.
4. A defensive check against a condition the types exclude.
5. A try/except or recover that swallows the error and continues.
6. Generic naming (`data`, `result`, `helper`, `utils2`) where the file's
   own vocabulary has a specific word.
7. A tautological condition or a guard that guards nothing.
8. Chat prose in a comment ("Sure! Here's", "Note that this function
   will").
9. A test that asserts nothing, or asserts what it set up.

The controls matter: a doc comment that adds a reason, a guard the
constructor does not already make, a comment that says why rather than
what, are not slop and are not reported. If a finding is one of these,
it is wrong, and saying so is part of the job.

## Acting on it

1. Fix in place. Slop fixes are deletions and renames, rarely rewrites:
   delete the restating comment, the dead branch, the guard that cannot
   fire; rename `data` to what it is; make the swallowed error return.
2. Rule 2 (a comment describing behaviour the code does not have) is the
   one that hides a bug. Decide which is wrong, the comment or the code,
   before touching either.
3. Rule 5 (a swallowed error) is a correctness fix wearing a slop label.
   Do not "clean it up" by deleting the try; make the error visible.
4. When the answer is clean for the paths given, say which paths were
   covered; `covered` and `skipped` are in the answer.
5. `hidden` lists the findings the review made outside the slop class
   (bugs, security). They were paid for; relay them rather than drop them.
6. `recommendations` is the fix list ordered by count. Work it from the
   top: the first item removes the most.

## What this is not

`identify_model` answers a different question (which of six models' styles
a file is nearest to) and is not evidence of slop; a file can be
model-written and clean, or human-written and slop. Use this skill for the
second question and `identify-model` for the first.
