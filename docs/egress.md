# Egress control on macOS

The egress proxy, its allowlist and the enforcement tiers are the harness's;
the reference is
[`docs/egress.md`](https://github.com/deploymenttheory/agentweave-harness/blob/main/docs/egress.md)
in agentweave-harness. This page covers what the macOS enforcer
(`internal/macguard`) does with the tiers, because pf is not Windows Firewall.

## Where the rules go

Rules are loaded into a sub-anchor, `com.apple/900.deploymenttheory.macosmcp`,
under Apple's `com.apple/*` anchor point, which `/etc/pf.conf` already
evaluates. The system ruleset is never edited. The rendered ruleset is written
to `/Library/Application Support/MacOSMCP/pf-anchor.conf` and loaded with
`pfctl -a <anchor> -f`; pf is enabled with a reference token (`pfctl -E`) that
only this run releases. Inspect what is live with:

```sh
sudo pfctl -a com.apple/900.deploymenttheory.macosmcp -sr
```

Every tier needs root. `networksetup` needs an administrator too, so
`set_system_proxy` is refused unprivileged rather than prompting on a console
nobody is watching. Missing elevation is fatal at startup, never degraded.

## The tiers

| Policy | Windows | macOS |
|---|---|---|
| `egress.enabled` only | proxy-only, advisory | the same |
| `egress.applications` | block the listed executables except to the proxy | **uid-scoped**: block the console user's outbound traffic except loopback; the listed applications are recorded in the audit entry but pf cannot match on them |
| `egress.block_all_outbound` | default-deny with the Windows service exceptions | `block drop out all` with the exceptions below |
| `egress.set_system_proxy` | WinINET | `networksetup -setwebproxy` / `-setsecurewebproxy` on every enabled network service, prior values saved and restored |

pf filters by socket owner, not by executable. The scoped tier therefore
blocks everything the console user runs, which is wider than the Windows
tier; the status surface and the audit record say `scope: user` so nobody
reads it as per-application. A process running as another uid (a LaunchDaemon,
root) is not covered. A Network Extension content filter would match by
application, but needs a signed system extension in an app bundle, which this
binary is not.

## Global block exceptions

A default-deny machine still needs these, each scoped to the daemon's own uid
so none is a route for the workload. The set is pinned by a test.

| Rule | Why |
|---|---|
| `pass out quick on lo0 all` | the proxy, and every local IPC |
| `pass out quick proto udp from any port 68 to any port 67` | DHCP; without it the lease lapses |
| `pass out quick proto { tcp udp } to any port 53 user _mdnsresponder` | DNS via mDNSResponder |
| `pass out quick proto udp to any port 123 user _timed` | time; certificates and tokens rot without it |
| `pass out quick proto tcp to any [port { allow_ports }] user root` | the proxy's own route out, bounded to the allowlist ports |

The proxy's rule names `root` because enforcement already requires it. A
harness-side proxy is assumed to run as root as well; if it does not, its
traffic is blocked under the global tier, which fails closed rather than open.

## Recovery and containment

The recovery state (`egress-state.json` in the state directory) is written
**before** the first pfctl call and names the anchor, the pf token and the
saved proxy settings. Every start runs `Recover`, even with egress off, so a
session killed mid-run cannot leave the console user blocked. On a kill-switch
trip the anchor is reloaded with its block rules only (`Suspend`): nothing is
restored, because that would countermand the isolation the ladder has just
applied; teardown waits for the exit path.

Network isolation (the kill ladder's first rung) uses its own anchor,
`com.apple/901.deploymenttheory.macosmcp.isolate`, with `block drop out all`,
`block drop in all` and loopback passed, and records the rules pf reads back
as the observed state.

## Live tests

The unit tests drive the pfctl sequence through a fake runner. Two opt-in
suites touch the machine, deliberately behind different variables:

```sh
sudo MACOS_MCP_SCOPED_TEST=1 go test ./internal/macguard/ -run TestScopedLive -v -count=1
sudo MACOS_MCP_GLOBAL_BLOCK_TEST=1 go test ./internal/macguard/ -run TestGlobalBlockLive -v -count=1
```

Run the second in a VM or on a machine whose network you can afford to lose.
