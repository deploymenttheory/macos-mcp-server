# Releasing the macOS server

The supported product is a local stdio server for **macOS 27 or later on Apple
silicon**. The release tarball, Homebrew cask and Claude Desktop bundle all
carry the same signed arm64 executable. Keep the signing identifier
`com.deploymenttheory.macos-mcp-server` stable so users retain their macOS
privacy grants.

## Version policy

Release Please owns `.release-please-manifest.json`, the changelog, tags and
GitHub releases. Before 1.0, a new user capability advances the minor version
and a fix advances the patch version. The first distribution release after
v0.1.2 should be v0.2.0. These releases are labelled **preview**; 1.0 waits
for clean-device install, client connection and upgrade evidence. Do not edit a
published tag or replace a public release asset. Fix a public defect in a new
version.

The binary `--version`, tarball and `.mcpb` names, MCPB manifest, Homebrew cask
and MCP Registry entry must all name the same version. The Registry entry must
carry the released MCPB's SHA-256.

MCPB 2.1.2's signing command leaves trailing signature bytes outside the ZIP
comment and its verify command calls an unimplemented PKCS#7 verifier. The
release therefore uses `scripts/mcpb-signature.py` to make the signed bundle a
valid ZIP and verifies its detached CMS signature with OpenSSL plus the exact
Developer ID signer. The resulting ZIP passes strict parsing;
recheck against a fixed MCPB release before removing the workaround.

## Cut a release

1. Merge feature and fix PRs only after the build, packaging and MCP spec gates
   pass. Review the release-please PR, then merge it to create a `v*` tag and a
   **draft** GitHub release. `force-tag-creation` lets the tag trigger
   `release.yml` while the release remains a draft.
2. The release workflow builds arm64 on `xcode-27`, Developer ID signs and
   notarizes the binary, packs the tarball, checksum signature and SBOM, then
   packs and signs the `.mcpb` from the tarball's binary. It checks both
   packages, the signing identity, Gatekeeper, version and a stdio MCP
   handshake. Missing signing credentials or any failed check leave the draft
   unpublished.
3. After those checks, the workflow publishes the GitHub release as a preview,
   installs the generated cask from the public asset, updates
   `deploymenttheory/homebrew-tap`, and publishes the bundle to the MCP
   Registry using GitHub OIDC. If the Registry is unavailable, rerun only
   `publish-mcp-registry` with the public tag. Its retry verifies an existing
   entry's checksum before declaring success.
4. Install the published cask on a clean macOS 27 Mac with a graphical session.
   Grant Accessibility and Screen Recording, connect Codex and Claude Code,
   and confirm a desktop tool can run. Install the release `.mcpb` in Claude
   Desktop, approve its prompted grants, restart Claude Desktop and confirm
   its tool connection. Repeat after an upgrade and check that TCC grants
   persist. Hosted CI cannot provide these desktop grants.
5. Once the bundle works in Claude Desktop, submit that exact release `.mcpb`
   through [Anthropic's desktop extension submission
   route](https://support.claude.com/en/articles/10949351-getting-started-with-local-mcp-servers-on-claude-desktop).
   The GitHub asset remains the direct download while Anthropic reviews the
   directory listing. Future directory updates follow its approval process.

## Recovery

The release workflow can rebuild a **draft** from the same tag through its
`workflow_dispatch` input. It refuses to rebuild an already public release.
If a public release is defective, keep its assets intact, publish a fixed patch
version and document the known-good older asset as a temporary rollback. The
Homebrew cask is updated only after the new release and install check pass;
mark a defective MCP Registry version deprecated when appropriate. Privately
installed `.mcpb` files are upgraded by installing the newer release file.
