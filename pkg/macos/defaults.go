//go:build darwin && (amd64 || arm64)

package macos

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/deploymenttheory/mcp-server-core/inventory"
)

// Defaults reads and writes preference domains — the macOS counterpart of the
// Windows `Registry` tool. Values are read and written through CFPreferences,
// which is what lets the tool tell a managed value (forced by a configuration
// profile) from an ordinary one and refuse to write over it.
func Defaults() inventory.ServerTool {
	destructive := true
	return NewToolFromHandler(
		ToolsetSystemAdmin,
		mcp.Tool{
			Name: "Defaults",
			Description: "macOS counterpart of the Windows Registry tool: read and write preference domains (what `defaults` " +
				"manages). A domain is a bundle identifier such as \"com.apple.finder\" or \"NSGlobalDomain\". " +
				"Modes: get (read a key), set (write a key), delete (remove a key), list (enumerate a domain's keys), " +
				"domains (list domains with preferences). A key managed by a configuration profile is reported " +
				"as managed and cannot be set or deleted.",
			Annotations: &mcp.ToolAnnotations{
				Title:           "Preference domain get/set/delete/list",
				ReadOnlyHint:    false,
				DestructiveHint: &destructive,
			},
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"mode":   {Type: "string", Enum: []any{"get", "set", "delete", "list", "domains"}, Description: "Operation."},
					"domain": {Type: "string", Description: "Preference domain (bundle id or NSGlobalDomain). Also accepted as 'path' for Windows parity."},
					"key":    {Type: "string", Description: "Preference key (get/set/delete). Also accepted as 'name'."},
					"value":  {Type: "string", Description: "Value to write (set). Parsed per 'type'; array/dict take JSON."},
					"type":   {Type: "string", Enum: []any{"string", "int", "float", "bool", "array", "dict"}, Description: "Value type for set (default string)."},
				},
				Required: []string{"mode"},
			},
		},
		func(ctx context.Context, deps ToolDependencies, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args, err := ArgsMap(req)
			if err != nil {
				return NewToolResultError(err.Error()), nil
			}
			mode, err := OptionalStringEnum(args, "mode", "", "get", "set", "delete", "list", "domains")
			if err != nil {
				return NewToolResultError(err.Error()), nil
			}
			dsk := deps.Desktop()
			if mode == "domains" {
				return NewToolResultText(strings.Join(dsk.PreferenceDomains(), "\n")), nil
			}
			domain := OptionalString(args, "domain", OptionalString(args, "path", ""))
			if domain == "" {
				return NewToolResultError("domain is required"), nil
			}
			key := OptionalString(args, "key", OptionalString(args, "name", ""))

			switch mode {
			case "get":
				if key == "" {
					return NewToolResultError("key is required for get"), nil
				}
				pv, err := dsk.PreferenceGet(domain, key)
				if err != nil {
					return NewToolResultErrorFromErr("read failed", err), nil
				}
				return NewToolResultText(renderPreference(pv.Value, pv.Managed)), nil

			case "list":
				prefs, err := dsk.PreferenceList(domain)
				if err != nil {
					return NewToolResultErrorFromErr("list failed", err), nil
				}
				if len(prefs) == 0 {
					return NewToolResultTextf("%s has no preferences for this user.", domain), nil
				}
				var b strings.Builder
				for _, p := range prefs {
					fmt.Fprintf(&b, "%s = %s\n", p.Key, renderPreference(p.Value, p.Managed))
				}
				return NewToolResultText(strings.TrimRight(b.String(), "\n")), nil

			case "set":
				if key == "" {
					return NewToolResultError("key is required for set"), nil
				}
				valType, err := OptionalStringEnum(args, "type", "string", "string", "int", "float", "bool", "array", "dict")
				if err != nil {
					return NewToolResultError(err.Error()), nil
				}
				value, err := parsePreferenceValue(OptionalString(args, "value", ""), valType)
				if err != nil {
					return NewToolResultError(err.Error()), nil
				}
				if err := dsk.PreferenceSet(ctx, domain, key, value); err != nil {
					return NewToolResultErrorFromErr("write failed", err), nil
				}
				return NewToolResultTextf("Set %s %s.", domain, key), nil

			case "delete":
				if key == "" {
					return NewToolResultError("key is required for delete"), nil
				}
				if err := dsk.PreferenceDelete(domain, key); err != nil {
					return NewToolResultErrorFromErr("delete failed", err), nil
				}
				return NewToolResultTextf("Deleted %s %s.", domain, key), nil

			default:
				return NewToolResultError("invalid mode"), nil
			}
		},
	)
}

// parsePreferenceValue turns the tool's string argument into the typed value.
func parsePreferenceValue(raw, valType string) (any, error) {
	switch valType {
	case "int":
		i, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("value %q is not an integer", raw)
		}
		return i, nil
	case "float":
		f, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if err != nil {
			return nil, fmt.Errorf("value %q is not a number", raw)
		}
		return f, nil
	case "bool":
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "true", "1", "yes":
			return true, nil
		case "false", "0", "no":
			return false, nil
		}
		return nil, fmt.Errorf("value %q is not a boolean", raw)
	case "array":
		var v []any
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			return nil, fmt.Errorf("value is not a JSON array: %w", err)
		}
		return v, nil
	case "dict":
		var v map[string]any
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			return nil, fmt.Errorf("value is not a JSON object: %w", err)
		}
		return v, nil
	default:
		return raw, nil
	}
}

// renderPreference renders a value as JSON, with the managed marker.
func renderPreference(v any, managed bool) string {
	b, err := json.Marshal(v)
	s := string(b)
	if err != nil {
		s = fmt.Sprint(v)
	}
	if managed {
		s += "  (managed by a configuration profile)"
	}
	return s
}
