# Model-identification experiment, 2026-09-05, with fingerprints

Output of `TestModelIdentificationExperiment` and `TestModelIdentificationAcrossGenerations` on the committed corpora, three methods each: the dense features (the original instrument), the fingerprint (character 3-grams and token bigrams, cosine to per-author centroids), and both combined. License headers are stripped before measuring. The idioms are the token bigrams most characteristic of each author, with the count of their files each appears in.

```
method features, split even tasks train:
go, features: 42 train, 36 test, 7 authors; accuracy 0.28 against a majority baseline of 0.17 (chance 0.14)
  actual \ predicted           claude-s gemma-4-    human  kimi-k3 gpt-5-6- qwen3-8- glm-5-3-
  claude-s                            3        0        1        0        1        0        1
  gemma-4-                            2        1        0        0        2        0        1
  human                               0        0        4        2        0        0        0
  kimi-k3                             2        0        1        1        0        0        1
  gpt-5-6-                            2        2        1        0        1        0        0
  qwen3-8-                            1        1        1        0        0        0        0
  glm-5-3-                            3        0        1        0        0        0        0

python, features: 41 train, 38 test, 7 authors; accuracy 0.24 against a majority baseline of 0.16 (chance 0.14)
  actual \ predicted           claude-s gemma-4-    human  kimi-k3 gpt-5-6- qwen3-8- glm-5-3-
  claude-s                            3        0        1        2        0        0        0
  gemma-4-                            2        0        2        0        1        0        1
  human                               0        1        3        1        0        0        1
  kimi-k3                             1        0        3        1        0        0        1
  gpt-5-6-                            0        0        2        2        1        0        1
  qwen3-8-                            1        0        1        1        0        0        1
  glm-5-3-                            0        0        1        2        0        0        1

typescript, features: 34 train, 32 test, 6 authors; accuracy 0.47 against a majority baseline of 0.19 (chance 0.17)
  actual \ predicted           claude-s gemma-4-  kimi-k3 gpt-5-6- qwen3-8- glm-5-3-
  claude-s                            3        0        0        1        2        0
  gemma-4-                            2        2        0        2        0        0
  kimi-k3                             1        0        0        1        0        4
  gpt-5-6-                            0        0        0        5        1        0
  qwen3-8-                            0        0        0        3        1        0
  glm-5-3-                            0        0        0        0        0        4
go, features: margin over majority 0.11 (no-go)
python, features: margin over majority 0.08 (no-go)
typescript, features: margin over majority 0.28 (go)
method features, split first half train:
go, features: 39 train, 39 test, 7 authors; accuracy 0.28 against a majority baseline of 0.15 (chance 0.14)
  actual \ predicted           claude-s gemma-4-    human  kimi-k3 gpt-5-6- qwen3-8- glm-5-3-
  claude-s                            0        1        0        2        0        0        3
  gemma-4-                            0        5        0        0        1        0        0
  human                               0        0        3        2        0        0        1
  kimi-k3                             0        2        0        1        0        0        3
  gpt-5-6-                            1        5        0        0        0        0        0
  qwen3-8-                            0        3        0        0        1        0        0
  glm-5-3-                            0        2        0        1        0        0        2

python, features: 40 train, 39 test, 7 authors; accuracy 0.28 against a majority baseline of 0.15 (chance 0.14)
  actual \ predicted           claude-s gemma-4-    human  kimi-k3 gpt-5-6- qwen3-8- glm-5-3-
  claude-s                            2        1        0        0        1        2        0
  gemma-4-                            1        2        1        0        2        0        0
  human                               0        0        1        0        2        2        1
  kimi-k3                             0        0        0        1        2        1        2
  gpt-5-6-                            0        0        0        1        3        1        1
  qwen3-8-                            1        0        0        1        2        0        0
  glm-5-3-                            0        0        0        1        2        0        2

typescript, features: 32 train, 34 test, 6 authors; accuracy 0.38 against a majority baseline of 0.18 (chance 0.17)
  actual \ predicted           claude-s gemma-4-  kimi-k3 gpt-5-6- qwen3-8- glm-5-3-
  claude-s                            4        0        2        0        0        0
  gemma-4-                            1        3        1        1        0        0
  kimi-k3                             2        0        1        1        0        2
  gpt-5-6-                            2        0        0        4        0        0
  qwen3-8-                            0        0        0        5        0        0
  glm-5-3-                            1        0        3        0        0        1
go, features: margin over majority 0.13 (no-go)
python, features: margin over majority 0.13 (no-go)
typescript, features: margin over majority 0.21 (go)
method fingerprint, split even tasks train:
go, fingerprint: 42 train, 36 test, 7 authors; accuracy 0.36 against a majority baseline of 0.17 (chance 0.14)
  actual \ predicted           claude-s gemma-4-    human  kimi-k3 gpt-5-6- qwen3-8- glm-5-3-
  claude-s                            2        0        4        0        0        0        0
  gemma-4-                            1        2        2        0        0        1        0
  human                               0        0        6        0        0        0        0
  kimi-k3                             0        0        4        1        0        0        0
  gpt-5-6-                            0        1        2        0        1        1        1
  qwen3-8-                            0        0        1        0        1        0        1
  glm-5-3-                            1        0        1        1        0        0        1

python, fingerprint: 41 train, 38 test, 7 authors; accuracy 0.58 against a majority baseline of 0.16 (chance 0.14)
  actual \ predicted           claude-s gemma-4-    human  kimi-k3 gpt-5-6- qwen3-8- glm-5-3-
  claude-s                            6        0        0        0        0        0        0
  gemma-4-                            4        0        1        0        1        0        0
  human                               0        0        6        0        0        0        0
  kimi-k3                             0        0        2        2        0        0        2
  gpt-5-6-                            0        0        1        0        4        0        1
  qwen3-8-                            0        0        2        0        2        0        0
  glm-5-3-                            0        0        0        0        0        0        4

typescript, fingerprint: 34 train, 32 test, 6 authors; accuracy 0.47 against a majority baseline of 0.19 (chance 0.17)
  actual \ predicted           claude-s gemma-4-  kimi-k3 gpt-5-6- qwen3-8- glm-5-3-
  claude-s                            5        0        0        0        0        1
  gemma-4-                            2        2        0        2        0        0
  kimi-k3                             1        0        0        1        0        4
  gpt-5-6-                            1        0        0        5        0        0
  qwen3-8-                            1        0        0        2        0        1
  glm-5-3-                            0        0        0        1        0        3
go, fingerprint: margin over majority 0.19 (go)
python, fingerprint: margin over majority 0.42 (go)
typescript, fingerprint: margin over majority 0.28 (go)
method fingerprint, split first half train:
go, fingerprint: 39 train, 39 test, 7 authors; accuracy 0.54 against a majority baseline of 0.15 (chance 0.14)
  actual \ predicted           claude-s gemma-4-    human  kimi-k3 gpt-5-6- qwen3-8- glm-5-3-
  claude-s                            4        1        1        0        0        0        0
  gemma-4-                            1        4        0        0        1        0        0
  human                               0        0        5        0        1        0        0
  kimi-k3                             0        2        1        3        0        0        0
  gpt-5-6-                            0        0        0        0        5        0        1
  qwen3-8-                            0        2        0        0        2        0        0
  glm-5-3-                            1        2        0        2        0        0        0

python, fingerprint: 40 train, 39 test, 7 authors; accuracy 0.49 against a majority baseline of 0.15 (chance 0.14)
  actual \ predicted           claude-s gemma-4-    human  kimi-k3 gpt-5-6- qwen3-8- glm-5-3-
  claude-s                            6        0        0        0        0        0        0
  gemma-4-                            3        0        1        1        1        0        0
  human                               0        0        6        0        0        0        0
  kimi-k3                             0        0        2        2        0        0        2
  gpt-5-6-                            0        0        1        0        3        0        2
  qwen3-8-                            0        0        1        1        1        0        1
  glm-5-3-                            1        0        0        2        0        0        2

typescript, fingerprint: 32 train, 34 test, 6 authors; accuracy 0.44 against a majority baseline of 0.18 (chance 0.17)
  actual \ predicted           claude-s gemma-4-  kimi-k3 gpt-5-6- qwen3-8- glm-5-3-
  claude-s                            5        0        1        0        0        0
  gemma-4-                            2        3        0        1        0        0
  kimi-k3                             2        0        2        2        0        0
  gpt-5-6-                            0        1        0        5        0        0
  qwen3-8-                            0        3        0        2        0        0
  glm-5-3-                            0        0        4        1        0        0
go, fingerprint: margin over majority 0.38 (go)
python, fingerprint: margin over majority 0.33 (go)
typescript, fingerprint: margin over majority 0.26 (go)
method combined, split even tasks train:
go, combined: 42 train, 36 test, 7 authors; accuracy 0.36 against a majority baseline of 0.17 (chance 0.14)
  actual \ predicted           claude-s gemma-4-    human  kimi-k3 gpt-5-6- qwen3-8- glm-5-3-
  claude-s                            3        0        2        0        1        0        0
  gemma-4-                            3        2        0        0        1        0        0
  human                               0        0        6        0        0        0        0
  kimi-k3                             1        0        2        1        0        0        1
  gpt-5-6-                            2        1        1        0        1        1        0
  qwen3-8-                            1        1        1        0        0        0        0
  glm-5-3-                            3        0        1        0        0        0        0

python, combined: 41 train, 38 test, 7 authors; accuracy 0.45 against a majority baseline of 0.16 (chance 0.14)
  actual \ predicted           claude-s gemma-4-    human  kimi-k3 gpt-5-6- qwen3-8- glm-5-3-
  claude-s                            5        0        0        1        0        0        0
  gemma-4-                            4        0        1        0        1        0        0
  human                               0        0        6        0        0        0        0
  kimi-k3                             1        0        1        2        0        0        2
  gpt-5-6-                            1        0        1        2        1        0        1
  qwen3-8-                            0        0        0        2        1        0        1
  glm-5-3-                            0        0        1        0        0        0        3

typescript, combined: 34 train, 32 test, 6 authors; accuracy 0.53 against a majority baseline of 0.19 (chance 0.17)
  actual \ predicted           claude-s gemma-4-  kimi-k3 gpt-5-6- qwen3-8- glm-5-3-
  claude-s                            5        0        0        1        0        0
  gemma-4-                            2        3        0        1        0        0
  kimi-k3                             1        0        0        1        0        4
  gpt-5-6-                            0        0        0        6        0        0
  qwen3-8-                            1        0        0        2        0        1
  glm-5-3-                            0        0        1        0        0        3
go, combined: margin over majority 0.19 (go)
python, combined: margin over majority 0.29 (go)
typescript, combined: margin over majority 0.34 (go)
method combined, split first half train:
go, combined: 39 train, 39 test, 7 authors; accuracy 0.41 against a majority baseline of 0.15 (chance 0.14)
  actual \ predicted           claude-s gemma-4-    human  kimi-k3 gpt-5-6- qwen3-8- glm-5-3-
  claude-s                            0        1        1        1        0        0        3
  gemma-4-                            0        5        0        0        1        0        0
  human                               0        0        5        0        0        0        1
  kimi-k3                             0        2        1        1        0        0        2
  gpt-5-6-                            1        1        0        0        4        0        0
  qwen3-8-                            0        3        0        0        1        0        0
  glm-5-3-                            0        2        1        1        0        0        1

python, combined: 40 train, 39 test, 7 authors; accuracy 0.38 against a majority baseline of 0.15 (chance 0.14)
  actual \ predicted           claude-s gemma-4-    human  kimi-k3 gpt-5-6- qwen3-8- glm-5-3-
  claude-s                            5        0        0        1        0        0        0
  gemma-4-                            1        0        2        0        3        0        0
  human                               0        0        2        1        0        2        1
  kimi-k3                             0        0        0        2        2        0        2
  gpt-5-6-                            0        0        0        0        4        0        2
  qwen3-8-                            0        0        0        1        3        0        0
  glm-5-3-                            0        0        0        3        0        0        2

typescript, combined: 32 train, 34 test, 6 authors; accuracy 0.47 against a majority baseline of 0.18 (chance 0.17)
  actual \ predicted           claude-s gemma-4-  kimi-k3 gpt-5-6- qwen3-8- glm-5-3-
  claude-s                            5        0        1        0        0        0
  gemma-4-                            1        3        0        2        0        0
  kimi-k3                             2        0        1        2        0        1
  gpt-5-6-                            0        0        0        6        0        0
  qwen3-8-                            0        0        0        5        0        0
  glm-5-3-                            0        0        4        0        0        1
go, combined: margin over majority 0.26 (go)
python, combined: margin over majority 0.23 (go)
typescript, combined: margin over majority 0.29 (go)
idioms, go:
  claude-s                     "creates a"(6) "/ Example"(4) "a new"(5) "the given"(5) "and returns"(5) ". \""(4) "with the"(5) "d \\"(5)
  gemma-4-                    
  human                        "internal /"(10) "\" internal"(9) "/ We"(6) "\" unsafe"(5) ", but"(5) "/ we"(5) "unsafe \""(5) "unsafe ."(5)
  kimi-k3                      "if v"(4) "v ,"(4) ": n"(4) "reports whether"(4) "( err"(4) "v :"(4) "n )"(5) "b ."(4)
  gpt-5-6-                     "func New"(5) "= errors"(4) "string ("(4)
  qwen3-8-                    
  glm-5-3-                     "= n"(4) "b ."(4) "number of"(6) ", n"(4)
idioms, python:
  claude-s                     "\" ="(5) "Returns -"(4) "[ {"(4) "e }"(4) "{ e"(4) "} ]"(4) ": main"(7) "as e"(4)
  gemma-4-                     "r '"(4)
  human                        "bytes ."(6) ". '"(5) "encoding ."(5) "name ="(5) "' f"(4) "* '"(4) ": assert"(4) "= b"(4)
  kimi-k3                      ": a"(4) "to `"(4) "` `"(5) "! r"(4) "r }"(4) "\" return"(6) ". The"(7)
  gpt-5-6-                     "( result"(5) "not isinstance"(5) ") or"(4) ") result"(4) ", str"(4) "int )"(4) ": result"(5)
  qwen3-8-                    
  glm-5-3-                     "0 }"(4) ") `"(4) ". __name__"(4) "__future__ import"(4) "from __future__"(4) "import annotations"(4) "\" from"(4) "type ("(4)
