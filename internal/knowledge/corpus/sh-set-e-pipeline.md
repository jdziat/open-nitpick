---
title: set -e does not fail a pipeline when a command before the last one fails
languages: [shell, bash]
classes: [correctness]
source: https://www.gnu.org/software/bash/manual/bash.html#Pipelines
checked: 2026-09-08
---
A pipeline's exit status is the status of its **last** command. With `set -e`
and no `set -o pipefail`, `curl ... | tar -xz` succeeds whenever `tar`
succeeds, and `tar` succeeds on empty input.

So a download that 404s produces an empty archive, an empty directory, and a
green step. The failure surfaces later as a missing binary, in a different job,
attributed to something else.

The same applies to `grep -q x file | wc -l` and to any `cmd | tee log`, where
the writer's failure is masked by `tee` succeeding.

`set -euo pipefail` is the usual line. In a GitHub Actions `run:` block the
default shell is `bash -e`, so pipefail is off unless the step sets it or the
workflow sets `defaults.run.shell`.

What to look for: a pipeline in a script that has `set -e` and no `pipefail`,
particularly one whose first command reaches the network.
