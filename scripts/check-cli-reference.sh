#!/usr/bin/env bash
set -euo pipefail

binary=$(mktemp "${TMPDIR:-/tmp}/nitpick-doc-check.XXXXXXXX")
trap 'unlink "$binary"' EXIT
go build -o "$binary" ./cmd/nitpick
python3 scripts/check-cli-reference.py "$binary"
