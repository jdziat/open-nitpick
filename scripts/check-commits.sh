#!/usr/bin/env sh
# Every commit in RANGE (default: the commits not on origin/main) has a
# Conventional Commits subject: type(scope)!: description. release-please
# reads these to choose the next version and write the notes, so a subject
# it cannot parse is a release it cannot describe. GitHub's own merge
# commits are exempt; they are not released.
#
# The offenders are collected before anything is printed. Piping the loop
# into `grep -q` ends the pipe at the first match, and every ::error:: line
# after it dies with "echo: I/O error", so the job fails naming nothing.
set -eu
range="${1:-origin/main..HEAD}"
pattern='^(feat|fix|docs|style|refactor|perf|test|build|ci|chore|revert|evals|prompt)(\([a-z0-9._/-]+\))?!?: [^ ].{0,71}$'

bad=$(git log --format='%h %s' "$range" | while read -r sha subject; do
  case "$subject" in "Merge "*) continue ;; esac
  printf '%s\n' "$subject" | grep -Eq "$pattern" || printf '%s %s\n' "$sha" "$subject"
done)

[ -z "$bad" ] && exit 0

printf '%s\n' "$bad" | while read -r sha subject; do
  echo "::error::$sha: not a conventional commit subject: $subject"
done
exit 1
