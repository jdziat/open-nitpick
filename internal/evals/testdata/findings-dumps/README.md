# Rows behind the callers section of docs/findings.md

The `z-ai/glm-5.3-flash` rows of the four run dumps that section cites,
trimmed to the fields `TestCallersSectionMatchesItsDumps` reads. They are
committed evidence in the sense `.gitignore` gives the phrase: the numbers
and the named fixtures in that section are re-derived from these files on
every test run, so the prose cannot drift from them unnoticed. The full
dumps, with every model and every field, stay under `.eval-runs/`.
