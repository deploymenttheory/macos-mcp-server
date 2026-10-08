//go:build darwin && (amd64 || arm64)

package macos

import (
	"context"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/deploymenttheory/mcp-server-core/inventory"
	"github.com/deploymenttheory/mcp-server-core/toolkit"
)

// App launches, switches to, resizes, hides and quits applications and windows.
func App() inventory.ServerTool {
	destructive, openWorld := true, true
	return NewToolFromHandler(
		ToolsetApps,
		mcp.Tool{
			Name: "App",
			Description: "Manage applications and windows. mode=launch starts an app by name, bundle identifier or .app path " +
				"(e.g. \"TextEdit\", \"com.apple.Safari\"), or opens a URL in its default handler; " +
				"mode=switch brings a window (matched by title or app-name substring) to the foreground; " +
				"mode=resize moves/resizes a matched window; mode=hide / mode=unhide hide or reveal an app; " +
				"mode=quit asks an app to quit (force=true kills it). " +
				"To start a program by full path, use LaunchExecutable (shell toolset).",
			// Destructive and open-world: a launch starts an arbitrary installed
			// application, and a URL-shaped name opens the browser.
			Annotations: &mcp.ToolAnnotations{
				Title:           "App / window control",
				ReadOnlyHint:    false,
				DestructiveHint: &destructive,
				OpenWorldHint:   &openWorld,
			},
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"mode":        {Type: "string", Enum: []any{"launch", "switch", "resize", "hide", "unhide", "quit"}, Description: "Operation."},
					"name":        {Type: "string", Description: "App name, bundle id, .app path or URL (launch); window title or app-name substring (switch/resize); app name or bundle id (hide/unhide/quit)."},
					"window_loc":  {Type: "array", Description: "Target [x,y] for resize, in points.", Items: &jsonschema.Schema{Type: "integer"}},
					"window_size": {Type: "array", Description: "Target [width,height] for resize, in points.", Items: &jsonschema.Schema{Type: "integer"}},
					"force":       {Type: "boolean", Description: "quit: kill the app instead of asking it to quit (default false)."},
				},
				Required: []string{"mode"},
			},
		},
		func(ctx context.Context, deps ToolDependencies, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args, err := ArgsMap(req)
			if err != nil {
				return NewToolResultError(err.Error()), nil
			}
			mode, err := OptionalStringEnum(args, "mode", "", "launch", "switch", "resize", "hide", "unhide", "quit")
			if err != nil {
				return NewToolResultError(err.Error()), nil
			}
			name, err := RequiredString(args, "name")
			if err != nil {
				return NewToolResultError(err.Error()), nil
			}
			dsk := deps.Desktop()

			switch mode {
			case "launch":
				// A URL-shaped name is a navigation, not an app launch: it opens the
				// default handler. Enforce HTTPS has to see it.
				if scheme, isURL := toolkit.URLSchemeIfURL(name); isURL && scheme == "http" && deps.EnforceHTTPS() {
					return NewToolResultErrorf("%s: %q would open the default browser at a plaintext "+
						"address. Retry with an https:// URL.", toolkit.ErrPlaintextHTTP, name), nil
				}
				target, err := dsk.LaunchApp(ctx, name)
				if err != nil {
					return NewToolResultErrorFromErr("launch failed", err), nil
				}
				return NewToolResultTextf("Launched %q (%s).", name, target), nil

			case "switch":
				w, err := dsk.ActivateWindow(name)
				if err != nil {
					return NewToolResultErrorFromErr("switch failed", err), nil
				}
				return NewToolResultTextf("Switched to %q (%s) [pid %d].", w.Title, w.App, w.ProcessID), nil

			case "resize":
				loc, err := OptionalIntSlice(args, "window_loc")
				if err != nil {
					return NewToolResultError(err.Error()), nil
				}
				size, err := OptionalIntSlice(args, "window_size")
				if err != nil {
					return NewToolResultError(err.Error()), nil
				}
				if len(loc) < 2 || len(size) < 2 {
					return NewToolResultError("resize requires window_loc [x,y] and window_size [width,height]"), nil
				}
				w, err := dsk.ResizeWindow(name, loc[0], loc[1], size[0], size[1])
				if err != nil {
					return NewToolResultErrorFromErr("resize failed", err), nil
				}
				return NewToolResultTextf("Resized %q to %dx%d at (%d,%d).", w.Title, size[0], size[1], loc[0], loc[1]), nil

			case "hide", "unhide":
				label, err := dsk.HideApp(name, mode == "hide")
				if err != nil {
					return NewToolResultErrorFromErr(mode+" failed", err), nil
				}
				if mode == "hide" {
					return NewToolResultTextf("Hid %q.", label), nil
				}
				return NewToolResultTextf("Revealed %q.", label), nil

			case "quit":
				force := OptionalBool(args, "force", false)
				label, err := dsk.QuitApp(name, force)
				if err != nil {
					return NewToolResultErrorFromErr("quit failed", err), nil
				}
				if force {
					return NewToolResultTextf("Killed %q.", label), nil
				}
				return NewToolResultTextf("Asked %q to quit.", label), nil

			default:
				return NewToolResultError("invalid mode"), nil
			}
		},
	)
}
