# Evidence bundles

The evidence bundle (seal, sign, verify) is the harness's: it comes from the
`guardrails/evidence` package in
[agentweave-harness](https://github.com/deploymenttheory/agentweave-harness),
and the reference lives with the code.

**See [`docs/evidence-bundles.md`](https://github.com/deploymenttheory/agentweave-harness/blob/main/docs/evidence-bundles.md) in the agentweave-harness repository.**

This server imports that package for its standalone (in-process) stack, so the
behaviour described there applies here unchanged. What is macOS-specific is only
where things live and what they are called:

- the sealed-at-exit bundle is written when `transparency.evidence_dir` is set,
  conventionally `/Library/Application Support/MacOSMCP/evidence`;
- the operator commands are `macos-mcp-server evidence bundle --dir
  "/Library/Application Support/MacOSMCP/audit" --session <stamp>`,
  `evidence verify <bundle.zip> [--pubkey <hex>]` and `evidence keygen`;
- the ed25519 signing key is named by `--key-file` or `MACOS_MCP_EVIDENCE_KEY_FILE`,
  which is scrubbed from the environment before the first tool runs.

See `docs/security-architecture.md` in this repo for the host-side residue
(keychain credentials, the main thread, the pf and System Events actuators, the
posture probes, the stdio-only posture) that did not move.
