//go:build darwin && (amd64 || arm64)

// Package macos defines the macOS-automation MCP tools and the glue that binds
// them to the shared inventory registry. Tool handlers retrieve their
// dependencies from the request context via the toolkit's InjectDepsMiddleware
// and MustDepsFromContext, mirroring github-mcp-server's dependency-injection
// design and windows-mcp-server's pkg/windows.
package macos

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/deploymenttheory/mcp-server-core/inventory"
	"github.com/deploymenttheory/mcp-server-core/toolkit"

	"github.com/deploymenttheory/macos-mcp-server/internal/macdesktop"
)

// ToolDependencies is the interface tool handlers use to reach shared services.
// It is the shared toolkit contract plus the macOS engine.
type ToolDependencies interface {
	toolkit.ToolDependencies
	// Desktop returns the macOS automation engine (accessibility tree, input,
	// screenshots, windows, applications, processes). Handlers submit work to
	// it; it owns the main thread.
	Desktop() *macdesktop.Desktop
}

// BaseDeps is the standard ToolDependencies implementation for the local
// (stdio) server: the shared base plus the engine.
type BaseDeps struct {
	*toolkit.BaseDeps
	desktop *macdesktop.Desktop
}

// Compile-time assertion that BaseDeps satisfies ToolDependencies.
var _ ToolDependencies = (*BaseDeps)(nil)

// NewBaseDeps constructs a BaseDeps. The path normaliser is the macOS one, so
// protected-path matching folds the spellings APFS folds.
func NewBaseDeps(dsk *macdesktop.Desktop, logger *slog.Logger, featureChecker inventory.FeatureFlagChecker) *BaseDeps {
	return &BaseDeps{
		BaseDeps: toolkit.NewBaseDeps(logger, featureChecker).WithPathNormalizer(NormalizePath),
		desktop:  dsk,
	}
}

// Desktop implements ToolDependencies.
func (d *BaseDeps) Desktop() *macdesktop.Desktop { return d.desktop }

// NormalizePath renders a path in the one form protected-path matching
// compares, so the same file cannot be reached under a different spelling.
//
// macOS folds these: `~` (the home directory), `/private/etc`, `/private/var`
// and `/private/tmp` (the real locations behind the top-level symlinks), and
// letter case (APFS volumes are case-insensitive by default; on a
// case-sensitive volume folding is merely over-protective). Hard links and
// firmlinks still reach a file under another name, which is why this is a
// guardrail rather than a sandbox.
func NormalizePath(path string) string {
	p := strings.TrimSpace(path)
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := homeDir(); err == nil {
			p = home + p[1:]
		}
	}
	p = filepath.Clean(p)
	for _, pfx := range []string{"/private/etc", "/private/var", "/private/tmp"} {
		if p == pfx || strings.HasPrefix(p, pfx+"/") {
			p = p[len("/private"):]
			break
		}
	}
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		p = resolved
		for _, pfx := range []string{"/private/etc", "/private/var", "/private/tmp"} {
			if p == pfx || strings.HasPrefix(p, pfx+"/") {
				p = p[len("/private"):]
				break
			}
		}
	}
	return strings.ToLower(p)
}

// NewToolFromHandler creates a ServerTool from a raw handler that receives
// dependencies from context.
func NewToolFromHandler(
	toolset inventory.ToolsetMetadata,
	tool mcp.Tool,
	handler func(ctx context.Context, deps ToolDependencies, req *mcp.CallToolRequest) (*mcp.CallToolResult, error),
) inventory.ServerTool {
	return toolkit.NewToolFromHandler[ToolDependencies](toolset, tool, handler)
}

// MustDepsFromContext retrieves ToolDependencies from ctx, panicking if absent.
func MustDepsFromContext(ctx context.Context) ToolDependencies {
	return toolkit.MustDepsFromContext[ToolDependencies](ctx)
}

// Result constructors and argument accessors, re-exported so tool files read
// as they do in the Windows server.
var (
	NewToolResultText         = toolkit.NewToolResultText
	NewToolResultTextf        = toolkit.NewToolResultTextf
	NewToolResultError        = toolkit.NewToolResultError
	NewToolResultErrorf       = toolkit.NewToolResultErrorf
	NewToolResultErrorFromErr = toolkit.NewToolResultErrorFromErr
	NewToolResultImage        = toolkit.NewToolResultImage
	ArgsMap                   = toolkit.ArgsMap
	RequiredString            = toolkit.RequiredString
	OptionalString            = toolkit.OptionalString
	OptionalInt               = toolkit.OptionalInt
	OptionalFloat             = toolkit.OptionalFloat
	OptionalBool              = toolkit.OptionalBool
	OptionalStringEnum        = toolkit.OptionalStringEnum
	OptionalIntSlice          = toolkit.OptionalIntSlice
)
