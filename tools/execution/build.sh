#!/usr/bin/env bash
set -euo pipefail
repo=$(cd "$(dirname "$0")/../.." && pwd)
context=$(mktemp -d -t nitpick-checker.XXXXXX)
trap 'python3 -c "import shutil,sys; shutil.rmtree(sys.argv[1])" "$context"' EXIT
cd "$repo"
go build -trimpath -o "$context/nitpick-checker" ./cmd/nitpick-checker
docker build --network=none -f "$repo/tools/execution/Dockerfile" -t nitpick-checker:local "$context"
docker image inspect nitpick-checker:local --format '{{.Id}}'
