# In-flight monitoring

The loopback status endpoint, the heartbeat, the in-flight monitor and the
hash-chained audit log are the harness's: they come from the `guardrails/status`,
`guardrails/watch` and `guardrails/audit` packages in
[agentweave-harness](https://github.com/deploymenttheory/agentweave-harness),
and the reference lives with the code.

**See [`docs/monitoring.md`](https://github.com/deploymenttheory/agentweave-harness/blob/main/docs/monitoring.md) in the agentweave-harness repository.**

This server imports those packages for its standalone (in-process) stack, so
the behaviour described there applies here unchanged. The macOS spellings:

- the audit chain is verified with `macos-mcp-server audit verify
  "/Library/Application Support/MacOSMCP/audit"` (a file or a directory), with
  `--key-env MACOS_MCP_AUDIT_KEY` to check the HMACs as well and `--strict` to
  fail on an unsealed session;
- the chain is keyed only when `transparency.audit_destination` is a file or a
  directory; with `stderr`, the shipped default, there is nowhere to keep a key
  and the chain is unkeyed;
- `transparency.anchor.destination: "eventlog"` writes each chain head to the
  unified log through `logger(1)` under the `macos-mcp-server` tag; read them
  back with `log show --predicate 'eventMessage CONTAINS "macos-mcp-server audit
  anchor"' --last 1h`;
- the status endpoint's credential belongs in `transparency.status_token_env`,
  never inline, because the policy document is readable through the
  `filesystem` toolset and `POST /revoke` behind that token runs the containment
  ladder.

See `docs/security-architecture.md` in this repo for the host-side residue that
did not move.
