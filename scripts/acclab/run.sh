#!/usr/bin/env bash
# Runs the acceptance suite inside a fresh clone of the golden lab image and
# deletes the clone afterwards (set MACOS_MCP_ACC_KEEP=1 to keep it).
#
#   scripts/acclab/run.sh [go test flags]
set -euo pipefail

GOLDEN=${MACOS_MCP_ACC_GOLDEN:-macos-mcp-golden}
VM=${MACOS_MCP_ACC_VM:-macos-mcp-acc-$$}
KEEP=${MACOS_MCP_ACC_KEEP:-0}
ROOT=$(cd "$(dirname "$0")/../.." && pwd)

tart clone "$GOLDEN" "$VM"
cleanup() {
  tart stop "$VM" 2>/dev/null || true
  if [ "$KEEP" != 1 ]; then tart delete "$VM"; else echo "kept $VM"; fi
}
trap cleanup EXIT

# A graphical session is required: the console must be logged in for the
# engine to drive it, so the guest runs with a display, not --no-graphics.
tart run "$VM" &
IP=""
for _ in $(seq 1 60); do
  IP=$(tart ip "$VM" 2>/dev/null || true)
  [ -n "$IP" ] && break
  sleep 2
done
[ -n "$IP" ] || { echo "guest never reported an address" >&2; exit 1; }

# The tests run inside the guest's console session via a gui/<uid> launchd
# job, because an ssh session has no desktop to drive.
sshpass -p admin ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "admin@$IP" \
  "mkdir -p ~/acc/src" 
sshpass -p admin rsync -a --exclude .git -e "ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null" \
  "$ROOT/" "admin@$IP:~/acc/src/"
sshpass -p admin ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "admin@$IP" \
  "cd ~/acc/src && launchctl asuser \$(id -u) env MACOS_MCP_ACC=1 MACOS_MCP_ACC_BINARY=\$HOME/acc/macos-mcp-server \
   go test ./internal/acceptance/ -count=1 -v $*"
