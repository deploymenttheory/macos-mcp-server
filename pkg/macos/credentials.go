//go:build darwin && (amd64 || arm64)

package macos

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/deploymenttheory/mcp-server-core/inventory"
	"github.com/deploymenttheory/mcp-server-core/toolkit"

	"github.com/deploymenttheory/macos-mcp-server/internal/macdesktop"
)

// Credentials exposes the credentials supplied to the server at startup.
//
// The security property this tool is built around: it can *use* a secret but
// can never *reveal* one. There is deliberately no action that returns
// plaintext. The inject action reads the secret inside the desktop engine and
// converts it straight to keystrokes, so the value never enters a tool result,
// the audit log, the conversation transcript, or the model's context — only a
// coarse description of how much was typed comes back.
func Credentials() inventory.ServerTool {
	destructive := true
	return NewToolFromHandler(
		ToolsetCredentials,
		mcp.Tool{
			Name: "Credentials",
			Description: "Use credentials supplied to this server at startup and held in the login keychain. " +
				"Modes: 'list' reports the available credentials (names, targets, usernames — never secrets); " +
				"'verify' checks one is still present; 'inject' types a credential's secret into a field, " +
				"optionally clicking a target first (by 'label'/'name_target' from the last Snapshot, or explicit " +
				"'loc' [x,y]).\n\n" +
				"Secrets cannot be read: no mode returns a credential's value, so use 'inject' to sign in rather " +
				"than trying to retrieve a password. Refer to credentials by their 'name'.",
			Annotations: &mcp.ToolAnnotations{
				Title:        "Credentials list/verify/inject",
				ReadOnlyHint: false, // inject synthesizes input
				// Destructive so rules and rate limits matching that annotation
				// cover credential injection.
				DestructiveHint: &destructive,
			},
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"mode": {
						Type:        "string",
						Description: "list (default), verify, or inject.",
						Enum:        []any{"list", "verify", "inject"},
					},
					"name": {
						Type:        "string",
						Description: "Credential name, as reported by mode 'list'. Required for verify and inject.",
					},
					"label": {
						Type:        "integer",
						Description: "Optional: click this Snapshot label (e.g. the password field) before typing.",
					},
					"automation_id": {
						Type: "string",
						Description: "Optional: click the element with this accessibility identifier before typing. " +
							"The most stable way to name a password field.",
					},
					"name_target": {
						Type:        "string",
						Description: "Optional: click the element with this accessibility name before typing (alternative to label).",
					},
					"control_type": {
						Type:        "string",
						Description: "Optional: narrow name_target to this control type (e.g. Edit).",
					},
					"name_match": {
						Type: "string", Enum: []any{"exact", "contains", "matches"},
						Description: "Optional: how name_target is matched. Default exact.",
					},
					"occurrence": {
						Type: "string",
						Description: "Optional: which match to use when several share a name — 'unique' " +
							"(default, ambiguity fails), 'first', or a 0-based index.",
					},
					"nth": {
						Type:        "integer",
						Description: "Deprecated alias for occurrence: a 0-based index among name_target matches.",
					},
					"loc": {
						Type:        "array",
						Description: "Optional: click these [x,y] screen points before typing.",
						Items:       &jsonschema.Schema{Type: "integer"},
					},
					"press_enter": {
						Type:        "boolean",
						Description: "Optional: press Return after injecting, to submit the form.",
					},
				},
				Required: []string{"mode"},
			},
		},
		credentialsHandler,
	)
}

func credentialsHandler(_ context.Context, deps ToolDependencies, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, err := ArgsMap(req)
	if err != nil {
		return NewToolResultError(err.Error()), nil
	}
	mode, err := OptionalStringEnum(args, "mode", "list", "list", "verify", "inject")
	if err != nil {
		return NewToolResultError(err.Error()), nil
	}

	registry := deps.Credentials()
	if len(registry) == 0 {
		return NewToolResultError("no credentials are configured; start the server with --credentials-file " +
			"to supply credentials at init"), nil
	}

	switch mode {
	case "list":
		return credentialsList(deps, registry)
	case "verify":
		return credentialsVerify(deps, registry, args)
	default:
		return credentialsInject(deps, registry, args)
	}
}

// credentialsList reports the configured credentials with live presence, and
// never includes a secret.
func credentialsList(deps ToolDependencies, registry []toolkit.CredentialInfo) (*mcp.CallToolResult, error) {
	out := make([]toolkit.CredentialInfo, 0, len(registry))
	for _, c := range registry {
		c.Present, _ = deps.Desktop().CredentialPresent(c.Target, c.Username)
		c.Injectable = macdesktop.CredentialType(c.Type).Readable()
		out = append(out, c)
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return NewToolResultErrorFromErr("failed to render credential list", err), nil
	}
	return NewToolResultText(string(b)), nil
}

