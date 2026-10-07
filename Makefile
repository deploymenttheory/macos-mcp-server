# Developer entry points. CI runs the same commands from the workflows; this
# file exists so a developer's loop and CI's cannot disagree.

BINARY   := macos-mcp-server
IDENT    := com.deploymenttheory.macos-mcp-server
# A self-signed code-signing certificate created once in Keychain Access
# (Certificate Assistant > Create a Certificate, type "Code Signing"). Signing
# dev builds with a stable identity is what lets Accessibility and Screen
# Recording grants survive a rebuild — see docs/permissions.md.
DEV_SIGN_IDENTITY ?= macOS MCP Dev

.PHONY: build build-amd64 test vet lint conformance-test sign-dev permissions check tidy

build:
	go build -trimpath -o $(BINARY) ./cmd/macos-mcp-server

build-amd64:
	GOARCH=amd64 CGO_ENABLED=0 go build ./...

vet:
	// unsafeptr is disabled: the journey recorder hands CGEventTapCreate a C
	// callback pointer minted by purego.NewCallback — the legitimate, unavoidable
	// idiom, which the checker cannot tell apart from misuse. Every other vet
	// analyzer stays on.
	go vet -unsafeptr=false ./...

test:
	go test ./... -count=1

conformance-test:
	go build -tags conformance ./...
	go test -tags conformance ./internal/macmcp/ -count=1

lint:
	golangci-lint run --config=./.golangci.yml

# sign-dev gives a local build the same identifier the release carries, under
# a developer's own self-signed certificate, so TCC grants are keyed on
# identifier + certificate rather than on the hash of this particular build.
sign-dev: build
	codesign --force --sign "$(DEV_SIGN_IDENTITY)" --identifier $(IDENT) \
		--entitlements entitlements/macos-mcp-server.entitlements $(BINARY)
	codesign -dv --verbose=2 $(BINARY)

permissions: build
	./$(BINARY) permissions check

check: vet test lint

# tidy must see mcp-server-core, which go.work supplies but `go mod tidy`
# ignores. A transient replace keeps go.mod free of one.
tidy:
	@cp go.mod .go.mod.tidy && \
	echo 'replace github.com/deploymenttheory/mcp-server-core => ../mcp-server-core' >> go.mod && \
	go mod tidy; status=$$?; \
	grep -v '^replace github.com/deploymenttheory/mcp-server-core' go.mod > .go.mod.new && mv .go.mod.new go.mod && rm .go.mod.tidy; \
	exit $$status
