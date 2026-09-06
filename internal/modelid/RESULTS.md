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

## Across generations

Output of `TestModelIdentificationAcrossGenerations`: trained on `corpus`, tested on `corpus2`, a second generation of the same tasks by the same models (197 files; qwen3.8-27b returned empty output for nineteen of its slots twice and has 13; the human control is in the training corpus only).

```
train on corpus, test on corpus2:
go: 78 train, 66 test, 7 authors; accuracy 0.33 against a majority baseline of 0.18 (chance 0.14)
  actual \ predicted           claude-s gemma-4-    human  kimi-k3 gpt-5-6- qwen3-8- glm-5-3-
  claude-s                            5        2        1        2        0        0        2
  gemma-4-                            2        7        0        1        2        0        0
  human                               0        0        0        0        0        0        0
  kimi-k3                             2        1        2        4        0        0        3
  gpt-5-6-                            0        7        0        1        3        1        0
  qwen3-8-                            0        4        1        0        0        0        2
  glm-5-3-                            4        1        1        2        0        0        3

python: 79 train, 66 test, 7 authors; accuracy 0.32 against a majority baseline of 0.18 (chance 0.14)
  actual \ predicted           claude-s gemma-4-    human  kimi-k3 gpt-5-6- qwen3-8- glm-5-3-
  claude-s                            2        1        0        3        1        1        4
  gemma-4-                            2        4        1        0        2        3        0
  human                               0        0        0        0        0        0        0
  kimi-k3                             1        0        0        5        1        0        5
  gpt-5-6-                            1        0        0        1        7        2        1
  qwen3-8-                            2        0        1        1        4        1        0
  glm-5-3-                            0        0        0        6        1        0        2

typescript: 66 train, 65 test, 6 authors; accuracy 0.48 against a majority baseline of 0.18 (chance 0.17)
  actual \ predicted           claude-s gemma-4-  kimi-k3 gpt-5-6- qwen3-8- glm-5-3-
  claude-s                            6        0        1        5        0        0
  gemma-4-                            3        5        1        3        0        0
  kimi-k3                             0        2        3        1        0        6
  gpt-5-6-                            0        0        2       10        0        0
  qwen3-8-                            0        0        1        6        0        0
  glm-5-3-                            2        0        1        0        0        7
go: margin over majority 0.15 (go)
python: margin over majority 0.14 (no-go)
typescript: margin over majority 0.29 (go)
```
