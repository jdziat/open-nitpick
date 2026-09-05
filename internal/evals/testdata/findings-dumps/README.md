# Rows behind the callers section of docs/findings.md

The `z-ai/glm-5.3-flash` rows of the eight run dumps that section cites,
trimmed to the fields `TestCallersSectionMatchesItsDumps` reads. They are
committed evidence in the sense `.gitignore` gives the phrase: the tables,
the noise range, the lost and flipped fixtures, the floor, and the
vocabulary claim in that section are re-derived from these files on every
test run. Sentences the test does not derive (the Incumbent column, the
rule the floor forced, the first cut's attribution, the TypeScript anchor
miss) are checked by hand
and say so where they appear. The full dumps, with every model and every
field, stay under `.eval-runs/`, and each trim records its source's
SHA-256 so a retrim is checkable by hand.

| trim | source dump | source sha256 |
|---|---|---|
| `multifile-callers-20260905T154530Z.jsonl` | `multifile-callers-20260905T154530Z-2912705.jsonl` | `f73c89d20eaf0397673286b2ed6ce32e925595f166544ede38d64da208ac617d` |
| `multifile-callers-20260905T155517Z.jsonl` | `multifile-callers-20260905T155517Z-3145670.jsonl` | `afcbbe4cbe778b04da3f9b67c955ce1744f2e43de95d78d34c6740f7abbb1a24` |
| `multifile-callers-20260905T160208Z.jsonl` | `multifile-callers-20260905T160208Z-3220409.jsonl` | `8b731c86a8cfa8d490c0264b5c735c00ac2182e80edbd786b83a91ddc61036ea` |
| `multifile-callers-20260905T163816Z.jsonl` | `multifile-callers-20260905T163816Z-3922355.jsonl` | `36b598fc3bf07309e3500ac7859c2909192d672498568299ca38dcf384008703` |
| `multifile-callers-20260905T164614Z.jsonl` | `multifile-callers-20260905T164614Z-4027568.jsonl` | `1eaa315c9d546b26ccbd04d0b489f074b45182b2a40fb38fa33713c58a92058b` |
| `multifile-callers-20260905T170221Z.jsonl` | `multifile-callers-20260905T170221Z-48876.jsonl` | `baac3ed3b73127f6b156b914e8ce1f19e0c3e6696afa2d2c59cab2ea37695aaa` |
| `multifile-multifile-20260905T170442Z.jsonl` | `multifile-multifile-20260905T170442Z-71820.jsonl` | `f84d4a9319500ea2a127f1ee3aea49f71fd74e4ec664c50a60f0cf20cae48f76` |
| `multifile-multifile-20260905T170918Z.jsonl` | `multifile-multifile-20260905T170918Z-146735.jsonl` | `09b0c30676236c2350bee7701fa5072fde2edb188251644905229eafdd3a883b` |
