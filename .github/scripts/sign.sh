#!/usr/bin/env bash
# Signs the arm64 binary for release: Developer ID, hardened runtime,
# secure timestamp, the fixed identifier TCC keys grants on, and the
# (deliberately empty) entitlements file.
#
#   sign.sh <binary> <is-snapshot>
#
# A snapshot build (goreleaser --snapshot, the local dry run) is ad-hoc
# signed so it still runs; a real release with no APPLE_SIGNING_IDENTITY
# fails here rather than shipping an ad-hoc binary, because an ad-hoc
# signature makes every user re-grant Accessibility on every update.
set -euo pipefail

BINARY=${1:?usage: sign.sh <binary> <is-snapshot>}
SNAPSHOT=${2:-false}
ENTITLEMENTS=entitlements/macos-mcp-server.entitlements
IDENTIFIER=com.deploymenttheory.macos-mcp-server

if [ "$SNAPSHOT" = "true" ]; then
  echo "snapshot build: ad-hoc signing $(basename "$BINARY")"
  codesign --force --sign - --identifier "$IDENTIFIER" --entitlements "$ENTITLEMENTS" "$BINARY"
  exit 0
fi

: "${APPLE_SIGNING_IDENTITY:?APPLE_SIGNING_IDENTITY is required for a release build}"
codesign --force --sign "$APPLE_SIGNING_IDENTITY" --options runtime --timestamp \
  --identifier "$IDENTIFIER" --entitlements "$ENTITLEMENTS" "$BINARY"
codesign --verify --strict --verbose=2 "$BINARY"
echo "signed $(basename "$BINARY") as $APPLE_SIGNING_IDENTITY"
