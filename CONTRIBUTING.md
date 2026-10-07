# Contributing

Thanks for helping build macos-mcp-server.

## Before you start

- Read `CLAUDE.md`. It is written for coding agents but it is the shortest
  honest account of the conventions that matter here.
- Install Go (the version in `go.mod`), `golangci-lint`, and, for release
  work, `goreleaser`, `cosign` and `syft`.
- The repo depends on `mcp-server-core`. Until its tag is published, create a
  `go.work` (gitignored) pointing at a sibling checkout; `make tidy` keeps
  `go.mod` free of `replace` directives, which CI refuses.

## The loop

```sh
make build          # ./macos-mcp-server
make sign-dev       # sign with a stable local identity so TCC grants persist
make permissions    # what this Mac has granted
make check          # vet + test + lint
```

Engine tests need Accessibility and Screen Recording and a console session;
they skip themselves otherwise, visibly (`go test -v`).

## Pull requests

- Titles are conventional commits (`feat:`, `fix:`, `docs:`, `chore:` ...);
  release-please builds the changelog from them.
- A new tool bumps `TestExpectedToolCount` and carries honest annotations.
- Nothing security-shaped is configured by a flag: it goes in the policy
  document, and secrets come from the environment.
- Don't add a `replace` to `go.mod`, a platform build tag to core, or a
  `get` mode to the Credentials tool.
