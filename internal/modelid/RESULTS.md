# Model-identification experiment, 2026-09-05, with fingerprints

Output of `TestModelIdentificationExperiment` and `TestModelIdentificationAcrossGenerations` on the committed corpora, four methods each: the dense features (the original instrument), the fingerprint (character 3-grams and token bigrams, cosine to per-author centroids), both combined, and a Bernoulli naive Bayes over the grams. License headers are stripped before measuring. The idioms are the token bigrams most characteristic of each author, with the count of their files each appears in.

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
method bayes, split even tasks train:
go, bayes: 42 train, 36 test, 7 authors; accuracy 0.33 against a majority baseline of 0.17 (chance 0.14)
  actual \ predicted           claude-s gemma-4-    human  kimi-k3 gpt-5-6- qwen3-8- glm-5-3-
  claude-s                            0        2        4        0        0        0        0
  gemma-4-                            0        3        0        0        0        3        0
  human                               0        0        6        0        0        0        0
  kimi-k3                             0        1        4        0        0        0        0
  gpt-5-6-                            0        1        1        0        2        2        0
  qwen3-8-                            0        1        0        0        1        1        0
  glm-5-3-                            0        0        1        0        1        2        0

python, bayes: 41 train, 38 test, 7 authors; accuracy 0.32 against a majority baseline of 0.16 (chance 0.14)
  actual \ predicted           claude-s gemma-4-    human  kimi-k3 gpt-5-6- qwen3-8- glm-5-3-
  claude-s                            0        3        2        0        1        0        0
  gemma-4-                            0        5        0        0        1        0        0
  human                               0        3        3        0        0        0        0
  kimi-k3                             0        2        2        1        1        0        0
  gpt-5-6-                            0        3        0        0        3        0        0
  qwen3-8-                            0        3        0        0        1        0        0
  glm-5-3-                            0        0        1        0        3        0        0

typescript, bayes: 34 train, 32 test, 6 authors; accuracy 0.44 against a majority baseline of 0.19 (chance 0.17)
  actual \ predicted           claude-s gemma-4-  kimi-k3 gpt-5-6- qwen3-8- glm-5-3-
  claude-s                            3        3        0        0        0        0
  gemma-4-                            1        5        0        0        0        0
  kimi-k3                             1        0        0        1        0        4
  gpt-5-6-                            0        2        0        4        0        0
  qwen3-8-                            0        3        0        1        0        0
  glm-5-3-                            1        0        0        1        0        2
go, bayes: margin over majority 0.17 (go)
python, bayes: margin over majority 0.16 (go)
typescript, bayes: margin over majority 0.25 (go)
method bayes, split first half train:
go, bayes: 39 train, 39 test, 7 authors; accuracy 0.49 against a majority baseline of 0.15 (chance 0.14)
  actual \ predicted           claude-s gemma-4-    human  kimi-k3 gpt-5-6- qwen3-8- glm-5-3-
  claude-s                            3        2        1        0        0        0        0
  gemma-4-                            0        6        0        0        0        0        0
  human                               0        0        5        0        1        0        0
  kimi-k3                             0        3        1        0        1        1        0
  gpt-5-6-                            0        1        0        0        5        0        0
  qwen3-8-                            0        2        0        0        2        0        0
  glm-5-3-                            1        4        0        0        0        0        0

python, bayes: 40 train, 39 test, 7 authors; accuracy 0.31 against a majority baseline of 0.15 (chance 0.14)
  actual \ predicted           claude-s gemma-4-    human  kimi-k3 gpt-5-6- qwen3-8- glm-5-3-
  claude-s                            1        4        0        0        1        0        0
  gemma-4-                            0        5        0        0        1        0        0
  human                               0        5        1        0        0        0        0
  kimi-k3                             0        3        1        0        2        0        0
  gpt-5-6-                            0        1        0        0        5        0        0
  qwen3-8-                            0        3        0        0        1        0        0
  glm-5-3-                            0        1        0        0        4        0        0

typescript, bayes: 32 train, 34 test, 6 authors; accuracy 0.38 against a majority baseline of 0.18 (chance 0.17)
  actual \ predicted           claude-s gemma-4-  kimi-k3 gpt-5-6- qwen3-8- glm-5-3-
  claude-s                            3        3        0        0        0        0
  gemma-4-                            0        6        0        0        0        0
  kimi-k3                             0        3        1        1        0        1
  gpt-5-6-                            0        3        0        3        0        0
  qwen3-8-                            0        4        0        1        0        0
  glm-5-3-                            2        2        0        1        0        0
go, bayes: margin over majority 0.33 (go)
python, bayes: margin over majority 0.15 (go)
typescript, bayes: margin over majority 0.21 (go)
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
method bayes, train on corpus, test on corpus2:
go, bayes: 78 train, 66 test, 7 authors; accuracy 0.64 against a majority baseline of 0.18 (chance 0.14)
  actual \ predicted           claude-s gemma-4-    human  kimi-k3 gpt-5-6- qwen3-8- glm-5-3-
  claude-s                            7        5        0        0        0        0        0
  gemma-4-                            0       12        0        0        0        0        0
  human                               0        0        0        0        0        0        0
  kimi-k3                             0        5        0        5        0        0        2
  gpt-5-6-                            0        1        0        0       11        0        0
  qwen3-8-                            0        4        0        0        2        1        0
  glm-5-3-                            1        2        0        1        1        0        6

python, bayes: 79 train, 66 test, 7 authors; accuracy 0.55 against a majority baseline of 0.18 (chance 0.14)
  actual \ predicted           claude-s gemma-4-    human  kimi-k3 gpt-5-6- qwen3-8- glm-5-3-
  claude-s                            8        4        0        0        0        0        0
  gemma-4-                            0       12        0        0        0        0        0
  human                               0        0        0        0        0        0        0
  kimi-k3                             0        4        0        6        2        0        0
  gpt-5-6-                            0        4        0        0        8        0        0
  qwen3-8-                            0        7        0        0        1        1        0
  glm-5-3-                            0        5        0        2        1        0        1

typescript, bayes: 66 train, 65 test, 6 authors; accuracy 0.51 against a majority baseline of 0.18 (chance 0.17)
  actual \ predicted           claude-s gemma-4-  kimi-k3 gpt-5-6- qwen3-8- glm-5-3-
  claude-s                            6        6        0        0        0        0
  gemma-4-                            0       12        0        0        0        0
  kimi-k3                             0        3        4        2        2        1
  gpt-5-6-                            0        4        0        8        0        0
  qwen3-8-                            0        5        0        2        0        0
  glm-5-3-                            1        3        1        1        1        3
go, bayes: margin over majority 0.45 (go)
python, bayes: margin over majority 0.36 (go)
typescript, bayes: margin over majority 0.32 (go)
```

# Contributor experiment, 2026-09-06

Output of `TestContributorExperiment` on the corpus `cmd/contrib-corpus` built from ten repositories (not committed). Labels: human, or the tool named by the commit trailer. Balanced accuracy against a chance of 0.50; three splits by commit date.

```
method features, human against model, older half trains:
aider-AI_aider/python, features: 22 train, 22 test, 2 authors; accuracy 0.55 against a majority baseline of 0.95 (chance 0.50)
  actual \ predicted              human    model
  human                              11       10
  model                               0        1

