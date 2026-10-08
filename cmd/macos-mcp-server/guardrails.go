//go:build darwin && (amd64 || arm64)

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"github.com/deploymenttheory/agentweave-harness/guardrails/audit"
	"github.com/deploymenttheory/agentweave-harness/guardrails/evidence"

	"github.com/deploymenttheory/mcp-server-core/runtime"

	"github.com/deploymenttheory/macos-mcp-server/internal/macmcp"
)

// policyCmd groups the policy-engine questions an operator asks without
// starting a server: is this document valid, what does this device look like
// now, why was that call refused, do these fixtures pass.
func policyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "policy",
		Short: "Inspect the device policy: validate a document, check this device, explain a tool, test fixtures",
		Long: "The policy engine decides every tool call against live device signals. These " +
			"subcommands answer the questions that come up around it, without starting a server.\n\n" +
			"With no --policy-config the first three operate on the built-in default: the engine " +
			"present, every declared signal evaluated and every verdict recorded, nothing refused. " +
			"`test` takes its policy from each fixture instead.",
	}
	cmd.AddCommand(policyValidateCmd(), policyCheckCmd(), policyExplainCmd(), policyTestCmd())
	return cmd
}

func policyValidateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate a policy document against this build's signal set",
		Long: "validate parses the document, rejects unknown fields, and checks that every signal " +
			"it names is one this build can evaluate and that every rule requires a declared signal.\n\n" +
			"It reads no device state, so it is safe to run anywhere. Exits 1 on any problem.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			v := viperFor(cmd)
			cfg := macmcp.Config{Version: version, PolicyConfig: v.GetString("policy-config")}
			p, err := macmcp.ValidatePolicy(cfg)
			if err != nil {
				return err
			}
			source := cfg.PolicyConfig
			if source == "" {
				source = "(built-in default)"
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"ok  %s\n    mode=%s  signals=%v  rules=%d  rate_limits=%d\n",
				source, p.Mode, p.SignalIDs(), len(p.Rules), len(p.RateLimits))
			return nil
		},
	}
	addPolicyConfigFlag(cmd.Flags())
	return cmd
}

func policyCheckCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Evaluate every declared signal against this device and print the decision",
		Long: "check reads every signal the policy declares, live and cache-bypassed, then applies " +
			"the startup-scoped rules and prints the decision document.\n\n" +
			"It is deliberately slow: profiles, csrutil, fdesetup and bputil all run. " +
			"Exits 2 when the device is not admitted, so CI and operators can gate on posture.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			v := viperFor(cmd)
			cfg := macmcp.Config{
				Version:      version,
				PolicyConfig: v.GetString("policy-config"),
				LogFile:      v.GetString("log-file"),
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			decision, err := macmcp.EvaluatePolicy(ctx, cfg)
			if err != nil {
				return err
			}
			out, err := json.MarshalIndent(decision, "", "  ")
			if err != nil {
				return fmt.Errorf("render decision: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(out))
			if !decision.Admit {
				os.Exit(2)
			}
			return nil
		},
	}
	addPolicyConfigFlag(cmd.Flags())
	cmd.Flags().String("log-file", "", "Write debug logs to this file (default: info logs to stderr).")
	return cmd
}

func policyExplainCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "explain",
		Short: "Show which rules cover a tool and what they require",
		Long: "explain reports every rule that covers a tool, what each requires, and what it does " +
			"on failure. It evaluates nothing, so it can be run on a machine other than the one " +
			"that refused the call.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			v := viperFor(cmd)
			tool := v.GetString("tool")
			if tool == "" {
				return errNoToolNamed
			}
			cfg := macmcp.Config{
				Version:      version,
				PolicyConfig: v.GetString("policy-config"),
				Persona:      v.GetString("persona"),
				Toolsets:     splitCSV(v.GetString("toolsets")),
			}
			// Explain against the whole manifest by default: a tool the
			// current selection excludes is still a tool the operator may be
			// asking about.
			if len(cfg.Toolsets) == 0 && cfg.Persona == "" {
				cfg.Toolsets = []string{"all"}
			}
			cov, err := macmcp.ExplainPolicy(cmd.Context(), cfg, tool)
			if err != nil {
				return err
			}
			if v.GetString("format") == "json" {
				out, err := json.MarshalIndent(cov, "", "  ")
				if err != nil {
					return fmt.Errorf("render coverage: %w", err)
				}
				fmt.Fprintln(cmd.OutOrStdout(), string(out))
				return nil
			}
			printCoverage(cmd.OutOrStdout(), cov)
			return nil
		},
	}
	addPolicyConfigFlag(cmd.Flags())
	f := cmd.Flags()
	f.String("tool", "", "Tool name to explain (required).")
	f.String("format", "text", "Output format: text or json.")
	f.String("toolsets", "", "Toolsets to resolve the tool against (default: all).")
	f.String("persona", "", "Resolve against the manifest a persona would serve.")
	return cmd
}

func policyTestCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "test <fixture.json>...",
		Short: "Run policy fixtures, asserting verdicts against fixture device states",
		Long: "test evaluates one or more fixture files — each a policy, a fixture device state, and " +
			"a list of tool calls with asserted verdicts — and reports whether the policy decides them " +
			"as written. It reads no live device state, so it runs anywhere, including CI. Exits 1 if " +
			"any case fails.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			reports, err := macmcp.TestPolicy(args)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				if err := enc.Encode(reports); err != nil {
					return fmt.Errorf("render report: %w", err)
				}
			} else {
				printPolicyTestReports(out, reports)
			}
			if !allPolicyTestsPassed(reports) {
				os.Exit(1)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Emit the results as JSON, for CI.")
	return cmd
}

func printPolicyTestReports(out io.Writer, reports []runtime.PolicyTestReport) {
	total, failed := 0, 0
	for _, r := range reports {
		fmt.Fprintln(out, r.Fixture)
		for _, c := range r.Cases {
			total++
			if c.OK {
				fmt.Fprintf(out, "  ok    %s\n", c.Name)
				continue
			}
			failed++
			fmt.Fprintf(out, "  FAIL  %s: %s\n", c.Name, c.Detail)
		}
	}
	fmt.Fprintf(out, "\n%d case(s), %d failed\n", total, failed)
}

func allPolicyTestsPassed(reports []runtime.PolicyTestReport) bool {
	for _, r := range reports {
		if !r.Passed() {
			return false
		}
	}
	return true
}

func printCoverage(w io.Writer, cov runtime.PolicyCoverage) {
	fmt.Fprintf(w, "tool: %s\n", cov.Tool)
	if !cov.Known {
		fmt.Fprintf(w, "  not in the served manifest — only rules matching every tool can cover it\n")
	} else {
		fmt.Fprintf(w, "  toolset=%s read-only=%t destructive=%t open-world=%t\n",
			cov.Facts.Toolset, cov.Facts.ReadOnly, cov.Facts.Destructive, cov.Facts.OpenWorld)
	}
	if len(cov.Rules) == 0 {
		fmt.Fprintf(w, "\nno rule covers this tool: calls to it are never refused by policy\n")
		return
	}
	fmt.Fprintf(w, "\ncovered by %d rule(s):\n", len(cov.Rules))
	for _, r := range cov.Rules {
		fmt.Fprintf(w, "  %-24s requires %-40v on failure: %s\n", r.Name, r.Requires, r.OnFail)
	}
	fmt.Fprintf(w, "\nsignals that must pass: %v\n", cov.Signals)
}

// addPolicyConfigFlag adds the flag every policy subcommand takes.
func addPolicyConfigFlag(f *pflag.FlagSet) {
	f.String("policy-config", "", "Path to the device-policy JSON document. Omit for the built-in default.")
}

