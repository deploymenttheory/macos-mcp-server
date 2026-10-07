//go:build darwin && (amd64 || arm64)

// Package macmcp wires the macOS automation engine and tool inventory into an
// MCP server and runs it over a transport. It is the bootstrap layer between
// the cobra CLI (cmd/macos-mcp-server) and the domain package (pkg/macos),
// composed from the shared mcp-server-core runtime.
package macmcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/deploymenttheory/macos-mcp-server/internal/macdesktop"
	"github.com/deploymenttheory/macos-mcp-server/pkg/macos"
	"github.com/deploymenttheory/mcp-server-core/inventory"
	"github.com/deploymenttheory/mcp-server-core/runtime"
	"github.com/deploymenttheory/mcp-server-core/surface"
)

// ServerName and ServerTitle identify this server in server/discover.
const (
	ServerName  = "macos-mcp-server"
	ServerTitle = "macOS MCP Server"
	// EnvPrefix is the prefix of every environment variable this server reads.
	EnvPrefix = "MACOS_MCP_"
)

// Config controls how the server is assembled and which tools it exposes.
type Config struct {
	// Version is reported in the MCP server implementation info.
	Version string

	// Persona, if set, selects a built-in preset (see macos.Personas) that
	// determines the toolset selection and read-only default. Explicit Toolsets
	// / ReadOnly settings override the persona.
	Persona string

	// Toolsets is the toolset selection (values accepted by
	// Builder.WithToolsets, including "all"/"default"). When nil and no persona
	// is set, the default toolsets are used.
	Toolsets []string
	// Tools is an additive allow-list of individual tools that bypass toolset
	// filtering.
	Tools []string
	// ExcludeTools is a deny-list applied last.
	ExcludeTools []string
	// ReadOnly, when true, exposes only read-only tools.
	ReadOnly bool
	// readOnlySet records whether ReadOnly was explicitly provided, so a persona
	// default is not silently overridden by the zero value.
	readOnlySet bool

	// LogFile, if set, directs debug logs to this file; otherwise info-level
	// logs go to stderr. stdout is reserved for the MCP stdio transport.
	LogFile string

	// PolicyConfig is the path to the device-policy document. Empty uses the
	// embedded default, which evaluates every declared signal, records every
	// verdict, and refuses nothing.
	PolicyConfig string

	// EnforceHTTPS blocks plaintext http:// targets. It is set from the policy
	// document at startup, not from a flag.
	EnforceHTTPS bool

	// --- Presentation and capture (not policy) ---
	Overlay     bool   // decorative window hue and click flash
	RecordFPS   int    // session recording frame rate
	RecordCodec string // session recording codec

	// --- Credentials ---
	// CredentialsFile is a JSON document of credentials to install into the
	// login keychain at init. Enabling this also enables the "credentials"
	// toolset.
	CredentialsFile string
}

// SetReadOnly records an explicit read-only choice (distinguishing it from the
// zero value so it can override a persona default).
func (c *Config) SetReadOnly(v bool) {
	c.ReadOnly = v
	c.readOnlySet = true
}

// Errors the startup path returns.
var (
	// ErrPersonaNeedsSession reports a persona requested with no graphical
	// session to drive. Personas are desktop-automation presets, so this is a
	// configuration error rather than a policy denial.
	ErrPersonaNeedsSession = errors.New(
		"persona requires a graphical login session, but the process has none")
	// ErrUnknownPersona reports a persona id that is not registered.
	ErrUnknownPersona = errors.New("unknown persona")
)

// nonAutomationToolsets is the toolset set permitted when the server runs
// without a graphical session (a LaunchDaemon, an ssh session): nothing that
// drives the desktop.
var nonAutomationToolsets = []string{"system", "shell", "filesystem", "diagnostics", "web"}

