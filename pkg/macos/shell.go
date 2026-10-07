//go:build darwin && (amd64 || arm64)

package macos

import (
	"context"
	"errors"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/deploymenttheory/mcp-server-core/inventory"
	"github.com/deploymenttheory/mcp-server-core/toolkit"

	"github.com/deploymenttheory/macos-mcp-server/internal/clirunner"
)

// Shell executes an arbitrary script — the macOS counterpart of the Windows
// PowerShell tool. It is powerful and potentially destructive, so it lives in
// the non-default 'shell' toolset.
func Shell() inventory.ServerTool {
	destructive := true
	openWorld := true
	return NewToolFromHandler(
		ToolsetShell,
		mcp.Tool{
			Name: "Shell",
			Description: "macOS counterpart of the Windows PowerShell tool: run a script with zsh (default), bash or " +
				"osascript (AppleScript) and return its output and exit code. Runs with no rc files, as the server's " +
				"user, with full system access.",
			Annotations: &mcp.ToolAnnotations{
				Title:           "Run shell script",
				ReadOnlyHint:    false,
				DestructiveHint: &destructive,
				OpenWorldHint:   &openWorld,
			},
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"command":     {Type: "string", Description: "The script to execute."},
					"interpreter": {Type: "string", Enum: []any{"zsh", "bash", "osascript"}, Description: "Interpreter (default zsh)."},
					"timeout":     {Type: "integer", Description: "Timeout in seconds (default 30, clamped to 1-600)."},
				},
				Required: []string{"command"},
			},
		},
		func(ctx context.Context, deps ToolDependencies, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args, err := ArgsMap(req)
			if err != nil {
				return NewToolResultError(err.Error()), nil
			}
			command, err := RequiredString(args, "command")
			if err != nil {
				return NewToolResultError(err.Error()), nil
			}
			interpreter, err := OptionalStringEnum(args, "interpreter", "zsh", "zsh", "bash", "osascript")
			if err != nil {
				return NewToolResultError(err.Error()), nil
			}
			timeoutSec, err := OptionalInt(args, "timeout", 30)
			if err != nil {
				return NewToolResultError(err.Error()), nil
			}
			timeoutSec = toolkit.ClampInt(timeoutSec, 1, 600)

			res, err := clirunner.RunScript(ctx, interpreter, command, clirunner.Options{Timeout: time.Duration(timeoutSec) * time.Second})
			if errors.Is(err, clirunner.ErrTimeout) {
				return NewToolResultErrorf("Command timed out after %d second(s).\nPartial output:\n%s%s", timeoutSec, res.Stdout, res.Stderr), nil
			}
			if err != nil && res.ExitCode == 0 {
				return NewToolResultErrorFromErr("failed to run script", err), nil
			}
			out := res.Stdout
			if res.Stderr != "" {
				out += res.Stderr
			}
			result := NewToolResultTextf("Exit code: %d\n%s", res.ExitCode, out)
			if res.ExitCode != 0 {
				result.IsError = true
			}
			return result, nil
		},
	)
}
