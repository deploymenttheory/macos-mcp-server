# Deciding to deploy this

For the person who has to **approve** this on a fleet, a security or risk
function, rather than the person who installs it. [Deployment](deployment.md) is
the *how* (it lands with the milestone that adds the LaunchAgent and MDM
profiles); this page is the *whether*, and *how far*.

- [What you are approving](#what-you-are-approving)
- [The threat model, briefly](#the-threat-model-briefly)
- [Non-goals: what it deliberately does not do](#non-goals-what-it-deliberately-does-not-do)
- [Choosing a posture](#choosing-a-posture)
- [What to monitor](#what-to-monitor)
- [Residual risk to sign off](#residual-risk-to-sign-off)

---

## What you are approving

An MCP server that gives an AI agent the same desktop reach a signed-in user
has: the accessibility tree and synthetic input, a shell, preference domains
and launchd jobs, files, applications. Every action is wrapped in a policy gate,
an egress allowlist, and a tamper-evident audit chain.

It runs with **exactly the privileges of the account it runs as** and raises
none. The one wrinkle macOS adds is that the enforcement tiers (pf rules, the
system proxy, network isolation) only work as root, so an elevated deployment is
a deliberate choice with its own consequences, spelled out below. The question
is not "is desktop automation safe" (it is as safe as that account) but "are the
agent's actions **conditional, bounded, and reviewable** enough for the risk you
are carrying". This server exists to make the answer yes.

## The threat model, briefly

The actor-by-actor model and its mapping to mechanisms are in the
[security architecture](security-architecture.md#threat-model-mapping). The one
paragraph an approver needs:

The local device signals and the on-box containment are **auditable
defense-in-depth and evidence, not a hard boundary**. A local administrator can
spoof a signal or attack the log; keying and off-box anchoring (to the unified
log) raise that bar but do not remove it. Treat this as one layer that
**records and raises the cost** of in-session compromise, and pair it with the
OS controls you already own: **MDM-managed configuration**, SIP and the sealed
system volume, Gatekeeper and notarization, Platform SSO with Conditional
Access, and **code signing** of the binary. The value against a privileged
adversary is evidence that survives, not prevention.

## Non-goals: what it deliberately does not do

These are design decisions, not gaps, and each is a smaller attack surface:

- **No vision model.** Perception is the macOS accessibility tree only. This is
  also a data-residency property: desktop pixels that may contain customer data
  never leave the machine.
- **No login-window, lock-screen, Touch ID or TCC-prompt automation.** The
  login window, the lock screen, the password sheets macOS raises for privileged
  operations and the privacy consent dialogs are not driveable through the
  accessibility tree. Assume an already-unlocked, signed-in session with the
  grants already made.
- **No network listener in the shipped binary.** The transport is stdio only,
  so there is nothing on the network to attack. (A loopback HTTP host exists
  behind the `conformance` build tag for the official suite and is never
  compiled into a release.)
- **No entitlements.** The binary asks for none; the only consent it needs is
  the per-application TCC grants (Accessibility, Screen Recording, optionally
  Full Disk Access), keyed on its signing identity.

A disposable virtual machine remains the stronger containment option when the
workload is untrusted; this repository does not yet carry a design note on it,
and the Windows counterpart's `docs/vm-isolation.md` states the reasoning,
which carries over.

## Choosing a posture

Adopt in this order. Each step is reversible and observable before you take the
next.

| Posture | What it does | Cost to adopt |
|---|---|---|
| **Audit** (the default) | Engine on, every signal evaluated, every verdict recorded, **nothing refused**. | None. Behaviour is unchanged from no policy, so it cannot break a working deployment. Adopt first and *read the audit log* to learn what your fleet actually does. |
| **Enforce** | Rules refuse or contain. Gate the destructive surface (`shell`, `filesystem`, destructive-annotated tools) on device posture: MDM enrolment, FileVault, boot security, SIP, the sealed system volume. | Write rules, then prove them with `policy test` fixtures in CI before rollout. A recovered signal restores service with no restart. Read [policy-config.md](policy-config.md) for what each Windows-named signal means on a Mac before relying on it. |
| **Egress, proxy-only** | A loopback allowlist the agent is asked to use. Advisory. | Declare the domains. No elevation. |
| **Egress, scoped / global** | pf rules so the console user (scoped) or the whole machine (global) cannot bypass the proxy. | Requires root; refuses to start without it rather than serving a weaker posture than the document says. The scoped tier is **uid-scoped**, not per-application, because pf cannot match an executable. |
| **Keyed + anchored audit** | HMAC the chain (`MACOS_MCP_AUDIT_KEY`, or auto-generated beside a file or directory destination) and publish the head to the unified log. | For when a local admin is in the threat model and the audit trail must resist them. |

The engine reads a policy document, not flags; it is meant to be reviewed and
checked into source control. Start from `policy/examples/` (`audit.json` first).
Note that `locked-down.json` requires `not-admin` on every call while arming
network isolation: on macOS root is a member of `admin`, so run it unelevated
(containment is then skipped and audited) or relax that rule before expecting
isolation to fire.

## What to monitor

From [Monitoring](monitoring.md):

- **The audit chain.** `macos-mcp-server audit verify <dir>` confirms it is
  unbroken; in directory mode it also checks the cross-session manifest.
- **`credentials.exposure.*`**, whether a session ran with credentials beside a
  toolset that can read them back out of the keychain (it refuses by default;
  the event records an acknowledged override).
- **`killswitch.disarmed`**, meaning something the policy *could* have contained
  happened and the policy chose not to. Watch this specifically.
- **`killaction.skipped … not elevated`**, meaning containment was armed but the
  process was not root. If you expected isolation, this is the line that says
  you did not get it.
- **`egress.summary`**, the domains refused this session, and
  **`egress.recovered`**, which means a previous session died with pf rules
  loaded and this start removed them.
- **The status endpoint**: `admit`, `killed`, `heartbeat_age_sec`, and whether
  egress enforcement is the tier you configured or silently `proxy-only`.
- **The unified log** for the anchored chain heads, when anchoring is on.

## Residual risk to sign off

What the controls do **not** remove, restated as things you are accepting:

- Benign-annotated tool calls can still **compose** into a harmful outcome
  within the served surface. Scope the surface with personas, and use
  [plan-and-apply](plan-and-apply.md) with `require_plan` where the composition
  risk is real; it adjudicates a whole sequence before any step runs.
- The **audit chain is unkeyed unless `audit_destination` is a file or
  directory**; with `stderr`, the shipped default, there is nowhere to keep a
  key. Keyed, the **HMAC key still sits on the box** unless you anchor the head
  off it.
- **pf matches the socket owner, not the executable.** The scoped tier blocks
  everything the console user runs, which is wider than the Windows tier, and a
  process under another uid is not covered at all. More importantly, the server
  must run as root to enforce, and root is the identity both tiers exempt (the
  proxy's own route out), so the server's own network-reaching tools and their
  child processes (`Shell`, `LaunchExecutable`, `Package`, `Network` test) are
  bounded by the proxy allowlist only where they use the proxy. The egress
  tiers constrain the console user's applications, not the agent's shell.
- **Credentials live in the login keychain** and are removed on every exit
  path, but a crash is not an exit path: a session killed with SIGKILL or lost
  to a power cut leaves the item in the keychain until the next clean run
  removes it. The `shell` toolset can read any keychain item back with
  `security find-generic-password -w`, which is why serving it beside
  credentials is refused unless the policy acknowledges it.
- **TCC grants are keyed on the signing identity.** Whoever can produce a binary
  signed under the same identity inherits every grant; an ad-hoc signed build
  loses them on every rebuild. Treat the signing identity as part of the trust
  boundary and manage grants through MDM where you can.
- **No platform attestation.** `tpm-attested` always errors; the Secure Enclave
  reports presence only. A policy that requires attestation refuses on a Mac,
  which is the honest outcome, not a bypass.
- **`FileSystem` path protection** folds case, `~`, `/private` and symlinks
  but not hard links or firmlinks, and binds the `FileSystem` tool only. It is a
  guardrail, not a sandbox.
- The **`status_token` belongs in `status_token_env`**, not inline: the policy
  document is readable through the `filesystem` toolset, and `POST /revoke`
  behind that credential runs the containment ladder.
- The **security banner is a log line and a status field** in this build, not
  pixels on screen, so a kill-switch trip is visible in the audit chain and to
  an operator reading stderr but not on the session recording.
- A **local administrator** can defeat any on-box control. The mitigation is
  the OS controls you pair this with, plus the surviving evidence.

When these are acceptable for the account and machine this runs on, deploy per
[Deployment](deployment.md).
