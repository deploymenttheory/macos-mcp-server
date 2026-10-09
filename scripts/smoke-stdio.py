#!/usr/bin/env python3
"""Check the packaged binary's MCP handshake without a desktop/TCC grant."""

import json
import selectors
import subprocess
import sys


def main() -> int:
    if len(sys.argv) != 3:
        print("usage: smoke-stdio.py <binary> <version>", file=sys.stderr)
        return 2

    process = subprocess.Popen(
        [sys.argv[1], "stdio", "--toolsets", "diagnostics"],
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        bufsize=0,
    )

    def send(message: dict) -> None:
        assert process.stdin is not None
        process.stdin.write((json.dumps(message) + "\n").encode())
        process.stdin.flush()

    def receive(expected_id: int) -> dict:
        assert process.stdout is not None
        ready = selectors.DefaultSelector()
        ready.register(process.stdout, selectors.EVENT_READ)
        if not ready.select(timeout=30):
            raise RuntimeError(f"timed out waiting for MCP response {expected_id}")
        response = json.loads(process.stdout.readline())
        if response.get("id") != expected_id or "error" in response:
            raise RuntimeError(f"unexpected MCP response: {response}")
        return response["result"]

    try:
        send(
            {
                "jsonrpc": "2.0",
                "id": 1,
                "method": "initialize",
                "params": {
                    "protocolVersion": "2026-07-28",
                    "capabilities": {},
                    "clientInfo": {"name": "release-smoke", "version": "1"},
                },
            }
        )
        initialized = receive(1)
        if initialized.get("serverInfo", {}).get("version") != sys.argv[2]:
            raise RuntimeError(f"wrong server version: {initialized.get('serverInfo')}")
        send({"jsonrpc": "2.0", "method": "notifications/initialized"})
        send({"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": {}})
        tools = receive(2).get("tools", [])
        if not tools:
            raise RuntimeError("server returned no tools")
        print(f"MCP initialized as {sys.argv[2]} with {len(tools)} diagnostic tools")
        return 0
    except Exception as error:
        process.terminate()
        try:
            _, stderr = process.communicate(timeout=5)
        except subprocess.TimeoutExpired:
            process.kill()
            _, stderr = process.communicate()
        print(f"MCP smoke failed: {error}\n{stderr.decode(errors='replace')}", file=sys.stderr)
        return 1
    finally:
        if process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()


if __name__ == "__main__":
    sys.exit(main())
