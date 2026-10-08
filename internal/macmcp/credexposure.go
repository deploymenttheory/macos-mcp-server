//go:build darwin && (amd64 || arm64)

package macmcp

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/deploymenttheory/agentweave-harness/guardrails/policy"
	"github.com/deploymenttheory/macos-mcp-server/pkg/macos"
	"github.com/deploymenttheory/mcp-server-core/inventory"
	"github.com/deploymenttheory/mcp-server-core/surface"
)

// credentialExposure names the toolsets that put installed credentials at risk
// on macOS.
//
// Risky: shell can read a keychain item back with
// `security find-generic-password -w`, and filesystem can copy the login
// keychain file for offline attack. Perception: screen, interaction (GetText)
// and system (Clipboard) can read a secret back off the screen once it has
// been typed somewhere unmasked, which only an allow_unmasked_target entry
// permits.
var credentialExposure = surface.CredentialExposure{
	Risky: []string{
		string(macos.ToolsetShell.ID),
		string(macos.ToolsetFilesystem.ID),
	},
	Perception: []string{
		string(macos.ToolsetScreen.ID),
		string(macos.ToolsetInteraction.ID),
		string(macos.ToolsetSystem.ID), // Clipboard
	},
}

// credentialsDeclareUnmaskedTargets reports whether any entry in the
// credentials document opts out of the masked-destination check, after the
// same permission check the real loader applies.
func credentialsDeclareUnmaskedTargets(path string) bool {
	return surface.CredentialsDeclareUnmaskedTargets(path, checkCredentialsFilePerms)
}

// ErrCredentialExposureDenied reports a --credentials-file served alongside a
// toolset that can read the installed credentials back (shell or filesystem),
// or — for a credential that opts out of the masked-destination check — read
// them off the screen, without the policy acknowledging that exposure.
var ErrCredentialExposureDenied = errors.New("credentials exposed to a toolset that can read them back")

// refuseCredentialExposure applies the exposure rule before anything is
// installed. Installed credentials live in the calling user's login keychain,
// so a toolset that can read that back defeats the never-read guarantee.
// Refuse rather than serve a weaker posture than the document describes,
// unless the policy explicitly accepts it; the acknowledged case is logged so
// the trade-off is visible.
func refuseCredentialExposure(
	cfg Config,
	inv *inventory.Inventory,
	devicePolicy *policy.Policy,
	logger *slog.Logger,
) error {
	if cfg.CredentialsFile == "" {
		return nil
	}
	unacked, acked := credentialExposure.Split(
		inv.EnabledToolsets(),
		devicePolicy.Credentials.AcknowledgeToolsetExposure,
		credentialsDeclareUnmaskedTargets(cfg.CredentialsFile),
	)
	if len(unacked) > 0 {
		return fmt.Errorf("%w: the %v toolset(s) can read installed credentials back out of the "+
			"keychain; remove them, or acknowledge the exposure in the policy document "+
			"(credentials.acknowledge_toolset_exposure)", ErrCredentialExposureDenied, unacked)
	}
	if len(acked) > 0 {
		logger.Warn("credentials served alongside toolsets that can read them back; exposure acknowledged in policy",
			"toolsets", acked)
	}
	return nil
}