better-auth_better-auth/typescript, features: 259 train, 262 test, 2 authors; accuracy 0.50 against a majority baseline of 0.50 (chance 0.50)
  actual \ predicted              human    model
  human                              58       74
  model                              57       73

browser-use_browser-use/python, features: 185 train, 186 test, 2 authors; accuracy 0.62 against a majority baseline of 0.62 (chance 0.50)
  actual \ predicted              human    model
  human                             116        0
  model                              70        0

cli_cli/go, features: 4 train, 437 test, 2 authors; accuracy 0.68 against a majority baseline of 0.68 (chance 0.50)
  actual \ predicted              human    model
  human                             296        0
  model                             141        0

github_github-mcp-server/go, features: 251 train, 251 test, 2 authors; accuracy 0.47 against a majority baseline of 0.67 (chance 0.50)
  actual \ predicted              human    model
  human                              42       41
  model                              93       75

gofiber_fiber/go, features: 207 train, 209 test, 2 authors; accuracy 0.75 against a majority baseline of 0.80 (chance 0.50)
  actual \ predicted              human    model
  human                               9       32
  model                              21      147

huggingface_huggingface_hub/python, features: 265 train, 271 test, 2 authors; accuracy 0.49 against a majority baseline of 0.52 (chance 0.50)
  actual \ predicted              human    model
  human                              55       74
  model                              63       79

temporalio_temporal/go, features: 245 train, 245 test, 2 authors; accuracy 0.56 against a majority baseline of 0.51 (chance 0.50)
  actual \ predicted              human    model
  human                              58       68
  model                              40       79

triggerdotdev_trigger.dev/typescript, features: 283 train, 328 test, 2 authors; accuracy 0.51 against a majority baseline of 0.52 (chance 0.50)
  actual \ predicted              human    model
  human                              40      119
  model                              42      127

vitest-dev_vitest/typescript, features: 219 train, 223 test, 2 authors; accuracy 0.53 against a majority baseline of 0.59 (chance 0.50)
  actual \ predicted              human    model
  human                              77       54
  model                              51       41
aider-AI_aider/python, features, older half trains: balanced accuracy 0.76 against chance 0.50; model recall 1.00, precision 0.09
better-auth_better-auth/typescript, features, older half trains: balanced accuracy 0.50 against chance 0.50; model recall 0.56, precision 0.50
browser-use_browser-use/python, features, older half trains: balanced accuracy 0.50 against chance 0.50; model recall 0.00, precision 0.00
cli_cli/go, features, older half trains: balanced accuracy 0.50 against chance 0.50; model recall 0.00, precision 0.00
github_github-mcp-server/go, features, older half trains: balanced accuracy 0.48 against chance 0.50; model recall 0.45, precision 0.65
gofiber_fiber/go, features, older half trains: balanced accuracy 0.55 against chance 0.50; model recall 0.88, precision 0.82
huggingface_huggingface_hub/python, features, older half trains: balanced accuracy 0.49 against chance 0.50; model recall 0.56, precision 0.52
temporalio_temporal/go, features, older half trains: balanced accuracy 0.56 against chance 0.50; model recall 0.66, precision 0.54
triggerdotdev_trigger.dev/typescript, features, older half trains: balanced accuracy 0.50 against chance 0.50; model recall 0.75, precision 0.52
vitest-dev_vitest/typescript, features, older half trains: balanced accuracy 0.52 against chance 0.50; model recall 0.45, precision 0.43
method fingerprint, human against model, older half trains:
aider-AI_aider/python, fingerprint: 22 train, 22 test, 2 authors; accuracy 0.73 against a majority baseline of 0.95 (chance 0.50)
  actual \ predicted              human    model
  human                              15        6
  model                               0        1

better-auth_better-auth/typescript, fingerprint: 259 train, 262 test, 2 authors; accuracy 0.38 against a majority baseline of 0.50 (chance 0.50)
  actual \ predicted              human    model
  human                              39       93
  model                              70       60

browser-use_browser-use/python, fingerprint: 185 train, 186 test, 2 authors; accuracy 0.61 against a majority baseline of 0.62 (chance 0.50)
  actual \ predicted              human    model
  human                             114        2
  model                              70        0

cli_cli/go, fingerprint: 4 train, 437 test, 2 authors; accuracy 0.68 against a majority baseline of 0.68 (chance 0.50)
  actual \ predicted              human    model
  human                             296        0
  model                             141        0

github_github-mcp-server/go, fingerprint: 251 train, 251 test, 2 authors; accuracy 0.45 against a majority baseline of 0.67 (chance 0.50)
  actual \ predicted              human    model
  human                              56       27
  model                             111       57

gofiber_fiber/go, fingerprint: 207 train, 209 test, 2 authors; accuracy 0.35 against a majority baseline of 0.80 (chance 0.50)
  actual \ predicted              human    model
  human                              35        6
  model                             130       38

huggingface_huggingface_hub/python, fingerprint: 265 train, 271 test, 2 authors; accuracy 0.52 against a majority baseline of 0.52 (chance 0.50)
  actual \ predicted              human    model
  human                              75       54
  model                              76       66

temporalio_temporal/go, fingerprint: 245 train, 245 test, 2 authors; accuracy 0.54 against a majority baseline of 0.51 (chance 0.50)
  actual \ predicted              human    model
  human                              94       32
  model                              81       38

triggerdotdev_trigger.dev/typescript, fingerprint: 283 train, 328 test, 2 authors; accuracy 0.60 against a majority baseline of 0.52 (chance 0.50)
  actual \ predicted              human    model
  human                              65       94
  model                              37      132

vitest-dev_vitest/typescript, fingerprint: 219 train, 223 test, 2 authors; accuracy 0.67 against a majority baseline of 0.59 (chance 0.50)
  actual \ predicted              human    model
  human                             103       28
  model                              46       46
aider-AI_aider/python, fingerprint, older half trains: balanced accuracy 0.86 against chance 0.50; model recall 1.00, precision 0.14
better-auth_better-auth/typescript, fingerprint, older half trains: balanced accuracy 0.38 against chance 0.50; model recall 0.46, precision 0.39
browser-use_browser-use/python, fingerprint, older half trains: balanced accuracy 0.49 against chance 0.50; model recall 0.00, precision 0.00
cli_cli/go, fingerprint, older half trains: balanced accuracy 0.50 against chance 0.50; model recall 0.00, precision 0.00
github_github-mcp-server/go, fingerprint, older half trains: balanced accuracy 0.51 against chance 0.50; model recall 0.34, precision 0.68
gofiber_fiber/go, fingerprint, older half trains: balanced accuracy 0.54 against chance 0.50; model recall 0.23, precision 0.86
huggingface_huggingface_hub/python, fingerprint, older half trains: balanced accuracy 0.52 against chance 0.50; model recall 0.46, precision 0.55
temporalio_temporal/go, fingerprint, older half trains: balanced accuracy 0.53 against chance 0.50; model recall 0.32, precision 0.54
triggerdotdev_trigger.dev/typescript, fingerprint, older half trains: balanced accuracy 0.59 against chance 0.50; model recall 0.78, precision 0.58
vitest-dev_vitest/typescript, fingerprint, older half trains: balanced accuracy 0.64 against chance 0.50; model recall 0.50, precision 0.62
method combined, human against model, older half trains:
aider-AI_aider/python, combined: 22 train, 22 test, 2 authors; accuracy 0.68 against a majority baseline of 0.95 (chance 0.50)
  actual \ predicted              human    model
  human                              14        7
  model                               0        1

