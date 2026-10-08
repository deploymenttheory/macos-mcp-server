# Policy configuration

The policy engine and its document schema come from
[agentweave-harness](https://github.com/deploymenttheory/agentweave-harness).
The base document reference (signals, rules, rate limits, egress,
transparency, approvals, kill, in-flight, `enforce_https`) is
[`docs/policy-config.md`](https://github.com/deploymenttheory/agentweave-harness/blob/main/docs/policy-config.md)
there, and the layered composition the harness adds when it governs a session
is in
[`docs/policy-layering.md`](https://github.com/deploymenttheory/agentweave-harness/blob/main/docs/policy-layering.md).
This server imports the same package, so `--policy-config` takes that schema
unchanged. What this page adds is the **macOS reading** of each signal.

```sh
macos-mcp-server policy validate --policy-config policy/examples/enterprise.json
macos-mcp-server policy check    --policy-config policy/examples/enterprise.json   # live; exit 2 if not admitted
macos-mcp-server policy explain  --tool Shell --policy-config policy/examples/enterprise.json
macos-mcp-server policy test     policy/examples/tests/*.json                      # fixtures, runs in CI
```

Without `--policy-config` the built-in default applies: audit mode, every
declared signal evaluated and recorded, nothing refused, no egress proxy.

## How the signals read on macOS

The harness's signal names are Windows-shaped. The macOS probe answers each
with the closest native fact, listed here so an audit record or a refusal can
be read correctly. The mapping is a shim until the harness grows
platform-neutral names; the table is the contract in the meantime.

| Signal | Windows meaning | macOS reading | Source |
|---|---|---|---|
| `run-context` | interactive user, not SYSTEM, not Session 0 | a console (graphical) login session; "system" means root with no console session | `CGSessionCopyCurrentDictionary`, euid |
| `not-admin` | not a local Administrator | not a member of the `admin` group (gid 80) | group membership |
| `mdm-enrolled` | MDM URL from `dsregcmd` | device enrolled in MDM | `profiles status -type enrollment` |
| `entra-joined` | AzureAdJoined from `dsregcmd` | Platform SSO registered with a device and tenant id | `app-sso platform -s` |
| `domain-joined` | AD domain membership | Active Directory binding | `dsconfigad -show` |
| `os-enterprise-sku` | Windows Enterprise edition | not meaningful; always fails | — |
| `device-allowlist` | serial in a file | serial in a file | IOKit `IOPlatformSerialNumber` |
| `secure-boot` | UEFI Secure Boot | Apple silicon boot security at Full Security (or T2 secure boot) | `bputil -d`, `csrutil status` |
| `tpm-present` | a TPM 2.0 | a Secure Enclave | `hw.optional.arm64`, T2 presence |
| `tpm-attestation-capable` | TPM can quote | never; no platform attestation path is exposed | — |
| `tpm-attested` | nonce-fresh TPM quote | always errors (signal skips) | — |
| `vbs` | virtualization-based security | System Integrity Protection enabled | `csrutil status` |
| `hvci` | memory integrity | sealed system volume (authenticated root) enabled | `csrutil authenticated-root status` |
| `credential-guard` | Credential Guard running | Gatekeeper assessments enabled | `spctl --status` |
| `bitlocker` | every fixed volume encrypted | FileVault on for every APFS volume, one entry per volume | `fdesetup status`, `diskutil apfs list -plist` |
| `logged-on-account` | the interactive account | the console user | `SCDynamicStoreCopyConsoleUser` |
| `remote-policy` | remote may-run endpoint | unchanged | HTTP |

The `dsregcmd /status` command that the harness's built-in `mdm-enrolled` and
`entra-joined` signals issue is answered from a **command table**, not a
shell: the probe synthesises the dsregcmd-shaped text those parsers expect
from the facts above. No other command is accepted through that seam.

## What the shipped examples drop

`policy/examples/` mirrors the Windows set with macOS paths and variable
names. Three signals are left out because they cannot pass here:
`os-enterprise-sku`, `tpm-attested` and `credential-guard` (the Gatekeeper
reading is available, but a rule named for Credential Guard would mislead an
auditor). `enterprise.json` therefore has an `encrypted-volume-for-shell`
rule in place of `credential-guard-for-shell`.

| Example | What it shows |
|---|---|
| `audit.json` | the default posture made explicit: everything evaluated, nothing refused |
| `secure.json` | managed device, hardware posture for destructive tools, rate limit, isolation on trip |
| `enterprise.json` | startup admission, FileVault for the shell, plan gating, acknowledged credential exposure |
| `locked-down.json` | allowlisted devices, `on_fail: kill`, lock on trip, unified-log anchoring, control directory |
| `dual-control.json` | `on_fail: hold` with an approvals webhook |
| `egress.json` | the egress proxy with an allowlist and an auth token |
| `evidence-export.json` | directory audit sink, recording, evidence bundle and signed-URL export |

## Paths and variables

Machine-wide state lives under `/Library/Application Support/MacOSMCP/`:
audit chains, recordings, evidence, the egress recovery state and the control
directory. Every secret is an environment variable with the `MACOS_MCP_`
prefix (`MACOS_MCP_AUDIT_KEY`, `MACOS_MCP_APPROVAL_KEY`,
`MACOS_MCP_EVIDENCE_KEY_FILE`, `MACOS_MCP_OTLP_HEADERS`,
`MACOS_MCP_REMOTE_POLICY_TOKEN`, the Graph trio) and is scrubbed from the
process environment before the first tool can run. The evidence export
signed-URL variables keep the harness's `WINDOWS_MCP_EXPORT_SIGNED_URL*`
names, because the harness defines them; they are scrubbed all the same.

## Anchoring

`transparency.anchor.destination: "eventlog"` is the only destination the
schema accepts. On macOS it writes each chain head to the unified log through
`logger(1)` under the `macos-mcp-server` tag, so the head is owned by the
system log store rather than by this process:

```sh
log show --predicate 'eventMessage CONTAINS "macos-mcp-server audit anchor"' --last 1h
```
