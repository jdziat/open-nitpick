#!/usr/bin/env sh
# Every commit in RANGE (default: the commits not on origin/main) has a
# Conventional Commits subject: type(scope)!: description. release-please
# reads these to choose the next version and write the notes, so a subject
# it cannot parse is a release it cannot describe. GitHub's own merge
# commits are exempt; they are not released.
set -eu
range="${1:-origin/main..HEAD}"
pattern='^(feat|fix|docs|style|refactor|perf|test|build|ci|chore|revert|evals|prompt)(\([a-z0-9._/-]+\))?!?: [^ ].{0,71}$'
status=0
git log --format='%h %s' "$range" | while read -r sha subject; do
  case "$subject" in "Merge "*) continue ;; esac
  if ! printf '%s\n' "$subject" | grep -Eq "$pattern"; then
    echo "::error::$sha: not a conventional commit subject: $subject"
    echo fail
  fi
done | grep -q '^fail$' && status=1
exit $status
