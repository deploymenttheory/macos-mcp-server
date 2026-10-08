# Deployment

Putting this on a Mac you care about: where things live, what needs to be locked
down, and what the security model assumes you have already done.

- [Layout](#layout)
- [Locking down the files](#locking-down-the-files)
- [Running at login](#running-at-login)
- [Pairing with OS controls](#pairing-with-os-controls)
- [Rollout order](#rollout-order)

---

## Layout

Nothing here is enforced by the server; it takes paths as configuration. But the
security model assumes the agent cannot rewrite its own policy, and that only
works if the files sit somewhere the automated user cannot modify.

```
/opt/homebrew/bin/
    macos-mcp-server                the Homebrew cask (/usr/local/bin on an Intel Mac); root-owned

/Library/Application Support/MacOSMCP/
    policy.json                     the device policy
    allowed-devices.txt             the serial allowlist, if a rule uses one
    audit/                          the audit chain, one sealed file per session plus the manifest
    recordings/                     session video and marker sidecars
    evidence/                       sealed evidence bundles
    control/                        the kill-switch control directory
    egress-state.json               written by the server; do not edit
    pf-anchor.conf                  written by the server; do not edit

~/.config/macos-mcp/
    creds.json                      credentials (if used): must be 0600 and owned by the running user

~/Library/LaunchAgents/
    com.deploymenttheory.macos-mcp-server.plist     if the server is started at login
```

The last two files under `MacOSMCP` are not yours to place: the egress enforcer
owns them, creates the directory root-only, writes `egress-state.json` before
its first `pfctl` call and reads it on every start, which is how a session
killed mid-run cannot leave the console user blocked. The shipped policy
examples (`policy/examples/*.json`) already use these paths.

The credentials file lives in the home directory on purpose: the server refuses
one that is readable by group or others, or owned by anyone but the user it runs
as, and a file under `/Library` owned by that user would be the odd one out.

---

## Locking down the files

The important asymmetry: the **user the agent runs as** must be able to *read*
the policy and *not* be able to *write* it. Otherwise a compromised session can
rewrite the rules that govern it.

```sh
sudo chown -R root:wheel "/Library/Application Support/MacOSMCP"
sudo chmod 755 "/Library/Application Support/MacOSMCP"
sudo chmod 644 "/Library/Application Support/MacOSMCP/policy.json"      # readable by the agent's user, writable by root only
```

Then the state the server has to write as the agent's user. Give those
directories to that account and nobody else:

```sh
for d in audit recordings evidence control; do
  sudo install -d -o rpa -g staff -m 700 "/Library/Application Support/MacOSMCP/$d"
done
```

The credentials file is tightened the other way, and the server refuses to
start if it is not:

```sh
umask 077
mkdir -p ~/.config/macos-mcp
cp creds.json ~/.config/macos-mcp/creds.json       # ends up 0600, owned by you
```

Unlike Windows, the Unix mode bits are the real permission here; see
[Credentials](credentials.md).

**The audit directory needs write access for the agent's user** but should not
be freely rewritable. macOS has append-only file flags (`chflags uappnd`), but
the directory sink creates a new session file per run and rewrites its
manifest, so the flag does not fit. If tamper-evidence matters more than
convenience, ship entries off the box (`transparency.anchor` writes each chain
head to the unified log, and `audit_destination` can be a file your log
collector tails) and treat the local copy as a buffer. The hash chain is what
detects editing; the permission bits only raise the cost.

---

## Running at login

The server drives an interactive desktop, so it needs a console session. It
cannot usefully run as a LaunchDaemon: with no window server session it drops
every desktop-automation toolset and refuses any persona (see
[what running without a console session changes](toolsets-and-personas.md#what-running-without-a-console-session-changes)).
A LaunchDaemon is the macOS analogue of a Windows service running as SYSTEM in
Session 0, and the behaviour is the same.

For an unattended RPA Mac, the usual arrangement is:

1. **Automatic login** to a dedicated, low-privilege account (System Settings >
   Users & Groups > Automatically log in as; not available when FileVault is
   on, so weigh disk encryption against unattended boot).
2. **Keep the session unlocked.** The lock screen is the login window, which
   nothing in a user session can see or drive, so disable the screen saver
   lock and display sleep for that account (`Defaults` or a profile).
3. **A per-user LaunchAgent** that starts the client at login, so the server is
   present before the journey starts.

LaunchAgents run in the `gui/<uid>` domain, inside the console session, with
that user's TCC grants, which is exactly what the server needs. A LaunchDaemon
does not. The server speaks stdio, so whatever `ProgramArguments` names is the
owner of its stdin and stdout: start the thing that drives it (the
agentweave-harness, or your agent runner) and let that spawn
`macos-mcp-server stdio` as its child. A bare `stdio` server under launchd has
no client and exits when its stdin closes.

`~/Library/LaunchAgents/com.deploymenttheory.macos-mcp-server.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.deploymenttheory.macos-mcp-server</string>
  <key>ProgramArguments</key>
  <array>
    <string>/usr/local/bin/agentweave-harness</string>
    <string>--server</string>
    <string>/opt/homebrew/bin/macos-mcp-server</string>
    <string>--</string>
    <string>stdio</string>
    <string>--persona</string>
    <string>business-user</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>MACOS_MCP_POLICY_CONFIG</key>
    <string>/Library/Application Support/MacOSMCP/policy.json</string>
  </dict>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardErrorPath</key>
  <string>/Library/Application Support/MacOSMCP/audit/launchagent.log</string>
</dict>
</plist>
```

```sh
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.deploymenttheory.macos-mcp-server.plist
launchctl print gui/$(id -u)/com.deploymenttheory.macos-mcp-server    # state, last exit status
launchctl bootout   gui/$(id -u)/com.deploymenttheory.macos-mcp-server
```

The harness command line above is illustrative; take the real one from the
[agentweave-harness](https://github.com/deploymenttheory/agentweave-harness)
documentation. The point is the shape: the client is the launchd job, the
server is its child, and both run in the console session.

> Automatic login stores the account's password in
> `/etc/kcpassword`, readable by root. Use a dedicated account with no rights
> beyond the machine, and treat the machine as disposable.

### When you do need root

Four things require it: the pf egress tiers (`egress.applications`,
`egress.block_all_outbound`, and `set_system_proxy`, which needs an
administrator for `networksetup`), the kill ladder's network-isolation rung,
`shutdown(8)` for the shutdown rung, and the system domain of `Service` and
`LaunchdJob`. Everything else works as a normal user, and the kill ladder
degrades honestly: a rung that needs root is skipped and audited rather than
failing the trip, and shutdown falls back to asking System Events.

Egress is the one that fails rather than degrades: a policy that asks for a pf
tier without root is a startup error, never a silently advisory proxy. See
[Egress control](egress.md).

Running the whole server as root widens what a compromised session can do, and
root launched from a LaunchDaemon has no console session at all. The workable
shape for root with a desktop is `sudo` from inside the console session (an
agent runner that elevates for the server), which keeps the session; the TCC
grants are keyed on the binary's identity, so run `permissions check` under the
same `sudo` to confirm what root actually holds before relying on it. If you
only need the egress tiers, that is the trade to weigh: enforcement that cannot
be bypassed, against a process with more privilege.

---

## Pairing with OS controls

The security architecture is explicit that the local device signals are
**auditable defense-in-depth, not a hard boundary**: a local administrator can
spoof them. It only adds up if you also have:

- **Binary authorization**, so the binary that runs is the binary you shipped
  and the agent cannot execute arbitrary tooling. Gatekeeper and notarization
  cover software that arrives through the normal channels; an MDM restriction
  or an allowlisting agent such as Santa covers everything else. This is the
  control that matters most, because `Shell`, `LaunchExecutable` and `App` can
  otherwise launch anything.
- **Developer ID signing** on the binary, which the release carries, so an
  allowlist and the TCC grants have something durable to key on.
- **A PPPC profile** from the MDM, so the grants are made once, centrally, and
  a standard user cannot remove or widen them. See
  [Permissions](permissions.md#managing-grants-with-mdm).
- **SIP and the sealed system volume** left on. The `vbs` and `hvci` policy
  signals read them, and they are what keeps `Defaults` and `LaunchdJob` from
  reaching the system's own configuration.
- **Conditional Access** through Platform SSO or your MDM's compliance
  integration, so a device that drops out of compliance loses access to the
  resources the agent would reach: enforcement that does not depend on this
  process behaving.
- **FileVault**, since the credentials file, the keychain and the recordings
  sit on disk. The `bitlocker` signal reads it per APFS volume.

Without these, the policy engine is a good audit trail and a real speed bump,
but a local admin can go around it.

---

## Rollout order

Each step is safe to sit on for a while.

1. **Grant the permissions.** `permissions check` on every target machine
   exits 0; with an MDM, that means the PPPC profile is installed and the one
   Screen Recording click has been made.
2. **Audit mode, no enforcement.** Copy `policy/examples/audit.json`, point the
   server at it, and let it run. Every verdict is recorded with the severity
   enforcing *would* have applied.
3. **Read the audit log.** Look for `policy.decided` entries whose intended
   verdict is deny. Those are the calls that will break when you flip the mode.
4. **Switch to `"mode": "enforce"`** once that list is empty or understood.
5. **Arm the kill triggers** you want, one at a time. Watch for
   `killswitch.disarmed`: that is a trigger firing while its switch is off, and
   it tells you what would have happened.
6. **Add containment actions** last. `isolate` before `lock` before `shutdown`,
   remembering that `isolate` needs root.
7. **Egress**, working up the tiers (proxy-only, then uid-scoped, then global)
   with [its own verification at each step](egress.md). pf cannot match on an
   executable, so the scoped tier blocks the whole console user; read the
   status surface's `scope: user` as exactly that.

At every stage, `policy validate` runs in CI and `policy check` gates a health
probe:

```sh
macos-mcp-server policy validate --policy-config policy.json   # exits 1 on a bad document
macos-mcp-server policy check    --policy-config policy.json   # exits 2 if this device is not admitted
macos-mcp-server permissions check                             # exits 2 if a required grant is missing
```

---

## Related

- [Getting started](getting-started.md): install and first run
- [Permissions](permissions.md): the grants, signing, and PPPC profiles
- [Policy configuration](policy-config.md): the document schema and the macOS reading of each signal
- [Egress control](egress.md): the pf tiers and what root buys
- [VM isolation](vm-isolation.md): for untrusted workloads
