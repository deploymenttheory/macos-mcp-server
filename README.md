# macos-mcp-server

[![Spec compliance](https://github.com/deploymenttheory/macos-mcp-server/actions/workflows/mcp-spec-compliance.yml/badge.svg)](https://github.com/deploymenttheory/macos-mcp-server/actions/workflows/mcp-spec-compliance.yml)

A [Model Context Protocol](https://modelcontextprotocol.io) server that bridges
AI agents to the **macOS desktop**: the accessibility tree, synthetic mouse and
keyboard input, screenshots, window and application control, the shell,
preferences, launchd, processes, the filesystem and web scraping. No
computer-vision model is required: the agent perceives the UI through the
accessibility tree, the way assistive technology does.

It is the macOS counterpart of
[windows-mcp-server](https://github.com/deploymenttheory/windows-mcp-server):
the same 35 tools, toolsets, personas, policy documents and journeys. What
separates both from a plain automation bridge is that every agent action is
**gated on live device posture**, the agent's **network egress is constrained**,
and both are recorded in a tamper-evident audit chain the agent cannot switch
off.

- **Device policy engine**: every tool call, resource read and prompt fetch is
  evaluated against live device signals (MDM enrolment, Platform SSO, boot
  security, FileVault, SIP, the sealed system volume) before it runs, and
  refused, warned or contained by policy. Rules match by tool, toolset or
  annotation, so a screenshot is not gated like a shell command.
  → [Policy configuration](docs/policy-config.md)
- **Egress allowlist**: a loopback proxy admits only the domains you declare,
  optionally backed by pf rules so the console user, or the whole machine,
  cannot go around it. → [Egress](docs/egress.md)
- **Tamper-evident transparency**: a hash-chained audit log (keyed, anchored
  to the unified log), heartbeat, rug-pull detection, whole-session recording,
  evidence bundles and an out-of-band tiered kill switch. None of it is
  agent-disableable. → [Security architecture](docs/security-architecture.md)

> **Several tools (Shell, LaunchExecutable, Defaults, LaunchdJob, FileSystem,
> Process, App) have full user-level system access with no sandboxing.** That
> is the design. Run untrusted workloads in a guest, see
> [VM isolation](docs/vm-isolation.md), and read [SECURITY.md](SECURITY.md)
> for what is and is not in scope as a vulnerability.

**Requires macOS 27 or later** (Apple silicon or Intel; the binary is universal).

---

## Quick start

```sh
brew install deploymenttheory/tap/macos-mcp-server
macos-mcp-server permissions request      # grant Accessibility and Screen Recording once
```

Point any MCP client at the binary with the `stdio` subcommand:

```json
{
  "mcpServers": {
    "macos": {
      "command": "/opt/homebrew/bin/macos-mcp-server",
      "args": ["stdio", "--persona", "first-line-support"]
    }
  }
}
```

**→ [Getting started](docs/getting-started.md)** covers Claude Code, Cursor,
Codex CLI and Claude Desktop, and what to do before pointing this at a machine
you care about. **→ [Permissions](docs/permissions.md)** explains the privacy
grants and why the signing identity matters.

Not sure this is for you? If you have to **approve** it on a fleet, read
[Deciding to deploy this](docs/deployment-decision.md). If you have a **job to
do**, see the walk-throughs for a
[UI regression suite](docs/use-case-ui-regression.md) or a
[first-line support queue](docs/use-case-first-line-support.md).

---

## Features

| | What it does | Guide |
|---|---|---|
| **Desktop automation** | 35 tools across 13 toolsets: accessibility-tree perception, AX actions, synthetic input, screenshots, apps, windows, the shell, preferences, the filesystem, processes, launchd, the unified log, network, packages, scraping, plan-and-apply | [Toolsets and personas](docs/toolsets-and-personas.md) |
| **Personas** | Presets that select toolsets *and* inject workflow guidance | [Toolsets and personas](docs/toolsets-and-personas.md#personas) |
| **Credentials** | The agent signs in to apps and sites without ever being told the secret: the `Credentials` tool has no read mode, injection requires a secure text field, and the keychain items are removed on every exit path | [Credentials](docs/credentials.md) |
| **Session recording** | With `transparency.recording_dir` set, the whole session goes to one `.mov` with timeline markers | [Session recording](docs/recording.md) |
| **Journeys** | Declarative UI journeys validated offline, run through the planner, recorded from a human session | [Journeys](docs/journeys.md) |
| **Kill switch** | Out-of-band, tiered containment: isolate (pf), kill processes, lock, shut down, with the recording finalised and the credentials revoked first | [Security architecture](docs/security-architecture.md) |
| **MCP conformance** | Implemented protocol behavior checked against the latest published spec on every PR | [MCP compliance](docs/mcp-compliance.md) |

---

## Tools

| Toolset | Default | Tools |
|---|---|---|
| screen | yes | Snapshot, Screenshot, DisplayInventory, Recording |
| interaction | yes | Click, Type, Invoke, GetText, Scroll, Move, Shortcut, Wait, WaitFor, MultiSelect, MultiEdit |
| apps | yes | App |
| system | yes | Clipboard, Process, Notification |
| system-admin | | Defaults, LaunchdJob |
| shell | | Shell, LaunchExecutable |
| filesystem | | FileSystem |
| web | | Scrape |
| diagnostics | | SystemInfo, Service, UnifiedLog, Network |
| packages | | Package |
| credentials | | Credentials (enabled by `--credentials-file`) |
| testing | | Assert, CaptureEvidence |
| planning | | Plan, Apply |
| always | | GuardrailStatus, Kill |

Four names differ from the Windows server because they name a different
subsystem: `Defaults` (Registry), `Shell` (PowerShell), `LaunchdJob`
(ScheduledTask) and `UnifiedLog` (EventLog). Everything else, including the
journey vocabulary, is identical.

Resources: `macos://desktop/snapshot`, `macos://desktop/displays`,
`macos://session/recording`, `macos://system/info`. Prompts: `rpa-journey`,
`triage-support-issue`, `capture-evidence`.

```sh
macos-mcp-server personas                       # first-line-support, qa-test-engineer, business-user
macos-mcp-server stdio --persona qa-test-engineer
macos-mcp-server stdio --toolsets screen,interaction,apps --read-only
```

### What cannot be automated, by design

The login window, the lock screen, Touch ID and password sheets, TCC consent
dialogs, and the contents of secure text fields. These are the system's own
trust boundaries; the server does not try to cross them.

---

## Security

> 📐 [Security architecture](docs/security-architecture.md) ·
> 📄 [Policy configuration](docs/policy-config.md) ·
> 🔎 [Security assessment](docs/security-assessment.md)

A policy engine sits between the MCP client and the tools:

```
MCP client ──▶ audit ──▶ rug-pull ──▶ policy engine ──▶ tool handler
                                            │
                                      device signals
```

```sh
macos-mcp-server stdio --policy-config "/Library/Application Support/MacOSMCP/policy.json"
```

**With no `--policy-config` the built-in default applies**: the engine is
present, every declared signal is evaluated and every verdict recorded, and
nothing is refused. Adopting the engine cannot break a working deployment
before its policy is written.

| `on_fail` | Effect |
|---|---|
| `allow` | Proceeds; the failure is still recorded |
| `warn` | Proceeds, and the warning rides back with the result so the model sees it |
| `hold` | The call waits on an out-of-band human authoriser and fails closed |
| `deny` | This call is refused, and re-evaluated next time |
| `kill` | The kill switch trips and the containment ladder runs |

```sh
macos-mcp-server policy validate --policy-config policy.json   # document + signal ids; exit 1
macos-mcp-server policy check    --policy-config policy.json   # this device now; exit 2 if not admitted
macos-mcp-server policy explain  --policy-config policy.json --tool Shell
macos-mcp-server policy test     policy/examples/tests/*.json  # fixtures, in CI
macos-mcp-server audit verify    "/Library/Application Support/MacOSMCP/audit"
macos-mcp-server evidence bundle --dir ... --session 20261007-120000
```

Seven starting points ship in `policy/examples/`: `audit.json` (adopt first,
refuses nothing), `secure.json`, `enterprise.json`, `locked-down.json`,
`egress.json`, `dual-control.json` and `evidence-export.json`.

### Trust model

The local device signals are **auditable defence-in-depth, not a hard
boundary**: root can spoof them, and privacy grants are keyed on the signing
identity rather than on a hash of the binary. The containment layers raise the
cost of, and record, in-session compromise; pair them with MDM, Gatekeeper and
notarization, Platform SSO and Conditional Access, and FileVault. Two macOS
specifics are worth knowing before trusting a posture: pf filters by socket
owner, so the "scoped" egress tier bounds the console user rather than named
applications, and the posture signals are Windows-named with a documented
macOS reading. Both are spelled out in [the assessment](docs/security-assessment.md).

---

## MCP conformance

Every PR runs the required **MCP spec gate** against the latest published MCP
revision. It captures the product's actual advertised surface and raw wire
responses, checks the definitions and safe method calls against the published
schema, and fails if a newer revision needs assessment. The check covers the
features this server implements, without assigning a percentage for optional
features it does not implement. See [MCP spec gate](docs/mcp-compliance.md).

---

## Architecture

```
cmd/macos-mcp-server     cobra/viper CLI (stdio transport; conformance-serve only behind a tag)
internal/macmcp          RunStdio orchestration, probes, credentials, permissions, policy ops
internal/macdesktop      the engine: accessibility tree, CGEvent input, ScreenCaptureKit
                         screenshots, screencapture recording, keychain, launchd, log, network
internal/macguard        pf egress enforcer, networksetup system proxy, containment actuator
internal/clirunner       argv-only process runner with a rebuilt environment
internal/acceptance      the tart-lab acceptance suite (MACOS_MCP_ACC=1)
pkg/macos                tool definitions, toolsets, personas, resources, prompts
mcp-server-core          (imported) inventory, toolkit, surface, guardrail runtime, journeys,
                         schema loader, conformance fixtures and host
agentweave-harness       (imported) policy engine, audit chain, rug-pull, kill switch, egress proxy
policy/examples          starting-point policy documents, with fixtures under tests/
schema/                  vendored MCP protocol schemas, one directory per revision
```

All accessibility, event and AppKit work runs on the process main thread
through `Desktop.Do`; tool handlers receive their dependencies from the request
context via receiving middleware.

---

## Development

```sh
make check              # vet, test, lint
make sign-dev           # a stable local signing identity so TCC grants survive rebuilds
make conformance        # validate the implemented product behavior
make release-snapshot   # goreleaser dry run, unsigned
```

Releases are cut by release-please and built by `release.yml`: a universal
binary, Developer ID signed and notarized, with a CycloneDX SBOM, a keyless
cosign signature over the checksums, and a Homebrew cask in
`deploymenttheory/homebrew-tap`. See [CONTRIBUTING.md](CONTRIBUTING.md) and
[CLAUDE.md](CLAUDE.md).

---

## Documentation

**[docs/](docs/README.md)**: setup and configuration guides, the security
architecture and assessment, the journey vocabulary and the conformance report.

## License

MIT, see [LICENSE](LICENSE).