// viperFor binds a command's flags so each is also readable as
// MACOS_MCP_<FLAG>.
func viperFor(cmd *cobra.Command) *viper.Viper {
	v := viper.New()
	v.SetEnvPrefix(envPrefix)
	v.SetEnvKeyReplacer(envKeyReplacer)
	v.AutomaticEnv()
	_ = v.BindPFlags(cmd.Flags())
	return v
}

// Errors the guardrail subcommands return.
var (
	errNoToolNamed       = errors.New("no tool named: use --tool <name>")
	errNeedDirAndSession = errors.New("evidence bundle needs both --dir and --session")
	errEmptyKeyEnv       = errors.New("the named key variable is empty; nothing to verify the MAC against")
)

// auditCmd groups operations on the tamper-evident audit chain.
func auditCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "audit",
		Short: "Inspect the tamper-evident audit chain",
		Long: "The audit chain records what a session did, each entry committing to the previous " +
			"entry's hash. These subcommands check that record without starting a server.",
	}
	cmd.AddCommand(auditVerifyCmd())
	return cmd
}

func auditVerifyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "verify <file|dir>",
		Short: "Verify a session audit file, or a directory of them linked by a manifest",
		Long: "verify checks a hash chain end to end. Given a single session-*.audit.jsonl file it " +
			"verifies that one chain. Given a directory (the audit_destination in directory mode) it " +
			"verifies the manifest chain, each session file, and that every sealed session's head " +
			"matches its manifest record.\n\n" +
			"With --key-env naming an environment variable that holds the same key the server ran " +
			"with (MACOS_MCP_AUDIT_KEY), it also checks every entry's HMAC. Exits 1 on any " +
			"integrity problem, reporting all of them.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]
			info, err := os.Stat(path)
			if err != nil {
				return fmt.Errorf("audit verify: %w", err)
			}
			out := cmd.OutOrStdout()

			var key []byte
			if env, _ := cmd.Flags().GetString("key-env"); env != "" {
				v := os.Getenv(env)
				if v == "" {
					return fmt.Errorf("audit verify: %s: %w", env, errEmptyKeyEnv)
				}
				key = []byte(v)
			}

			if info.IsDir() {
				rep, err := audit.VerifyDir(path, key)
				if err != nil {
					return fmt.Errorf("audit verify: %w", err)
				}
				fmt.Fprint(out, rep.String())
				strict, _ := cmd.Flags().GetBool("strict")
				if (strict && !rep.StrictOK()) || !rep.OK() {
					os.Exit(1)
				}
				return nil
			}

			entries, err := audit.VerifyFile(path, key)
			if err != nil {
				fmt.Fprintf(out, "BROKEN  %s: %v\n", path, err)
				os.Exit(1)
			}
			fmt.Fprintf(out, "ok  %s: %d entries, chain intact\n", path, len(entries))
			return nil
		},
	}
	cmd.Flags().String("key-env", "", "Name of an environment variable holding the audit HMAC key; "+
		"when set, entry MACs are verified in addition to the hash chain.")
	cmd.Flags().Bool("strict", false, "Also fail when any session carries no seal.")
	return cmd
}

// evidenceCmd groups the evidence-bundle operations: seal a session's record
// into a signed archive, verify one, and generate a signing key.
func evidenceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "evidence",
		Short: "Seal, verify, and key session evidence bundles",
		Long: "An evidence bundle packages a session's audit chain, extracted verdicts, and any " +
			"recording into one self-verifying, optionally signed archive — the artifact handed to an " +
			"auditor or an incident review.",
	}
	cmd.AddCommand(evidenceBundleCmd(), evidenceVerifyCmd(), evidenceKeygenCmd())
	return cmd
}