// RunStdio builds the server and serves the MCP protocol over stdio until the
// context is cancelled or the client disconnects.
//
// This is the milestone-1 shape: engine, inventory, surface and the two
// unconditional middleware layers. The guardrail stack is wired in as the
// milestones land, in the order windows-mcp-server pinned.
func RunStdio(ctx context.Context, cfg Config) error {
	logger, cleanup, err := runtime.NewLogger(cfg.LogFile)
	if err != nil {
		return fmt.Errorf("logger: %w", err)
	}
	defer cleanup()

	perms := CheckPermissions(ctx)
	if !perms.ConsoleSession && cfg.Persona != "" {
		return fmt.Errorf("%w: %q", ErrPersonaNeedsSession, cfg.Persona)
	}
	for _, w := range perms.Warnings {
		logger.Warn(w)
	}

	dsk, err := macdesktop.New(logger, macdesktop.Options{
		Overlay: cfg.Overlay,
	})
	if err != nil {
		return fmt.Errorf("failed to start desktop engine: %w", err)
	}
	defer func() { _ = dsk.Close() }()

	inv, personaInstructions, err := buildInventory(cfg, !perms.ConsoleSession)
	if err != nil {
		return err
	}
	for _, unknown := range inv.UnrecognizedToolsets() {
		logger.Warn("unrecognized toolset requested", "toolset", unknown)
	}

	deps := macos.NewBaseDeps(dsk, logger, nil)
	deps.WithEnforceHTTPS(cfg.EnforceHTTPS)

	s := newSurface(cfg, inv, personaInstructions, deps)
	s.InstallReceiving()
	inv.RegisterAll(ctx, s.Server, deps)

	logger.Info("starting macos-mcp-server over stdio",
		"version", cfg.Version,
		"enabled_toolsets", surface.ToolsetIDs(inv.EnabledToolsets()),
		"accessibility", perms.Accessibility,
		"screen_recording", perms.ScreenRecording,
	)
	if err := s.Server.Run(ctx, &mcp.StdioTransport{}); err != nil {
		return fmt.Errorf("server run: %w", err)
	}
	return nil
}

// newSurface builds the protocol-facing server. One constructor for every entry
// point — stdio, the offline capture and the conformance host — so the surface
// the official suite is measured against is the surface the binary serves.
func newSurface(
	cfg Config,
	inv *inventory.Inventory,
	personaInstructions string,
	deps *macos.BaseDeps,
) *surface.Surface {
	return surface.New(
		surface.Config{Name: ServerName, Title: ServerTitle, Version: cfg.Version},
		inv, personaInstructions, deps,
		surface.CompletionHandlerFor(inv, surface.CompletionSources{
			PersonaIDs: macos.PersonaIDs(),
			CommonApps: commonApps,
		}),
	)
}

// commonApps are frequently automated applications, offered as launch
// suggestions. A short curated list rather than an enumeration of installed
// software, which would be slow and leak the machine's inventory.
var commonApps = []string{
	"Calculator", "Finder", "Mail", "Notes", "Preview",
	"Safari", "System Settings", "Terminal", "TextEdit",
}

// buildInventory applies persona, toolset, read-only, and allow/deny
// configuration to the full tool manifest. It also returns the selected
// persona's instructions (empty when no persona is selected).
func buildInventory(cfg Config, noSession bool) (*inventory.Inventory, string, error) {
	toolsets := cfg.Toolsets
	readOnly := cfg.ReadOnly
	var personaInstructions string

	if cfg.Persona != "" {
		persona, ok := macos.LookupPersona(cfg.Persona)
		if !ok {
			return nil, "", fmt.Errorf("%w: %q", ErrUnknownPersona, cfg.Persona)
		}
		personaInstructions = persona.Instructions
		if toolsets == nil {
			toolsets = persona.Toolsets
		}
		if !cfg.readOnlySet {
			readOnly = persona.ReadOnly
		}
	}

	if noSession {
		toolsets = nonAutomationToolsets
	} else if cfg.CredentialsFile != "" {
		toolsets = runtime.WithToolset(toolsets, string(macos.ToolsetCredentials.ID))
	}

	inv, err := macos.NewInventory().
		WithToolsets(toolsets).
		WithReadOnly(readOnly).
		WithTools(cfg.Tools).
		WithExcludeTools(cfg.ExcludeTools).
		WithServerInstructions().
		Build()
	if err != nil {
		return nil, "", fmt.Errorf("failed to build tool inventory: %w", err)
	}
	return inv, personaInstructions, nil
}

// CaptureSurface assembles the tool manifest this configuration would serve,
// runs a real in-process MCP session against it, and returns the wire objects
// a client actually receives. No desktop engine is created: tools/list never
// invokes a handler.
func CaptureSurface(ctx context.Context, cfg Config) (surface.Captured, error) {
	logger := slog.New(slog.DiscardHandler)
	inv, personaInstructions, err := buildInventory(cfg, false)
	if err != nil {
		return surface.Captured{}, fmt.Errorf("build inventory: %w", err)
	}
	deps := macos.NewBaseDeps(nil, logger, nil)
	s := newSurface(cfg, inv, personaInstructions, deps)
	s.InstallReceiving()
	got, err := surface.Capture(ctx, s, inv, deps, cfg.Version)
	if err != nil {
		return surface.Captured{}, fmt.Errorf("capture surface: %w", err)
	}
	return got, nil
}
