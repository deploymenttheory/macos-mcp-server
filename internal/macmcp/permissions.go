//go:build darwin && (amd64 || arm64)

package macmcp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/deploymenttheory/go-bindings-macosplatform/bindings/frameworks/coregraphics"
	"github.com/deploymenttheory/go-bindings-macosplatform/bindings/frameworks/hiservices"
	"github.com/deploymenttheory/macos-mcp-server/internal/clirunner"
)

// Permissions is what the `permissions check` subcommand reports: every grant
// the server's tools depend on, read from the OS rather than assumed.
//
// macOS gates desktop automation behind per-application consent (TCC). The
// grants are keyed on the binary's code-signing identity, which is why the
// signing block is here beside them: an ad-hoc signed build gets a new
// designated requirement on every rebuild, so its grants do not survive
// the next `go build`.
type Permissions struct {
	// Accessibility covers the accessibility tree (Snapshot, Click by label,
	// Invoke, GetText, ...) and posting synthetic input (Click, Type, Shortcut).
	Accessibility bool `json:"accessibility"`
	// ScreenRecording covers Screenshot, Recording, CaptureEvidence and window
	// titles read through the window server.
	ScreenRecording bool `json:"screen_recording"`
	// FullDiskAccess covers the unified log store, other users' preference
	// domains and the paths macOS protects (Mail, Messages, Safari data, ...).
	// Detected by probing a TCC-protected file, which is the only honest test.
	FullDiskAccess bool `json:"full_disk_access"`
	// ConsoleSession reports whether the process is attached to a graphical
	// login session. Without one there is no desktop to drive — the macOS
	// analogue of Windows Session 0.
	ConsoleSession bool `json:"console_session"`
	// User is the account the process runs as.
	User string `json:"user"`
	// Elevated reports an effective uid of 0.
	Elevated bool `json:"elevated"`
	// Signing describes the binary's code-signing identity.
	Signing Signing `json:"signing"`
	// Warnings are human-readable notes about anything that will bite.
	Warnings []string `json:"warnings,omitempty"`
}

// Signing is the code-signing identity the TCC grants are keyed on.
type Signing struct {
	Identifier     string `json:"identifier,omitempty"`
	TeamIdentifier string `json:"team_identifier,omitempty"`
	// Authority is the leaf certificate's name ("Developer ID Application: ...")
	// or empty for an ad-hoc signature.
	Authority string `json:"authority,omitempty"`
	// AdHoc reports a signature with no certificate chain: a stable identity
	// only until the next build.
	AdHoc bool `json:"ad_hoc"`
	// Unsigned reports no signature at all.
	Unsigned bool `json:"unsigned"`
}

// Required reports whether the grants the default toolsets depend on are all
// present. Full Disk Access is not required: it widens diagnostics, it does
// not gate the desktop.
func (p Permissions) Required() bool {
	return p.Accessibility && p.ScreenRecording && p.ConsoleSession
}

// CheckPermissions reads every grant live. It prompts for nothing.
func CheckPermissions(ctx context.Context) Permissions {
	p := Permissions{
		Accessibility:   hiservices.AXIsProcessTrusted() != 0,
		ScreenRecording: coregraphics.CGPreflightScreenCaptureAccess(),
		FullDiskAccess:  fullDiskAccess(),
		ConsoleSession:  consoleSession(),
		Elevated:        os.Geteuid() == 0,
		Signing:         selfSigning(ctx),
	}
	if u, err := user.Current(); err == nil {
		p.User = u.Username
	}
	switch {
	case p.Signing.Unsigned:
		p.Warnings = append(
			p.Warnings,
			"the binary is unsigned: macOS cannot attach privacy grants to it; sign it (make sign-dev) before granting Accessibility",
		)
	case p.Signing.AdHoc:
		p.Warnings = append(p.Warnings,
			"the binary is ad-hoc signed: privacy grants are keyed on its hash and will not survive the next build; "+
				"sign with a stable identity (make sign-dev, or the Developer ID release build)")
	}
	if !p.ConsoleSession {
		p.Warnings = append(p.Warnings,
			"no graphical login session: desktop-automation toolsets are unavailable in this context")
	}
	if !p.Accessibility {
		p.Warnings = append(
			p.Warnings,
			"Accessibility is not granted: System Settings > Privacy & Security > Accessibility, or run `permissions request`",
		)
	}
	if !p.ScreenRecording {
		p.Warnings = append(p.Warnings,
			"Screen Recording is not granted: System Settings > Privacy & Security > Screen & System Audio Recording")
	}
	return p
}

