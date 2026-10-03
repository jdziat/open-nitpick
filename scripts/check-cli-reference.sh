#!/usr/bin/env bash
set -euo pipefail

reference=docs/reference/cli.md
commands=(
  'review' 'fast-review' 'improve' 'full-review' 'repo-score'
  'commits' 'repo-standards' 'standards' 'security' 'slop'
  'init' 'explain-config' 'config-reference' 'providers' 'linters' 'knowledge-index'
  'respond' 'mcp' 'mcp install' 'auth' 'auth set' 'auth delete' 'auth list' 'version'
)

for command in "${commands[@]}"; do
  case "$command" in
    "auth set"|"auth delete"|"auth list") go run ./cmd/nitpick auth -h >/dev/null 2>&1 ;;
    *) go run ./cmd/nitpick $command -h >/dev/null 2>&1 ;;
  esac
  rg -Fq "$command" "$reference" || {
    echo "CLI reference does not document: nitpick $command" >&2
    exit 1
  }
done
