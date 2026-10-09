# Getting started

From nothing to an MCP client driving the macOS desktop, then the two things
worth doing before you point it at anything real.

- [Install](#install)
- [Grant permissions](#grant-permissions)
- [First run](#first-run)
- [Connect a client](#connect-a-client)
- [Upgrade or recover](#upgrade-or-recover)
- [Choose what the agent can do](#choose-what-the-agent-can-do)
- [Before anything real](#before-anything-real)

---

## Install

Requires macOS 27 or later on Apple silicon. Apple does not offer macOS 27 for
[Intel Macs](https://support.apple.com/en-us/127455).

For Codex or Claude Code, install the command-line binary:

```sh
brew install deploymenttheory/tap/macos-mcp-server
```

The cask installs a signed and notarized arm64 binary. Find its actual path
with `command -v macos-mcp-server`; client configuration needs that absolute
path. The same binary ships as
`macos-mcp-server_<version>_darwin_arm64.tar.gz` on the
[GitHub release](https://github.com/deploymenttheory/macos-mcp-server/releases),
with a checksums file and a cosign signature, for machines without Homebrew.

For Claude Desktop, download `macos-mcp-server_<version>_darwin_arm64.mcpb`
from that release and use **Settings > Extensions > Advanced settings > Install
Extension**. This package includes the signed binary; it does not require
Homebrew. Install the extension or configure the Homebrew binary manually in
Claude Desktop, so the same server is not listed twice.

To build from source you need Go (the version in `go.mod`) and, so that the
privacy grants survive a rebuild, a local code-signing identity:

```sh
go build -o macos-mcp-server ./cmd/macos-mcp-server
make sign-dev          # signs with the "macOS MCP Dev" identity; see permissions.md
```

One binary, no runtime dependencies. Recording uses ScreenCaptureKit and the
system H.264 and HEVC encoders; there is nothing to install for it.

---

## Grant permissions

This is the step that has no Windows equivalent. macOS gates desktop automation
behind per-application consent, and nothing in the default toolsets works until
the binary has been granted **Accessibility** (the tree and synthetic input) and
**Screen Recording** (screenshots, recording, window titles from the window
server).

```sh
macos-mcp-server permissions check      # reads every grant live; exit 2 if a required one is missing
macos-mcp-server permissions request    # triggers the system prompts, then reports again
```

`request` gets the binary listed in **System Settings > Privacy & Security**;
you still switch each grant on there. The grants are keyed on the binary's
code-signing identity, so the Homebrew and release builds keep them across
upgrades, and an ad-hoc signed `go build` loses them on every rebuild.
The Claude Desktop bundle requests these grants when it first launches; switch
them on in System Settings, then restart Claude Desktop and check its extension
status. Its first request can return before you have approved the prompts.
[Permissions](permissions.md) has the per-tool matrix, the signing story and
how to pre-approve with an MDM profile.

---

## First run

The server speaks MCP over stdio, so running it by hand just waits on stdin.
That is enough to confirm it starts:

```sh
macos-mcp-server stdio
# Ctrl-C to stop
```

Startup logs go to stderr; stdout belongs to the transport. You should see the
device policy load, the permission warnings (if any) and the toolsets resolve.

Check what it would serve, and what the device looks like:

```sh
macos-mcp-server personas                    # the presets
macos-mcp-server policy check                # this device, against the default policy
```

`policy check` is deliberately slow: `profiles`, `csrutil`, `fdesetup` and
`bputil` all run.

---

## Connect a client

Everything after `--` (or in `args`) is the command the client launches. Use the
absolute path to the binary because a client's environment may differ from
your shell's:

```sh
SERVER="$(command -v macos-mcp-server)"
test -n "$SERVER" && "$SERVER" --version
```

### Claude Code

```sh
claude mcp add --scope user macos -- "$SERVER" stdio --persona business-user
```

Or commit a project-scoped `.mcp.json`:

```json
{
  "mcpServers": {
    "macos": {
      "command": "/absolute/path/from/command-v/macos-mcp-server",
      "args": ["stdio", "--persona", "first-line-support",
               "--policy-config", "/Library/Application Support/MacOSMCP/policy.json"]
    }
  }
}
```

Verify with `claude mcp list`; inside a session, `/mcp` shows the connection
and tools. A saved config alone does not prove the server has started.

### Cursor

`.cursor/mcp.json` in the project, or `~/.cursor/mcp.json` for all projects,
then enable it under **Settings > MCP**:

```json
{
  "mcpServers": {
    "macos": {
      "command": "/absolute/path/from/command-v/macos-mcp-server",
      "args": ["stdio", "--persona", "qa-test-engineer"]
    }
  }
}
```

### Codex CLI

```sh
codex mcp add macos -- "$SERVER" stdio --persona business-user
codex mcp list
```

Or `~/.codex/config.toml`:

```toml
[mcp_servers.macos]
command = "/absolute/path/from/command-v/macos-mcp-server"
args = ["stdio", "--persona", "business-user"]

[mcp_servers.macos.env]
MACOS_MCP_OVERLAY = "true"
```

### Claude Desktop

Install the release `.mcpb` under **Settings > Extensions > Advanced settings >
Install Extension**. After granting macOS permissions, restart Claude Desktop
and check the extension's connection and tools in Developer settings. The
bundle selects the `business-user` persona. If Anthropic accepts the extension
into its directory, it will also appear under **Browse extensions**.

For a Homebrew installation instead, add this to `claude_desktop_config.json`
(Settings > Developer > Edit Config), replacing the command with the output of
`command -v macos-mcp-server`:

```json
{
  "mcpServers": {
    "macos": {
      "command": "/absolute/path/from/command-v/macos-mcp-server",
      "args": ["stdio", "--persona", "business-user"]
    }
  }
}
```

## Upgrade or recover

For the Homebrew installation, run `brew upgrade --cask
deploymenttheory/tap/macos-mcp-server` and confirm `macos-mcp-server --version`.
Client entries keep using the same Homebrew command path. For Claude Desktop,
install the newer `.mcpb` from its GitHub release; privately installed bundles
are updated manually, while directory-installed extensions update through
Claude Desktop. Keep only one Claude Desktop entry for this server.

The Developer ID signing identity remains the same across releases, so macOS
privacy grants should persist. If a client stops connecting, check its MCP
status, run `macos-mcp-server permissions check` for Homebrew installs, and
inspect the client's MCP logs. For a bad public release, install a known-good
version from its immutable GitHub release while a fixed patch is prepared;
published release assets and tags are not replaced.

### Any other stdio client

Command: the absolute path to the binary. First argument: `stdio`. Everything
else is optional configuration. There is no URL to configure; MCP is spoken over
stdio only. (The policy document can stand up two loopback HTTP listeners, the
status endpoint and the [egress proxy](egress.md), but neither carries MCP.)

> **Flags and variables:** every `stdio` flag is also an environment variable,
> `MACOS_MCP_<FLAG>` with dashes replaced by underscores, so `--policy-config`
> is `MACOS_MCP_POLICY_CONFIG`. That is how a client whose config has no `args`
> field is configured. The child process never sees these variables: the
> server withholds every `MACOS_MCP_*` variable from anything it launches.

> **The client runs in your session.** The server drives whatever desktop the
> client process is attached to. A client launched from a terminal over ssh
> has no console session, and the server will serve only the non-desktop
> toolsets. See [what running without a console session changes](toolsets-and-personas.md#what-running-without-a-console-session-changes).

---

## Choose what the agent can do

The default selection is `screen`, `interaction`, `apps`, `system`: enough to
see the desktop and drive it, without a shell, the filesystem, or anything that
persists across a reboot.

```sh
--persona qa-test-engineer            # a preset: toolsets + stance + instructions
--toolsets all --exclude-tools Shell,Defaults
--read-only                           # only read-only tools
```

See [Toolsets and personas](toolsets-and-personas.md) for what each tool does and
how the knobs combine.

---

## Before anything real

Two things, in this order.

### 1. Understand the blast radius

`Shell`, `LaunchExecutable`, `Defaults`, `LaunchdJob`, `FileSystem`, `Process`
and `App` have **full access as the user the server runs as, with no
sandboxing**. That is the design, not an oversight. The server is a plain
command-line binary: it has no App Sandbox entitlement, and TCC consent is the
only thing macOS puts between it and your data. For untrusted workloads run the
whole thing in a disposable virtual machine; see [VM isolation](vm-isolation.md).

### 2. Write a policy, starting in audit mode

With no `--policy-config`, the built-in default evaluates every declared signal
and records every verdict but **refuses nothing**. That is deliberate: adopting
the engine cannot break a working deployment before its policy is written.

Start from the shipped example that refuses nothing, and watch what it would do:

```sh
sudo mkdir -p "/Library/Application Support/MacOSMCP"
sudo cp policy/examples/audit.json "/Library/Application Support/MacOSMCP/policy.json"
macos-mcp-server policy validate --policy-config "/Library/Application Support/MacOSMCP/policy.json"
macos-mcp-server stdio           --policy-config "/Library/Application Support/MacOSMCP/policy.json"
```

Every verdict is written to the audit log including the `intended` severity, so
you can see exactly what `"mode": "enforce"` would have refused before you switch
it on. When the log is quiet, flip the mode.

```sh
macos-mcp-server policy explain --policy-config policy.json --tool Shell
```

`explain` prints which rules cover a tool and what they require, evaluating
nothing, so a refusal in the field is attributable without re-running probes.

---

## Where next

| If you want to | Read |
|---|---|
| Know which grant each tool needs, and pre-approve them with MDM | [Permissions](permissions.md) |
| Gate tools on device posture | [Policy configuration](policy-config.md) |
| Restrict which domains the device may reach | [Egress control](egress.md) |
| Let the agent sign in without seeing secrets | [Credentials](credentials.md) |
| Deploy this on managed Macs | [Deployment](deployment.md) |
| Run untrusted workloads | [VM isolation](vm-isolation.md) |
