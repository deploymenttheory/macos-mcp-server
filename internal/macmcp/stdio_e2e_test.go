//go:build darwin && (amd64 || arm64)

package macmcp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestStdioEndToEnd builds the real binary and drives it with an MCP client
// over stdio: the handshake, tools/list, a prompt, a resource read and a tool
// call that needs no permissions. It is what proves the main-thread plumbing
// — DispatchMain on the main thread, the server on a goroutine — serves a
// session rather than deadlocking it.
func TestStdioEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := filepath.Join(t.TempDir(), "macos-mcp-server")
	build := exec.Command("go", "build", "-o", bin, "../../cmd/macos-mcp-server")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, "stdio", "--toolsets", "all")
	cmd.Stderr = os.Stderr
	client := mcp.NewClient(&mcp.Implementation{Name: "e2e", Version: "test"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = session.Close() }()

	init := session.InitializeResult()
	if init == nil || init.ServerInfo == nil || init.ServerInfo.Name != ServerName {
		t.Fatalf("server identity: %+v", init)
	}
	// On 2026-07-28 the SDK's InitializeResult is a synthesised legacy view that
	// may omit instructions; the wire-level capture test covers them.
	t.Logf("instructions: %q", init.Instructions)

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	names := map[string]bool{}
	for _, tool := range tools.Tools {
		names[tool.Name] = true
	}
	for _, want := range []string{"Snapshot", "Click", "App", "Clipboard", "Process"} {
		if !names[want] {
			t.Errorf("tools/list is missing %s", want)
		}
	}

	prompts, err := session.ListPrompts(ctx, nil)
	if err != nil || len(prompts.Prompts) == 0 {
		t.Fatalf("prompts/list: %v (%d)", err, len(prompts.Prompts))
	}
	got, err := session.GetPrompt(ctx, &mcp.GetPromptParams{Name: "rpa-journey", Arguments: map[string]string{"goal": "open TextEdit"}})
	if err != nil || len(got.Messages) == 0 {
		t.Fatalf("prompts/get: %v", err)
	}

	res, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: "macos://desktop/snapshot"})
	if err != nil || len(res.Contents) == 0 {
		t.Fatalf("resources/read: %v", err)
	}
	if !strings.Contains(res.Contents[0].Text, "No snapshot") {
		t.Errorf("a fresh session has no snapshot: %q", res.Contents[0].Text)
	}

	// Wait needs no grant and exercises a real tool call end to end.
	call, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "Wait", Arguments: map[string]any{"duration": 0}})
	if err != nil {
		t.Fatalf("tools/call Wait: %v", err)
	}
	if call.IsError {
		t.Fatalf("Wait returned an error result: %+v", call.Content)
	}
	// Process list needs no grant either and reaches the engine.
	call, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "Process", Arguments: map[string]any{"mode": "list", "limit": 3}})
	if err != nil || call.IsError {
		t.Fatalf("tools/call Process: err=%v result=%+v", err, call)
	}
}
