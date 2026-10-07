//go:build darwin && (amd64 || arm64)

package macos

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/deploymenttheory/mcp-server-core/inventory"

	"github.com/deploymenttheory/macos-mcp-server/internal/clirunner"
)

// packageTimeout bounds an install/uninstall: they download and run
// installers, which can take minutes, so the window is far longer than a
// query's.
const packageTimeout = 10 * time.Minute

// packageQueryTimeout bounds list/search/receipts/updates.
const packageQueryTimeout = 120 * time.Second

// Package installs, removes, lists, and searches software via Homebrew, the
// macOS installer, pkgutil receipts and Software Update.
//
// It is destructive and open-world: install and uninstall change what is on
// the machine, and brew and softwareupdate download from the network — outside
// the egress proxy — so the tool is annotated open-world and lives in an
// opt-in toolset that no persona carries.
func Package() inventory.ServerTool {
	destructive := true
	openWorld := true
	return NewToolFromHandler(
		ToolsetPackages,
		mcp.Tool{
			Name: "Package",
			Description: "Install, remove, list, and search software. mode=list shows installed Homebrew " +
				"formulae and casks; mode=search finds Homebrew packages by query; mode=install installs a " +
				"Homebrew package by name (or a local .pkg by path, which needs root); mode=uninstall removes a " +
				"Homebrew package by name; mode=receipts lists installer package receipts (pkgutil); " +
				"mode=updates lists available macOS software updates. Installs download from the network, " +
				"outside the egress proxy.",
			Annotations: &mcp.ToolAnnotations{
				Title:           "Software package management",
				ReadOnlyHint:    false,
				DestructiveHint: &destructive,
				OpenWorldHint:   &openWorld,
			},
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"mode": {
						Type:        "string",
						Enum:        []any{"list", "search", "install", "uninstall", "receipts", "updates"},
						Description: "Operation.",
					},
					"query": {Type: "string", Description: "Search term (mode=search)."},
					"id": {
						Type:        "string",
						Description: "Homebrew formula or cask name (mode=install/uninstall).",
					},
					"cask": {
						Type:        "boolean",
						Description: "Treat id as a Homebrew cask (mode=install/uninstall). Default false.",
					},
					"pkg": {
						Type:        "string",
						Description: "Absolute path to a local .pkg to install (mode=install; an alternative to id).",
					},
				},
				Required: []string{"mode"},
			},
		},
		func(ctx context.Context, _ ToolDependencies, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args, err := ArgsMap(req)
			if err != nil {
				return NewToolResultError(err.Error()), nil
			}
			mode, err := OptionalStringEnum(args, "mode", "", "list", "search", "install", "uninstall", "receipts", "updates")
			if err != nil {
				return NewToolResultError(err.Error()), nil
			}

			argv, timeout, err := packageCommand(mode, args)
			if err != nil {
				return NewToolResultError(err.Error()), nil
			}

			res, err := clirunner.Run(ctx, argv, clirunner.Options{
				Timeout: timeout,
				// Homebrew's own analytics and auto-update make every call
				// slower and noisier; neither is wanted from an agent.
				Env: []string{"HOMEBREW_NO_AUTO_UPDATE=1", "HOMEBREW_NO_ANALYTICS=1", "HOMEBREW_NO_ENV_HINTS=1"},
			})
			out := strings.TrimSpace(res.Stdout)
			if errors.Is(err, clirunner.ErrTimeout) {
				return NewToolResultError("package command timed out"), nil
			}
			if errors.Is(err, clirunner.ErrNotFound) {
				return NewToolResultErrorFromErr("package manager not installed", err), nil
			}
			if err != nil {
				detail := strings.TrimSpace(res.Stderr)
				if detail == "" {
					detail = out
				}
				return NewToolResultErrorf("package command failed (exit %d): %s", res.ExitCode, detail), nil
			}
			if out == "" {
				return NewToolResultText("OK"), nil
			}
			return NewToolResultText(out), nil
		},
	)
}

