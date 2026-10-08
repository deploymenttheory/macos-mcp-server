//go:build darwin && (amd64 || arm64)

package macmcp

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/deploymenttheory/agentweave-harness/guardrails/audit"
	"github.com/deploymenttheory/agentweave-harness/guardrails/egress"
	"github.com/deploymenttheory/agentweave-harness/guardrails/policy"
	"github.com/deploymenttheory/macos-mcp-server/internal/macguard"
	"github.com/deploymenttheory/mcp-server-core/runtime"
)

// newEnforcer is the one place the platform firewall enforcer is constructed,
// so every path — local egress, delegated egress, the harness rungs — gets
// the same pf implementation.
func newEnforcer(logger *slog.Logger) *macguard.Enforcer {
	return &macguard.Enforcer{Logger: logger}
}

// provisionEgress is the core provisioning with the pf enforcer plugged in.
func provisionEgress(
	ctx context.Context,
	devicePolicy *policy.Policy,
	auditLog *audit.AuditLog,
	logger *slog.Logger,
	harnessProxy runtime.HarnessEgress,
) (*egress.Service, func(), func(), error) {
	svc, cleanup, suspend, err := runtime.ProvisionEgress(
		ctx,
		devicePolicy,
		auditLog,
		logger,
		harnessProxy,
		newEnforcer(logger),
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("egress: %w", err)
	}
	return svc, cleanup, suspend, nil
}
