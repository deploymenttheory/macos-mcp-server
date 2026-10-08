#!/usr/bin/env bash
# Notarizes a signed Mach-O binary with Apple's notary service.
#
# A bare binary cannot be stapled (only bundles, disk images and installer
# packages carry a ticket), so Gatekeeper validates the notarization online
# when the binary first runs. The submission is what makes that lookup
# succeed; `--wait` makes a rejection fail this build rather than a user's
# first launch.
#
# Required environment: APPLE_API_KEY_ID, APPLE_API_ISSUER_ID and
# APPLE_API_KEY_P8 (the App Store Connect API key, PEM content). Missing
# credentials fail loudly: a release must never ship unnotarized by accident.
set -euo pipefail

BINARY=${1:?usage: notarize.sh <signed-binary> <is-snapshot>}
if [ "${2:-false}" = "true" ]; then
  echo "snapshot build: notarization skipped"
  exit 0
fi
: "${APPLE_API_KEY_ID:?APPLE_API_KEY_ID is required}"
: "${APPLE_API_ISSUER_ID:?APPLE_API_ISSUER_ID is required}"
: "${APPLE_API_KEY_P8:?APPLE_API_KEY_P8 is required}"

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
KEY="$WORK/AuthKey_${APPLE_API_KEY_ID}.p8"
printf '%s' "$APPLE_API_KEY_P8" > "$KEY"
chmod 600 "$KEY"

codesign --verify --strict --verbose=2 "$BINARY"

ZIP="$WORK/$(basename "$BINARY").zip"
ditto -c -k --keepParent "$BINARY" "$ZIP"

xcrun notarytool submit "$ZIP" \
  --key "$KEY" --key-id "$APPLE_API_KEY_ID" --issuer "$APPLE_API_ISSUER_ID" \
  --wait --timeout 30m

echo "notarized $(basename "$BINARY")"
