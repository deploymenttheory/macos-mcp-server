# Acceptance testing on a disposable guest

The acceptance suite drives the **shipped binary** against a disposable macOS
guest. It covers three things a hosted CI runner cannot:

- **the desktop engine**, which needs a console session, the Accessibility
  grant and the Screen Recording grant. The CI suites skip with *"engine
  unavailable"*, *"needs the Accessibility grant"* and *"no displays
  (headless)"*;
- **the guardrails that need root or change machine state**: the pf egress
  tiers, default-deny outbound, the system proxy, network isolation;
- **the audit chain when a process is killed** rather than shut down, which is
  the state that leaves a session unsealed.

It is gated, operator-run, and never part of CI. The hosted `xcode-27` runner
has no Accessibility grant and no console session, and `go test ./...` there
guarantees compilation plus the pure-logic suites, nothing more.

```sh
export MACOS_MCP_ACC=1
scripts/acclab/run.sh                      # MACOS_MCP_ACC=1 go test ./internal/acceptance/ -count=1 -v -timeout 60m
```

With the gate unset every test skips. The scenarios delete guests and kill
processes, so they must not run unattended.

## What it needs

- An **Apple-silicon Mac** running macOS 27 with
  [`tart`](https://github.com/cirruslabs/tart) installed
  (`brew install cirruslabs/cli/tart`). No elevation on the host: Virtualization.framework
  is a user-space API.
- A **golden guest** built once by `scripts/acclab/provision.sh`.
- **A stable signing identity** for the binary under test, because the guest's
  TCC grants are keyed on it. The lab uses the `make sign-dev` identity; the
  provision script installs that certificate's designated requirement into the
  guest alongside the binary.

The suite checks for `tart` and the golden guest before it clones, boots or
builds anything, so a missing tool or an unknown image fails in seconds with
the names available rather than partway through a boot.

You do **not** need to source macOS media. `provision.sh` starts from a public
base image (Cirrus Labs publishes one per macOS release, with automatic
login, ssh and SIP disabled), or from an IPSW you point it at.

## Building the golden image, once

```sh
scripts/acclab/provision.sh
```

Which does, in order:

1. `tart clone <base image> macos-mcp-golden`, then boots it headless.
2. Builds the binary on the host, signs it with the dev identity, and `scp`s it
   into the guest at `~/acc/macos-mcp-server`.
3. **Pre-grants TCC.** Accessibility and Automation can be granted by a PPPC
   profile; Screen Recording cannot be granted by a profile at all, so on the
   lab guest, where SIP is off, the script seeds the per-user TCC database
   directly with entries for `com.deploymenttheory.macos-mcp-server` keyed on
   the dev identity's code requirement. This is the only place this repo
   writes a TCC database, and it is only sensible on a guest that is about to
   be thrown away.
4. Installs the interactive runner: a `gui/<uid>` launchd job that runs a
   command inside the console session on request.
5. Disables the screen saver and display sleep for the autologin account, so
   the session stays driveable.
6. Shuts the guest down. The stopped `macos-mcp-golden` image is the baseline.

**Keep automatic login.** The base images enable it, and the console session
it creates after every boot is what the desktop engine and TCC both depend on.
Turning on FileVault in the guest disables it.

**Clone after provisioning, not before.** A run clones the golden image and
deletes the clone, which is the isolation the suite relies on; a golden image
that is itself booted and modified by a run is no longer golden.

## What a run does

1. Builds the binary under test on the host, into `t.TempDir()`, signed with the
   dev identity.
2. `tart clone macos-mcp-golden macos-mcp-acc-<pid>`, boots it with a display (the console must be logged in), waits for ssh.
3. Pushes the binary into `~/acc` on the guest.
4. Runs the scenarios.
5. Deletes the clone, unless `MACOS_MCP_ACC_KEEP=1`.

Isolation is by clone-and-delete rather than by cleanup: the scenarios kill
processes and tamper with files, so a test that fails partway through cannot be
relied on to undo its own damage. `tart` has no snapshots, and an APFS clone of
the golden image is cheap enough that it does not need them.

### Two worlds inside the guest

| Runs where | Reached by | Used for |
|---|---|---|
| ssh session (no console) | `ssh admin@$(tart ip acc)` | every CLI verb: `audit verify`, `evidence ...`, tampering, process control, `policy check` |
| Console session | `scripts/acclab/run.sh`, which hands the suite to the console user's `gui/<uid>` launchd domain with `launchctl asuser` | anything touching the accessibility tree or the screen: a journey run, `permissions check` for the real answer |

A command sent over ssh has no window server session, so
`CGSessionCopyCurrentDictionary` is nil there, the server reports
`Console session: MISSING`, and accessibility calls fail or return nothing.
`run.sh` hands the suite to the autologin user's console session through
`launchctl asuser`, so the engine has a desktop to drive. Scenarios needing it **skip** when it is absent,
so a missing runner does not present as an engine failure.

## Knobs

| Variable | Effect |
|---|---|
| `MACOS_MCP_ACC=1` | Required. Without it everything skips. |
| `MACOS_MCP_ACC_VM` | The clone name to drive (default `macos-mcp-acc-<pid>`). |
| `MACOS_MCP_ACC_BASE` | The base image the golden image is cloned from (`provision.sh`). |
| `MACOS_MCP_ACC_BINARY` | A prebuilt binary to drive instead of building from the tree. |
| `MACOS_MCP_ACC_GOLDEN` | The golden image to clone (default `macos-mcp-golden`). |
| `MACOS_MCP_ACC_KEEP=1` | Leave the clone running after the suite, to inspect a failure with `tart vnc` or ssh. |

The names mirror the Windows suite's with the prefix swapped, so a lab script
that drives both reads the same.

## What slice 1 covers

The audit chain and the evidence bundle:

- a clean session **seals**, and `--strict` passes;
- a **sealed** session's chain catches a removed tail, because the manifest holds
  a head the session file cannot rewrite;
- an **unsealed** session is reported (`UNSEALED`, a warning, `--strict` fails)
  but not detected: with no seal there is no recorded head to compare against,
  and any prefix of a valid chain is itself valid. Asserted as it stands;
  detection is open in the shared core, and that test is what changes when it
  lands;
- a keyed chain carries its MACs and verifies against `MACOS_MCP_AUDIT_KEY`;
- a bundle round-trips, and fails on a **tampered member**, a **dropped member**
  and the **wrong public key**, while still hash-verifying unsigned: integrity
  and provenance are separate promises;
- a journey run (`journeys/examples/textedit-smoke.json`, which opens TextEdit,
  types a line and asserts on it) produces an **OTLP/JSON record and
  screenshots** that are sealed into the bundle, and `journey.finished` reaches
  `verdicts.json`. *(Needs the console session.)*

## The paired harness+server slice

> Not yet ported to the macOS suite. The Windows design is kept here as the
> target shape; `internal/acceptance` currently carries the TextEdit journey,
> the guardrail-tools check and the shipped journey example.

Gated behind `MACOS_MCP_ACC_PAIRED=1` (on top of the base gate). It is the
end-to-end pin for the cross-process behaviour a single-process test cannot
reach: the [agentweave-harness](https://github.com/deploymenttheory/agentweave-harness)
governing the shipped server on the guest as two real processes over the
control channel. The harness binary is built from this repo's pinned module
dependency, so the two binaries under test are the versions this repo actually
ships against, then pushed alongside the server.

- **Refusal on the wire.** An enforcing harness policy denies a `tools/call`;
  the client receives an `IsError` refusal synthesized in the harness, and the
  call never reaches the server (a sentinel in the argument does not
  round-trip).
- **Two chains.** The harness writes its own audit chain of the proxied
  conversation; both it and any server-side host chain `audit verify` green.
- **Never-read across the boundary.** The sentinel argument value appears in no
  recorded frame on either chain: arguments are digested, never recorded raw,
  now provable end to end rather than within one process.
- **Channel-loss teardown.** Killing the harness mid-session closes the
  servant's control channel, cancels the run context, and the server's LIFO
  teardown runs, asserted by the governed child exiting rather than orphaning.

## The recorder is checked by hand

> Not yet scripted on macOS; the steps below are the manual check.

The journey recorder (`journey record`) is a CGEvent tap. Unlike the Windows
low-level hook, a macOS tap does not distinguish synthetic input from a
person's, so there is no injected-input filter to protect; what the recorder
guarantees instead is that **input into a password field is never written**:
before each character is recorded, the engine checks on the main thread
whether the focused element is an `AXSecureTextField`, and a keystroke whose
destination cannot be established is treated as secret. That check is about
what a person is typing into a real secure field in a real application, which
is the part worth a human's eyes.

The harness therefore prepares the guest and stops:

```sh
export MACOS_MCP_ACC=1 MACOS_MCP_ACC_KEEP=1
go test ./internal/acceptance/ -run TestRecorderManualPrep -v
# follow the printed instructions in `tart vnc acc`
go test ./internal/acceptance/ -run TestRecorderManualVerify -v
```

The verify step checks that the typed password appears **nowhere** in the
recorded file, then the parts that depend on what the accessibility tree
reports: the identifier selector ladder, action-driven verb inference, and F8
assertion marking.

## Adding a slice

Candidates, all unsafe on a workstation and safe on a guest that is about to be
deleted: the root-only and machine-state guardrails (`MACOS_MCP_SCOPED_TEST`,
`MACOS_MCP_GLOBAL_BLOCK_TEST`, the system proxy), the credentials never-read
invariant against a real login keychain, the behaviour with a grant missing
(`tccutil reset` inside the guest, then assert the `IsError` shape), and the
adversarial prompt harness.

Add a file per area beside `audit_test.go` and reuse the lab harness: the guest
lifecycle, the interactive runner and the transfer mechanism are shared.
