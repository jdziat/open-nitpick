#!/usr/bin/env sh
# Keep the CI entry point while sharing policy with `nitpick commits`.
set -eu
[ "$#" -le 1 ] || { echo 'usage: check-commits.sh [base..head]' >&2; exit 2; }
range="${1:-origin/main..HEAD}"
case "$range" in
  *...*) echo 'use a two-dot base..head commit range' >&2; exit 2 ;;
  *..*)
    base=${range%%..*}; head=${range#*..}
    case "$head" in *..*) echo 'use a two-dot base..head commit range' >&2; exit 2 ;; esac
    ;;
  *) echo 'use a two-dot base..head commit range' >&2; exit 2 ;;
esac
[ -n "$base" ] && [ -n "$head" ] || { echo 'both base and head are required' >&2; exit 2; }
[ "$(git --no-replace-objects rev-parse --is-shallow-repository)" = false ] || { echo 'commit coverage requires complete Git history' >&2; exit 2; }
base_sha=$(git --no-replace-objects rev-parse --verify --end-of-options "$base^{commit}")
head_sha=$(git --no-replace-objects rev-parse --verify --end-of-options "$head^{commit}")
# The historical CI wrapper accepts a valid range with no commits to lint.
[ "$(git --no-replace-objects rev-list --count "$base_sha..$head_sha")" -ne 0 ] || exit 0
repo=$(git --no-replace-objects rev-parse --show-toplevel)
tool_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
exec go -C "$tool_root" run ./cmd/nitpick commits -repo "$repo" -base "$base_sha" -head "$head_sha" -check
