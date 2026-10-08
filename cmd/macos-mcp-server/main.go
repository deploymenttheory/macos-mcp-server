//go:build darwin && (amd64 || arm64)

// Command macos-mcp-server is an MCP server bridging AI agents to the macOS
// desktop over stdio. See the README for the tool surface and the security
// model.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/deploymenttheory/macos-mcp-server/internal/macmcp"
	"github.com/deploymenttheory/macos-mcp-server/pkg/macos"
)

// version is stamped by the release build (-X main.version=...).
var version = "dev"

// envPrefix is the viper prefix: every flag is also MACOS_MCP_<FLAG>.
const envPrefix = "MACOS_MCP"

func init() {
	// The engine's AppKit and accessibility calls dispatch to the process main
	// thread, and the SDK needs that thread kept alive and serviced. Locking
	// here, before the runtime schedules anything else, is what makes main()
	// run on the OS main thread; the stdio command hands it to the main queue
	// once the server goroutine is up.
	runtime.LockOSThread()
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "macos-mcp-server",
		Short:         "MCP server for macOS desktop automation and administration",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	viper.SetEnvPrefix(envPrefix)
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	viper.AutomaticEnv()

	root.AddCommand(newStdioCmd(), newPersonasCmd(), newPermissionsCmd())
	return root
}

func newPersonasCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "personas",
		Short: "List the built-in personas and the toolsets each enables",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ids := macos.PersonaIDs()
			sort.Strings(ids)
			for _, id := range ids {
				p := macos.Personas[id]
				fmt.Fprintf(cmd.OutOrStdout(), "%-20s %s\n%-20s toolsets: %s (read-only: %t)\n\n",
					id, p.Description, "", strings.Join(p.Toolsets, ", "), p.ReadOnly)
			}
			return nil
		},
	}
}

func newPermissionsCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "permissions",
		Short: "Inspect or request the macOS privacy grants the tools depend on",
	}
	report := func(cmd *cobra.Command, p macmcp.Permissions) error {
		if asJSON {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			if err := enc.Encode(p); err != nil {
				return fmt.Errorf("encode permissions: %w", err)
			}
		} else {
			fmt.Fprint(cmd.OutOrStdout(), p.Describe())
		}
		if !p.Required() {
			// Exit 2 so a script can tell "missing a grant" from "crashed".
			os.Exit(2)
		}
		return nil
	}
	check := &cobra.Command{
		Use:   "check",
		Short: "Report Accessibility, Screen Recording, Full Disk Access, session and signing state (exit 2 if a required grant is missing)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return report(cmd, macmcp.CheckPermissions(context.Background()))
		},
	}
	request := &cobra.Command{
		Use:   "request",
		Short: "Trigger the system consent prompts for Accessibility and Screen Recording, then report",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return report(cmd, macmcp.RequestPermissions(context.Background()))
		},
	}
	for _, c := range []*cobra.Command{check, request} {
		c.Flags().BoolVar(&asJSON, "json", false, "emit JSON")
	}
	cmd.AddCommand(check, request)
	return cmd
}
