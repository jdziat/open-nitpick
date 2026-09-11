#!/usr/bin/env bash
set -euo pipefail

# Install outside the checkout: analyzer resolution refuses project binaries.
tools_dir="$(mkdir -p "${1:?usage: install-language-linters.sh DIRECTORY}" && cd "$1" && pwd)"
python3 -m venv "$tools_dir/python"
"$tools_dir/python/bin/pip" install --disable-pip-version-check 'ruff==0.16.1'
npm install --prefix "$tools_dir/node" --no-audit --no-fund 'eslint@10.8.0'
gem install rubocop --version 1.81.7 --install-dir "$tools_dir/gems" --bindir "$tools_dir/gems/bin" --no-document

curl --fail --location --retry 3 \
  https://github.com/pmd/pmd/releases/download/pmd_releases/7.27.0/pmd-dist-7.27.0-bin.zip \
  --output "$tools_dir/pmd.zip"
printf '%s  %s\n' 4ae396ffaf2b0d3ef0b73a10b2925e77066f73d57a4ce9078c60e7302bcddec9 "$tools_dir/pmd.zip" | sha256sum --check
unzip -q -o "$tools_dir/pmd.zip" -d "$tools_dir"

if [[ -n "${GITHUB_PATH:-}" && -n "${GITHUB_ENV:-}" ]]; then
  printf '%s\n' "$tools_dir/python/bin" "$tools_dir/node/node_modules/.bin" \
    "$tools_dir/gems/bin" "$tools_dir/pmd-bin-7.27.0/bin" >> "$GITHUB_PATH"
  printf 'GEM_HOME=%s\nGEM_PATH=%s\n' "$tools_dir/gems" "$tools_dir/gems" >> "$GITHUB_ENV"
fi