better-auth_better-auth/typescript, combined: 259 train, 262 test, 2 authors; accuracy 0.48 against a majority baseline of 0.50 (chance 0.50)
  actual \ predicted              human    model
  human                              47       85
  model                              52       78

browser-use_browser-use/python, combined: 185 train, 186 test, 2 authors; accuracy 0.62 against a majority baseline of 0.62 (chance 0.50)
  actual \ predicted              human    model
  human                             116        0
  model                              70        0

cli_cli/go, combined: 4 train, 437 test, 2 authors; accuracy 0.68 against a majority baseline of 0.68 (chance 0.50)
  actual \ predicted              human    model
  human                             296        0
  model                             141        0

github_github-mcp-server/go, combined: 251 train, 251 test, 2 authors; accuracy 0.45 against a majority baseline of 0.67 (chance 0.50)
  actual \ predicted              human    model
  human                              52       31
  model                             106       62

gofiber_fiber/go, combined: 207 train, 209 test, 2 authors; accuracy 0.62 against a majority baseline of 0.80 (chance 0.50)
  actual \ predicted              human    model
  human                              13       28
  model                              52      116

huggingface_huggingface_hub/python, combined: 265 train, 271 test, 2 authors; accuracy 0.51 against a majority baseline of 0.52 (chance 0.50)
  actual \ predicted              human    model
  human                              68       61
  model                              73       69

temporalio_temporal/go, combined: 245 train, 245 test, 2 authors; accuracy 0.58 against a majority baseline of 0.51 (chance 0.50)
  actual \ predicted              human    model
  human                              85       41
  model                              62       57

triggerdotdev_trigger.dev/typescript, combined: 283 train, 328 test, 2 authors; accuracy 0.58 against a majority baseline of 0.52 (chance 0.50)
  actual \ predicted              human    model
  human                              47      112
  model                              25      144

vitest-dev_vitest/typescript, combined: 219 train, 223 test, 2 authors; accuracy 0.65 against a majority baseline of 0.59 (chance 0.50)
  actual \ predicted              human    model
  human                             105       26
  model                              51       41
aider-AI_aider/python, combined, older half trains: balanced accuracy 0.83 against chance 0.50; model recall 1.00, precision 0.12
better-auth_better-auth/typescript, combined, older half trains: balanced accuracy 0.48 against chance 0.50; model recall 0.60, precision 0.48
browser-use_browser-use/python, combined, older half trains: balanced accuracy 0.50 against chance 0.50; model recall 0.00, precision 0.00
cli_cli/go, combined, older half trains: balanced accuracy 0.50 against chance 0.50; model recall 0.00, precision 0.00
github_github-mcp-server/go, combined, older half trains: balanced accuracy 0.50 against chance 0.50; model recall 0.37, precision 0.67
gofiber_fiber/go, combined, older half trains: balanced accuracy 0.50 against chance 0.50; model recall 0.69, precision 0.81
huggingface_huggingface_hub/python, combined, older half trains: balanced accuracy 0.51 against chance 0.50; model recall 0.49, precision 0.53
temporalio_temporal/go, combined, older half trains: balanced accuracy 0.58 against chance 0.50; model recall 0.48, precision 0.58
triggerdotdev_trigger.dev/typescript, combined, older half trains: balanced accuracy 0.57 against chance 0.50; model recall 0.85, precision 0.56
vitest-dev_vitest/typescript, combined, older half trains: balanced accuracy 0.62 against chance 0.50; model recall 0.45, precision 0.61
method bayes, human against model, older half trains:
aider-AI_aider/python, bayes: 22 train, 22 test, 2 authors; accuracy 0.36 against a majority baseline of 0.95 (chance 0.50)
  actual \ predicted              human    model
  human                               7       14
  model                               0        1

better-auth_better-auth/typescript, bayes: 259 train, 262 test, 2 authors; accuracy 0.45 against a majority baseline of 0.50 (chance 0.50)
  actual \ predicted              human    model
  human                              77       55
  model                              90       40

browser-use_browser-use/python, bayes: 185 train, 186 test, 2 authors; accuracy 0.62 against a majority baseline of 0.62 (chance 0.50)
  actual \ predicted              human    model
  human                             116        0
  model                              70        0

cli_cli/go, bayes: 4 train, 437 test, 2 authors; accuracy 0.68 against a majority baseline of 0.68 (chance 0.50)
  actual \ predicted              human    model
  human                             296        0
  model                             141        0

github_github-mcp-server/go, bayes: 251 train, 251 test, 2 authors; accuracy 0.44 against a majority baseline of 0.67 (chance 0.50)
  actual \ predicted              human    model
  human                              66       17
  model                             124       44

gofiber_fiber/go, bayes: 207 train, 209 test, 2 authors; accuracy 0.28 against a majority baseline of 0.80 (chance 0.50)
  actual \ predicted              human    model
  human                              39        2
  model                             148       20

huggingface_huggingface_hub/python, bayes: 265 train, 271 test, 2 authors; accuracy 0.55 against a majority baseline of 0.52 (chance 0.50)
  actual \ predicted              human    model
  human                              79       50
  model                              71       71

temporalio_temporal/go, bayes: 245 train, 245 test, 2 authors; accuracy 0.54 against a majority baseline of 0.51 (chance 0.50)
  actual \ predicted              human    model
  human                             111       15
  model                              97       22

triggerdotdev_trigger.dev/typescript, bayes: 283 train, 328 test, 2 authors; accuracy 0.58 against a majority baseline of 0.52 (chance 0.50)
  actual \ predicted              human    model
  human                              80       79
  model                              60      109

vitest-dev_vitest/typescript, bayes: 219 train, 223 test, 2 authors; accuracy 0.66 against a majority baseline of 0.59 (chance 0.50)
  actual \ predicted              human    model
  human                             128        3
  model                              73       19
