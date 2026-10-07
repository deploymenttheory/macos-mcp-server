//go:build darwin && (amd64 || arm64)

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/deploymenttheory/go-bindings-macosplatform/opinionated/tools/grandcentraldispatch/mainthread"

	"github.com/deploymenttheory/mcp-server-core/journeys"

	"github.com/deploymenttheory/macos-mcp-server/internal/macmcp"
)

var errNeedOutputPath = errors.New("an output path is required (--out)")

// runOnWorker runs fn on a goroutine while this (main) thread pumps the main
// run loop, so the engine's main-thread work and any run-loop sources (the
// journey recorder's event tap) are serviced until fn returns.
func runOnWorker(fn func() error) error {
	done := make(chan error, 1)
	go func() { done <- fn() }()
	for {
		select {
		case err := <-done:
			return err
		default:
			mainthread.PumpMainRunLoop(0.05)
		}
	}
}

// journeyCmd groups the journeys-as-code operations: validate a journey
// document offline, run one against the live desktop as a test, or record one.
func journeyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "journey",
		Short: "Validate, run and record declarative UI journeys",
		Long: "A journey is a named sequence of UI actions with assertions and evidence, authored as " +
			"JSON. It compiles to a plan and runs through the same policy-evaluated, audited, " +
			"fail-stopped executor as Apply — so a UI regression test is expressed as code and run " +
			"deterministically.",
	}
	cmd.AddCommand(journeyValidateCmd(), journeyRunCmd(), journeyRecordCmd())
	return cmd
}

// journeyValidateCmd parses, validates and compiles a journey without touching
// the desktop, so a file can be checked in CI on any machine.
func journeyValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate <journey.json>",
		Short: "Check a journey document parses, validates, and compiles to a plan",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := os.ReadFile(args[0]) //nolint:gosec // an operator-supplied path
			if err != nil {
				return fmt.Errorf("read journey %s: %w", args[0], err)
			}
			j, err := journeys.Parse(raw)
			if err != nil {
				return fmt.Errorf("parse: %w", err)
			}
			if err := j.Validate(); err != nil {
				return fmt.Errorf("validate: %w", err)
			}
			doc, err := journeys.Compile(j, "")
			if err != nil {
				return fmt.Errorf("compile: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "ok: %q — %d step(s) compile to %d plan step(s) (plan %s…)\n",
				j.Name, len(j.Steps), len(doc.Steps), doc.PlanID[:16])
			return nil
		},
	}
}

// journeyRunCmd runs a journey against the live desktop and reports pass/fail.
func journeyRunCmd() *cobra.Command {
	var asJSON bool
	var policyConfig, logFile string
	cmd := &cobra.Command{
		Use:   "run <journey.json>",
		Short: "Run a journey against the live desktop and report pass/fail",
		Long: "run compiles the journey to a plan and executes it against the real UI through the " +
			"policy-evaluated, audited, fail-stopped executor. A failed assertion stops the run. Exits " +
			"1 if the journey does not pass, so CI can gate on it. Needs the Accessibility and Screen " +
			"Recording grants and a graphical session.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := macmcp.Config{Version: version, PolicyConfig: policyConfig, LogFile: logFile}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			var rep = struct {
				Passed    bool
				Name      string
				Completed int
				Failed    int
				Skipped   int
				Report    string
				raw       any
			}{}
			err := runOnWorker(func() error {
				r, err := macmcp.RunJourney(ctx, cfg, args[0])
				if err != nil {
					return err
				}
				rep.Passed, rep.Name, rep.Completed, rep.Failed, rep.Skipped, rep.Report, rep.raw =
					r.Passed, r.Name, r.Completed, r.Failed, r.Skipped, r.Report, r
				return nil
			})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				if err := enc.Encode(rep.raw); err != nil {
					return fmt.Errorf("render report: %w", err)
				}
			} else {
				verdict := "PASS"
				if !rep.Passed {
					verdict = "FAIL"
				}
				fmt.Fprintf(out, "%s  %s — %d completed, %d failed, %d skipped\n%s\n",
					verdict, rep.Name, rep.Completed, rep.Failed, rep.Skipped, rep.Report)
			}
			if !rep.Passed {
				os.Exit(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&policyConfig, "policy-config", "", "Path to the device-policy JSON document (default: the built-in audit-only policy).")
	cmd.Flags().StringVar(&logFile, "log-file", "", "Write debug logs to this file (default: info logs to stderr).")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Emit the report as JSON, for CI.")
	return cmd
}

// journeyRecordCmd records a human's desktop interaction into a journey file.
func journeyRecordCmd() *cobra.Command {
	var out, name string
	cmd := &cobra.Command{
		Use:   "record --out <journey.json>",
		Short: "Record a desktop session into a journey file (press F9 to stop, F8 to mark an assertion)",
		Long: "record listens to the session's input, resolving each click to a UI element and each " +
			"keystroke to text. Input into password fields is redacted — the keystrokes are never " +
			"written. Press F8 with the pointer over an element to mark an assertion; press F9 to stop. " +
			"The captured steps are written to --out as a reviewable draft. Needs the Accessibility grant.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if out == "" {
				return errNeedOutputPath
			}
			if name == "" {
				name = "recorded-journey"
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			fmt.Fprintln(cmd.ErrOrStderr(), "Recording… interact with the desktop; F8 marks an assertion, F9 stops.")
			var journey journeys.Journey
			err := runOnWorker(func() error {
				j, err := macmcp.RecordJourney(ctx, macmcp.Config{Version: version}, name, out)
				journey = j
				return err
			})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "wrote %s: %q with %d step(s). Review it and add assertions before use.\n",
				out, journey.Name, len(journey.Steps))
			return nil
		},
	}
	cmd.Flags().StringVar(&out, "out", "", "Path to write the recorded journey (required).")
	cmd.Flags().StringVar(&name, "name", "", "Name for the recorded journey (default \"recorded-journey\").")
	return cmd
}
