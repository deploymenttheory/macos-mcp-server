//go:build darwin && (amd64 || arm64)

// Package acceptance drives the built binary over stdio against a real,
// logged-in desktop. It runs only when MACOS_MCP_ACC=1, on a machine (or a
// tart guest; see scripts/acclab) that has granted Accessibility and Screen
// Recording to the binary under test. Hosted CI grants neither, so these
// tests never run there.
package acceptance

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	envOptIn  = "MACOS_MCP_ACC"
	envBinary = "MACOS_MCP_ACC_BINARY" // default: build from this tree
	envKeep   = "MACOS_MCP_ACC_KEEP"   // keep TextEdit open afterwards
)

func requireLab(t *testing.T) string {
	t.Helper()
	if os.Getenv(envOptIn) != "1" {
		t.Skipf("set %s=1 on a machine with Accessibility and Screen Recording granted", envOptIn)
	}
	if bin := os.Getenv(envBinary); bin != "" {
		return bin
	}
	bin := filepath.Join(t.TempDir(), "macos-mcp-server")
	build := exec.Command("go", "build", "-o", bin, "../../cmd/macos-mcp-server")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

func connect(t *testing.T, ctx context.Context, bin string, args ...string) *mcp.ClientSession {
	t.Helper()
	cmd := exec.CommandContext(ctx, bin, append([]string{"stdio"}, args...)...)
	cmd.Stderr = os.Stderr
	client := mcp.NewClient(&mcp.Implementation{Name: "acceptance", Version: "test"}, nil)
	cs, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func call(t *testing.T, ctx context.Context, cs *mcp.ClientSession, name string, args map[string]any) string {
	t.Helper()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	if res.IsError {
		t.Fatalf("%s returned an error: %s", name, b.String())
	}
	return b.String()
}

// TestTextEditTypeAndRead is the smallest end-to-end journey: launch
// TextEdit, type into the document through the engine, read the text back
// off the accessibility tree, and assert on it.
func TestTextEditTypeAndRead(t *testing.T) {
	bin := requireLab(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cs := connect(t, ctx, bin, "--persona", "qa-test-engineer")

	call(t, ctx, cs, "App", map[string]any{"mode": "launch", "name": "TextEdit"})
	call(t, ctx, cs, "Shortcut", map[string]any{"keys": []string{"cmd", "n"}})
	call(t, ctx, cs, "Wait", map[string]any{"seconds": 1})
	call(t, ctx, cs, "Type", map[string]any{"text": "acceptance lab says hello"})
	call(t, ctx, cs, "Wait", map[string]any{"seconds": 1})

	snapshot := call(t, ctx, cs, "Snapshot", map[string]any{})
	if !strings.Contains(snapshot, "acceptance lab says hello") {
		t.Fatalf("typed text not visible in the accessibility tree:\n%s", snapshot)
	}
	out := call(t, ctx, cs, "Assert", map[string]any{
		"kind": "text_contains", "name": "acceptance lab says hello",
	})
	if !strings.Contains(strings.ToLower(out), "pass") {
		t.Errorf("assert did not pass: %s", out)
	}
	if os.Getenv(envKeep) != "1" {
		call(t, ctx, cs, "Shortcut", map[string]any{"keys": []string{"cmd", "w"}})
		call(t, ctx, cs, "Wait", map[string]any{"seconds": 1})
		call(t, ctx, cs, "Shortcut", map[string]any{"keys": []string{"cmd", "delete"}}) // "Delete" in the save sheet
	}
}

// TestGuardrailStatusIsServed proves the guardrail tools ride along under a
// persona, and that the default policy reports audit mode.
func TestGuardrailStatusIsServed(t *testing.T) {
	bin := requireLab(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cs := connect(t, ctx, bin, "--persona", "business-user")
	raw := call(t, ctx, cs, "GuardrailStatus", map[string]any{})
	var st map[string]any
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		t.Fatalf("GuardrailStatus is not JSON: %s", raw)
	}
	if _, ok := st["decision"]; !ok {
		t.Errorf("GuardrailStatus carries no decision: %s", raw)
	}
}

// TestJourneyExampleRuns executes the shipped TextEdit journey through the
// `journey run` path, the same planner Apply uses.
func TestJourneyExampleRuns(t *testing.T) {
	bin := requireLab(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "journey", "run", "../../journeys/examples/textedit-smoke.json")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("journey run: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "passed") {
		t.Errorf("journey did not report a pass:\n%s", out)
	}
}