aider-AI_aider/python, bayes, older half trains: balanced accuracy 0.67 against chance 0.50; model recall 1.00, precision 0.07
better-auth_better-auth/typescript, bayes, older half trains: balanced accuracy 0.45 against chance 0.50; model recall 0.31, precision 0.42
browser-use_browser-use/python, bayes, older half trains: balanced accuracy 0.50 against chance 0.50; model recall 0.00, precision 0.00
cli_cli/go, bayes, older half trains: balanced accuracy 0.50 against chance 0.50; model recall 0.00, precision 0.00
github_github-mcp-server/go, bayes, older half trains: balanced accuracy 0.53 against chance 0.50; model recall 0.26, precision 0.72
gofiber_fiber/go, bayes, older half trains: balanced accuracy 0.54 against chance 0.50; model recall 0.12, precision 0.91
huggingface_huggingface_hub/python, bayes, older half trains: balanced accuracy 0.56 against chance 0.50; model recall 0.50, precision 0.59
temporalio_temporal/go, bayes, older half trains: balanced accuracy 0.53 against chance 0.50; model recall 0.18, precision 0.59
triggerdotdev_trigger.dev/typescript, bayes, older half trains: balanced accuracy 0.57 against chance 0.50; model recall 0.64, precision 0.58
vitest-dev_vitest/typescript, bayes, older half trains: balanced accuracy 0.59 against chance 0.50; model recall 0.21, precision 0.86
method features, human against model, blocks of fifty alternate:
aider-AI_aider/python, features: 19 train, 25 test, 2 authors; accuracy 0.72 against a majority baseline of 1.00 (chance 0.50)
  actual \ predicted              human    model
  human                              18        7
  model                               0        0

better-auth_better-auth/typescript, features: 273 train, 248 test, 2 authors; accuracy 0.50 against a majority baseline of 0.61 (chance 0.50)
  actual \ predicted              human    model
  human                              91       61
  model                              63       33

browser-use_browser-use/python, features: 172 train, 199 test, 2 authors; accuracy 0.68 against a majority baseline of 0.86 (chance 0.50)
  actual \ predicted              human    model
  human                             115       57
  model                               7       20

cli_cli/go, features: 96 train, 345 test, 2 authors; accuracy 0.33 against a majority baseline of 0.78 (chance 0.50)
  actual \ predicted              human    model
  human                              66      204
  model                              26       49

github_github-mcp-server/go, features: 240 train, 262 test, 2 authors; accuracy 0.55 against a majority baseline of 0.53 (chance 0.50)
  actual \ predicted              human    model
  human                              55       67
  model                              52       88

gofiber_fiber/go, features: 208 train, 208 test, 2 authors; accuracy 0.78 against a majority baseline of 0.53 (chance 0.50)
  actual \ predicted              human    model
  human                              68       30
  model                              15       95

huggingface_huggingface_hub/python, features: 333 train, 203 test, 2 authors; accuracy 0.59 against a majority baseline of 0.52 (chance 0.50)
  actual \ predicted              human    model
  human                              40       57
  model                              27       79

temporalio_temporal/go, features: 237 train, 253 test, 2 authors; accuracy 0.54 against a majority baseline of 0.53 (chance 0.50)
  actual \ predicted              human    model
  human                              64       56
  model                              61       72

triggerdotdev_trigger.dev/typescript, features: 369 train, 242 test, 2 authors; accuracy 0.57 against a majority baseline of 0.56 (chance 0.50)
  actual \ predicted              human    model
  human                              53       54
  model                              51       84

vitest-dev_vitest/typescript, features: 197 train, 245 test, 2 authors; accuracy 0.58 against a majority baseline of 0.67 (chance 0.50)
  actual \ predicted              human    model
  human                              89       75
  model                              28       53
aider-AI_aider/python, features, blocks of fifty alternate: balanced accuracy 0.72 against chance 0.50; model recall 0.00, precision 0.00
better-auth_better-auth/typescript, features, blocks of fifty alternate: balanced accuracy 0.47 against chance 0.50; model recall 0.34, precision 0.35
browser-use_browser-use/python, features, blocks of fifty alternate: balanced accuracy 0.70 against chance 0.50; model recall 0.74, precision 0.26
cli_cli/go, features, blocks of fifty alternate: balanced accuracy 0.45 against chance 0.50; model recall 0.65, precision 0.19
github_github-mcp-server/go, features, blocks of fifty alternate: balanced accuracy 0.54 against chance 0.50; model recall 0.63, precision 0.57
gofiber_fiber/go, features, blocks of fifty alternate: balanced accuracy 0.78 against chance 0.50; model recall 0.86, precision 0.76
huggingface_huggingface_hub/python, features, blocks of fifty alternate: balanced accuracy 0.58 against chance 0.50; model recall 0.75, precision 0.58
temporalio_temporal/go, features, blocks of fifty alternate: balanced accuracy 0.54 against chance 0.50; model recall 0.54, precision 0.56
triggerdotdev_trigger.dev/typescript, features, blocks of fifty alternate: balanced accuracy 0.56 against chance 0.50; model recall 0.62, precision 0.61
vitest-dev_vitest/typescript, features, blocks of fifty alternate: balanced accuracy 0.60 against chance 0.50; model recall 0.65, precision 0.41
method fingerprint, human against model, blocks of fifty alternate:
aider-AI_aider/python, fingerprint: 19 train, 25 test, 2 authors; accuracy 1.00 against a majority baseline of 1.00 (chance 0.50)
  actual \ predicted              human    model
  human                              25        0
  model                               0        0

better-auth_better-auth/typescript, fingerprint: 273 train, 248 test, 2 authors; accuracy 0.48 against a majority baseline of 0.61 (chance 0.50)
  actual \ predicted              human    model
  human                              83       69
  model                              61       35

browser-use_browser-use/python, fingerprint: 172 train, 199 test, 2 authors; accuracy 0.77 against a majority baseline of 0.86 (chance 0.50)
  actual \ predicted              human    model
  human                             134       38
  model                               8       19

cli_cli/go, fingerprint: 96 train, 345 test, 2 authors; accuracy 0.29 against a majority baseline of 0.78 (chance 0.50)
  actual \ predicted              human    model
  human                              42      228
  model                              18       57

github_github-mcp-server/go, fingerprint: 240 train, 262 test, 2 authors; accuracy 0.58 against a majority baseline of 0.53 (chance 0.50)
  actual \ predicted              human    model
  human                              43       79
  model                              31      109

gofiber_fiber/go, fingerprint: 208 train, 208 test, 2 authors; accuracy 0.79 against a majority baseline of 0.53 (chance 0.50)
  actual \ predicted              human    model
  human                              82       16
  model                              27       83

huggingface_huggingface_hub/python, fingerprint: 333 train, 203 test, 2 authors; accuracy 0.58 against a majority baseline of 0.52 (chance 0.50)
  actual \ predicted              human    model
  human                              50       47
  model                              38       68

temporalio_temporal/go, fingerprint: 237 train, 253 test, 2 authors; accuracy 0.51 against a majority baseline of 0.53 (chance 0.50)
  actual \ predicted              human    model
  human                              69       51
  model                              74       59

triggerdotdev_trigger.dev/typescript, fingerprint: 369 train, 242 test, 2 authors; accuracy 0.50 against a majority baseline of 0.56 (chance 0.50)
  actual \ predicted              human    model
  human                              83       24
  model                              96       39

