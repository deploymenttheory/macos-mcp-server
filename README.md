# macos-mcp-server

An MCP server that lets AI agents perceive and drive the macOS desktop, run
diagnostics and administration, and do it under an auditable guardrail stack.
It is the macOS counterpart of
[windows-mcp-server](https://github.com/deploymenttheory/windows-mcp-server):
the same tools, toolsets, personas, policy documents and journeys, on the
accessibility tree instead of UI Automation.

> **Status:** under construction toward v1.0.0 — feature parity with
> windows-mcp-server. The milestone table is in `CLAUDE.md`'s companion plan;
> this README grows with each milestone. **Requires macOS 27 or later.**

## What it will do

| Toolset | Tools |
|---|---|
| screen (default) | Snapshot, Screenshot, DisplayInventory, Recording |
| interaction (default) | Click, Type, Invoke, GetText, Scroll, Move, Shortcut, Wait, WaitFor, MultiSelect, MultiEdit |
| apps (default) | App |
| system (default) | Clipboard, Process, Notification |
| system-admin | Defaults, LaunchdJob |
| shell | Shell, LaunchExecutable |
| filesystem | FileSystem |
| web | Scrape |
| diagnostics | SystemInfo, Service, UnifiedLog, Network |
| packages | Package |
| credentials | Credentials |
| testing | Assert, CaptureEvidence |
| planning | Plan, Apply |
| always | GuardrailStatus, Kill |

## Permissions

macOS gates desktop automation behind per-application consent. Check what
this machine has granted, and trigger the prompts:

```sh
make sign-dev                       # a stable local signing identity, so grants survive rebuilds
./macos-mcp-server permissions check
./macos-mcp-server permissions request
```

See `docs/permissions.md` once it lands for the per-tool matrix.

## Development

```sh
make check      # vet + test + lint
make build      # ./macos-mcp-server
```

The server is built on [go-bindings-macosplatform](https://github.com/deploymenttheory/go-bindings-macosplatform),
[mcp-server-core](https://github.com/deploymenttheory/mcp-server-core),
[agentweave-harness](https://github.com/deploymenttheory/agentweave-harness)
and the official [go-sdk](https://github.com/modelcontextprotocol/go-sdk).
Read `CLAUDE.md` for the conventions.

## License

MIT — see [LICENSE](LICENSE).
