# macos-mcp-server

An MCP server that lets AI agents perceive and drive the macOS desktop, run
diagnostics and administration, and do it under an auditable guardrail stack.
It is the macOS counterpart of
[windows-mcp-server](https://github.com/deploymenttheory/windows-mcp-server):
the same tools, toolsets, personas, policy documents and journeys, on the
accessibility tree instead of UI Automation.

> **Status:** feature-complete against windows-mcp-server (35 tools, the
> guardrail stack, journeys, credentials); conformance evidence and the signed
> release pipeline land with v1.0.0. **Requires macOS 27 or later.**

## What it does

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

## Guardrails

Every session runs under a policy document — audit-only by default, never
refusing — with a hash-chained audit log, rug-pull detection, an out-of-band
kill switch, an optional egress proxy enforced through pf, and evidence
bundles. `--policy-config` is the only security flag; everything else is in
the document.

```sh
macos-mcp-server policy validate --policy-config policy/examples/secure.json
macos-mcp-server policy check    --policy-config policy/examples/secure.json
macos-mcp-server policy explain  --tool Shell
macos-mcp-server policy test     policy/examples/tests/*.json
macos-mcp-server audit verify    "/Library/Application Support/MacOSMCP/audit"
macos-mcp-server evidence bundle --dir ... --session 20261007-120000
```

See `docs/policy-config.md` for the macOS reading of each posture signal,
`docs/egress.md` for the pf tiers, and `docs/credentials.md` for the keychain
model.

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
