//go:build darwin && (amd64 || arm64)

package macmcp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"strings"
	"sync"
	"time"

	"github.com/deploymenttheory/agentweave-harness/guardrails/signals"
	"github.com/deploymenttheory/macos-mcp-server/internal/clirunner"
	"github.com/deploymenttheory/macos-mcp-server/internal/macdesktop"
)

// systemProbe adapts the macOS engine to signals.SystemProbe and
// signals.HealthProbe. A fresh probe is created per evaluation so posture
// re-checks see current state; within one evaluation the facts are cached.
type systemProbe struct {
	dsk   *macdesktop.Desktop
	once  sync.Once
	facts macdesktop.HostFacts
	// Entra device and tenant identifiers, from Platform SSO when registered.
	entraID  string
	tenantID string
	mdmURL   string
}

func newSystemProbe(dsk *macdesktop.Desktop) *systemProbe { return &systemProbe{dsk: dsk} }

// errUnsupportedProbeCommand reports a RunShell request the command table does
// not carry.
var errUnsupportedProbeCommand = errors.New("probe command is not in the command table")

func (p *systemProbe) load(ctx context.Context) {
	p.once.Do(func() {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		if p.dsk != nil {
			p.facts = p.dsk.HostFacts(ctx)
		}
		if out, err := clirunner.Run(
			ctx,
			[]string{"profiles", "status", "-type", "enrollment"},
			clirunner.Options{Timeout: 10 * time.Second},
		); err == nil {
			_, p.mdmURL = macdesktop.ParseEnrollment(out.Stdout)
		}
		if out, err := clirunner.Run(
			ctx,
			[]string{"app-sso", "platform", "-s"},
			clirunner.Options{Timeout: 10 * time.Second},
		); err == nil {
			p.entraID, p.tenantID = parsePlatformSSO(out.Stdout)
		}
	})
}

// RunShell is a command table, not a shell. The harness's built-in signals
// issue one Windows command through this seam — `dsregcmd /status` — and the
// table answers it with the dsregcmd-shaped text the harness's parser expects,
// synthesised from the macOS facts: MDM enrollment from `profiles`, and the
// Entra device and tenant from Platform SSO. Anything else is refused: the
// probe never becomes a way to run arbitrary commands.
func (p *systemProbe) RunShell(ctx context.Context, command string) (string, error) {
	if strings.ToLower(strings.TrimSpace(command)) == "dsregcmd /status" {
		p.load(ctx)
		return p.dsregStatus(), nil
	}
	return "", fmt.Errorf("%w: %q", errUnsupportedProbeCommand, command)
}

// dsregStatus renders the dsregcmd-shaped report signals.ParseDsreg reads.
func (p *systemProbe) dsregStatus() string {
	yesNo := func(b bool) string {
		if b {
			return "YES"
		}
		return "NO"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "             AzureAdJoined : %s\n", yesNo(p.entraID != ""))
	fmt.Fprintf(&b, "                  DeviceId : %s\n", p.entraID)
	fmt.Fprintf(&b, "                  TenantId : %s\n", p.tenantID)
	fmt.Fprintf(&b, "                    MdmUrl : %s\n", p.mdmURL)
	fmt.Fprintf(&b, "              DomainJoined : %s\n", yesNo(p.facts.PartOfDomain))
	fmt.Fprintf(&b, "                DomainName : %s\n", p.facts.Domain)
	return b.String()
}

// parsePlatformSSO reads the device and tenant identifiers out of
// `app-sso platform -s`, when a Platform SSO extension has registered.
func parsePlatformSSO(out string) (deviceID, tenantID string) {
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "device id", "deviceid", "device identifier":
			deviceID = v
		case "tenant id", "tenantid", "tenant identifier":
			tenantID = v
		}
	}
	return deviceID, tenantID
}

func (p *systemProbe) DomainSKU() (signals.DomainSKU, error) {
	p.load(context.Background())
	return signals.DomainSKU{
		PartOfDomain: p.facts.PartOfDomain,
		Domain:       p.facts.Domain,
		OSCaption:    p.facts.OSCaption,
	}, nil
}

// RunContext describes the process: elevated means euid 0; "system" is the
// macOS reading of Windows' Session 0 — no graphical session to drive.
func (p *systemProbe) RunContext() signals.RunContext {
	rc := signals.RunContext{Elevated: os.Geteuid() == 0}
	rc.IsSystem = rc.Elevated && !consoleSession()
	if u, err := user.Current(); err == nil {
		rc.User = u.Username
	}
	return rc
}

// IsAdmin reports membership of the admin group (gid 80).
func (p *systemProbe) IsAdmin() bool {
	u, err := user.Current()
	if err != nil {
		return false
	}
	gids, err := u.GroupIds()
	if err != nil {
		return false
	}
	for _, g := range gids {
		if g == "80" {
			return true
		}
	}
	return false
}

func (p *systemProbe) DeviceIdentity() signals.DeviceIdentity {
	p.load(context.Background())
	return signals.DeviceIdentity{
		Hostname:      p.facts.Hostname,
		Serial:        p.facts.Serial,
		EntraDeviceID: p.entraID,
		TenantID:      p.tenantID,
	}
}