func credentialsVerify(
	deps ToolDependencies,
	registry []toolkit.CredentialInfo,
	args map[string]any,
) (*mcp.CallToolResult, error) {
	cred, err := lookupCredential(registry, args)
	if err != nil {
		return NewToolResultError(err.Error()), nil
	}
	present, err := deps.Desktop().CredentialPresent(cred.Target, cred.Username)
	if err != nil {
		return NewToolResultErrorFromErr("failed to check the keychain", err), nil
	}
	if !present {
		return NewToolResultTextf("Credential %q (target %q) is NOT present in the login keychain.",
			cred.Name, cred.Target), nil
	}
	return NewToolResultTextf("Credential %q (target %q, username %q) is present and injectable.",
		cred.Name, cred.Target, cred.Username), nil
}

// credentialsInject types the secret, optionally clicking a target field first
// so the click and the keystrokes are one serialised operation and no other
// window can take focus between them.
func credentialsInject(
	deps ToolDependencies,
	registry []toolkit.CredentialInfo,
	args map[string]any,
) (*mcp.CallToolResult, error) {
	cred, err := lookupCredential(registry, args)
	if err != nil {
		return NewToolResultError(err.Error()), nil
	}

	clickAt, err := resolveCredentialTarget(deps, args)
	if err != nil {
		return NewToolResultError(err.Error()), nil
	}

	// The engine confirms, on the main thread and immediately before typing,
	// that the destination is a secure text field — unless this credential
	// opts out. See macdesktop.requireMaskedFocus.
	typed, err := deps.Desktop().InjectCredential(cred.Target, cred.Username, clickAt, cred.AllowUnmaskedTarget)
	if err != nil {
		// A refused destination is a decision the model can act on — retarget,
		// or ask the operator for the opt-out — so it gets the remedy spelled
		// out rather than reading as an engine failure.
		if errors.Is(err, macdesktop.ErrInjectTargetNotMasked) {
			return NewToolResultErrorf("%s%s", err.Error(), macdesktop.InjectRemedy()), nil
		}
		return NewToolResultErrorFromErr(fmt.Sprintf("failed to inject credential %q", cred.Name), err), nil
	}

	pressEnter := OptionalBool(args, "press_enter", false)
	if pressEnter {
		if err := deps.Desktop().SendShortcut([]string{"enter"}); err != nil {
			return NewToolResultErrorFromErr("injected the credential but failed to press Return", err), nil
		}
	}

	// Confirming the injection happened does not require the exact length. A
	// precise count, alongside the target and username that list already
	// reports, narrows an offline search and confirms a guessed password
	// instantly. A coarse band tells the model what it needs — that keystrokes
	// were delivered — and tells an attacker much less.
	msg := fmt.Sprintf("Injected credential %q (%s) for target %q.", cred.Name, describeTyped(typed), cred.Target)
	if pressEnter {
		msg += " Pressed Return."
	}
	return NewToolResultText(msg + " Take a Snapshot to confirm the result."), nil
}

// resolveCredentialTarget maps the optional click-target arguments to a point.
// It reuses the shared label/name/loc resolution so credential injection
// targets elements exactly the way Click and Type do. Returns nil for "type at
// focus".
func resolveCredentialTarget(deps ToolDependencies, args map[string]any) (*macdesktop.Point, error) {
	// resolveTarget reads the element name from "name", which this tool uses
	// for the credential name; expose it as "name_target" and translate here.
	local := map[string]any{}
	for k, v := range args {
		if k == "name" {
			continue
		}
		local[k] = v
	}
	if nt, ok := args["name_target"]; ok {
		local["name"] = nt
	}

	x, y, ok, err := resolveTarget(deps, local)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil //nolint:nilnil // nil means "type at the current focus"
	}
	return &macdesktop.Point{X: x, Y: y}, nil
}

// ErrUnknownCredential reports a name that is not in the registry.
var ErrUnknownCredential = errors.New("no such credential")

// lookupCredential resolves the required "name" argument to a configured entry.
func lookupCredential(registry []toolkit.CredentialInfo, args map[string]any) (toolkit.CredentialInfo, error) {
	name, err := RequiredString(args, "name")
	if err != nil {
		return toolkit.CredentialInfo{}, err
	}
	for _, c := range registry {
		if c.Name == name {
			c.Injectable = macdesktop.CredentialType(c.Type).Readable()
			return c, nil
		}
	}
	available := make([]string, 0, len(registry))
	for _, c := range registry {
		available = append(available, c.Name)
	}
	return toolkit.CredentialInfo{}, fmt.Errorf("%w: %q; configured credentials: %v", ErrUnknownCredential, name, available)
}

// describeTyped renders how much was typed as a band rather than a count.
func describeTyped(n int) string {
	switch {
	case n == 0:
		return "nothing was typed"
	case n < 8:
		return "a short secret"
	case n < 16:
		return "a medium-length secret"
	default:
		return "a long secret"
	}
}
