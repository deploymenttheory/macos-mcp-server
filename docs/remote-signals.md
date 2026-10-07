# Remote signals

Microsoft Graph device compliance and the external may-run endpoint are the
harness's signals: they come from the `guardrails/signals` package in
[agentweave-harness](https://github.com/deploymenttheory/agentweave-harness),
and the reference lives with the code.

**See [`docs/remote-signals.md`](https://github.com/deploymenttheory/agentweave-harness/blob/main/docs/remote-signals.md) in the agentweave-harness repository.**

This server imports that package for its standalone (in-process) stack, so the
behaviour described there applies here unchanged. The macOS spellings and the
one honest gap:

- the Graph trio is `MACOS_MCP_GRAPH_TENANT`, `MACOS_MCP_GRAPH_CLIENT_ID` and
  `MACOS_MCP_GRAPH_CLIENT_SECRET`; the may-run device token is
  `MACOS_MCP_REMOTE_POLICY_TOKEN`. A signal registers only when its credentials
  are present, and the secrets are scrubbed from the environment before the
  first tool runs;
- the device identity Graph is asked about comes from Platform SSO
  (`app-sso platform -s`), which is also what answers the harness's built-in
  `entra-joined` signal through the `dsregcmd /status` command table described
  in [policy-config.md](policy-config.md);
- `tpm-attested` always errors on macOS: there is no platform attestation path
  this server can quote against, so the Secure Enclave reports presence only and
  a rule requiring attestation fails at the rule's severity rather than passing.

See `docs/security-architecture.md` in this repo for the host-side residue that
did not move.
