//go:build darwin && (amd64 || arm64)

package macos

import (
	"context"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/deploymenttheory/mcp-server-core/inventory"
)

// Notification shows a user notification through Notification Center.
//
// Destructive: this writes model-authored text to the user's screen. The target
// is the human at the console rather than the machine — "your session expired,
// sign in again" is a phishing primitive — so it carries the hint the Windows
// toast does, and the app_id attribution the Windows tool accepts is deliberately
// not offered: macOS attributes the notification to the posting application.
func Notification() inventory.ServerTool {
	destructive := true
	return NewToolFromHandler(
		ToolsetSystem,
		mcp.Tool{
			Name:        "Notification",
			Description: "Show a macOS user notification with a title and message.",
			Annotations: &mcp.ToolAnnotations{Title: "Show notification", ReadOnlyHint: false, DestructiveHint: &destructive},
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"title":   {Type: "string", Description: "Notification title."},
					"message": {Type: "string", Description: "Notification body text."},
					"app_id":  {Type: "string", Description: "Accepted for compatibility with the Windows server and ignored: macOS attributes a notification to the posting application."},
				},
				Required: []string{"title", "message"},
			},
		},
		func(ctx context.Context, deps ToolDependencies, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args, err := ArgsMap(req)
			if err != nil {
				return NewToolResultError(err.Error()), nil
			}
			title, err := RequiredString(args, "title")
			if err != nil {
				return NewToolResultError(err.Error()), nil
			}
			message, err := RequiredString(args, "message")
			if err != nil {
				return NewToolResultError(err.Error()), nil
			}
			if err := deps.Desktop().PostNotification(ctx, title, message); err != nil {
				return NewToolResultErrorFromErr("notification failed", err), nil
			}
			return NewToolResultTextf("Notification shown: %q", title), nil
		},
	)
}