idioms, typescript:
  claude-s                     "; console"(8) "{ console"(6) ": $"(7) "Example usage"(5) "0 \""(7) "\" \\"(5) "/ Example"(4) ". log"(8)
  gemma-4-                     ") return"(6)
  kimi-k3                      "ts *"(5) ", *"(4) "* A"(6) "- -"(6) "' s"(4) "( message"(4) "* const"(4) "true ,"(4)
  gpt-5-6-                     "export default"(6)
  qwen3-8-                    
  glm-5-3-                     "/ const"(5) "with the"(5) ", or"(4) ": *"(5) ". *"(7) "/ export"(5) "* /"(7) "* *"(7)
method features, train on corpus, test on corpus2:
go, features: 78 train, 66 test, 7 authors; accuracy 0.35 against a majority baseline of 0.18 (chance 0.14)
  actual \ predicted           claude-s gemma-4-    human  kimi-k3 gpt-5-6- qwen3-8- glm-5-3-
  claude-s                            6        2        0        2        0        0        2
  gemma-4-                            2        7        0        1        2        0        0
  human                               0        0        0        0        0        0        0
  kimi-k3                             2        1        2        4        0        0        3
  gpt-5-6-                            0        7        0        1        3        1        0
  qwen3-8-                            0        4        1        0        0        0        2
  glm-5-3-                            4        1        1        2        0        0        3

