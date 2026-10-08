//go:build darwin && (amd64 || arm64)

package macos

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/deploymenttheory/mcp-server-core/inventory"
)

// SystemInfo reports OS and hardware inventory for troubleshooting.
func SystemInfo() inventory.ServerTool {
	return NewToolFromHandler(
		ToolsetDiagnostics,
		mcp.Tool{
			Name:        "SystemInfo",
			Description: "Report OS, hardware, memory, disk, SIP, FileVault and MDM-enrollment inventory. Read-only; useful as the first step in a support diagnosis.",
			Annotations: &mcp.ToolAnnotations{Title: "System information", ReadOnlyHint: true},
			InputSchema: &jsonschema.Schema{Type: "object"},
		},
		func(ctx context.Context, deps ToolDependencies, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			si, err := deps.Desktop().GetSystemInfo(ctx)
			if err != nil {
				return NewToolResultErrorFromErr("failed to gather system info", err), nil
			}
			var b strings.Builder
			fmt.Fprintf(&b, "Host:      %s (%s)\n", si.ComputerName, si.Hostname)
			fmt.Fprintf(&b, "OS:        %s, %s\n", si.OSCaption, si.OSArch)
			fmt.Fprintf(&b, "Machine:   %s, serial %s\n", si.Model, si.Serial)
			fmt.Fprintf(&b, "CPU:       %s (%d cores / %d logical)\n", si.CPUName, si.PhysicalCores, si.LogicalCPUs)
			fmt.Fprintf(&b, "Memory:    %d MB free of %d MB\n", si.FreeMemoryMB, si.TotalMemoryMB)
			fmt.Fprintf(&b, "Last boot: %s\n", si.LastBoot)
			fmt.Fprintf(&b, "SIP:       %s\n", optBool(si.SIPEnabled, "enabled", "disabled"))
			fmt.Fprintf(&b, "FileVault: %s\n", optBool(si.FileVaultOn, "on", "off"))
			mdm := optBool(si.MDMEnrolled, "enrolled", "not enrolled")
			if si.MDMServer != "" {
				mdm += " (" + si.MDMServer + ")"
			}
			fmt.Fprintf(&b, "MDM:       %s\n", mdm)
			if len(si.Disks) > 0 {
				b.WriteString("Volumes:\n")
				for _, dk := range si.Disks {
					fmt.Fprintf(&b, "  %s (%s, %s) — %.1f GB free of %.1f GB (%d%% used)\n",
						dk.Mount, dk.Device, dk.Type, dk.FreeGB, dk.SizeGB, dk.UsedPct)
				}
			}
			return NewToolResultText(b.String()), nil
		},
	)
}

func optBool(v *bool, yes, no string) string {
	switch {
	case v == nil:
		return "unknown"
	case *v:
		return yes
	default:
		return no
	}
}

// Service lists or controls launchd services.
func Service() inventory.ServerTool {
	destructive := true
	return NewToolFromHandler(
		ToolsetDiagnostics,
		mcp.Tool{
			Name:        "Service",
			Description: "List or control launchd services. mode=list shows jobs in the domain (optionally filtered by label); mode=start/stop/restart/enable/disable controls a job by exact label. domain=user (default) or system (needs root).",
			Annotations: &mcp.ToolAnnotations{
				Title:           "launchd service control",
				ReadOnlyHint:    false,
				DestructiveHint: &destructive,
			},
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"mode":   {Type: "string", Enum: []any{"list", "start", "stop", "restart", "enable", "disable"}, Description: "Operation."},
					"name":   {Type: "string", Description: "Job label (exact for control; substring filter for list)."},
					"domain": {Type: "string", Enum: []any{"user", "system"}, Description: "user (gui/<uid>, default) or system (needs root)."},
				},
				Required: []string{"mode"},
			},
		},
		func(ctx context.Context, deps ToolDependencies, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args, err := ArgsMap(req)
			if err != nil {
				return NewToolResultError(err.Error()), nil
			}
			mode, err := OptionalStringEnum(args, "mode", "", "list", "start", "stop", "restart", "enable", "disable")
			if err != nil {
				return NewToolResultError(err.Error()), nil
			}
			domain, err := OptionalStringEnum(args, "domain", "user", "user", "system")
			if err != nil {
				return NewToolResultError(err.Error()), nil
			}
			system := domain == "system"
			dsk := deps.Desktop()

			if mode == "list" {
				services, err := dsk.ListServices(ctx, system, OptionalString(args, "name", ""))
				if err != nil {
					return NewToolResultErrorFromErr("failed to list services", err), nil
				}
				var b strings.Builder
				fmt.Fprintf(&b, "%-8s %-12s %s\n", "PID", "State", "Label")
				for _, s := range services {
					pid := "-"
					if s.PID > 0 {
						pid = fmt.Sprint(s.PID)
					}
					fmt.Fprintf(&b, "%-8s %-12s %s\n", pid, s.State, s.Label)
				}
				fmt.Fprintf(&b, "\n%d job(s) in %s.", len(services), domain)
				return NewToolResultText(b.String()), nil
			}

			name, err := RequiredString(args, "name")
			if err != nil {
				return NewToolResultError(err.Error()), nil
			}
			result, err := dsk.ControlService(ctx, system, name, mode)
			if err != nil {
				return NewToolResultErrorFromErr("service control failed", err), nil
			}
			return NewToolResultText(result), nil
		},
	)
}
