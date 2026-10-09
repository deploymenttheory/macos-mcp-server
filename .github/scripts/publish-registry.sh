#!/usr/bin/env bash
# Publish the already public, signed MCPB through the official MCP Registry.
set -euo pipefail

tag=${1:?usage: publish-registry.sh <tag> <bundle.mcpb>}
bundle=${2:?usage: publish-registry.sh <tag> <bundle.mcpb>}
version=${tag#v}
name=io.github.deploymenttheory/macos-mcp-server
sha=$(shasum -a 256 "$bundle" | cut -d ' ' -f 1)
url="https://github.com/deploymenttheory/macos-mcp-server/releases/download/$tag/$(basename "$bundle")"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

jq -n \
  --arg name "$name" --arg version "$version" --arg url "$url" --arg sha "$sha" \
  '{
    "$schema": "https://static.modelcontextprotocol.io/schemas/2025-12-11/server.schema.json",
    name: $name,
    title: "macOS MCP Server",
    description: "Local macOS 27 desktop automation through MCP on Apple silicon.",
    websiteUrl: "https://github.com/deploymenttheory/macos-mcp-server",
    repository: {
      url: "https://github.com/deploymenttheory/macos-mcp-server",
      source: "github"
    },
    version: $version,
    packages: [{
      registryType: "mcpb",
      identifier: $url,
      fileSha256: $sha,
      transport: {type: "stdio"}
    }]
  }' > "$work/server.json"

mcp-publisher validate "$work/server.json"
if [ "${MCP_REGISTRY_VALIDATE_ONLY:-0}" = 1 ]; then
  echo "MCP Registry metadata validated"
  exit 0
fi

mcp-publisher login github-oidc
if mcp-publisher publish "$work/server.json"; then
  exit 0
fi

# A retry can legitimately encounter an already published immutable version.
# Never claim success for one whose bytes differ from this released bundle.
registry_url="https://registry.modelcontextprotocol.io/v0.1/servers/io.github.deploymenttheory%2Fmacos-mcp-server/versions/$version"
status=$(curl --silent --show-error --max-time 20 --output "$work/existing.json" \
  --write-out '%{http_code}' "$registry_url")
if [ "$status" = 200 ] && \
   [ "$(jq -r '.server.packages[] | select(.registryType == "mcpb") | .fileSha256' "$work/existing.json")" = "$sha" ]; then
  echo "MCP Registry already has $name@$version with the matching bundle"
  exit 0
fi
echo "MCP Registry publication failed or version $name@$version differs" >&2
exit 1
