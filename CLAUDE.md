# CLAUDE.md

Guidance for AI coding agents working in this repository. These are the
conventions that are load-bearing but not obvious from any single file — read
this before adding a tool, touching the desktop engine, or changing the security
subsystem. It mirrors windows-mcp-server's CLAUDE.md deliberately: the two
servers are one design on two platforms.

## What this is

An MCP server (stdio transport only) bridging AI agents to the macOS desktop:
the macOS counterpart of
[windows-mcp-server](https://github.com/deploymenttheory/windows-mcp-server),
built on `deploymenttheory/go-bindings-macosplatform` (purego bindings for
Apple's frameworks, no cgo), the shared `deploymenttheory/mcp-server-core`, the
`deploymenttheory/agentweave-harness` guardrails, and the official
`modelcontextprotocol/go-sdk`. Perception is the accessibility tree — there is
no CV model.

| Package | Role |
|---|---|
| `cmd/macos-mcp-server` | cobra CLI, viper `MACOS_MCP_*` env binding |
| `internal/macmcp` | `RunStdio` startup orchestration + the OS adapters (system/health probes, credentials, permissions) |
| `internal/macdesktop` | the AX/CGEvent/AppKit engine — owns the process main thread |
| `internal/macguard` | the macOS guardrail actuators: pf egress enforcer, system proxy, lock/shutdown/kill/isolate |
| `internal/clirunner` | argv-only process runner with the rebuilt environment |
| `pkg/macos` | tool definitions (one file per topic) + toolset/persona metadata |
| `mcp-server-core` (imported) | inventory engine, tool toolkit, MCP surface, guardrail runtime, journeys, schema loader, conformance host |
| `agentweave-harness` (imported) | the policy engine, audit chain, rug-pull detection, kill switch, egress proxy |
| `policy/examples` | starting-point policy documents (validated by the test suite) |
| `schema/` | vendored MCP protocol schemas + `versions.json` |
| `conformance/` | expected-failure baselines + committed suite results |

## Build, test, lint

```sh
go build ./...
go vet -unsafeptr=false ./...   # unsafeptr: the event-tap callback pointer idiom
go test ./... -count=1
GOARCH=amd64 CGO_ENABLED=0 go build ./...   # the (amd64 || arm64) tag is asserted everywhere
golangci-lint run --config=./.golangci.yml
make sign-dev && ./macos-mcp-server permissions check
```

CI is **macOS-only by design** (`.github/workflows/go-build-test.yml`, runner
`xcode-27`): nearly every file is `//go:build darwin`. The engine tests
self-skip when the process has no Accessibility grant or no console session, so
on a hosted runner CI guarantees compilation plus the pure-logic suites. Lint
runs on Linux with `GOOS=darwin`, which works because the SDK is pure Go.

**Local development needs `go.work`** (gitignored) pointing
`mcp-server-core` at the sibling checkout until its tag is published; `make
tidy` keeps `go.mod` free of a `replace`, which CI refuses.

Two lint settings shape how you work: `.golangci.yml` sets `new: true` with
`new-from-merge-base: origin/main` and `whole-files: true`, and the formatters
are `gofumpt` + `goimports` + `gci` + `golines`. The `gci` section order is
**Standard → Default → `Prefix(github.com/deploymenttheory)`**.

## The main-thread rule — the engine's central constraint

AppKit, the accessibility API and event posting all expect the process main
thread, and the SDK's `@MainActor` wrappers dispatch there. `main.go` locks the
OS thread in `init()` so `main` *is* the main thread; the `stdio` command runs
`RunStdio` on a goroutine and hands the main thread to `mainthread.DispatchMain()`
(which never returns — the server goroutine `os.Exit`s after teardown).

Rules that follow:

- **All** AX/CGEvent/AppKit work goes through `Desktop.Do`, which serialises
  callers and runs them on the main thread via `mainthread.Do`. `safeCall`
  recovers panics so one bad call cannot kill the engine.
- `Do` is **never** called from outside `internal/macdesktop`. Tool handlers
  call engine methods; the engine decides what runs on the main thread.
- CF references retained during a snapshot are released on the main thread
  when the snapshot is swapped. Never release from a caller goroutine.
- Screenshot and recording capture are deliberately thread-agnostic (the
  screenshot awaits ScreenCaptureKit's completion handler with a context; the
  recorder is a `screencapture -v` child process), which is what lets the
  recorder capture concurrently. Don't "fix" it by routing through `Do`.
- Subcommands that never touch AppKit (`policy test`, `journey validate`,
  `evidence verify`, `permissions check`) must not pump the run loop.

## TCC permissions and the signing identity

macOS gates the engine behind per-application consent: **Accessibility** (the
tree and synthetic input), **Screen Recording** (screenshots, recording, window
titles via the window server), **Full Disk Access** (the unified log store,
other users' preferences). `macos-mcp-server permissions check` reads them
live; `permissions request` triggers the prompts.

The grants are keyed on the binary's **code-signing identity**. An ad-hoc
signature's designated requirement is the binary's hash, so every rebuild
re-prompts. Dev builds are signed with a stable self-signed identity (`make
sign-dev`); releases are Developer ID signed and notarized, and `release.yml`
fails rather than falling back to ad-hoc. There are no entitlements: the file
in `entitlements/` is deliberately an empty dict.

## Shelling out: argv only, never a shell

Where the SDK has no binding (launchctl, log, profiles, pfctl, networksetup,
osascript, codesign, ...) the server calls the CLI through
`internal/clirunner`: an executable plus arguments, resolved against a PATH
rebuilt from `/etc/paths` and the Homebrew prefix (MCP hosts strip the
environment), with every `MACOS_MCP_*` variable withheld from the child. The
one path that hands text to an interpreter is `clirunner.RunScript`, used only
by the Shell tool, with the interpreter from a closed set and no rc files.

## Adding a tool

Every inventory tool uses `NewToolFromHandler` (`pkg/macos/dependencies.go`).
Canonical shape:

```go
func Clipboard() inventory.ServerTool {
    return NewToolFromHandler(
        ToolsetSystem,                                  // 1. toolset membership
        mcp.Tool{
            Name: "Clipboard", Description: "...",
            Annotations: &mcp.ToolAnnotations{Title: "Clipboard get/set", ReadOnlyHint: false},
            InputSchema: &jsonschema.Schema{Type: "object", /* ... */},
        },
        func(ctx context.Context, deps ToolDependencies, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
            args, err := toolkit.ArgsMap(req)              // 2. always ArgsMap first
            if err != nil { return toolkit.NewToolResultError(err.Error()), nil }
            text, err := deps.Desktop().ClipboardGet()   // 3. OS work via deps.Desktop()
            if err != nil { return toolkit.NewToolResultErrorFromErr("failed to read clipboard", err), nil }
            return toolkit.NewToolResultText(text), nil  // 4. result constructor, nil Go error
        },
    )
}
```

Checklist:

1. Topic file under `pkg/macos`, `func X() inventory.ServerTool`.
2. `Annotations.Title` plus a correct `ReadOnlyHint`; add `DestructiveHint` /
   `OpenWorldHint` pointers where apt.
3. Parse with `toolkit.ArgsMap` then the `toolkit` accessors — **never hand-roll
   arg parsing**. They coerce on purpose: Claude Desktop strips `anyOf` and
   stringifies bools/arrays.
4. Register in the correct comment group of `AllTools()` (`tools.go`).
5. **Bump `TestExpectedToolCount`** (`tools_test.go`) — a deliberate tripwire.
6. Extend `TestReadOnlyToolsAreSafe` / `TestExecutionPrimitivesAreAnnotatedDestructive`
   if it belongs in either list.
7. Set `DestructiveHint` honestly. Policy rules match on it; it is load-bearing
   metadata. `TestEveryWriteToolIsAnnotatedDestructive` makes this deny-by-default.

Tool names match windows-mcp-server's wherever semantics are platform-neutral.
Four are renamed because the Windows name denotes a Windows subsystem:
`Registry→Defaults`, `PowerShell→Shell`, `ScheduledTask→LaunchdJob`,
`EventLog→UnifiedLog`. Do not add Windows-named aliases: policy rules, audit
entries and personas reference the names, and a `Registry` tool on a Mac would
be a lie in an audit chain.

### The `IsError` convention — read this before returning an error

An **expected or user-facing failure returns an `IsError` result with a nil Go
error**, so the model can read the message and self-correct. A non-nil Go error
is reserved for genuine infrastructure failure. Use only the `toolkit` result
constructors.

### Dependency injection is middleware, not closures

`toolkit.InjectDepsMiddleware` puts the server's dependency set on the context;
handlers pull it via `MustDepsFromContext`. Receiving-middleware order,
outermost first: **inject-deps → cache-hints → audit → telemetry → rug-pull →
tool-policy**. Install the whole chain in **one** `Surface.InstallReceiving`
call; separate calls reverse the order (the SDK wraps each call around the
chain so far). `TestReceivingMiddlewareRunsOutermostFirst` pins it in core.

### Personas and toolsets

A persona (`toolsets.go`) is *only* a (toolset selection + read-only stance +
instructions text) preset over the one manifest. The IDs and memberships are
identical to windows-mcp-server's so a policy document means the same thing on
both platforms. Adding a persona never means adding tools.

## The security subsystem

The policy engine, audit chain, rug-pull detection, kill switch and egress proxy
come from agentweave-harness; the wiring comes from `mcp-server-core/runtime`;
this repo supplies the macOS probes and actuators (`internal/macmcp`,
`internal/macguard`). Everything is configured by a JSON document —
`--policy-config` is the only security flag. The invariants in
windows-mcp-server's CLAUDE.md ("the default must never refuse", "transparency
is never conditional on containment", the kill-ladder ordering, "refuse in the
shape the method requires") hold here unchanged and are pinned by the same
tests, now in core.

### macOS-specific: the posture probes are a mapping

`signals.HealthProbe` is Windows-shaped. `healthprobe_darwin.go` maps it:
Secure Boot → Apple silicon boot security (`bputil`/`csrutil`), TPM → Secure
Enclave presence, Device Guard (VBS/HVCI/Credential Guard) → SIP / sealed
system volume / Gatekeeper, BitLocker → FileVault per APFS volume, platform
attestation → unavailable. `docs/policy-config.md` spells out the reading;
keep it current. The `dsregcmd /status` command the harness's built-in signals
issue through `RunShell` is answered by a command table that synthesises the
expected shape from `profiles` and Platform SSO — it is a shim, documented as
such, until the harness grows platform-neutral names.

### macOS-specific: pf, not Windows Firewall

The egress enforcer (`internal/macguard/pf.go`) loads rules into a sub-anchor
under Apple's `com.apple/*` reference so `/etc/pf.conf` is never edited. pf
cannot match on an executable, so the "scoped" tier is **uid-scoped** and the
status surface says so. The invariants carry over, read for pf's last-match semantics: the block
is rendered first and the `quick` passes after it; restore runs in reverse; the
state file is written before any mutation and read on every start; `Suspend`
reloads the anchor with the block rules only and never restores; missing
elevation is fatal, not degraded. The actuator (`actuator.go`) uses its own
isolation anchor, kills by exact process name with TERM then KILL, locks
through System Events and shuts down through `shutdown(8)` as root. Live tests are gated by `MACOS_MCP_GLOBAL_BLOCK_TEST=1` and
`MACOS_MCP_SCOPED_TEST=1` — deliberately different variables.

## Credentials — the never-read invariant

Secrets live in the login keychain (`opinionated/tools/keychain`), are read as
UTF-16 code units, typed through `CGEventKeyboardSetUnicodeString`, and zeroed.
Only a coarse length band comes back. The `Credentials` tool has exactly
`list`/`verify`/`inject` — **never add a `get`/`read` mode**. The credentials
file must be `0600` and owned by the effective user; unlike Windows the Unix
bits are the real permission here. Removal runs on every shutdown path.

## MCP conformance

The server targets protocol revision **2026-07-28**, and the verdict comes from
the official suite (`github.com/modelcontextprotocol/conformance`, HTTP-only)
run by `.github/workflows/mcp-spec-compliance.yml` against `conformance-serve`,
which exists only behind `//go:build ... && conformance`. `go build ./...` must
never compile it; the workflow asserts this. One constructor (`surface.New`)
builds the server for stdio, the capture and the conformance host alike. Two
passes, recorded separately (product, fixtures), plus a 2025-11-25 backcompat
pass. Gate on the suite, never re-derive it; never reintroduce a score.

## Release

`release.yml` runs on a `v*` tag from release-please: goreleaser builds both
architectures, lipo's one universal binary, and the post hooks sign it
(`.github/scripts/sign.sh`: Developer ID, hardened runtime, timestamp, the
`com.deploymenttheory.macos-mcp-server` identifier TCC keys grants on) and
notarize it (`notarize.sh`). A release with a missing secret **fails**; it
never falls back to an ad-hoc signature, because that would make every user
re-grant Accessibility on update. `make release-snapshot` is the local dry run
(ad-hoc signed, notarization skipped, nothing published). Checksums are
cosign-signed keyless, each archive carries a syft SBOM, and a cask lands in
`deploymenttheory/homebrew-tap`. A bare Mach-O cannot be stapled; Gatekeeper
checks the notarization online on first run.

## Build tags

Everything is `//go:build darwin && (amd64 || arm64)` except nothing: the
platform-agnostic code lives in mcp-server-core. One extra tag, `conformance`,
adds the loopback HTTP host and the suite fixtures and nothing else.

## Notable gotchas

- **stdout is reserved** for the MCP stdio transport. Logs go to stderr or a
  file; the audit `stderr` destination writes `AUDIT {json}` lines to stderr.
- **Never import `opinionated/tools/oslog`** from the SDK: it is the one cgo
  package, and it would break `CGO_ENABLED=0` and the amd64 cross-compile.
- **Window titles from the window server need Screen Recording**;
  `CGWindowListCopyWindowInfo` returns empty names without it. Titles come
  from the accessibility tree, which needs only Accessibility.
- **`SMCopyAllJobDictionaries` and `CGWindowListCreateImage` are deprecated**
  and used only as flag-gated cross-checks behind `launchctl` and the
  ScreenCaptureKit screenshot path.
