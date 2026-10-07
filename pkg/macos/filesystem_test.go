//go:build darwin && (amd64 || arm64)

package macos

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/deploymenttheory/mcp-server-core/inventory"
	"github.com/deploymenttheory/mcp-server-core/toolkit"
)

// callTool invokes a tool's handler directly with the given dependencies, the
// way the planner and the tests do.
func callTool(t *testing.T, tool inventory.ServerTool, deps ToolDependencies, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	res, err := tool.Handler(deps)(toolkit.ContextWithDeps(context.Background(), deps),
		&mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: tool.Tool.Name, Arguments: raw}})
	if err != nil {
		t.Fatalf("%s returned a Go error: %v", tool.Tool.Name, err)
	}
	return res
}

func resultText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func protect(path, label string, tree, denyRead, denyWrite bool) toolkit.ProtectedPath {
	return toolkit.NewProtectedPath(NormalizePath, path, label, tree, denyRead, denyWrite)
}

func TestFileSystemRefusesProtectedPaths(t *testing.T) {
	creds := "/etc/macos-mcp/creds.json"
	auditDir := "/Library/Application Support/MacOSMCP/audit/"
	deps := NewBaseDeps(nil, nil, nil)
	deps.WithProtectedPaths([]toolkit.ProtectedPath{
		protect(creds, "the credentials file", false, true, true),
		protect(auditDir, "the audit log", true, false, true),
	})
	fs := FileSystem()

	// The credentials file is refused for read (a read is plaintext into context)
	// and for write, before any disk access.
	for _, mode := range []string{"read", "write"} {
		res := callTool(t, fs, deps, map[string]any{"mode": mode, "path": creds, "content": "x"})
		if !res.IsError || !strings.Contains(resultText(res), "credentials file") {
			t.Errorf("%s of the credentials file should be refused: isErr=%v text=%q",
				mode, res.IsError, resultText(res))
		}
	}
	// The /private alias of the same file is the same file.
	res := callTool(t, fs, deps, map[string]any{"mode": "read", "path": "/private" + creds})
	if !res.IsError || !strings.Contains(resultText(res), "credentials file") {
		t.Errorf("the /private spelling must be folded: %q", resultText(res))
	}

	// An audit session file is readable but not writable/deletable.
	auditFile := auditDir + "session-x.audit.jsonl"
	if res := callTool(t, fs, deps, map[string]any{"mode": "delete", "path": auditFile}); !res.IsError ||
		!strings.Contains(resultText(res), "audit log") {
		t.Errorf("delete of an audit file should be refused: %q", resultText(res))
	}
}

// TestFileSystemAllowsUnprotectedPaths confirms the guard does not block ordinary
// work: a write to an unrelated path succeeds, and a read gets it back.
func TestFileSystemAllowsUnprotectedPaths(t *testing.T) {
	deps := NewBaseDeps(nil, nil, nil)
	deps.WithProtectedPaths([]toolkit.ProtectedPath{
		protect("/etc/macos-mcp/creds.json", "the credentials file", false, true, true),
	})
	fs := FileSystem()

	target := filepath.Join(t.TempDir(), "note.txt")
	res := callTool(t, fs, deps, map[string]any{"mode": "write", "path": target, "content": "hello"})
	if res.IsError {
		t.Fatalf("write to an unprotected path should succeed: %q", resultText(res))
	}
	if got, _ := os.ReadFile(target); string(got) != "hello" {
		t.Errorf("file content = %q, want hello", got)
	}
	if res := callTool(t, fs, deps, map[string]any{"mode": "read", "path": target}); res.IsError || resultText(res) != "hello" {
		t.Errorf("read back = %q", resultText(res))
	}
	if res := callTool(t, fs, deps, map[string]any{"mode": "delete", "path": target, "permanent": true}); res.IsError {
		t.Errorf("permanent delete: %q", resultText(res))
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Error("the file should be gone after a permanent delete")
	}
}
