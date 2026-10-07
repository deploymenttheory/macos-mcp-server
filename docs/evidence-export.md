# Evidence export

Shipping the sealed bundle off the device at session end is the harness's
feature: it comes from the `guardrails/export` package in
[agentweave-harness](https://github.com/deploymenttheory/agentweave-harness),
and the reference lives with the code.

**See [`docs/evidence-export.md`](https://github.com/deploymenttheory/agentweave-harness/blob/main/docs/evidence-export.md) in the agentweave-harness repository.**

This server imports that package for its standalone (in-process) stack, so the
`transparency.export` block, the create-only upload rule, the hardened dialer and
the receipt all behave here exactly as documented there. Three things to know on
macOS:

- the receipt, `session-<stamp>.export.json`, is written beside the bundle under
  `transparency.evidence_dir` (`/Library/Application Support/MacOSMCP/evidence`
  in the shipped examples; see `policy/examples/evidence-export.json`);
- the signed-URL credentials keep the harness's variable names,
  `WINDOWS_MCP_EXPORT_SIGNED_URL`, `WINDOWS_MCP_EXPORT_SIGNED_URL_MANIFEST` and
  `WINDOWS_MCP_EXPORT_SIGNED_URL_SIGNATURE`, because the harness defines them.
  This is a documented quirk, not an oversight: they are scrubbed from the
  environment and withheld from child processes like every `MACOS_MCP_*` secret;
- under the pf `global` tier the upload leaves through the proxy owner's (root)
  allow rule, so the destination port must be in `egress.allow_ports` and, when
  the egress proxy is on, the destination host must be in `egress.allow`.

See `docs/security-architecture.md` in this repo for the host-side residue that
did not move.
