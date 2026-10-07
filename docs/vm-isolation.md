# Isolating the server in a disposable VM

This server's tools have **full, unsandboxed access as the user it runs as**
(Shell, LaunchExecutable, Defaults, LaunchdJob, FileSystem, Process, synthetic
input). The in-process security model contains a *session*: startup admission,
the policy engine, audit, the kill switch, egress. But the strongest containment
for untrusted workloads is to run the whole server inside a **disposable
virtual machine** whose blast radius is the VM, not the host.

This note covers the options on a Mac and, in particular, the
**Virtualization.framework** path through `tart`: programmatic, disposable
macOS guests that are plain files on disk, which is the most operator-friendly
fit and is what the [acceptance lab](acceptance-testing.md) already uses.

## The options

| Approach | Isolation | Disposable | Host-visible? | Notes |
|---|---|---|---|---|
| **`tart` (Virtualization.framework)** | Strong (VM, Apple's hypervisor) | Yes (clone, run, delete) | Only to whoever can read `~/.tart`; no shared VM registry | CLI-driven, scriptable, APFS clones make a fresh guest cheap. Apple silicon hosts only. The route this repo uses |
| **Virtualization.framework directly** | Strong (VM) | Yes | No | The API `tart` and UTM are built on. A supervisor process owns the guest and it dies with it. No binding in this repo today |
| **UTM / Parallels / VMware Fusion** | Strong (VM) | Manual | **Yes**: a GUI object anyone at the console can start, stop and snapshot | Full control and persistent, but a shared host object that out-of-band tools can touch |
| **A second physical Mac** | Strongest | No | n/a | The right answer when the workload must not share hardware; reimage with MDM between runs |

There is no macOS equivalent of Windows Sandbox or of the Host Compute System
API: Apple ships no built-in disposable desktop, and Virtualization.framework
is the only supported VM primitive. A macOS guest also cannot run on an Intel
host under Virtualization.framework, which is why the lab is Apple-silicon only.

## Why tart

`tart` wraps Virtualization.framework in a CLI whose VMs are directories under
`~/.tart/vms`. That gives the properties the Windows note wanted from HCS, by
different means:

- **Invisible to the desktop.** There is no VM manager application and no
  global registry. A guest exists only to the user whose home holds it and to
  the `tart run` process that started it; nothing on the host can reach in
  through a GUI and checkpoint or stop it.
- **Disposable.** `tart clone golden work`, `tart run work`, `tart delete work`.
  The clone is an APFS copy-on-write of the golden image, so it costs seconds
  and only the changed blocks. Isolation is by clone-and-delete rather than
  snapshot-and-revert; there are no snapshots to drift.
- **Network-isolated on request.** `tart run --net-softnet` puts the guest on
  an isolated network that cannot reach the host's LAN, complementing the kill
  switch's pf isolation inside the guest.
- **Headless or headed.** `--no-graphics` for a runner; a window on the host
  when you want to watch. The guest keeps its own console session either way,
  which is what the desktop engine needs.

This pairs naturally with the existing model: the containment layers guard
what happens *inside* a session; a disposable guest bounds *where* it can
happen.

## What a guest needs

A macOS guest is a full macOS, with the same consent model as the host. The
guest therefore needs its own grants for the server's signing identity before
the desktop toolsets work, and that is the one piece of real setup:

1. **Automatic login** to the guest's account, so a console session exists
   after every boot. The public base images ship with it.
2. **Accessibility and Screen Recording** granted to the binary inside the
   guest. On a managed guest that is a PPPC profile plus the one Screen
   Recording click; on a lab guest with SIP off, the TCC database is seeded
   directly. See [Permissions](permissions.md#managing-grants-with-mdm).
3. **The binary signed with a stable identity**, because the grants are keyed
   on it. A release build, or `make sign-dev`, not a bare `go build`.
4. **ssh** into the guest for the control plane (`tart ip`, `scp`), remembering
   that an ssh session has no console session and cannot drive the desktop; a
   command that needs the desktop is handed to the console session through a
   `gui/<uid>` launchd job.

The [acceptance lab](acceptance-testing.md) scripts all four.

## Operational notes

- **Licensing.** Apple permits two macOS guests per Apple-silicon host. A
  runner that needs more parallelism needs more hosts.
- **Getting the server into the guest.** Bake it into the golden image
  (`provision.sh` does this for the lab), or `scp` it to `$(tart ip <vm>)`
  per run. A shared directory (`tart run --dir`) also works and is
  convenient for pulling recordings and evidence bundles back out.
- **Console.** `tart run` without `--no-graphics` opens a window; `tart vnc`
  gives a VNC session. For an RPA agent this is usually headless with
  `transparency.recording_dir` capturing evidence inside the guest.
- **Root in the guest.** The pf egress tiers and the isolation rung need root
  there too. A guest is the right place to try `block_all_outbound`, which is
  why the live pf tests say to run them in a VM.

## Status

This is a **design and operations note**, not a shipped feature: today, isolate
the server by running it in a `tart` guest (or any Virtualization.framework
VM) as described above, which is what the acceptance lab does. A `sandbox`
mode that auto-provisions a disposable guest, boots the server inside it and
tears it down per session is the natural next step; it would need
Virtualization.framework bindings, which the SDK this server is built on does
not carry yet.