// packageCommand builds the argv and its timeout for one mode. Every
// caller-supplied value is a separate argv element, never spliced into a
// command line, so a package name or path cannot break out into a statement.
func packageCommand(mode string, args map[string]any) ([]string, time.Duration, error) {
	cask := OptionalBool(args, "cask", false)
	switch mode {
	case "list":
		return []string{"brew", "list", "--versions"}, packageQueryTimeout, nil
	case "search":
		query, err := RequiredString(args, "query")
		if err != nil {
			return nil, 0, err
		}
		return []string{"brew", "search", "--", query}, packageQueryTimeout, nil
	case "install":
		// A local .pkg is an alternative to a Homebrew name; if given, it takes
		// precedence.
		if pkg := OptionalString(args, "pkg", ""); pkg != "" {
			if err := requireLocalPkg(pkg); err != nil {
				return nil, 0, err
			}
			return []string{"installer", "-pkg", pkg, "-target", "/"}, packageTimeout, nil
		}
		id, err := RequiredString(args, "id")
		if err != nil {
			return nil, 0, err
		}
		return brewArgs("install", id, cask), packageTimeout, nil
	case "uninstall":
		id, err := RequiredString(args, "id")
		if err != nil {
			return nil, 0, err
		}
		return brewArgs("uninstall", id, cask), packageTimeout, nil
	case "receipts":
		return []string{"pkgutil", "--pkgs"}, packageQueryTimeout, nil
	case "updates":
		return []string{"softwareupdate", "--list", "--no-scan"}, packageQueryTimeout, nil
	default:
		return nil, 0, fmt.Errorf("%w: %q", ErrUnknownPackageMode, mode)
	}
}

// brewArgs builds a non-interactive brew command. "--" ends option parsing so
// a name beginning with "-" is data, not a flag.
func brewArgs(verb, id string, cask bool) []string {
	argv := []string{"brew", verb}
	if cask {
		argv = append(argv, "--cask")
	}
	return append(argv, "--", id)
}

// requireLocalPkg rejects an installer path that is not a local .pkg file.
//
// `installer -pkg` accepts any path the process can read, and a network mount
// or a URL-shaped value would fetch an installer from outside the egress
// proxy and the allowlist, never seen by enforceHTTPSScheme. The schema and
// description both say "local .pkg by path", so the documented disclosure that
// installs reach the network does not cover an operator-unexpected fetch.
func requireLocalPkg(pkg string) error {
	lower := strings.ToLower(pkg)
	if !strings.HasSuffix(lower, ".pkg") && !strings.HasSuffix(lower, ".mpkg") {
		return fmt.Errorf("%w: pkg must be a path to a .pkg file", ErrRemoteInstaller)
	}
	if strings.Contains(pkg, "://") {
		return fmt.Errorf("%w: %q is a URL; download it first and install the local file",
			ErrRemoteInstaller, pkg)
	}
	// A network mount executes an installer hosted on another machine.
	if strings.HasPrefix(pkg, "//") || strings.HasPrefix(lower, "/volumes/") ||
		strings.HasPrefix(lower, "/net/") || strings.HasPrefix(lower, "/network/") {
		return fmt.Errorf("%w: %q is on a mounted volume or network path; copy it locally first",
			ErrRemoteInstaller, pkg)
	}
	if !filepath.IsAbs(pkg) {
		return fmt.Errorf("%w: %q must be an absolute path", ErrRemoteInstaller, pkg)
	}
	return nil
}

// Package tool errors.
var (
	// ErrRemoteInstaller reports an installer that is not a local file.
	ErrRemoteInstaller = errors.New("installer must be a local .pkg")
	// ErrUnknownPackageMode reports an unrecognised mode.
	ErrUnknownPackageMode = errors.New("unknown package mode")
)
