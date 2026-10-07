# Use case: a first-line support queue that triages itself

**The job:** a queue of "my Mac is doing X" tickets, most of which are the same
handful of checks and fixes: is the launchd service running, what does the
unified log say, is the VPN up, restart the thing. First-line time goes on
gathering state before anyone can act.

**Why this helps:** the agent can gather that state and perform the routine fixes
the way a technician would, through the real desktop, and every action is gated
on device posture and recorded, so "the agent restarted a service on a managed
laptop" is a policy decision and an audit entry, not an unlogged side effect.

This is the `first-line-support` persona: **diagnose before acting**.

## Start it

```sh
macos-mcp-server stdio --persona first-line-support
```

It serves `screen`, `interaction`, `apps`, `system`, `system-admin`, `shell` and
`diagnostics`, so the agent can read system state (`SystemInfo`, `Process`,
`Service`, `UnifiedLog`, `Network`), act through the UI, edit a preference domain
with `Defaults` or a job with `LaunchdJob`, and drop to `Shell` (zsh, bash or
AppleScript, argv-only, no rc files) for the checks a tool does not cover, with
the workflow guidance to look before it leaps.

The machine needs the Accessibility grant for the tree and synthetic input, and
Screen Recording for screenshots; `macos-mcp-server permissions check` says
which are missing. The login window, the lock screen, Touch ID and password
sheets, and privacy consent dialogs cannot be driven, so the ticket has to start
from an unlocked, signed-in session.

## Gate it on posture: the point of the security model

First-line support runs on real user machines, so this is exactly where you want
the policy engine. A starting shape, using the macOS reading of each signal from
[policy configuration](policy-config.md):

```jsonc
{
  "mode": "enforce",
  "rules": [
    { "name": "managed-device",      "match": { "toolset": "*" },     "require": ["mdm-enrolled", "run-context"], "on_fail": "deny" },
    { "name": "filevault-for-shell", "match": { "toolset": "shell" }, "require": ["bitlocker"],                   "on_fail": "deny" }
  ]
}
```

`mdm-enrolled` reads `profiles status -type enrollment`, `run-context` requires a
console login session, and `bitlocker` is answered by FileVault on every APFS
volume (the signal keeps the harness's name; the shipped `enterprise.json`
example uses it the same way). Now the agent triages freely on a healthy managed
device, and refuses the shell on one whose posture has slipped, with the refusal
recorded and re-evaluated next time, so a device that recovers restores service
on its own. Prove the rules with `policy test` fixtures before rollout:

```sh
macos-mcp-server policy test policy/examples/tests/*.json
```

## Signing in without handing over secrets

If a fix needs the agent to sign in somewhere, `--credentials-file` installs the
secret into the login keychain and the `Credentials` tool **injects** it as
keystrokes into a secure text field; the model never receives the plaintext.

**One caveat that will stop startup, by design.** `first-line-support` carries the
`shell` toolset, and `security find-generic-password -w` can read a stored
credential back out of the keychain, which would defeat the never-read guarantee.
So combining `--credentials-file` with this persona **refuses to start** unless
the policy acknowledges the exposure:

```jsonc
"credentials": { "acknowledge_toolset_exposure": ["shell"] }
```

That makes the residual risk a deliberate, audited choice. If you do not need the
agent to sign in, do not supply a credentials file and the question never arises.
See [Credentials](credentials.md).

## What you get on the record

- **The audit chain**: every check and fix, with argument digests, verifiable
  after the fact with `macos-mcp-server audit verify`, and anchored to the
  unified log when the policy asks for it.
- **`SystemInfo`** as a resource (`macos://system/info`) for a one-shot inventory.
- The **`triage-support-issue`** prompt, which scripts the diagnose-then-act
  loop from the persona's own instructions.
- Optional **session recording** for the tickets where a picture settles it; see
  [Session recording](recording.md).

## Related

- [Toolsets and personas](toolsets-and-personas.md): trimming or extending the
  persona.
- [Policy configuration](policy-config.md): the rules that gate it, and what each
  signal means on a Mac.
- [Credentials](credentials.md): the keychain-backed sign-in path.
- [Deciding to deploy this](deployment-decision.md): for whoever signs off on it.
