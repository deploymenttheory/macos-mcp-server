//go:build darwin && (amd64 || arm64)

package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/deploymenttheory/go-bindings-macosplatform/opinionated/tools/grandcentraldispatch/mainthread"

	"github.com/deploymenttheory/macos-mcp-server/internal/macmcp"
)

func newStdioCmd() *cobra.Command {
	var (
		persona            string
		toolsets           []string
		tools              []string
		excludeTools       []string
		readOnly           bool
		logFile            string
		policyConfig       string
		overlay            bool
		recordFPS          int
		recordCodec        string
		credsFile          string
		requestPermissions bool
	)
	cmd := &cobra.Command{
		Use:   "stdio",
		Short: "Serve MCP over stdio (the transport every client uses)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := macmcp.Config{
				Version:         version,
				Persona:         persona,
				Toolsets:        toolsets,
				Tools:           tools,
				ExcludeTools:    excludeTools,
				LogFile:         logFile,
				PolicyConfig:    policyConfig,
				Overlay:         overlay,
				RecordFPS:       recordFPS,
				RecordCodec:     recordCodec,
				CredentialsFile: credsFile,
			}
			if cmd.Flags().Changed("read-only") || viper.IsSet("read-only") {
				cfg.SetReadOnly(readOnly)
			}
			if len(toolsets) == 0 && cmd.Flags().Changed("toolsets") {
				cfg.Toolsets = []string{}
			}

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			if requestPermissions {
				perms := macmcp.CheckPermissions(ctx)
				if perms.ConsoleSession && (!perms.Accessibility || !perms.ScreenRecording) {
					macmcp.RequestPermissions(ctx)
				}
			}

			// The server runs on a goroutine and the main thread — this one — is
			// handed to the main dispatch queue, which the engine's AppKit and
			// accessibility calls dispatch to. DispatchMain never returns; the
			// process exits from the server goroutine.
			errCh := make(chan error, 1)
			go func() {
				err := macmcp.RunStdio(ctx, cfg)
				errCh <- err
				if err != nil {
					fmt.Fprintln(os.Stderr, "error:", err)
					os.Exit(1)
				}
				os.Exit(0)
			}()
			mainthread.DispatchMain()
			return <-errCh // unreachable; DispatchMain does not return
		},
	}
	f := cmd.Flags()
	f.StringVar(&persona, "persona", viper.GetString("persona"), "built-in persona preset (see `personas`)")
	f.StringSliceVar(&toolsets, "toolsets", viper.GetStringSlice("toolsets"), "toolsets to enable (comma-separated; 'all', 'default', or names)")
	f.StringSliceVar(&tools, "tools", viper.GetStringSlice("tools"), "additional individual tools to enable (bypasses toolset filtering)")
	f.StringSliceVar(&excludeTools, "exclude-tools", viper.GetStringSlice("exclude-tools"), "tools to exclude (applied last)")
	f.BoolVar(&readOnly, "read-only", viper.GetBool("read-only"), "expose only read-only tools")
	f.StringVar(&logFile, "log-file", viper.GetString("log-file"), "write debug logs to this file instead of info logs to stderr")
	f.StringVar(&policyConfig, "policy-config", viper.GetString("policy-config"), "path to the device-policy document (default: built-in audit-only policy)")
	f.BoolVar(&overlay, "overlay", viper.GetBool("overlay"), "decorative overlays: highlight the active window and flash clicks")
	f.IntVar(&recordFPS, "record-fps", 4, "session recording frame rate")
	f.StringVar(&recordCodec, "record-codec", "h264", "session recording codec: h264 or hevc")
	f.StringVar(&credsFile, "credentials-file", viper.GetString("credentials-file"), "JSON document of credentials to install into the keychain at init (enables the credentials toolset)")
	f.BoolVar(&requestPermissions, "request-permissions", viper.GetBool("request-permissions"), "register this binary in macOS Accessibility and Screen Recording settings on startup")
	return cmd
}
