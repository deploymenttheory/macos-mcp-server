# Permissions

macOS gates desktop automation behind per-application consent, enforced by the
Transparency, Consent and Control subsystem (TCC). There is no Windows
equivalent: a Windows process in an interactive session can read the UI
Automation tree and post input by virtue of being there, while a macOS process
can do neither until the user, or an MDM profile, has said so for that
particular binary. This page is the per-tool matrix, how to check and request
the grants, why code signing decides whether they stick, and how to manage them
on a fleet.

- [The grants](#the-grants)
- [Which tool needs what](#which-tool-needs-what)
- [Checking and requesting](#checking-and-requesting)
- [Why signing matters](#why-signing-matters)
- [Managing grants with MDM](#managing-grants-with-mdm)
- [Resetting a grant](#resetting-a-grant)

---

## The grants

| Grant | System Settings pane | What it unlocks here | Required? |
|---|---|---|---|
| **Accessibility** | Privacy & Security > Accessibility | The accessibility tree (`AXUIElement`) and posting synthetic input (`CGEvent`) | Yes, for every default toolset |
| **Screen Recording** | Privacy & Security > Screen & System Audio Recording | ScreenCaptureKit capture, and window titles read through the window server (`CGWindowListCopyWindowInfo` returns empty names without it) | Yes, for `Screenshot`, `Recording`, `CaptureEvidence` |
| **Automation** (Apple Events to System Events) | Privacy & Security > Automation | Anything the server does through `osascript ... tell application "System Events"` | For `Notification`, the lock rung of the kill ladder, and non-root shutdown |
| **Full Disk Access** | Privacy & Security > Full Disk Access | The paths macOS protects: other users' preference domains, Mail, Messages and Safari data, the per-user TCC database itself | No; it widens `Defaults`, `FileSystem` and `UnifiedLog`, it gates nothing in the default toolsets |
| **A console session** | not a grant; a property of how the process was started | A window server session to drive | Yes, for every desktop toolset; see [what running without one changes](toolsets-and-personas.md#what-running-without-a-console-session-changes) |

`permissions check` treats Accessibility, Screen Recording and the console
session as required and exits 2 if any is missing. Full Disk Access is reported
as optional. Automation is granted per target application the first time the
server sends an Apple Event, and is not probed.

Window titles in a `Snapshot` come from the accessibility tree, which needs only
Accessibility; Screen Recording is needed only where pixels or the window
server are involved.

---

## Which tool needs what

| Tool | Accessibility | Screen Recording | Automation | Full Disk Access |
|---|---|---|---|---|
| `Snapshot`, `GetText`, `Invoke` | yes | | | |
| `Click`, `Type`, `Scroll`, `Move`, `Shortcut`, `MultiSelect`, `MultiEdit` | yes | | | |
| `Wait`, `WaitFor`, `Assert` | yes (they read the tree) | | | |
| `App` | yes (switch, resize and hide go through the tree) | | | |
| `Screenshot`, `Recording`, `CaptureEvidence` | yes (`CaptureEvidence` also snapshots) | yes | | |
| `DisplayInventory` | | | | |
| `Credentials` (`inject`) | yes | | | |
| `Notification` | | | yes (System Events) | |
| `Clipboard`, `Process` | | | | |
| `Defaults` | | | | other users' domains only |
| `FileSystem` | | | | protected directories only |
| `UnifiedLog` | | | | some entries in the persisted store |
| `LaunchdJob`, `Service`, `SystemInfo`, `Network`, `Shell`, `LaunchExecutable`, `Package`, `Scrape`, `Plan`, `Apply` | | | | |
| `journey run`, `journey record` | yes | yes (evidence screenshots) | | |
| kill ladder: `lock` | | | yes | |
| kill ladder: `shutdown` | | | yes, unless root | |

`Shell` and `LaunchExecutable` carry no grant of their own, but a child they
start inherits the server's TCC identity for the Apple Events and file-access
checks, so a script that calls `osascript` or reads a protected path is judged
as the server.

---

## Checking and requesting

```sh
macos-mcp-server permissions check
```

```
Accessibility:      granted
Screen Recording:   MISSING
Full Disk Access:   MISSING (optional)
Console session:    granted
User:               rpa (elevated: false)
Signing:            Developer ID Application: Deployment Theory (com.deploymenttheory.macos-mcp-server, team XXXXXXXXXX)
  ! Screen Recording is not granted: System Settings > Privacy & Security > Screen & System Audio Recording
```

`check` reads every grant live and prompts for nothing. `--json` emits the same
facts as a document for a health probe. The exit code is 2 when a required grant
is missing, so it can gate a deployment script the way `policy check` gates a
posture.

```sh
macos-mcp-server permissions request
```

`request` calls `CGRequestPostEventAccess` and `CGRequestScreenCaptureAccess`,
which is what registers an unlisted binary in the Accessibility and Screen
Recording panes and shows the system prompts. The prompts are asynchronous:
macOS shows them and returns at once, so the report printed afterwards usually
still says `MISSING` until you act in System Settings. Run `check` again when
you have.

The server itself prompts for nothing at startup. It runs `CheckPermissions`,
logs each warning to stderr, and serves whatever the grants allow; a tool that
needs a missing grant returns an `IsError` result naming it, so the model can
tell the operator instead of guessing.

---

## Why signing matters

A TCC grant is attached to a **code-signing identity**, not to a path. macOS
records the binary's designated requirement when the grant is made and checks
the running binary against it on every call.

| Build | Designated requirement | What happens to the grants |
|---|---|---|
| Unsigned | none | Nothing can be granted; `check` warns `the binary is unsigned` |
| Ad-hoc (`go build` alone, or `codesign -s -`) | the hash of this exact binary | Survive only until the next build; `check` warns `privacy grants ... will not survive the next build` |
| `make sign-dev` | identifier `com.deploymenttheory.macos-mcp-server` and your self-signed "macOS MCP Dev" certificate | Survive every rebuild on your machine |
| Release (Homebrew cask, GitHub tarball) | the same identifier and the Developer ID Application certificate, notarized | Survive every upgrade on every machine |

`make sign-dev` is the developer loop. Create the identity once in Keychain
Access (Certificate Assistant > Create a Certificate, name `macOS MCP Dev`,
type Code Signing), then:

```sh
make sign-dev                                  # build, sign with the identity, print the signature
./macos-mcp-server permissions check
```

Set `DEV_SIGN_IDENTITY` to use a certificate with another name. The signing
step applies `entitlements/macos-mcp-server.entitlements`, which is deliberately
an empty dictionary: Accessibility, event posting, screen capture and the
keychain are gated by consent, not by entitlements, and an entitlement added
there is a change to what the binary may do and should show up in a diff.

Two consequences of identity-keyed grants:

- **Moving the binary does not lose the grant**; rebuilding it with a different
  signature does. Replacing `/opt/homebrew/bin/macos-mcp-server` with a locally
  built, ad-hoc signed copy loses the grant the release build held.
- **A grant made to the dev identity is not a grant to the release.** They are
  two entries in the Accessibility pane, and you can tell them apart by the
  identity `check` prints.

Releases are Developer ID signed and notarized by the release workflow, which
fails rather than falling back to an ad-hoc signature.

---

## Managing grants with MDM

On a managed fleet the grants are pre-approved with a **Privacy Preferences
Policy Control** (PPPC) configuration profile, delivered by the MDM, so no
user has to click through System Settings and no standard user is blocked by a
pane they cannot change. The profile identifies the binary by:

| Field | Value |
|---|---|
| Identifier | `com.deploymenttheory.macos-mcp-server` |
| Identifier type | `bundleID` |
| Code requirement | the output of `codesign -dr - /opt/homebrew/bin/macos-mcp-server` |

The identifier is the signing identifier the server carries (it is a bare
Mach-O, not an app bundle, but PPPC keys on the signing identifier and that is
what `bundleID` means here). The code requirement must be the release build's,
so take it from the binary you ship, not from a dev build.

```sh
codesign -dr - /opt/homebrew/bin/macos-mcp-server
# designated => identifier "com.deploymenttheory.macos-mcp-server" and anchor apple generic and ... /* Developer ID */
```

What a profile can do for each service:

| Service | PPPC key | MDM can |
|---|---|---|
| Accessibility | `Accessibility` | **Allow** outright |
| Automation to System Events | `AppleEvents` with receiver `com.apple.systemevents` | **Allow** outright |
| Full Disk Access | `SystemPolicyAllFiles` | **Allow** outright |
| Screen Recording | `ScreenCapture` | Only `AllowStandardUserToSetSystemService`: Apple does not let MDM grant Screen Recording silently, so a user still approves it once, but a standard user is allowed to do so without an administrator |

That last row is the one to plan around. The first run on each managed Mac
needs one click in the Screen Recording pane from whoever is signed in, after
which the grant is keyed on the Developer ID identity and persists across
upgrades. Where that is unacceptable (an unattended lab VM), the per-user TCC
database can be seeded directly, which needs SIP off and is only sensible on a
disposable guest; see [Acceptance testing](acceptance-testing.md).

The profile should be scoped to the device, installed before the server is
first launched, and reissued whenever the Developer ID certificate rotates,
because the code requirement names the certificate.

---

## Resetting a grant

```sh
tccutil reset Accessibility com.deploymenttheory.macos-mcp-server
tccutil reset ScreenCapture  com.deploymenttheory.macos-mcp-server
tccutil reset AppleEvents    com.deploymenttheory.macos-mcp-server
tccutil reset All            com.deploymenttheory.macos-mcp-server
```

Resetting removes the entry; the next `permissions request` or tool call that
needs it re-registers the binary and prompts again. A grant made by a PPPC
profile cannot be reset by the user; remove or change the profile.

---

## Related

- [Getting started](getting-started.md): install and the first `permissions check`
- [Toolsets and personas](toolsets-and-personas.md): the tool surface the grants unlock
- [Deployment](deployment.md): installing on managed Macs