vitest-dev_vitest/typescript, fingerprint: 197 train, 245 test, 2 authors; accuracy 0.73 against a majority baseline of 0.67 (chance 0.50)
  actual \ predicted              human    model
  human                             128       36
  model                              31       50
aider-AI_aider/python, fingerprint, blocks of fifty alternate: balanced accuracy 1.00 against chance 0.50; model recall 0.00, precision 0.00
better-auth_better-auth/typescript, fingerprint, blocks of fifty alternate: balanced accuracy 0.46 against chance 0.50; model recall 0.36, precision 0.34
browser-use_browser-use/python, fingerprint, blocks of fifty alternate: balanced accuracy 0.74 against chance 0.50; model recall 0.70, precision 0.33
cli_cli/go, fingerprint, blocks of fifty alternate: balanced accuracy 0.46 against chance 0.50; model recall 0.76, precision 0.20
github_github-mcp-server/go, fingerprint, blocks of fifty alternate: balanced accuracy 0.57 against chance 0.50; model recall 0.78, precision 0.58
gofiber_fiber/go, fingerprint, blocks of fifty alternate: balanced accuracy 0.80 against chance 0.50; model recall 0.75, precision 0.84
huggingface_huggingface_hub/python, fingerprint, blocks of fifty alternate: balanced accuracy 0.58 against chance 0.50; model recall 0.64, precision 0.59
temporalio_temporal/go, fingerprint, blocks of fifty alternate: balanced accuracy 0.51 against chance 0.50; model recall 0.44, precision 0.54
triggerdotdev_trigger.dev/typescript, fingerprint, blocks of fifty alternate: balanced accuracy 0.53 against chance 0.50; model recall 0.29, precision 0.62
vitest-dev_vitest/typescript, fingerprint, blocks of fifty alternate: balanced accuracy 0.70 against chance 0.50; model recall 0.62, precision 0.58
method combined, human against model, blocks of fifty alternate:
aider-AI_aider/python, combined: 19 train, 25 test, 2 authors; accuracy 0.96 against a majority baseline of 1.00 (chance 0.50)
  actual \ predicted              human    model
  human                              24        1
  model                               0        0

better-auth_better-auth/typescript, combined: 273 train, 248 test, 2 authors; accuracy 0.46 against a majority baseline of 0.61 (chance 0.50)
  actual \ predicted              human    model
  human                              95       57
  model                              76       20

browser-use_browser-use/python, combined: 172 train, 199 test, 2 authors; accuracy 0.78 against a majority baseline of 0.86 (chance 0.50)
  actual \ predicted              human    model
  human                             134       38
  model                               5       22

cli_cli/go, combined: 96 train, 345 test, 2 authors; accuracy 0.29 against a majority baseline of 0.78 (chance 0.50)
  actual \ predicted              human    model
  human                              46      224
  model                              20       55

github_github-mcp-server/go, combined: 240 train, 262 test, 2 authors; accuracy 0.57 against a majority baseline of 0.53 (chance 0.50)
  actual \ predicted              human    model
  human                              44       78
  model                              34      106

gofiber_fiber/go, combined: 208 train, 208 test, 2 authors; accuracy 0.81 against a majority baseline of 0.53 (chance 0.50)
  actual \ predicted              human    model
  human                              74       24
  model                              16       94

huggingface_huggingface_hub/python, combined: 333 train, 203 test, 2 authors; accuracy 0.61 against a majority baseline of 0.52 (chance 0.50)
  actual \ predicted              human    model
  human                              46       51
  model                              29       77

temporalio_temporal/go, combined: 237 train, 253 test, 2 authors; accuracy 0.49 against a majority baseline of 0.53 (chance 0.50)
  actual \ predicted              human    model
  human                              66       54
  model                              75       58

triggerdotdev_trigger.dev/typescript, combined: 369 train, 242 test, 2 authors; accuracy 0.53 against a majority baseline of 0.56 (chance 0.50)
  actual \ predicted              human    model
  human                              76       31
  model                              82       53

vitest-dev_vitest/typescript, combined: 197 train, 245 test, 2 authors; accuracy 0.69 against a majority baseline of 0.67 (chance 0.50)
  actual \ predicted              human    model
  human                             117       47
  model                              28       53
aider-AI_aider/python, combined, blocks of fifty alternate: balanced accuracy 0.96 against chance 0.50; model recall 0.00, precision 0.00
better-auth_better-auth/typescript, combined, blocks of fifty alternate: balanced accuracy 0.42 against chance 0.50; model recall 0.21, precision 0.26
browser-use_browser-use/python, combined, blocks of fifty alternate: balanced accuracy 0.80 against chance 0.50; model recall 0.81, precision 0.37
cli_cli/go, combined, blocks of fifty alternate: balanced accuracy 0.45 against chance 0.50; model recall 0.73, precision 0.20
github_github-mcp-server/go, combined, blocks of fifty alternate: balanced accuracy 0.56 against chance 0.50; model recall 0.76, precision 0.58
gofiber_fiber/go, combined, blocks of fifty alternate: balanced accuracy 0.80 against chance 0.50; model recall 0.85, precision 0.80
huggingface_huggingface_hub/python, combined, blocks of fifty alternate: balanced accuracy 0.60 against chance 0.50; model recall 0.73, precision 0.60
temporalio_temporal/go, combined, blocks of fifty alternate: balanced accuracy 0.49 against chance 0.50; model recall 0.44, precision 0.52
triggerdotdev_trigger.dev/typescript, combined, blocks of fifty alternate: balanced accuracy 0.55 against chance 0.50; model recall 0.39, precision 0.63
vitest-dev_vitest/typescript, combined, blocks of fifty alternate: balanced accuracy 0.68 against chance 0.50; model recall 0.65, precision 0.53
method bayes, human against model, blocks of fifty alternate:
aider-AI_aider/python, bayes: 19 train, 25 test, 2 authors; accuracy 1.00 against a majority baseline of 1.00 (chance 0.50)
  actual \ predicted              human    model
  human                              25        0
  model                               0        0

better-auth_better-auth/typescript, bayes: 273 train, 248 test, 2 authors; accuracy 0.56 against a majority baseline of 0.61 (chance 0.50)
  actual \ predicted              human    model
  human                             134       18
  model                              90        6

browser-use_browser-use/python, bayes: 172 train, 199 test, 2 authors; accuracy 0.82 against a majority baseline of 0.86 (chance 0.50)
  actual \ predicted              human    model
  human                             157       15
  model                              21        6

cli_cli/go, bayes: 96 train, 345 test, 2 authors; accuracy 0.24 against a majority baseline of 0.78 (chance 0.50)
  actual \ predicted              human    model
  human                              12      258
  model                               5       70

github_github-mcp-server/go, bayes: 240 train, 262 test, 2 authors; accuracy 0.62 against a majority baseline of 0.53 (chance 0.50)
  actual \ predicted              human    model
  human                              85       37
  model                              63       77

