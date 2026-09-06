---
name: identify-model
description: Ask which of six models' styles a Go, Python or TypeScript file is most similar to, using the nitpick MCP server's identify_model tool. Use when asked which model, which AI, or which tool wrote a file. Always a style match against a small corpus, never an attribution, and the answer must say so.
---

# Which model's style

Call `identify_model` from the nitpick server with `files` naming the
files (relative to the repository root, or absolute). It costs nothing:
no model is called. The answer per file is the nearest of six corpus
authors (claude-sonnet-4.6, gpt-5.6-luna, gemma-4-31b, kimi-k3,
qwen3.8-27b, glm-5.3-flash, plus a human control from the Go and Python
standard libraries), with the margin over the runner-up, or `unknown`
with the reason.

## What the answer means

The instrument is a fingerprint (character 3-grams and token bigrams)
trained on twelve small programs each model wrote twice. On that corpus it
is right about three files in four across a second generation, and at
the margin floor of 0.05 a given answer is right about nine times in ten.
On real repositories, where a person and a tool share files and a
formatter runs over both, the same instrument is near chance in most
projects (docs/findings.md, "Contributors"). So:

- Report it as "most similar to X among six models", with the margin.
- Never report it as "written by X", and never as evidence that a file
  is or is not model-written; the human control is standard-library
  code, and a person's file is reported as whichever of the six it is
  nearest to.
- A model outside the six (any Claude after sonnet-4.6, any GPT after
  5.6, Gemini, Mistral, DeepSeek, and every coding agent's own model) is
  reported as the nearest of the six. Say this when the reader may not
  know it.
- `unknown` is the ordinary answer for a file the instrument cannot
  place, and the reason says whether the language is outside the corpus
  or the margin was below the floor.

For whether a file reads as generated and left unread, which is a
different question with checkable rules, use the `ai-slop` skill.
