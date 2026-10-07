#!/usr/bin/env bash
# Provisions the acceptance lab: a macOS 27 guest under tart with the
# dev-signed binary installed and the privacy grants it needs pre-seeded.
#
#   scripts/acclab/provision.sh [--golden NAME] [--base IMAGE] [--vm NAME]
#
# The guest is cloned from a golden image so a run always starts from the
# same state; `run.sh` clones the golden image again and deletes the clone
# afterwards. Apple's MDM profile cannot pre-grant Screen Recording (a PPPC
# profile can only let a standard user grant it), so the lab seeds the TCC
# database directly inside the guest, which needs SIP disabled there. A lab
# guest is the only place that is acceptable.
set -euo pipefail

GOLDEN=${MACOS_MCP_ACC_GOLDEN:-macos-mcp-golden}
BASE=${MACOS_MCP_ACC_BASE:-ghcr.io/cirruslabs/macos-tahoe-base:latest}
VM=${MACOS_MCP_ACC_VM:-macos-mcp-acc}
while [ $# -gt 0 ]; do
  case "$1" in
    --golden) GOLDEN=$2; shift 2 ;;
    --base) BASE=$2; shift 2 ;;
    --vm) VM=$2; shift 2 ;;
    *) echo "unknown flag $1" >&2; exit 2 ;;
  esac
done

command -v tart >/dev/null || { echo 'tart is required: brew install cirruslabs/cli/tart' >&2; exit 1; }
ROOT=$(cd "$(dirname "$0")/../.." && pwd)

if ! tart list | awk '{print $2}' | grep -qx "$GOLDEN"; then
  echo "cloning $BASE -> $GOLDEN"
  tart clone "$BASE" "$GOLDEN"
fi

echo "building and signing the binary under test"
(cd "$ROOT" && make sign-dev >/dev/null)

echo "starting $GOLDEN"
tart run --no-graphics "$GOLDEN" &
TART_PID=$!
trap 'kill $TART_PID 2>/dev/null || true' EXIT
IP=""
for _ in $(seq 1 60); do
  IP=$(tart ip "$GOLDEN" 2>/dev/null || true)
  [ -n "$IP" ] && break
  sleep 2
done
[ -n "$IP" ] || { echo "guest never reported an address" >&2; exit 1; }
SSH="ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null admin@$IP"

# The guest default credentials are admin/admin on the cirruslabs images.
echo "installing the binary and seeding TCC in the guest at $IP"
sshpass -p admin scp -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
  "$ROOT/macos-mcp-server" "admin@$IP:/tmp/macos-mcp-server"
REQ=$(codesign -dr - "$ROOT/macos-mcp-server" 2>&1 | sed -n 's/^designated => //p')
sshpass -p admin $SSH "sudo mkdir -p ~/acc && sudo mv /tmp/macos-mcp-server ~/acc/ && \
  sudo sqlite3 ~/Library/Application\ Support/com.apple.TCC/TCC.db \
  \"INSERT OR REPLACE INTO access (service, client, client_type, auth_value, auth_reason, auth_version, csreq, flags) \
   VALUES ('kTCCServiceAccessibility', 'com.deploymenttheory.macos-mcp-server', 0, 2, 4, 1, NULL, 0), \
          ('kTCCServiceScreenCapture', 'com.deploymenttheory.macos-mcp-server', 0, 2, 4, 1, NULL, 0);\" && \
  ~/acc/macos-mcp-server permissions check --json" || {
  echo "seeding TCC failed; the guest needs SIP disabled (csrutil disable in Recovery) for the lab" >&2
  exit 1
}
echo "designated requirement: $REQ"
echo "stopping $GOLDEN"
tart stop "$GOLDEN"
echo "golden image $GOLDEN is ready; run scripts/acclab/run.sh"
