# Security

## Reporting a vulnerability

Please report security issues privately to the maintainers through GitHub's
private vulnerability reporting on this repository rather than a public issue.
We aim to acknowledge within three working days.

## What this server does and does not protect against

The threat model, trust boundaries and the guardrail stack are documented in
`docs/security-architecture.md`. In short: the server is stdio-only, serves a
pinned tool manifest, audits every call to a hash-chained log, and can be
governed by a policy document that refuses, holds for approval, or contains.
It is a guardrail, not a sandbox — an agent with the shell toolset has the
reach of the user running the server.

## Supply chain

Release binaries are built by GitHub Actions from a tag, Developer ID signed
and notarized, with a CycloneDX SBOM and a keyless cosign signature over the
checksums file. Verification steps are on every release.
