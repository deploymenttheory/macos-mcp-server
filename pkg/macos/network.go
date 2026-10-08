//go:build darwin && (amd64 || arm64)

package macos

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/deploymenttheory/mcp-server-core/inventory"
	"github.com/deploymenttheory/mcp-server-core/toolkit"
)

// Network reports interface, DNS, proxy and route configuration, Wi-Fi state,
// and tests connectivity to a host. Read-only, but open-world: the "test" mode
// reaches the network directly and so is not constrained by the egress proxy —
// an honest residual.
func Network() inventory.ServerTool {
	openWorld := true
	return NewToolFromHandler(
		ToolsetDiagnostics,
		mcp.Tool{
			Name: "Network",
			Description: "Inspect networking and test connectivity. mode=adapters lists network interfaces; " +
				"mode=dns shows the resolver configuration; mode=config shows the default route; mode=proxies shows " +
				"the system proxy settings; mode=wifi shows the Wi-Fi network; mode=test pings a host or opens a TCP " +
				"port. Read-only. Note: mode=test reaches the network directly and is not routed through the egress proxy.",
			Annotations: &mcp.ToolAnnotations{
				Title:         "Network diagnostics",
				ReadOnlyHint:  true,
				OpenWorldHint: &openWorld,
			},
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"mode": {Type: "string", Enum: []any{"adapters", "dns", "config", "proxies", "wifi", "test"}, Description: "Operation."},
					"host": {Type: "string", Description: "Target host or IP (required for mode=test)."},
					"port": {Type: "integer", Description: "TCP port to test (mode=test); omit for an ICMP ping."},
				},
				Required: []string{"mode"},
			},
		},
		func(ctx context.Context, deps ToolDependencies, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args, err := ArgsMap(req)
			if err != nil {
				return NewToolResultError(err.Error()), nil
			}
			mode, err := OptionalStringEnum(args, "mode", "", "adapters", "dns", "config", "proxies", "wifi", "test")
			if err != nil {
				return NewToolResultError(err.Error()), nil
			}
			dsk := deps.Desktop()
			var out string
			switch mode {
			case "adapters":
				adapters, aerr := dsk.Adapters()
				if aerr != nil {
					return NewToolResultErrorFromErr("adapter query failed", aerr), nil
				}
				var b strings.Builder
				for _, a := range adapters {
					state := "down"
					if a.Up {
						state = "up"
					}
					fmt.Fprintf(&b, "%-6s %-24s %-10s %-5s %s\n", a.Device, a.Name, a.Type, state, a.MAC)
					for _, addr := range a.Addresses {
						fmt.Fprintf(&b, "       %s\n", addr)
					}
				}
				out = b.String()
			case "dns":
				out, err = dsk.DNSConfig(ctx)
			case "config":
				out, err = dsk.RouteConfig(ctx)
			case "proxies":
				out, err = dsk.ProxyConfig(ctx)
			case "wifi":
				out, err = dsk.WiFiStatus(ctx)
			case "test":
				host, herr := RequiredString(args, "host")
				if herr != nil {
					return NewToolResultError(herr.Error()), nil
				}
				port, perr := OptionalInt(args, "port", 0)
				if perr != nil {
					return NewToolResultError(perr.Error()), nil
				}
				if port > 0 {
					port = toolkit.ClampInt(port, 1, 65535)
				}
				out, err = dsk.TestConnection(ctx, host, port)
			}
			if err != nil {
				return NewToolResultErrorFromErr("network query failed", err), nil
			}
			if strings.TrimSpace(out) == "" {
				return NewToolResultText("(no output)"), nil
			}
			return NewToolResultText(strings.TrimRight(out, "\n")), nil
		},
	)
}