gofiber_fiber/go, bayes: 208 train, 208 test, 2 authors; accuracy 0.76 against a majority baseline of 0.53 (chance 0.50)
  actual \ predicted              human    model
  human                              89        9
  model                              40       70

huggingface_huggingface_hub/python, bayes: 333 train, 203 test, 2 authors; accuracy 0.54 against a majority baseline of 0.52 (chance 0.50)
  actual \ predicted              human    model
  human                              74       23
  model                              71       35

temporalio_temporal/go, bayes: 237 train, 253 test, 2 authors; accuracy 0.53 against a majority baseline of 0.53 (chance 0.50)
  actual \ predicted              human    model
  human                              95       25
  model                              93       40

triggerdotdev_trigger.dev/typescript, bayes: 369 train, 242 test, 2 authors; accuracy 0.48 against a majority baseline of 0.56 (chance 0.50)
  actual \ predicted              human    model
  human                              82       25
  model                             100       35

vitest-dev_vitest/typescript, bayes: 197 train, 245 test, 2 authors; accuracy 0.73 against a majority baseline of 0.67 (chance 0.50)
  actual \ predicted              human    model
  human                             152       12
  model                              53       28
aider-AI_aider/python, bayes, blocks of fifty alternate: balanced accuracy 1.00 against chance 0.50; model recall 0.00, precision 0.00
better-auth_better-auth/typescript, bayes, blocks of fifty alternate: balanced accuracy 0.47 against chance 0.50; model recall 0.06, precision 0.25
browser-use_browser-use/python, bayes, blocks of fifty alternate: balanced accuracy 0.57 against chance 0.50; model recall 0.22, precision 0.29
cli_cli/go, bayes, blocks of fifty alternate: balanced accuracy 0.49 against chance 0.50; model recall 0.93, precision 0.21
github_github-mcp-server/go, bayes, blocks of fifty alternate: balanced accuracy 0.62 against chance 0.50; model recall 0.55, precision 0.68
gofiber_fiber/go, bayes, blocks of fifty alternate: balanced accuracy 0.77 against chance 0.50; model recall 0.64, precision 0.89
huggingface_huggingface_hub/python, bayes, blocks of fifty alternate: balanced accuracy 0.55 against chance 0.50; model recall 0.33, precision 0.60
temporalio_temporal/go, bayes, blocks of fifty alternate: balanced accuracy 0.55 against chance 0.50; model recall 0.30, precision 0.62
triggerdotdev_trigger.dev/typescript, bayes, blocks of fifty alternate: balanced accuracy 0.51 against chance 0.50; model recall 0.26, precision 0.58
vitest-dev_vitest/typescript, bayes, blocks of fifty alternate: balanced accuracy 0.64 against chance 0.50; model recall 0.35, precision 0.70
method features, human against model, interleaved by date:
aider-AI_aider/python, features: 22 train, 22 test, 2 authors; accuracy 0.82 against a majority baseline of 0.86 (chance 0.50)
  actual \ predicted              human    model
  human                              17        2
  model                               2        1

better-auth_better-auth/typescript, features: 330 train, 191 test, 2 authors; accuracy 0.53 against a majority baseline of 0.59 (chance 0.50)
  actual \ predicted              human    model
  human                              80       33
  model                              56       22

browser-use_browser-use/python, features: 175 train, 196 test, 2 authors; accuracy 0.64 against a majority baseline of 0.81 (chance 0.50)
  actual \ predicted              human    model
  human                              98       60
  model                              10       28

cli_cli/go, features: 110 train, 331 test, 2 authors; accuracy 0.34 against a majority baseline of 0.80 (chance 0.50)
  actual \ predicted              human    model
  human                              72      192
  model                              28       39

github_github-mcp-server/go, features: 281 train, 221 test, 2 authors; accuracy 0.61 against a majority baseline of 0.57 (chance 0.50)
  actual \ predicted              human    model
  human                              75       51
  model                              36       59

gofiber_fiber/go, features: 198 train, 218 test, 2 authors; accuracy 0.75 against a majority baseline of 0.54 (chance 0.50)
  actual \ predicted              human    model
  human                              81       36
  model                              18       83

huggingface_huggingface_hub/python, features: 272 train, 264 test, 2 authors; accuracy 0.62 against a majority baseline of 0.61 (chance 0.50)
  actual \ predicted              human    model
  human                              90       72
  model                              27       75

temporalio_temporal/go, features: 213 train, 277 test, 2 authors; accuracy 0.53 against a majority baseline of 0.56 (chance 0.50)
  actual \ predicted              human    model
  human                              92       64
  model                              65       56

triggerdotdev_trigger.dev/typescript, features: 362 train, 249 test, 2 authors; accuracy 0.52 against a majority baseline of 0.59 (chance 0.50)
  actual \ predicted              human    model
  human                              58       88
  model                              32       71

vitest-dev_vitest/typescript, features: 247 train, 195 test, 2 authors; accuracy 0.65 against a majority baseline of 0.76 (chance 0.50)
  actual \ predicted              human    model
  human                              95       53
  model                              16       31
aider-AI_aider/python, features, interleaved by date: balanced accuracy 0.61 against chance 0.50; model recall 0.33, precision 0.33
better-auth_better-auth/typescript, features, interleaved by date: balanced accuracy 0.49 against chance 0.50; model recall 0.28, precision 0.40
browser-use_browser-use/python, features, interleaved by date: balanced accuracy 0.68 against chance 0.50; model recall 0.74, precision 0.32
cli_cli/go, features, interleaved by date: balanced accuracy 0.43 against chance 0.50; model recall 0.58, precision 0.17
github_github-mcp-server/go, features, interleaved by date: balanced accuracy 0.61 against chance 0.50; model recall 0.62, precision 0.54
gofiber_fiber/go, features, interleaved by date: balanced accuracy 0.76 against chance 0.50; model recall 0.82, precision 0.70
huggingface_huggingface_hub/python, features, interleaved by date: balanced accuracy 0.65 against chance 0.50; model recall 0.74, precision 0.51
temporalio_temporal/go, features, interleaved by date: balanced accuracy 0.53 against chance 0.50; model recall 0.46, precision 0.47
triggerdotdev_trigger.dev/typescript, features, interleaved by date: balanced accuracy 0.54 against chance 0.50; model recall 0.69, precision 0.45
vitest-dev_vitest/typescript, features, interleaved by date: balanced accuracy 0.65 against chance 0.50; model recall 0.66, precision 0.37
method fingerprint, human against model, interleaved by date:
aider-AI_aider/python, fingerprint: 22 train, 22 test, 2 authors; accuracy 0.86 against a majority baseline of 0.86 (chance 0.50)
  actual \ predicted              human    model
  human                              17        2
  model                               1        2

better-auth_better-auth/typescript, fingerprint: 330 train, 191 test, 2 authors; accuracy 0.50 against a majority baseline of 0.59 (chance 0.50)
  actual \ predicted              human    model
  human                              57       56
  model                              39       39