python, features: 79 train, 66 test, 7 authors; accuracy 0.32 against a majority baseline of 0.18 (chance 0.14)
  actual \ predicted           claude-s gemma-4-    human  kimi-k3 gpt-5-6- qwen3-8- glm-5-3-
  claude-s                            2        1        0        3        1        1        4
  gemma-4-                            2        4        1        0        2        3        0
  human                               0        0        0        0        0        0        0
  kimi-k3                             1        0        0        5        1        0        5
  gpt-5-6-                            1        0        0        1        7        2        1
  qwen3-8-                            2        0        1        1        4        1        0
  glm-5-3-                            0        0        0        6        1        0        2

typescript, features: 66 train, 65 test, 6 authors; accuracy 0.48 against a majority baseline of 0.18 (chance 0.17)
  actual \ predicted           claude-s gemma-4-  kimi-k3 gpt-5-6- qwen3-8- glm-5-3-
  claude-s                            6        0        1        5        0        0
  gemma-4-                            3        5        1        3        0        0
  kimi-k3                             0        2        3        1        0        6
  gpt-5-6-                            0        0        2       10        0        0
  qwen3-8-                            0        0        1        6        0        0
  glm-5-3-                            2        0        1        0        0        7
go, features: margin over majority 0.17 (go)
python, features: margin over majority 0.14 (no-go)
typescript, features: margin over majority 0.29 (go)
method fingerprint, train on corpus, test on corpus2:
go, fingerprint: 78 train, 66 test, 7 authors; accuracy 0.77 against a majority baseline of 0.18 (chance 0.14)
  actual \ predicted           claude-s gemma-4-    human  kimi-k3 gpt-5-6- qwen3-8- glm-5-3-
  claude-s                           12        0        0        0        0        0        0
  gemma-4-                            0       12        0        0        0        0        0
  human                               0        0        0        0        0        0        0
  kimi-k3                             2        0        0        8        0        1        1
  gpt-5-6-                            0        0        0        0        9        3        0
  qwen3-8-                            0        2        0        0        2        3        0
  glm-5-3-                            1        0        0        3        0        0        7

