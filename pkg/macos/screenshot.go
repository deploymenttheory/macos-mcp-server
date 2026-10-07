//go:build darwin && (amd64 || arm64)

package macos

import (
	"context"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/deploymenttheory/mcp-server-core/inventory"
)

// Screenshot captures every display as a PNG image. Read-only.
func Screenshot() inventory.ServerTool {
	return NewToolFromHandler(
		ToolsetScreen,
		mcp.Tool{
			Name:        "Screenshot",
			Description: "Capture every display as one PNG image. Read-only. Coordinates from Snapshot are in screen points; multiply image coordinates by the reported factor before using them with Click/Move.",
			Annotations: &mcp.ToolAnnotations{
				Title:        "Screenshot",
				ReadOnlyHint: true,
			},
			InputSchema: &jsonschema.Schema{Type: "object"},
		},
		func(ctx context.Context, deps ToolDependencies, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			pngData, w, h, ppp, err := deps.Desktop().Screenshot()
			if err != nil {
				return NewToolResultErrorFromErr("screenshot failed", err), nil
			}
			caption := fmt.Sprintf("Screenshot %dx%d", w, h)
			if ppp != 1 {
				caption += fmt.Sprintf(" (multiply image coordinates by %.3g for screen points)", ppp)
			}
			return NewToolResultImage(caption, pngData, "image/png"), nil
		},
	)
}

// DisplayInventory reports the connected displays, their geometry and scale.
func DisplayInventory() inventory.ServerTool {
	return NewToolFromHandler(
		ToolsetScreen,
		mcp.Tool{
			Name:        "DisplayInventory",
			Description: "List the connected displays with their bounds (points), work area, backing resolution and scale factor. Read-only. Useful for understanding the multi-display coordinate space used by Click/Move.",
			Annotations: &mcp.ToolAnnotations{Title: "Display inventory", ReadOnlyHint: true},
			InputSchema: &jsonschema.Schema{Type: "object"},
		},
		func(ctx context.Context, deps ToolDependencies, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			displays, err := deps.Desktop().Displays()
			if err != nil {
				return NewToolResultErrorFromErr("failed to enumerate displays", err), nil
			}
			var b []byte
			b = fmt.Appendf(b, "%d display(s):\n", len(displays))
			for _, d := range displays {
				primary := ""
				if d.Primary {
					primary = " (primary)"
				}
				b = fmt.Appendf(b, "  [%d]%s %s: %dx%d points at (%d,%d), work area %dx%d, %dx%d pixels (scale %gx)\n",
					d.Index, primary, d.Name,
					d.Bounds.Width(), d.Bounds.Height(), d.Bounds.Left, d.Bounds.Top,
					d.WorkArea.Width(), d.WorkArea.Height(),
					d.PixelWidth, d.PixelHeight, d.Scale)
			}
			return NewToolResultText(string(b)), nil
		},
	)
}
