#!/usr/bin/env bash
# Publish the tested cask only after its release archive is public.
set -euo pipefail

cask=${1:?usage: publish-cask.sh <generated-cask> <version>}
version=${2:?usage: publish-cask.sh <generated-cask> <version>}
path=repos/deploymenttheory/homebrew-tap/contents/Casks/macos-mcp-server.rb
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

current_sha=
if current_sha=$(gh api "$path" --jq .sha 2>/dev/null); then
  gh api "$path" --jq .content | base64 --decode > "$work/current.rb"
  if cmp -s "$work/current.rb" "$cask"; then
    echo "Homebrew cask already matches v$version"
    exit 0
  fi
fi

jq -n \
  --arg message "chore: update macos-mcp-server to v$version" \
  --arg content "$(base64 < "$cask" | tr -d '\n')" \
  --arg sha "$current_sha" \
  '{message: $message, content: $content} + (if $sha == "" then {} else {sha: $sha} end)' \
  > "$work/request.json"
gh api -X PUT "$path" --input "$work/request.json" --jq .content.path
