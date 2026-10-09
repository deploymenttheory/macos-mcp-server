#!/usr/bin/env bash
# Homebrew 7 accepts casks only from taps; stage the generated file in a
# disposable local tap for audit and (after publication) an install smoke test.
set -euo pipefail

cask=${1:?usage: check-cask.sh <generated-cask> [--install]}
mode=${2:-}
tap=local/macos-mcp-distribution-check
brew tap-new "$tap"
tap_dir=$(brew --repository "$tap")
trap 'brew untap "$tap" >/dev/null 2>&1 || true' EXIT
mkdir -p "$tap_dir/Casks"
cp "$cask" "$tap_dir/Casks/macos-mcp-server.rb"
brew audit --cask --strict "$tap/macos-mcp-server"
if [ "$mode" = "--install" ]; then
  brew install --cask "$tap/macos-mcp-server"
fi
