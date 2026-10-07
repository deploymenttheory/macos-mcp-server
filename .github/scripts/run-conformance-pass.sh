#!/usr/bin/env bash
# Runs one pass of the official MCP conformance suite against a freshly
# started conformance host, then stops it.
#
# The host's lifetime is scoped to this script rather than spread across
# workflow steps: a runner may reap a step's process tree when the step ends,
# and a host that is already gone makes every scenario report "fetch failed",
# which reads as a catastrophic protocol failure and is actually an empty
# socket.
#
# Readiness is proved with a real server/discover POST rather than a TCP
# connect: under the stateless transport a GET returns 405, and a connect
# only proves the listener exists.
#
# Exits with the suite's own exit code: 0 when every result matched the
# baseline, 1 on an unexpected failure or a stale baseline entry.
set -euo pipefail

usage() {
  echo "usage: $0 --name NAME --host-bin PATH --port PORT --spec-version V --harness-version V \\" >&2
  echo "          --baseline FILE --output-dir DIR --log-dir DIR [--fixtures]" >&2
  exit 2
}

NAME= HOST_BIN= PORT= SPEC_VERSION= HARNESS_VERSION= BASELINE= OUTPUT_DIR= LOG_DIR= FIXTURES=0
while [ $# -gt 0 ]; do
  case "$1" in
    --name) NAME=$2; shift 2 ;;
    --host-bin) HOST_BIN=$2; shift 2 ;;
    --port) PORT=$2; shift 2 ;;
    --spec-version) SPEC_VERSION=$2; shift 2 ;;
    --harness-version) HARNESS_VERSION=$2; shift 2 ;;
    --baseline) BASELINE=$2; shift 2 ;;
    --output-dir) OUTPUT_DIR=$2; shift 2 ;;
    --log-dir) LOG_DIR=$2; shift 2 ;;
    --fixtures) FIXTURES=1; shift ;;
    *) usage ;;
  esac
done
[ -n "$NAME" ] && [ -n "$HOST_BIN" ] && [ -n "$PORT" ] && [ -n "$SPEC_VERSION" ] && \
  [ -n "$HARNESS_VERSION" ] && [ -n "$BASELINE" ] && [ -n "$OUTPUT_DIR" ] && [ -n "$LOG_DIR" ] || usage

mkdir -p "$OUTPUT_DIR" "$LOG_DIR"
STDOUT="$LOG_DIR/$NAME.out"
STDERR="$LOG_DIR/$NAME.err"

HOST_ARGS=(conformance-serve --addr "127.0.0.1:$PORT")
if [ "$FIXTURES" = 1 ]; then HOST_ARGS+=(--fixtures); fi

echo "starting conformance host for '$NAME' on 127.0.0.1:$PORT (fixtures=$FIXTURES)"
"$HOST_BIN" "${HOST_ARGS[@]}" >"$STDOUT" 2>"$STDERR" &
HOST_PID=$!

cleanup() {
  if kill -0 "$HOST_PID" 2>/dev/null; then
    kill -INT "$HOST_PID" 2>/dev/null || true
    sleep 1
    kill -KILL "$HOST_PID" 2>/dev/null || true
  fi
  # The host's own log is evidence too: it carries the audit chain for the run.
  if [ -f "$STDERR" ]; then
    echo "--- conformance host log ($NAME) ---"
    tail -n 40 "$STDERR"
  fi
}
trap cleanup EXIT

BODY='{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"'"$SPEC_VERSION"'","io.modelcontextprotocol/clientCapabilities":{}}}}'
READY=0
for _ in $(seq 1 40); do
  if ! kill -0 "$HOST_PID" 2>/dev/null; then
    cat "$STDERR" || true
    echo "::error::conformance host for '$NAME' exited during startup"
    exit 1
  fi
  CODE=$(curl -s -o /dev/null -w '%{http_code}' -X POST "http://127.0.0.1:$PORT/mcp" \
    -H 'Mcp-Method: server/discover' -H "Mcp-Protocol-Version: $SPEC_VERSION" \
    -H 'Accept: application/json, text/event-stream' -H 'Content-Type: application/json' \
    --max-time 5 --data "$BODY" || true)
  if [ "$CODE" = "200" ]; then READY=1; break; fi
  sleep 0.5
done
if [ "$READY" != 1 ]; then
  cat "$STDERR" || true
  echo "::error::conformance host for '$NAME' never became ready on port $PORT"
  exit 1
fi
echo "host for '$NAME' ready; running the suite at spec $SPEC_VERSION"

# --suite all, not active: the suite classifies 2026-07-28 as its draft
# revision, so `active` excludes exactly the scenarios that revision added.
set +e
npx -y "@modelcontextprotocol/conformance@$HARNESS_VERSION" server \
  --url "http://127.0.0.1:$PORT/mcp" \
  --suite all --spec-version "$SPEC_VERSION" \
  --expected-failures "$BASELINE" \
  --output-dir "$OUTPUT_DIR"
EXIT=$?
set -e
echo "pass '$NAME' finished with exit code $EXIT"
exit $EXIT
