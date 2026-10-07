//go:build darwin && (amd64 || arm64)

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/deploymenttheory/mcp-server-core/mcpconf"
)

// conformanceReportCmd turns the official suite's checks.json output into
// something readable and committable. It does not gate: the suite already
// does, via --expected-failures and its own exit code.
func conformanceReportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "conformance-report",
		Short: "Render official MCP conformance suite results as the compliance report",
		Long: "conformance-report reads one or more checks.json files produced by " +
			"github.com/modelcontextprotocol/conformance and renders them as markdown or JSON.\n\n" +
			"Each --pass is name=path/to/checks.json. Two passes are expected: `product`, run " +
			"against the manifest this server ships, and `fixtures`, run with the suite's named " +
			"fixture tools registered.\n\nNo score is emitted: conformance is per-check pass or " +
			"fail, gated by the suite.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			v := viperFor(cmd)
			report := &mcpconf.Report{
				ServerVersion: version,
				Commit:        v.GetString("commit"),
				GeneratedAt:   v.GetString("generated-at"),
				RunURL:        v.GetString("run-url"),
			}
			specVersion := v.GetString("spec-version")
			harness := v.GetString("harness-version")

			for _, spec := range v.GetStringSlice("pass") {
				name, path, ok := strings.Cut(spec, "=")
				if !ok {
					return fmt.Errorf("%w: %q", errBadPassSpec, spec)
				}
				checks, err := mcpconf.LoadChecks(path)
				if err != nil {
					return fmt.Errorf("load %s: %w", path, err)
				}
				report.Passes = append(report.Passes, &mcpconf.Pass{
					Name:           name,
					Description:    passDescriptions[name],
					SpecVersion:    specVersion,
					HarnessVersion: harness,
					Baseline:       v.GetString("baseline-" + name),
					Checks:         checks,
				})
			}
			if len(report.Passes) == 0 {
				return errNoPasses
			}

			perm := os.FileMode(0o644) //nolint:gosec // a report is not a secret
			if badgeOut := v.GetString("badge-out"); badgeOut != "" {
				badge, err := json.MarshalIndent(report.BadgeFor(v.GetString("badge-pass")), "", "  ")
				if err != nil {
					return fmt.Errorf("marshal badge: %w", err)
				}
				if err := os.WriteFile(badgeOut, append(badge, '\n'), perm); err != nil {
					return fmt.Errorf("write badge: %w", err)
				}
			}
			rendered, err := renderConformanceReport(report, v.GetString("format"))
			if err != nil {
				return err
			}
			if out := v.GetString("out"); out != "" {
				if err := os.WriteFile(out, []byte(rendered), perm); err != nil {
					return fmt.Errorf("write report: %w", err)
				}
				return nil
			}
			fmt.Fprint(cmd.OutOrStdout(), rendered)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringSlice("pass", nil, "A suite run to include, as name=path/to/checks.json. Repeatable.")
	f.String("spec-version", "2026-07-28", "Protocol revision the suite was run at.")
	f.String("harness-version", "", "Exact npm version of the conformance suite that produced the results.")
	f.String("baseline-product", "", "Expected-failures file the product pass was gated against.")
	f.String("baseline-fixtures", "", "Expected-failures file the fixtures pass was gated against.")
	f.String("commit", "", "Commit the tested binary was built from.")
	f.String("generated-at", "", "Timestamp for the report; supplied by the caller so the output is reproducible.")
	f.String("run-url", "", "Link to the workflow run that produced the results.")
	f.String("format", "markdown", "Output format: markdown or json.")
	f.String("out", "", "Write the report to this file instead of stdout.")
	f.String("badge-out", "", "Also write a shields.io endpoint badge to this file.")
	f.String("badge-pass", "product", "Which pass the badge summarises.")
	return cmd
}

// Report errors.
var (
	errNoPasses    = errors.New("no --pass results supplied")
	errBadPassSpec = errors.New("--pass must be name=path/to/checks.json")
	errBadFormat   = errors.New("unknown --format (want markdown or json)")
)

// passDescriptions says what each pass proves, so the committed report
// explains itself without reference to the workflow that produced it.
var passDescriptions = map[string]string{
	"product": "Run against the manifest this server actually ships. Scenarios needing the suite's " +
		"named fixtures cannot execute here and are listed in the baseline; what this pass covers is " +
		"the transport and wire conformance that the 2026-07-28 revision is about.",
	"fixtures": "Run with the suite's fixture tools, resources and prompts registered, so tools/call, " +
		"resources/read, resources/templates/list and prompts/get are exercised through the real " +
		"middleware and result constructors. The fixtures exist only under the `conformance` build " +
		"tag and are never present in a released binary.",
}

func renderConformanceReport(r *mcpconf.Report, format string) (string, error) {
	switch format {
	case "json":
		b, err := json.MarshalIndent(r, "", "  ")
		if err != nil {
			return "", fmt.Errorf("marshal report: %w", err)
		}
		return string(b) + "\n", nil
	case "", "markdown", "md":
		return r.Markdown(), nil
	default:
		return "", fmt.Errorf("%w: %q", errBadFormat, format)
	}
}
