# Toolsets and personas

What each tool does, how to select a subset, and what "customising a persona"
actually means.

The toolset IDs, the persona IDs and their memberships are identical to
[windows-mcp-server](https://github.com/deploymenttheory/windows-mcp-server)'s,
so a policy document, a persona or a journey means the same thing on both
platforms. Four tools carry a different name because the Windows name denotes a
Windows subsystem: `Registry` is `Defaults`, `PowerShell` is `Shell`,
`ScheduledTask` is `LaunchdJob` and `EventLog` is `UnifiedLog`. There are no
aliases: a `Registry` entry in a Mac's audit chain would be a lie.

- [Toolset reference](#toolset-reference)
- [Choosing a selection](#choosing-a-selection)
- [Personas](#personas)
- [Customising](#customising)
- [What running without a console session changes](#what-running-without-a-console-session-changes)

---

## Toolset reference

Thirteen toolsets holding 35 tools. Four toolsets are on by default; the rest
are opt-in because they reach further than looking at the screen. Every tool in
`screen`, `interaction`, `apps` and `testing` needs the Accessibility grant, and
anything that captures pixels needs Screen Recording; the per-tool matrix is in
[Permissions](permissions.md).

### `screen` (default)

| Tool | Does |
|---|---|
| `Snapshot` | The foreground window, the open windows, and a labeled tree of interactive elements from the accessibility API. The perception primitive; most loops start here |
| `Screenshot` | A PNG of every display, through ScreenCaptureKit |
| `DisplayInventory` | Connected displays: bounds in points, work area, backing resolution, scale factor |
| `Recording` | Session-recording status, and timeline markers |

### `interaction` (default)

| Tool | Does |
|---|---|
| `Invoke` | Acts through an element's accessibility action: invoke, set_value, toggle, select, expand/collapse. **Prefer this**: it does not depend on window focus |
| `Click` | Synthetic mouse click (CGEvent) |
| `Type` | Synthetic keystrokes |
| `GetText` | Reads an element's name or value |
| `Scroll`, `Move`, `Shortcut` | Wheel, pointer move, key chords |
| `Wait`, `WaitFor` | Fixed delay; wait for a condition: `active_window`, `element_exists`, `text_exists` |
| `MultiSelect`, `MultiEdit` | Batch selection (Command-click) and batch field entry |

### `apps` (default)

| Tool | Does |
|---|---|
| `App` | Launch by name, bundle identifier, `.app` path or URL; switch to, resize, hide, unhide or quit an application |

Launching an arbitrary executable by path is not here. That is `LaunchExecutable`
in the `shell` toolset, because on a Mac a bare binary is a shell-grade capability
rather than an app.

### `system` (default)

| Tool | Does |
|---|---|
| `Clipboard` | Get or set the general pasteboard text |
| `Process` | List and terminate processes (an application is asked to quit; `force` sends SIGKILL). Without root, only this user's processes |
| `Notification` | Post a user notification through System Events |

### `system-admin` (opt-in)

| Tool | Does |
|---|---|
| `Defaults` | Read and write preference domains, what `defaults(1)` does. **Destructive** |
| `LaunchdJob` | List / get / run / enable / disable / delete / create launchd jobs, in the user (`gui/<uid>`) or system domain. **Destructive** |

Split from `system`, and off by default, because both outlive the session: a
launchd job registers a program at login or on a schedule and a preference write
changes application or system configuration, so either survives the session,
the kill switch and a reboot. `first-line-support` carries it explicitly because
its job description includes editing preferences; the other personas do not.

### `shell` (opt-in)

| Tool | Does |
|---|---|
| `Shell` | Runs a script with `zsh` (default), `bash` or `osascript`. **Destructive, full access as the running user** |
| `LaunchExecutable` | Starts a program by full path with arguments and a working directory. **Destructive** |

The interpreter comes from a closed set and runs with no rc files; the script
text is the only thing handed to an interpreter anywhere in the server.

### `filesystem` (opt-in)

| Tool | Does |
|---|---|
| `FileSystem` | read / write / copy / move / delete / list / search / info. **Destructive**. Relative paths resolve against the Desktop |

### `web` (opt-in)

| Tool | Does |
|---|---|
| `Scrape` | Fetches a URL and returns text. Routed through the session's egress proxy when one runs, and subject to `enforce_https` |

### `diagnostics` (opt-in)

| Tool | Does |
|---|---|
| `SystemInfo` | OS, hardware, memory, disk, SIP, FileVault and MDM-enrollment inventory |
| `Service` | List / start / stop / restart / enable / disable launchd services, user or system domain (system needs root). **Destructive** |
| `UnifiedLog` | Query the unified log via `log show`: subsystem / process / level / text / look-back |
| `Network` | Inspect interfaces, DNS, IP configuration, proxies and Wi-Fi, and test connectivity. `test` is **open-world** (reaches the network outside the egress proxy) |

Also carries the `macos://system/info` resource and the `triage-support-issue`
prompt.

### `testing` (opt-in)

| Tool | Does |
|---|---|
| `Assert` | PASS/FAIL a UI condition. Subjects: `screen.text`, `window.title`, `window`, `element` and its properties, `result.text`; operators from `is` and `contains` through `matches`, `exists` and the numeric comparisons. The verification primitive for journeys. (Note the vocabulary differs from `WaitFor`'s three conditions) |
| `CaptureEvidence` | Screenshot plus accessibility tree, labeled |

Also carries the `capture-evidence` prompt.

### `planning` (opt-in)

| Tool | Does |
|---|---|
| `Plan` | Propose a whole sequence of tool calls; returns a change manifest and a plan id. Changes nothing |
| `Apply` | Execute a proposed plan by id: verbatim, posture re-checked, fail-stop |

### `packages` (opt-in)

| Tool | Does |
|---|---|
| `Package` | List / search / install / uninstall software via Homebrew (formulae and casks), a local `.pkg` through the macOS installer, and `softwareupdate`. **Destructive** and **open-world** (installs download from the network, outside the egress proxy) |

In **no persona**: a persona never installs software silently. Enable it
explicitly with `--toolsets` when that is the job.

### `credentials` (opt-in)

| Tool | Does |
|---|---|
| `Credentials` | list / verify / inject. Secrets live in the login keychain and are typed, never returned. See [Credentials](credentials.md) |

Enabled automatically by `--credentials-file`, **additively**: your other
toolsets are kept.

### Always served

`GuardrailStatus` and `Kill` belong to no toolset and are present under every
persona and every selection. They are the agent's read-only view of the security
posture and its way to stop the session. Neither can actuate containment the
policy did not configure.

---

## Choosing a selection

Four knobs, applied in this order:

```sh
--persona qa-test-engineer          # a named preset: toolsets + read-only stance + instructions
--toolsets screen,interaction,web   # explicit toolsets; 'all' and 'default' are special
--tools GetText,Assert              # add individual tools, bypassing toolset filtering
--exclude-tools Shell,Defaults      # remove specific tools, applied last
```

`--exclude-tools` always wins. Use it to carve a dangerous tool out of an
otherwise convenient toolset:

```sh
# everything except the shell
macos-mcp-server stdio --toolsets all --exclude-tools Shell,LaunchExecutable
```

`--read-only` exposes only tools annotated read-only. It is the blunt instrument;
a persona's own stance is usually better, and note that passing `--read-only`
explicitly overrides a persona's stance in either direction. `--tools` does **not**
escape `--read-only`: a write tool added there is still filtered.

`--tools` bypasses toolset filtering, which is fine when you are composing a
surface by hand. But a **persona is a documented guarantee** about what is
served, so `--tools` naming a tool outside the active persona's toolsets is
**refused at startup** (and recorded as `tools.persona_bypass.denied` in the
audit chain) rather than silently widening the persona. Select `--toolsets`
explicitly instead of a persona if you want to add to that set.

Each flag is also an environment variable: `MACOS_MCP_PERSONA`,
`MACOS_MCP_TOOLSETS`, `MACOS_MCP_TOOLS`, `MACOS_MCP_EXCLUDE_TOOLS`,
`MACOS_MCP_READ_ONLY`.

### Resources and prompts follow their toolset

This surprises people: resources and prompts are filtered like tools.

| Surface | Toolset | Present by default? |
|---|---|---|
| `macos://desktop/snapshot` | screen | Yes |
| `macos://desktop/displays` | screen | Yes |
| `macos://session/recording` | screen | Yes |
| `macos://system/info` | diagnostics | **No** |
| `rpa-journey` prompt | interaction | Yes |
| `triage-support-issue` prompt | diagnostics | **No** |
| `capture-evidence` prompt | testing | **No** |

So `business-user` (no diagnostics) serves no `triage-support-issue`, and
`first-line-support` (no testing) serves no `capture-evidence`. If a prompt you
expect is missing, check whether its toolset is enabled.

---

## Personas

A persona is a preset over one manifest: a toolset selection, a read-only
stance, and instructions text injected into the server's own instructions so the
model adopts that workflow. **Adding a persona never adds tools.**

```sh
macos-mcp-server personas          # list them
macos-mcp-server stdio --persona qa-test-engineer
```

| Persona | Toolsets | Built for |
|---|---|---|
| `first-line-support` | screen, interaction, apps, system, system-admin, shell, diagnostics | Diagnose before acting: `SystemInfo`, `Process`, `Service`, `Shell`; say what you will change and why before `Defaults` or stopping a launchd service |
| `qa-test-engineer` | screen, interaction, apps, system, filesystem, web, testing | Deterministic UI tests: label targeting, `Assert`, `CaptureEvidence` |
| `business-user` | screen, interaction, apps, web, testing | End-user journeys through the real UI, one observable step at a time, preferring `Invoke` over raw input. No shell, preferences or filesystem |

The `business-user` instructions also tell the model what cannot be automated
on a Mac: the login window, the lock screen, Touch ID and password sheets for
privileged operations, and the privacy (TCC) consent dialogs. The persona
assumes an unlocked, signed-in session with the grants already made.

The workflow prompts build their text from the matching persona's instructions
rather than restating it, so `--persona` and the prompts cannot drift apart.

---

## Customising

**Personas are compiled in.** They live in `pkg/macos/toolsets.go`; there is no
file or flag that adds one. Changing a persona means editing that map and
rebuilding (and re-signing; see [Permissions](permissions.md)).

For anything short of that, compose the flags instead. It covers most of what
people want a custom persona for:

```sh
# business-user, but with filesystem for evidence handling
macos-mcp-server stdio --persona business-user --toolsets screen,interaction,apps,web,testing,filesystem

# qa-test-engineer minus the destructive tools
macos-mcp-server stdio --persona qa-test-engineer --exclude-tools FileSystem,Process
```

If you do add a persona in a fork, keep its ID and membership identical to the
Windows server's if the same ID exists there: a policy document that names a
persona is expected to mean the same thing on both platforms. The inventory
engine in `mcp-server-core` is domain-agnostic and does the filtering; the
persona map and the toolset constants are the only things you need to touch.

---

## What running without a console session changes

A process with no graphical login session cannot drive a desktop: there is no
window server session to snapshot or post events into. That is the state of a
LaunchDaemon, a process started over ssh, or a CI job with no logged-in user,
and it is the macOS analogue of a Windows service in Session 0. It is detected
(through `CGSessionCopyCurrentDictionary`), not declared, and the toolset
selection is replaced wholesale:

```
system, shell, filesystem, diagnostics, web
```

Two consequences worth knowing:

- **A persona explicitly requested without a session is refused.** The server
  exits with `persona requires a graphical login session` rather than serving a
  reshaped version of it, so a scripted deployment that asks for
  `business-user` and lands in a LaunchDaemon fails loudly instead of quietly
  serving shell and filesystem.
- **Without a persona, the replacement is applied and announced.** The server
  warns on stderr at startup and the `server.configured` audit entry records
  the toolsets actually served. There is no notification: with no session there
  is nobody to show it to.

Credentials are also not auto-enabled without a session, and
`permissions check` reports `Console session: MISSING` so the condition can be
seen before a deployment is scripted.

---

## Related

- [Getting started](getting-started.md): install, permissions and client setup
- [Permissions](permissions.md): which grant each tool needs
- [Policy configuration](policy-config.md): gating tools on device posture
- [Credentials](credentials.md): the `credentials` toolset