python, fingerprint: 79 train, 66 test, 7 authors; accuracy 0.73 against a majority baseline of 0.18 (chance 0.14)
  actual \ predicted           claude-s gemma-4-    human  kimi-k3 gpt-5-6- qwen3-8- glm-5-3-
  claude-s                           11        1        0        0        0        0        0
  gemma-4-                            0       11        0        0        0        1        0
  human                               0        0        0        0        0        0        0
  kimi-k3                             0        0        0        6        0        0        6
  gpt-5-6-                            0        0        0        0       11        1        0
  qwen3-8-                            0        0        0        2        3        4        0
  glm-5-3-                            1        0        0        3        0        0        5

typescript, fingerprint: 66 train, 65 test, 6 authors; accuracy 0.74 against a majority baseline of 0.18 (chance 0.17)
  actual \ predicted           claude-s gemma-4-  kimi-k3 gpt-5-6- qwen3-8- glm-5-3-
  claude-s                           11        0        0        1        0        0
  gemma-4-                            1       11        0        0        0        0
  kimi-k3                             0        1        6        2        1        2
  gpt-5-6-                            0        0        0       11        0        1
  qwen3-8-                            0        2        0        2        3        0
  glm-5-3-                            1        0        1        2        0        6
go, fingerprint: margin over majority 0.59 (go)
python, fingerprint: margin over majority 0.55 (go)
typescript, fingerprint: margin over majority 0.55 (go)
method combined, train on corpus, test on corpus2:
go, combined: 78 train, 66 test, 7 authors; accuracy 0.58 against a majority baseline of 0.18 (chance 0.14)
  actual \ predicted           claude-s gemma-4-    human  kimi-k3 gpt-5-6- qwen3-8- glm-5-3-
  claude-s                            8        2        0        0        0        0        2
  gemma-4-                            1        9        0        1        1        0        0
  human                               0        0        0        0        0        0        0
  kimi-k3                             1        0        1        8        0        1        1
  gpt-5-6-                            0        0        0        1        9        2        0
  qwen3-8-                            0        4        0        0        0        1        2
  glm-5-3-                            3        1        1        3        0        0        3

