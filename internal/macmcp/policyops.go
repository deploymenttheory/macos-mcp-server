//go:build darwin && (amd64 || arm64)

package macmcp

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/deploymenttheory/agentweave-harness/guardrails/policy"
	"github.com/deploymenttheory/agentweave-harness/guardrails/signals"
	"github.com/deploymenttheory/macos-mcp-server/internal/macdesktop"
	"github.com/deploymenttheory/mcp-server-core/runtime"
)

// The operator-facing operations behind the `policy` subcommands: is this
// document valid, what does this device look like right now, why was that
// call refused, and do these fixtures pass. None starts a server.

// ValidatePolicy loads and validates a policy document without touching the
// device.
func ValidatePolicy(cfg Config) (*policy.Policy, error) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	p, err := runtime.ValidatePolicy(cfg.PolicyConfig, runtime.NewGuardrailRegistry(envNames, logger))
	if err != nil {
		return nil, fmt.Errorf("policy: %w", err)
	}
	return p, nil
}

// EvaluatePolicy reads every signal the policy declares, live and cache
// bypassed, and returns the decision for the startup scope.
func EvaluatePolicy(ctx context.Context, cfg Config) (signals.Decision, error) {
	logger, cleanup, err := runtime.NewLogger(cfg.LogFile)
	if err != nil {
		return signals.Decision{}, fmt.Errorf("logger: %w", err)
	}
	defer cleanup()

	reg := runtime.NewGuardrailRegistry(envNames, logger)
	devicePolicy, err := runtime.LoadPolicy(cfg.PolicyConfig, reg, logger)
	if err != nil {
		return signals.Decision{}, fmt.Errorf("policy: %w", err)
	}
	cfg.EnforceHTTPS = devicePolicy.EnforceHTTPS

	dsk, err := macdesktop.New(logger, macdesktop.Options{})
	if err != nil {
		return signals.Decision{}, fmt.Errorf("failed to start desktop engine: %w", err)
	}
	defer func() { _ = dsk.Close() }()

	return runtime.EvaluatePolicy(ctx, devicePolicy, reg,
		func() *signals.Env { return guardrailEnv(cfg, dsk, logger) }), nil
}

// ExplainPolicy reports which rules cover a tool and what they require,
// evaluating nothing.
func ExplainPolicy(ctx context.Context, cfg Config, tool string) (runtime.PolicyCoverage, error) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	reg := runtime.NewGuardrailRegistry(envNames, logger)
	devicePolicy, err := runtime.LoadPolicy(cfg.PolicyConfig, reg, logger)
	if err != nil {
		return runtime.PolicyCoverage{}, fmt.Errorf("policy: %w", err)
	}
	inv, _, err := buildInventory(cfg, false)
	if err != nil {
		return runtime.PolicyCoverage{}, fmt.Errorf("build inventory: %w", err)
	}
	return runtime.ExplainPolicy(devicePolicy, reg, runtime.NewToolIndex(ctx, inv), tool), nil
}

// TestPolicy runs fixture files against the signal set this build knows.
func TestPolicy(fixturePaths []string) ([]runtime.PolicyTestReport, error) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	known := runtime.NewGuardrailRegistry(envNames, logger).IDs()
	reports, err := runtime.RunPolicyFixtures(known, fixturePaths)
	if err != nil {
		return nil, fmt.Errorf("policy test: %w", err)
	}
	return reports, nil
}
