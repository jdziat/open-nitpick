# Model-identification experiment, 2026-09-05

Output of `TestModelIdentificationExperiment` on the committed corpus.

```
split even tasks train:
go: 42 train, 36 test, 7 authors; accuracy 0.28 against a majority baseline of 0.17 (chance 0.14)
  actual \ predicted           claude-s gemma-4-    human  kimi-k3 gpt-5-6- qwen3-8- glm-5-3-
  claude-s                            2        0        2        0        1        0        1
  gemma-4-                            2        1        0        0        2        0        1
  human                               0        0        4        2        0        0        0
  kimi-k3                             2        0        1        1        0        0        1
  gpt-5-6-                            2        2        1        0        1        0        0
  qwen3-8-                            1        1        1        0        0        0        0
  glm-5-3-                            2        0        1        0        0        0        1

python: 41 train, 38 test, 7 authors; accuracy 0.24 against a majority baseline of 0.16 (chance 0.14)
  actual \ predicted           claude-s gemma-4-    human  kimi-k3 gpt-5-6- qwen3-8- glm-5-3-
  claude-s                            3        0        1        2        0        0        0
  gemma-4-                            2        0        2        0        1        0        1
  human                               0        1        3        1        0        0        1
  kimi-k3                             1        0        3        1        0        0        1
  gpt-5-6-                            0        0        2        2        1        0        1
  qwen3-8-                            1        0        1        1        0        0        1
  glm-5-3-                            0        0        1        2        0        0        1

typescript: 34 train, 32 test, 6 authors; accuracy 0.47 against a majority baseline of 0.19 (chance 0.17)
  actual \ predicted           claude-s gemma-4-  kimi-k3 gpt-5-6- qwen3-8- glm-5-3-
  claude-s                            3        0        0        1        2        0
  gemma-4-                            2        2        0        2        0        0
  kimi-k3                             1        0        0        1        0        4
  gpt-5-6-                            0        0        0        5        1        0
  qwen3-8-                            0        0        0        3        1        0
  glm-5-3-                            0        0        0        0        0        4
split first half train:
go: 39 train, 39 test, 7 authors; accuracy 0.31 against a majority baseline of 0.15 (chance 0.14)
  actual \ predicted           claude-s gemma-4-    human  kimi-k3 gpt-5-6- qwen3-8- glm-5-3-
  claude-s                            0        1        0        2        0        0        3
  gemma-4-                            0        5        0        0        1        0        0
  human                               0        0        3        2        0        0        1
  kimi-k3                             0        2        0        2        0        0        2
  gpt-5-6-                            1        5        0        0        0        0        0
  qwen3-8-                            0        3        0        0        1        0        0
  glm-5-3-                            0        2        0        1        0        0        2

python: 40 train, 39 test, 7 authors; accuracy 0.28 against a majority baseline of 0.15 (chance 0.14)
  actual \ predicted           claude-s gemma-4-    human  kimi-k3 gpt-5-6- qwen3-8- glm-5-3-
  claude-s                            2        1        0        0        1        2        0
  gemma-4-                            1        2        1        0        2        0        0
  human                               0        0        1        0        2        2        1
  kimi-k3                             0        0        0        1        2        1        2
  gpt-5-6-                            0        0        0        1        3        1        1
  qwen3-8-                            1        0        0        1        2        0        0
  glm-5-3-                            0        0        0        1        2        0        2

typescript: 32 train, 34 test, 6 authors; accuracy 0.38 against a majority baseline of 0.18 (chance 0.17)
  actual \ predicted           claude-s gemma-4-  kimi-k3 gpt-5-6- qwen3-8- glm-5-3-
  claude-s                            4        0        2        0        0        0
  gemma-4-                            1        3        1        1        0        0
  kimi-k3                             2        0        1        1        0        2
  gpt-5-6-                            2        0        0        4        0        0
  qwen3-8-                            0        0        0        5        0        0
  glm-5-3-                            1        0        3        0        0        1
```