python, combined: 79 train, 66 test, 7 authors; accuracy 0.67 against a majority baseline of 0.18 (chance 0.14)
  actual \ predicted           claude-s gemma-4-    human  kimi-k3 gpt-5-6- qwen3-8- glm-5-3-
  claude-s                           11        0        0        0        0        0        1
  gemma-4-                            0       11        0        0        0        1        0
  human                               0        0        0        0        0        0        0
  kimi-k3                             0        0        0        6        0        1        5
  gpt-5-6-                            0        0        0        1       10        1        0
  qwen3-8-                            0        0        0        2        4        3        0
  glm-5-3-                            1        0        0        4        0        1        3

typescript, combined: 66 train, 65 test, 6 authors; accuracy 0.63 against a majority baseline of 0.18 (chance 0.17)
  actual \ predicted           claude-s gemma-4-  kimi-k3 gpt-5-6- qwen3-8- glm-5-3-
  claude-s                            9        0        0        3        0        0
  gemma-4-                            1       10        0        1        0        0
  kimi-k3                             0        2        3        1        0        6
  gpt-5-6-                            0        0        0       11        0        1
  qwen3-8-                            0        1        0        6        0        0
  glm-5-3-                            1        0        1        0        0        8
go, combined: margin over majority 0.39 (go)
python, combined: margin over majority 0.48 (go)
typescript, combined: margin over majority 0.45 (go)
```
