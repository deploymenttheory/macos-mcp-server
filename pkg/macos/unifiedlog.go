//go:build darwin && (amd64 || arm64)

package macos

import (
	"context"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/deploymenttheory/mcp-server-core/inventory"
	"github.com/deploymenttheory/mcp-server-core/toolkit"

	"github.com/deploymenttheory/macos-mcp-server/internal/macdesktop"
)

// UnifiedLog queries the unified log — the macOS counterpart of the Windows
// EventLog tool. Read-only: the natural first step in diagnosing a crash or a
// failed service.
func UnifiedLog() inventory.ServerTool {
	return NewToolFromHandler(
		ToolsetDiagnostics,
		mcp.Tool{
			Name: "UnifiedLog",
			Description: "macOS counterpart of the Windows EventLog tool: query the unified log (`log show`). Filter by " +
				"subsystem, process, level and a look-back window in hours, or by message text; returns the most " +
				"recent matching entries as JSON. Read-only. Also accepts 'log' for subsystem and 'provider' for " +
				"process, for Windows parity.",
			Annotations: &mcp.ToolAnnotations{Title: "Query unified log", ReadOnlyHint: true},
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"subsystem": {Type: "string", Description: "Only entries from this subsystem, e.g. com.apple.securityd."},
					"process":   {Type: "string", Description: "Only entries from this process name."},
					"level":     {Type: "string", Enum: []any{"default", "info", "debug", "error", "fault"}, Description: "Only entries at this level."},
					"contains":  {Type: "string", Description: "Only entries whose message contains this text (case-insensitive)."},
					"hours":     {Type: "integer", Description: "Look back this many hours (default 1, max 168)."},
					"max":       {Type: "integer", Description: "Maximum entries to return (default 50, max 500)."},
					"log":       {Type: "string", Description: "Alias of subsystem (Windows parity)."},
					"provider":  {Type: "string", Description: "Alias of process (Windows parity)."},
				},
			},
		},
		func(ctx context.Context, deps ToolDependencies, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args, err := ArgsMap(req)
			if err != nil {
				return NewToolResultError(err.Error()), nil
			}
			level, err := OptionalStringEnum(args, "level", "", "default", "info", "debug", "error", "fault")
			if err != nil {
				return NewToolResultError(err.Error()), nil
			}
			hours, err := OptionalInt(args, "hours", 1)
			if err != nil {
				return NewToolResultError(err.Error()), nil
			}
			max, err := OptionalInt(args, "max", 50)
			if err != nil {
				return NewToolResultError(err.Error()), nil
			}
			q := macdesktop.LogQuery{
				Subsystem: OptionalString(args, "subsystem", OptionalString(args, "log", "")),
				Process:   OptionalString(args, "process", OptionalString(args, "provider", "")),
				Level:     level,
				Contains:  OptionalString(args, "contains", ""),
				Hours:     toolkit.ClampInt(hours, 1, 168),
				Max:       toolkit.ClampInt(max, 1, 500),
			}
			entries, err := deps.Desktop().QueryLog(ctx, q)
			if err != nil {
				return NewToolResultErrorFromErr("log query failed", err), nil
			}
			if len(entries) == 0 {
				return NewToolResultText("No matching entries."), nil
			}
			return jsonText(entries)
		},
	)
}
