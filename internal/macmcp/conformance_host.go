//go:build darwin && (amd64 || arm64) && conformance

// This file exists only under the `conformance` build tag. `go build ./...`
// does not compile it, so the released binary has no HTTP listener and
// remains stdio-only — the posture the guardrail threat model is written
// against. The official suite can only reach a server over HTTP, so proving
// conformance needs an endpoint it can connect to; this is the smallest one
// that still serves the real thing.

package macmcp

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/deploymenttheory/agentweave-harness/guardrails/audit"
	"github.com/deploymenttheory/agentweave-harness/guardrails/contain"
	"github.com/deploymenttheory/agentweave-harness/guardrails/enforce"
	"github.com/deploymenttheory/agentweave-harness/guardrails/policy"
	"github.com/deploymenttheory/agentweave-harness/guardrails/signals"
	"github.com/deploymenttheory/agentweave-harness/guardrails/status"
	"github.com/deploymenttheory/agentweave-harness/guardrails/watch"
	"github.com/deploymenttheory/macos-mcp-server/internal/macdesktop"
	"github.com/deploymenttheory/macos-mcp-server/pkg/macos"
	"github.com/deploymenttheory/mcp-server-core/conformance"
	"github.com/deploymenttheory/mcp-server-core/runtime"
	"github.com/deploymenttheory/mcp-server-core/surface"
)

// ConformanceConfig configures the loopback host.
type ConformanceConfig struct {
	// Addr is the listen address. It must be a loopback address.
	Addr string
	// Path is the HTTP path the MCP endpoint is served on.
	Path string
	// Fixtures registers the named tools, resources and prompts the suite
	// requires to exercise tools/call, resources/read and prompts/get at all.
	// Off measures the manifest this server ships; on measures the
	// handler-to-wire path. Recording them separately keeps both claims honest.
	Fixtures bool
}

// RunConformanceHost serves this server's MCP surface over Streamable HTTP on
// loopback so the official conformance suite can connect to it. The surface
// is built by newSurface and wrapped in the same middleware chain as RunStdio,
// in the same order; only the transport differs.
func RunConformanceHost(ctx context.Context, cfg Config, hostCfg ConformanceConfig) error {
	if err := conformance.RequireLoopback(hostCfg.Addr); err != nil {
		return fmt.Errorf("conformance host: %w", err)
	}
	// Logs go to stderr: stdout carries the bound URL for the runner.
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	server, err := buildConformanceServer(ctx, cfg, hostCfg.Fixtures, logger)
	if err != nil {
		return err
	}
	if err := conformance.Serve(ctx, server, hostCfg.Addr, hostCfg.Path, logger); err != nil {
		return fmt.Errorf("conformance host: %w", err)
	}
	return nil
}

// buildConformanceServer assembles the server the host serves. It is separate
// from the transport so a test can drive the same object over an in-memory
// transport and compare its manifest with the stdio one.
//
// Three deliberate differences from a production run: the policy engine runs
// under the built-in default (present, recording, refusing nothing); the
// desktop engine is best-effort (a headless runner degrades to nil rather
// than refusing to serve); and handler panics are recovered.
func buildConformanceServer(
	ctx context.Context, cfg Config, fixturesEnabled bool, logger *slog.Logger,
) (*mcp.Server, error) {
	inv, personaInstructions, err := buildInventory(cfg, false)
	if err != nil {
		return nil, fmt.Errorf("build inventory: %w", err)
	}

	dsk, err := macdesktop.New(logger, macdesktop.Options{})
	if err != nil {
		logger.Warn("no desktop engine for the conformance host; "+
			"engine-backed resources will report an error rather than data", "error", err)
		dsk = nil
	}

	deps := macos.NewBaseDeps(dsk, logger, nil)
	deps.WithEnforceHTTPS(cfg.EnforceHTTPS)
	s := newSurface(cfg, inv, personaInstructions, deps)
	server := s.Server

	// The transparency services, as RunStdio wires them. A trip here logs and
	// records rather than actuating containment: a kill mid-suite would
	// destroy the evidence.
	auditLog := audit.NewAuditLog(&conformance.AuditDestination{Logger: logger})
	rugpull := watch.NewRugPull(func(reason string) {
		logger.Error("guardrail.rugpull", "reason", reason)
	}, auditLog)

	engine := policy.NewEngine(policy.Default(), runtime.NewGuardrailRegistry(envNames, logger), nil,
		func() *signals.Env { return &signals.Env{Sys: &conformance.Probe{}, Logger: logger} })

	s.InstallReceiving(
		conformance.RecoverMiddleware(logger),
		auditLog.Middleware(),
		rugpull.Middleware(),
		rugpull.PromptMiddleware(),
		rugpull.ResourceMiddleware(),
		rugpull.DiscoverMiddleware(),
		enforce.Middleware(engine, enforce.EnforcerDeps{Audit: auditLog, Logger: logger}),
	)

	inv.RegisterAll(ctx, server, deps)
	engine.SetIndex(runtime.NewToolIndex(ctx, inv))

	kill := contain.NewKillSwitch(nil)
	statusTool, statusHandler := status.StatusTool(
		func() signals.Decision { return signals.Decision{} },
		func() status.ServerStatus { return status.ServerStatus{} },
		kill,
	)
	server.AddTool(statusTool, statusHandler)
	killTool, killHandler := status.KillTool(func(string) {})
	server.AddTool(killTool, killHandler)

	// Baselines are pinned after the fixtures are registered, over what the
	// server will actually serve.
	fixtures := conformance.RegisterFixtures(server, fixturesEnabled)
	tools := append(surface.MCPTools(ctx, inv), statusTool, killTool)
	rugpull.SetBaseline(append(tools, fixtures.Tools...))
	rugpull.SetPromptBaseline(append(surface.MCPPrompts(ctx, inv), fixtures.Prompts...))
	rugpull.SetResourceBaseline(append(surface.MCPResources(ctx, inv), fixtures.Resources...))
	rugpull.SetDiscoverBaseline(s.Capabilities, s.Instructions)

	return server, nil
}
