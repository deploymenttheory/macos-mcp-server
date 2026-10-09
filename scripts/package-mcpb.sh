#!/usr/bin/env bash
# Pack the already signed and notarized release binary for Claude Desktop.
set -euo pipefail

if [ "$#" -ne 3 ]; then
  echo "usage: $0 <binary> <version-without-v> <output.mcpb>" >&2
  exit 2
fi

binary=$1
version=$2
output=$3
[[ -f "$binary" ]] || { echo "binary not found: $binary" >&2; exit 1; }
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+([-.+][a-zA-Z0-9.-]+)?$ ]] || {
  echo "invalid version: $version" >&2
  exit 1
}

stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
mkdir -p "$stage/server" "$(dirname "$output")"
cp -p "$binary" "$stage/server/macos-mcp-server"
jq --arg version "$version" '.version = $version' \
  packaging/mcpb/manifest.json > "$stage/manifest.json"

mcpb validate "$stage"
mcpb pack "$stage" "$output"
mcpb info "$output"

# A bundle must carry the exact signed Mach-O that the tarball and cask use.
if [ "$(shasum -a 256 "$binary" | cut -d ' ' -f 1)" != \
     "$(unzip -p "$output" server/macos-mcp-server | shasum -a 256 | cut -d ' ' -f 1)" ]; then
  echo "MCPB binary differs from release binary" >&2
  exit 1
fi