func evidenceBundleCmd() *cobra.Command {
	var dir, session, recordingDir, out, keyFile string
	cmd := &cobra.Command{
		Use:   "bundle",
		Short: "Seal a session's evidence into a signed archive",
		Long: "bundle reads a session's audit chain from --dir, extracts its verdicts, gathers any " +
			"recording, and writes a self-verifying archive. With a signing key it is signed with " +
			"ed25519; without one it is unsigned but still hash-verifiable.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if dir == "" || session == "" {
				return errNeedDirAndSession
			}
			if keyFile == "" {
				keyFile = os.Getenv(macmcp.EnvPrefix + "EVIDENCE_KEY_FILE")
			}
			man, err := runtime.BundleEvidence(dir, session, recordingDir, out, keyFile)
			if err != nil {
				return fmt.Errorf("evidence bundle: %w", err)
			}
			state := "unsigned"
			if man.Signed {
				state = "signed by " + man.PublicKey[:16] + "…"
			}
			if out == "" {
				out = filepath.Join(dir, "session-"+session+".evidence.zip")
			}
			fmt.Fprintf(cmd.OutOrStdout(), "sealed %s (%d file(s), %s)\n", out, len(man.Files), state)
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "The audit directory holding the session's chain (required).")
	cmd.Flags().StringVar(&session, "session", "", "The session stamp, e.g. 20261007-120000 (required).")
	cmd.Flags().StringVar(&recordingDir, "recording-dir", "", "Directory holding the session recording, if any.")
	cmd.Flags().StringVar(&out, "out", "", "Output path (default: <dir>/session-<session>.evidence.zip).")
	cmd.Flags().StringVar(&keyFile, "key-file", "", "ed25519 signing key (default: $MACOS_MCP_EVIDENCE_KEY_FILE; unsigned if unset).")
	return cmd
}

func evidenceVerifyCmd() *cobra.Command {
	var pubKey string
	cmd := &cobra.Command{
		Use:   "verify <bundle.zip>",
		Short: "Verify an evidence bundle's integrity and signature",
		Long: "verify checks that every member hashes as the manifest records, that nothing was added, " +
			"and — when signed — that the signature is valid. Pass --pubkey with the key you expect " +
			"to check provenance, not just internal consistency. Exits 1 on any problem.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rep, err := runtime.VerifyEvidence(args[0], pubKey)
			if err != nil {
				return fmt.Errorf("evidence verify: %w", err)
			}
			fmt.Fprint(cmd.OutOrStdout(), rep.String())
			if !rep.OK() {
				os.Exit(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&pubKey, "pubkey", "", "The hex ed25519 public key you expect the bundle to be signed by.")
	return cmd
}

func evidenceKeygenCmd() *cobra.Command {
	var outDir string
	cmd := &cobra.Command{
		Use:   "keygen",
		Short: "Generate an ed25519 evidence signing key",
		Long: "keygen writes a private seed to evidence.key (0600) and the public key to evidence.pub. " +
			"Point --key-file (or $MACOS_MCP_EVIDENCE_KEY_FILE) at the seed to sign bundles, and " +
			"publish the public key so a reviewer can verify provenance.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			signer, err := evidence.GenerateSigner()
			if err != nil {
				return fmt.Errorf("generate signer: %w", err)
			}
			if err := os.MkdirAll(outDir, 0o700); err != nil {
				return fmt.Errorf("create %s: %w", outDir, err)
			}
			keyPath := filepath.Join(outDir, "evidence.key")
			pubPath := filepath.Join(outDir, "evidence.pub")
			if err := os.WriteFile(keyPath, []byte(signer.SeedHex()), 0o600); err != nil {
				return fmt.Errorf("write key: %w", err)
			}
			perm := os.FileMode(0o644) //nolint:gosec // the public key is meant to be published
			if err := os.WriteFile(pubPath, []byte(signer.PublicHex()), perm); err != nil {
				return fmt.Errorf("write public key: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "wrote %s (keep secret) and %s\npublic key: %s\n",
				keyPath, pubPath, signer.PublicHex())
			return nil
		},
	}
	cmd.Flags().StringVar(&outDir, "out", ".", "Directory to write evidence.key and evidence.pub into.")
	return cmd
}

// envKeyReplacer maps flag names to environment variable spelling.
var envKeyReplacer = strings.NewReplacer("-", "_")

// splitCSV splits a comma-separated flag value, dropping empties.
func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