browser-use_browser-use/python, fingerprint: 175 train, 196 test, 2 authors; accuracy 0.78 against a majority baseline of 0.81 (chance 0.50)
  actual \ predicted              human    model
  human                             125       33
  model                              10       28

cli_cli/go, fingerprint: 110 train, 331 test, 2 authors; accuracy 0.47 against a majority baseline of 0.80 (chance 0.50)
  actual \ predicted              human    model
  human                              99      165
  model                              12       55

github_github-mcp-server/go, fingerprint: 281 train, 221 test, 2 authors; accuracy 0.62 against a majority baseline of 0.57 (chance 0.50)
  actual \ predicted              human    model
  human                              81       45
  model                              40       55

gofiber_fiber/go, fingerprint: 198 train, 218 test, 2 authors; accuracy 0.78 against a majority baseline of 0.54 (chance 0.50)
  actual \ predicted              human    model
  human                              92       25
  model                              23       78

huggingface_huggingface_hub/python, fingerprint: 272 train, 264 test, 2 authors; accuracy 0.52 against a majority baseline of 0.61 (chance 0.50)
  actual \ predicted              human    model
  human                              60      102
  model                              25       77

temporalio_temporal/go, fingerprint: 213 train, 277 test, 2 authors; accuracy 0.57 against a majority baseline of 0.56 (chance 0.50)
  actual \ predicted              human    model
  human                              99       57
  model                              63       58

triggerdotdev_trigger.dev/typescript, fingerprint: 362 train, 249 test, 2 authors; accuracy 0.60 against a majority baseline of 0.59 (chance 0.50)
  actual \ predicted              human    model
  human                              88       58
  model                              42       61

vitest-dev_vitest/typescript, fingerprint: 247 train, 195 test, 2 authors; accuracy 0.78 against a majority baseline of 0.76 (chance 0.50)
  actual \ predicted              human    model
  human                             123       25
  model                              17       30
aider-AI_aider/python, fingerprint, interleaved by date: balanced accuracy 0.78 against chance 0.50; model recall 0.67, precision 0.50
better-auth_better-auth/typescript, fingerprint, interleaved by date: balanced accuracy 0.50 against chance 0.50; model recall 0.50, precision 0.41
browser-use_browser-use/python, fingerprint, interleaved by date: balanced accuracy 0.76 against chance 0.50; model recall 0.74, precision 0.46
cli_cli/go, fingerprint, interleaved by date: balanced accuracy 0.60 against chance 0.50; model recall 0.82, precision 0.25
github_github-mcp-server/go, fingerprint, interleaved by date: balanced accuracy 0.61 against chance 0.50; model recall 0.58, precision 0.55
gofiber_fiber/go, fingerprint, interleaved by date: balanced accuracy 0.78 against chance 0.50; model recall 0.77, precision 0.76
huggingface_huggingface_hub/python, fingerprint, interleaved by date: balanced accuracy 0.56 against chance 0.50; model recall 0.75, precision 0.43
temporalio_temporal/go, fingerprint, interleaved by date: balanced accuracy 0.56 against chance 0.50; model recall 0.48, precision 0.50
triggerdotdev_trigger.dev/typescript, fingerprint, interleaved by date: balanced accuracy 0.60 against chance 0.50; model recall 0.59, precision 0.51
vitest-dev_vitest/typescript, fingerprint, interleaved by date: balanced accuracy 0.73 against chance 0.50; model recall 0.64, precision 0.55
method combined, human against model, interleaved by date:
aider-AI_aider/python, combined: 22 train, 22 test, 2 authors; accuracy 0.95 against a majority baseline of 0.86 (chance 0.50)
  actual \ predicted              human    model
  human                              19        0
  model                               1        2

better-auth_better-auth/typescript, combined: 330 train, 191 test, 2 authors; accuracy 0.52 against a majority baseline of 0.59 (chance 0.50)
  actual \ predicted              human    model
  human                              77       36
  model                              56       22

browser-use_browser-use/python, combined: 175 train, 196 test, 2 authors; accuracy 0.72 against a majority baseline of 0.81 (chance 0.50)
  actual \ predicted              human    model
  human                             114       44
  model                              10       28

cli_cli/go, combined: 110 train, 331 test, 2 authors; accuracy 0.39 against a majority baseline of 0.80 (chance 0.50)
  actual \ predicted              human    model
  human                              78      186
  model                              15       52

github_github-mcp-server/go, combined: 281 train, 221 test, 2 authors; accuracy 0.62 against a majority baseline of 0.57 (chance 0.50)
  actual \ predicted              human    model
  human                              77       49
  model                              36       59

gofiber_fiber/go, combined: 198 train, 218 test, 2 authors; accuracy 0.80 against a majority baseline of 0.54 (chance 0.50)
  actual \ predicted              human    model
  human                              92       25
  model                              18       83

huggingface_huggingface_hub/python, combined: 272 train, 264 test, 2 authors; accuracy 0.56 against a majority baseline of 0.61 (chance 0.50)
  actual \ predicted              human    model
  human                              64       98
  model                              19       83

temporalio_temporal/go, combined: 213 train, 277 test, 2 authors; accuracy 0.56 against a majority baseline of 0.56 (chance 0.50)
  actual \ predicted              human    model
  human                              95       61
  model                              62       59

triggerdotdev_trigger.dev/typescript, combined: 362 train, 249 test, 2 authors; accuracy 0.61 against a majority baseline of 0.59 (chance 0.50)
  actual \ predicted              human    model
  human                              84       62
  model                              36       67

vitest-dev_vitest/typescript, combined: 247 train, 195 test, 2 authors; accuracy 0.78 against a majority baseline of 0.76 (chance 0.50)
  actual \ predicted              human    model
  human                             118       30
  model                              13       34
aider-AI_aider/python, combined, interleaved by date: balanced accuracy 0.83 against chance 0.50; model recall 0.67, precision 1.00
better-auth_better-auth/typescript, combined, interleaved by date: balanced accuracy 0.48 against chance 0.50; model recall 0.28, precision 0.38
browser-use_browser-use/python, combined, interleaved by date: balanced accuracy 0.73 against chance 0.50; model recall 0.74, precision 0.39
cli_cli/go, combined, interleaved by date: balanced accuracy 0.54 against chance 0.50; model recall 0.78, precision 0.22
github_github-mcp-server/go, combined, interleaved by date: balanced accuracy 0.62 against chance 0.50; model recall 0.62, precision 0.55
gofiber_fiber/go, combined, interleaved by date: balanced accuracy 0.80 against chance 0.50; model recall 0.82, precision 0.77
huggingface_huggingface_hub/python, combined, interleaved by date: balanced accuracy 0.60 against chance 0.50; model recall 0.81, precision 0.46
temporalio_temporal/go, combined, interleaved by date: balanced accuracy 0.55 against chance 0.50; model recall 0.49, precision 0.49
triggerdotdev_trigger.dev/typescript, combined, interleaved by date: balanced accuracy 0.61 against chance 0.50; model recall 0.65, precision 0.52
vitest-dev_vitest/typescript, combined, interleaved by date: balanced accuracy 0.76 against chance 0.50; model recall 0.72, precision 0.53
method bayes, human against model, interleaved by date:
aider-AI_aider/python, bayes: 22 train, 22 test, 2 authors; accuracy 0.91 against a majority baseline of 0.86 (chance 0.50)
  actual \ predicted              human    model
  human                              19        0
  model                               2        1

