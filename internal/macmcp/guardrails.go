//go:build darwin && (amd64 || arm64)

package macmcp

import (
	"log/slog"

	"github.com/deploymenttheory/agentweave-harness/guardrails/policy"
	"github.com/deploymenttheory/agentweave-harness/guardrails/signals"
	"github.com/deploymenttheory/macos-mcp-server/internal/macdesktop"
	"github.com/deploymenttheory/macos-mcp-server/pkg/macos"
	"github.com/deploymenttheory/mcp-server-core/runtime"
	"github.com/deploymenttheory/mcp-server-core/toolkit"
)

// envNames derives the secret-carrying variable names from the prefix.
var envNames = runtime.EnvNames{Prefix: EnvPrefix}

// guardrailEnv builds a fresh evaluation environment over the macOS probes.
// The same probe backs both the SystemProbe and HealthProbe surfaces, so
// posture is measured just-in-time on each evaluation.
func guardrailEnv(cfg Config, dsk *macdesktop.Desktop, logger *slog.Logger) *signals.Env {
	p := newSystemProbe(dsk)
	return &signals.Env{Sys: p, Health: p, Logger: logger, EnforceHTTPS: cfg.EnforceHTTPS}
}

// guardrailPaths lists the guardrail files the FileSystem tool must not touch
// for this configuration, normalised the macOS way.
func guardrailPaths(cfg Config, p *policy.Policy) []toolkit.ProtectedPath {
	return runtime.GuardrailPaths(runtime.GuardrailPathsConfig{
		CredentialsFile: cfg.CredentialsFile,
		PolicyConfig:    cfg.PolicyConfig,
		Normalize:       macos.NormalizePath,
	}, p)
}
