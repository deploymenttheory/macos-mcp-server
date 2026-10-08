//go:build darwin && (amd64 || arm64)

package macos

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/deploymenttheory/mcp-server-core/inventory"

	"github.com/deploymenttheory/macos-mcp-server/internal/macdesktop"
)

// LaunchdJob lists, inspects and manages launchd jobs — the macOS counterpart
// of the Windows `ScheduledTask` tool.
//
// The tool is annotated destructive as a whole: list and get read, but create,
// delete, enable, disable and run all change the machine's scheduled work, and a
// policy rule matching the destructive annotation should cover every mode.
func LaunchdJob() inventory.ServerTool {
	destructive := true
	return NewToolFromHandler(
		ToolsetSystemAdmin,
		mcp.Tool{
			Name: "LaunchdJob",
			Description: "macOS counterpart of the Windows ScheduledTask tool: list, inspect and manage launchd jobs. " +
				"mode=list shows jobs (optionally filtered by label); mode=get shows one job's state; " +
				"mode=run/enable/disable/delete control a job by exact label; mode=create registers a job that runs a " +
				"program on a trigger (login, daily, weekly, interval, once). Jobs land in the user's LaunchAgents " +
				"unless domain=system (needs root).",
			Annotations: &mcp.ToolAnnotations{
				Title:           "launchd job management",
				ReadOnlyHint:    false,
				DestructiveHint: &destructive,
			},
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"mode":        {Type: "string", Enum: []any{"list", "get", "run", "enable", "disable", "delete", "create"}, Description: "Operation."},
					"name":        {Type: "string", Description: "Job label (exact for get/control/create; substring filter for list), e.g. com.example.backup."},
					"domain":      {Type: "string", Enum: []any{"user", "system"}, Description: "user (gui/<uid>, default) or system (LaunchDaemons, needs root)."},
					"program":     {Type: "string", Description: "Program to run (mode=create). Also accepted as 'action'."},
					"arguments":   {Type: "array", Description: "Arguments passed to the program (mode=create).", Items: &jsonschema.Schema{Type: "string"}},
					"trigger":     {Type: "string", Enum: []any{"login", "daily", "weekly", "interval", "once"}, Description: "When the job fires (mode=create)."},
					"time":        {Type: "string", Description: "Time of day HH:mm for daily/weekly/once (mode=create; default 09:00)."},
					"weekday":     {Type: "integer", Description: "0 (Sunday) to 6 for the weekly trigger."},
					"interval":    {Type: "integer", Description: "Seconds between runs for the interval trigger (minimum 60)."},
					"description": {Type: "string", Description: "Optional description (mode=create)."},
				},
				Required: []string{"mode"},
			},
		},
		func(ctx context.Context, deps ToolDependencies, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args, err := ArgsMap(req)
			if err != nil {
				return NewToolResultError(err.Error()), nil
			}
			mode, err := OptionalStringEnum(args, "mode", "", "list", "get", "run", "enable", "disable", "delete", "create")
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
				jobs, err := dsk.ListServices(ctx, system, OptionalString(args, "name", ""))
				if err != nil {
					return NewToolResultErrorFromErr("list failed", err), nil
				}
				return jsonText(jobs)
			}
			name, err := RequiredString(args, "name")
			if err != nil {
				return NewToolResultError(err.Error()), nil
			}
			switch mode {
			case "get":
				job, err := dsk.GetService(ctx, system, name)
				if err != nil {
					return NewToolResultErrorFromErr("get failed", err), nil
				}
				return jsonText(job)
			case "run":
				out, err := dsk.ControlService(ctx, system, name, "start")
				if err != nil {
					return NewToolResultErrorFromErr("run failed", err), nil
				}
				return NewToolResultText(out), nil
			case "enable", "disable":
				out, err := dsk.ControlService(ctx, system, name, mode)
				if err != nil {
					return NewToolResultErrorFromErr(mode+" failed", err), nil
				}
				return NewToolResultText(out), nil
			case "delete":
				if err := dsk.DeleteJob(ctx, system, name); err != nil {
					return NewToolResultErrorFromErr("delete failed", err), nil
				}
				return NewToolResultTextf("Deleted %s.", name), nil
			case "create":
				spec, err := jobSpecFromArgs(name, system, args)
				if err != nil {
					return NewToolResultError(err.Error()), nil
				}
				path, err := dsk.CreateJob(ctx, spec)
				if err != nil {
					return NewToolResultErrorFromErr("create failed", err), nil
				}
				return NewToolResultTextf("Created %s (%s).", name, path), nil
			default:
				return NewToolResultError("invalid mode"), nil
			}
		},
	)
}

func jobSpecFromArgs(label string, system bool, args map[string]any) (macdesktop.JobSpec, error) {
	program := OptionalString(args, "program", OptionalString(args, "action", ""))
	if program == "" {
		return macdesktop.JobSpec{}, fmt.Errorf("create requires a program")
	}
	trigger, err := OptionalStringEnum(args, "trigger", "", "login", "daily", "weekly", "interval", "once")
	if err != nil {
		return macdesktop.JobSpec{}, err
	}
	if trigger == "" {
		return macdesktop.JobSpec{}, fmt.Errorf("create requires a trigger (login, daily, weekly, interval, once)")
	}
	weekday, err := OptionalInt(args, "weekday", 0)
	if err != nil {
		return macdesktop.JobSpec{}, err
	}
	interval, err := OptionalInt(args, "interval", 0)
	if err != nil {
		return macdesktop.JobSpec{}, err
	}
	var arguments []string
	if raw, ok := args["arguments"].([]any); ok {
		for _, a := range raw {
			if s, ok := a.(string); ok {
				arguments = append(arguments, s)
			}
		}
	}
	return macdesktop.JobSpec{
		Label: label, Program: program, Arguments: arguments, Trigger: trigger,
		Time: OptionalString(args, "time", ""), Weekday: weekday, IntervalSec: interval,
		Description: OptionalString(args, "description", ""), System: system,
	}, nil
}

// jsonText renders v as an indented JSON text result.
func jsonText(v any) (*mcp.CallToolResult, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return NewToolResultErrorFromErr("render failed", err), nil
	}
	return NewToolResultText(string(b)), nil
}