better-auth_better-auth/typescript, bayes: 330 train, 191 test, 2 authors; accuracy 0.58 against a majority baseline of 0.59 (chance 0.50)
  actual \ predicted              human    model
  human                             102       11
  model                              70        8

browser-use_browser-use/python, bayes: 175 train, 196 test, 2 authors; accuracy 0.78 against a majority baseline of 0.81 (chance 0.50)
  actual \ predicted              human    model
  human                             143       15
  model                              28       10

cli_cli/go, bayes: 110 train, 331 test, 2 authors; accuracy 0.37 against a majority baseline of 0.80 (chance 0.50)
  actual \ predicted              human    model
  human                              62      202
  model                               7       60

github_github-mcp-server/go, bayes: 281 train, 221 test, 2 authors; accuracy 0.63 against a majority baseline of 0.57 (chance 0.50)
  actual \ predicted              human    model
  human                              96       30
  model                              51       44

gofiber_fiber/go, bayes: 198 train, 218 test, 2 authors; accuracy 0.74 against a majority baseline of 0.54 (chance 0.50)
  actual \ predicted              human    model
  human                             103       14
  model                              42       59

huggingface_huggingface_hub/python, bayes: 272 train, 264 test, 2 authors; accuracy 0.52 against a majority baseline of 0.61 (chance 0.50)
  actual \ predicted              human    model
  human                              71       91
  model                              35       67

temporalio_temporal/go, bayes: 213 train, 277 test, 2 authors; accuracy 0.57 against a majority baseline of 0.56 (chance 0.50)
  actual \ predicted              human    model
  human                             127       29
  model                              89       32

triggerdotdev_trigger.dev/typescript, bayes: 362 train, 249 test, 2 authors; accuracy 0.69 against a majority baseline of 0.59 (chance 0.50)
  actual \ predicted              human    model
  human                             116       30
  model                              47       56

vitest-dev_vitest/typescript, bayes: 247 train, 195 test, 2 authors; accuracy 0.79 against a majority baseline of 0.76 (chance 0.50)
  actual \ predicted              human    model
  human                             140        8
  model                              32       15
aider-AI_aider/python, bayes, interleaved by date: balanced accuracy 0.67 against chance 0.50; model recall 0.33, precision 1.00
better-auth_better-auth/typescript, bayes, interleaved by date: balanced accuracy 0.50 against chance 0.50; model recall 0.10, precision 0.42
browser-use_browser-use/python, bayes, interleaved by date: balanced accuracy 0.58 against chance 0.50; model recall 0.26, precision 0.40
cli_cli/go, bayes, interleaved by date: balanced accuracy 0.56 against chance 0.50; model recall 0.90, precision 0.23
github_github-mcp-server/go, bayes, interleaved by date: balanced accuracy 0.61 against chance 0.50; model recall 0.46, precision 0.59
gofiber_fiber/go, bayes, interleaved by date: balanced accuracy 0.73 against chance 0.50; model recall 0.58, precision 0.81
huggingface_huggingface_hub/python, bayes, interleaved by date: balanced accuracy 0.55 against chance 0.50; model recall 0.66, precision 0.42
temporalio_temporal/go, bayes, interleaved by date: balanced accuracy 0.54 against chance 0.50; model recall 0.26, precision 0.52
triggerdotdev_trigger.dev/typescript, bayes, interleaved by date: balanced accuracy 0.67 against chance 0.50; model recall 0.54, precision 0.65
vitest-dev_vitest/typescript, bayes, interleaved by date: balanced accuracy 0.63 against chance 0.50; model recall 0.32, precision 0.65
fingerprint, by tool, interleaved:
aider-AI_aider/python, fingerprint: 22 train, 22 test, 2 authors; accuracy 0.86 against a majority baseline of 0.86 (chance 0.50)
  actual \ predicted              human model-ai
  human                              17        2
  model-ai                            1        2

better-auth_better-auth/typescript, fingerprint: 330 train, 191 test, 4 authors; accuracy 0.34 against a majority baseline of 0.59 (chance 0.25)
  actual \ predicted              human model-cl model-co model-cu
  human                              59       18        0       36
  model-cl                           10        1        0        3
  model-co                           22        4        0       12
  model-cu                           16        6        0        4

browser-use_browser-use/python, fingerprint: 175 train, 196 test, 2 authors; accuracy 0.78 against a majority baseline of 0.81 (chance 0.50)
  actual \ predicted              human model-cl
  human                             125       33
  model-cl                           10       28

cli_cli/go, fingerprint: 110 train, 331 test, 2 authors; accuracy 0.47 against a majority baseline of 0.80 (chance 0.50)
  actual \ predicted              human model-co
  human                              99      165
  model-co                           12       55

github_github-mcp-server/go, fingerprint: 281 train, 221 test, 4 authors; accuracy 0.59 against a majority baseline of 0.57 (chance 0.25)
  actual \ predicted              human model-cl model-co model-cu
  human                              75        6       45        0
  model-cl                            0        0        0        0
  model-co                           38        1       55        0
  model-cu                            1        0        0        0

gofiber_fiber/go, fingerprint: 198 train, 218 test, 3 authors; accuracy 0.77 against a majority baseline of 0.54 (chance 0.33)
  actual \ predicted              human model-cl model-co
  human                              91       22        4
  model-cl                           20       76        1
  model-co                            4        0        0

huggingface_huggingface_hub/python, fingerprint: 272 train, 264 test, 4 authors; accuracy 0.42 against a majority baseline of 0.61 (chance 0.25)
  actual \ predicted              human model-cl model-co model-cu
  human                              66       52       26       18
  model-cl                           12       31        2       19
  model-co                            0        0        1        1
  model-cu                            9        9        4       14

temporalio_temporal/go, fingerprint: 213 train, 277 test, 3 authors; accuracy 0.56 against a majority baseline of 0.56 (chance 0.33)
  actual \ predicted              human model-cl model-cu
  human                              98       58        0
  model-cl                           64       55        0
  model-cu                            0        1        1

triggerdotdev_trigger.dev/typescript, fingerprint: 362 train, 249 test, 3 authors; accuracy 0.60 against a majority baseline of 0.59 (chance 0.33)
  actual \ predicted              human model-cl model-de
  human                              89       55        2
  model-cl                           36       61        0
  model-de                            6        0        0

vitest-dev_vitest/typescript, fingerprint: 247 train, 195 test, 4 authors; accuracy 0.70 against a majority baseline of 0.76 (chance 0.25)
  actual \ predicted              human model-cl model-co model-co
  human                             121       11       16        0
  model-cl                            8        5        4        0
  model-co                            9        5       10        0
  model-co                            2        3        1        0
```
