//go:build darwin && (amd64 || arm64)

package macmcp

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/deploymenttheory/agentweave-harness/guardrails/signals"
	"github.com/deploymenttheory/macos-mcp-server/internal/clirunner"
)

// signals.HealthProbe is Windows-shaped. This file is its macOS reading,
// documented in docs/policy-config.md and in the harness-side follow-up that
// should give the postures platform-neutral names:
//
//	SecureBoot   → Apple silicon boot security (Full Security) / T2 secure boot
//	TPM          → Secure Enclave presence (never attestation-capable from a CLI)
//	DeviceGuard  → VBS = System Integrity Protection, HVCI = sealed system
//	               volume, Credential Guard = Gatekeeper assessments
//	BitLocker    → FileVault, one entry per encrypted APFS volume
//	Attestation  → unavailable: there is no attestation service to quote against
//
// Every probe is a CLI read, best-effort and bounded: a tool that is missing
// or refuses reports an error for that signal, never a fabricated pass.

// ErrAttestationUnavailable reports that macOS has no platform attestation
// this server can produce.
var ErrAttestationUnavailable = errors.New(
	"platform attestation is not available on macOS without an attestation service",
)

const probeTimeout = 10 * time.Second

func probeRun(argv ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	res, err := clirunner.Run(ctx, argv, clirunner.Options{Timeout: probeTimeout})
	if err != nil {
		return res.Stdout + res.Stderr, fmt.Errorf("probe %s: %w", argv[0], err)
	}
	return res.Stdout, nil
}

// SecureBoot reads the boot security policy. On Apple silicon `csrutil
// status` reflects the current policy and `bputil -d` the configured one;
// bputil needs root, so csrutil is read first and bputil refines it when
// available.
func (p *systemProbe) SecureBoot() (signals.SecureBootState, error) {
	st := signals.SecureBootState{Supported: true}
	out, err := probeRun("csrutil", "status")
	if err != nil {
		return st, err
	}
	lower := strings.ToLower(out)
	st.Enabled = strings.Contains(lower, "system integrity protection status: enabled")
	if runtime.GOARCH == "arm64" {
		if b, berr := probeRun("bputil", "-d"); berr == nil {
			bl := strings.ToLower(b)
			st.Enabled = strings.Contains(bl, "full security") || strings.Contains(bl, "security mode: full")
		}
	}
	return st, nil
}

// TPM reads Secure Enclave presence: every Apple silicon Mac and every
// T2-equipped Intel Mac has one. It is never attestation-capable from a CLI.
func (p *systemProbe) TPM() (signals.TPMState, error) {
	st := signals.TPMState{Manufacturer: "Apple", Version: "SEP"}
	if runtime.GOARCH == "arm64" {
		st.Present, st.Ready = true, true
		return st, nil
	}
	out, err := probeRun("system_profiler", "SPiBridgeDataType")
	if err != nil {
		return st, err
	}
	st.Present = strings.Contains(out, "T2")
	st.Ready = st.Present
	return st, nil
}

// DeviceGuard maps the three Windows integrity features onto macOS's: SIP,
// the sealed system volume, and Gatekeeper.
func (p *systemProbe) DeviceGuard() (signals.DeviceGuardState, error) {
	var st signals.DeviceGuardState
	if out, err := probeRun("csrutil", "status"); err == nil {
		st.VBSRunning = strings.Contains(strings.ToLower(out), "status: enabled")
	} else {
		return st, err
	}
	if out, err := probeRun("csrutil", "authenticated-root", "status"); err == nil {
		st.HVCIRunning = strings.Contains(strings.ToLower(out), "enabled")
	}
	if out, err := probeRun("spctl", "--status"); err == nil {
		st.CredentialGuardRunning = strings.Contains(strings.ToLower(out), "assessments enabled")
	}
	return st, nil
}

// BitLocker reads FileVault: one volume, the boot volume, protected when
// `fdesetup status` says FileVault is on.
func (p *systemProbe) BitLocker() ([]signals.BitLockerVolume, error) {
	out, err := probeRun("fdesetup", "status")
	if err != nil {
		return nil, err
	}
	on := strings.Contains(out, "FileVault is On")
	return []signals.BitLockerVolume{{Mount: "/", Protected: on}}, nil
}

// PlatformAttestation is unavailable on macOS; the signal skips rather than
// fabricating a quote.
func (p *systemProbe) PlatformAttestation(_ []byte) (*signals.Attestation, error) {
	return nil, ErrAttestationUnavailable
}