// RequestPermissions triggers the system consent prompts for Accessibility and
// Screen Recording, then re-reads the grants. The prompts are asynchronous —
// macOS shows them and returns at once — so the returned state usually still
// says "not granted" until the user acts; the point is to get the application
// listed in System Settings so it can be switched on.
func RequestPermissions(ctx context.Context) Permissions {
	// CGRequestPostEventAccess is the event-posting half of Accessibility and is
	// what registers an unlisted application in the Accessibility pane.
	_ = coregraphics.CGRequestPostEventAccess()
	_ = coregraphics.CGRequestScreenCaptureAccess()
	return CheckPermissions(ctx)
}

// fullDiskAccess probes a file macOS only lets Full Disk Access holders read.
// There is no API that answers the question directly; TCC is enforced at the
// file system, so the file system is asked.
func fullDiskAccess() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	probe := filepath.Join(home, "Library", "Application Support", "com.apple.TCC", "TCC.db")
	f, err := os.Open(probe) //nolint:gosec // a fixed, well-known path
	if err != nil {
		// A missing file is not a denial — an account that has never had a
		// per-user TCC database (a fresh CI runner) has nothing to protect.
		return errors.Is(err, os.ErrNotExist)
	}
	_ = f.Close()
	return true
}

// consoleSession reports whether the window server knows this process: the
// session dictionary is nil for a process with no graphical session (ssh, a
// LaunchDaemon, a CI job with no logged-in user).
func consoleSession() bool {
	return coregraphics.CGSessionCopyCurrentDictionary() != nil
}

// selfSigning reads the running binary's signature with codesign(1). The
// Security framework exposes the same facts through SecCodeCopySigningInformation,
// but as a CFDictionary of degraded pointer types; the CLI is the same source
// of truth with a stable text format.
func selfSigning(ctx context.Context) Signing {
	exe, err := os.Executable()
	if err != nil {
		return Signing{Unsigned: true}
	}
	res, err := clirunner.Run(ctx, []string{"codesign", "-dv", "--verbose=2", exe}, clirunner.Options{})
	// codesign writes its report to stderr and exits 1 for an unsigned binary.
	report := res.Stderr
	if err != nil && strings.Contains(report, "code object is not signed") {
		return Signing{Unsigned: true}
	}
	return ParseCodesign(report)
}

// ParseCodesign reads the fields `codesign -dv --verbose=2` prints.
func ParseCodesign(report string) Signing {
	var s Signing
	if strings.TrimSpace(report) == "" {
		return Signing{Unsigned: true}
	}
	for _, line := range strings.Split(report, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch key {
		case "Identifier":
			s.Identifier = value
		case "TeamIdentifier":
			if value != "not set" {
				s.TeamIdentifier = value
			}
		case "Signature":
			if value == "adhoc" {
				s.AdHoc = true
			}
		case "Authority":
			// The first Authority line is the leaf.
			if s.Authority == "" {
				s.Authority = value
			}
		}
	}
	return s
}

// Describe renders the permissions for a terminal.
func (p Permissions) Describe() string {
	tick := func(b bool) string {
		if b {
			return "granted"
		}
		return "MISSING"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Accessibility:      %s\n", tick(p.Accessibility))
	fmt.Fprintf(&b, "Screen Recording:   %s\n", tick(p.ScreenRecording))
	fmt.Fprintf(&b, "Full Disk Access:   %s (optional)\n", tick(p.FullDiskAccess))
	fmt.Fprintf(&b, "Console session:    %s\n", tick(p.ConsoleSession))
	fmt.Fprintf(&b, "User:               %s (elevated: %t)\n", p.User, p.Elevated)
	switch {
	case p.Signing.Unsigned:
		b.WriteString("Signing:            unsigned\n")
	case p.Signing.AdHoc:
		fmt.Fprintf(&b, "Signing:            ad-hoc (%s)\n", p.Signing.Identifier)
	default:
		fmt.Fprintf(
			&b,
			"Signing:            %s (%s, team %s)\n",
			p.Signing.Authority,
			p.Signing.Identifier,
			p.Signing.TeamIdentifier,
		)
	}
	for _, w := range p.Warnings {
		fmt.Fprintf(&b, "  ! %s\n", w)
	}
	return b.String()
}
