package macos

import "github.com/deploymenttheory/mcp-server-core/inventory"

// Toolset metadata. Each tool declares membership in exactly one of these. The
// Default flag marks toolsets included when the caller asks for the "default"
// configuration (or passes no --toolsets selection). These groupings are also
// the building blocks of the persona presets below, and they are the same set,
// under the same IDs, as windows-mcp-server's — so a policy document, a persona
// and a journey mean the same thing on either platform.
var (
	// ToolsetScreen: read-only perception of the desktop.
	ToolsetScreen = inventory.ToolsetMetadata{
		ID:          "screen",
		Description: "Read-only perception of the desktop: accessibility-tree snapshots, screenshots, and display inventory.",
		Default:     true,
		Icon:        "device-desktop",
	}
	// ToolsetInteraction: synthetic input and UI interaction.
	ToolsetInteraction = inventory.ToolsetMetadata{
		ID:          "interaction",
		Description: "Mouse and keyboard interaction: click, type, scroll, move, shortcuts, waits, and multi-element actions.",
		Default:     true,
		Icon:        "pointer",
	}
	// ToolsetApps: application and window lifecycle.
	ToolsetApps = inventory.ToolsetMetadata{
		ID:          "apps",
		Description: "Launch, switch, resize, hide, and manage applications and windows.",
		Default:     true,
		Icon:        "browser",
	}
	// ToolsetSystem: local system state and objects.
	ToolsetSystem = inventory.ToolsetMetadata{
		ID:          "system",
		Description: "Local system operations: process list/kill, clipboard, and notifications.",
		Default:     true,
		Icon:        "gear",
	}
	// ToolsetSystemAdmin: persistence and machine-configuration tools.
	//
	// Separate from ToolsetSystem, and non-default, because these outlive the
	// session. A launchd job registers a program at login or on a schedule and a
	// defaults write changes application and system configuration, so either
	// survives the session, the kill switch and a reboot — a different class of
	// risk from listing processes or reading the clipboard, and not something a
	// persona should carry implicitly.
	ToolsetSystemAdmin = inventory.ToolsetMetadata{
		ID: "system-admin",
		Description: "Preference-domain (defaults) read/write and launchd job management. These change machine " +
			"configuration and can persist across reboots; disabled by default.",
		Icon: "gear",
	}
	// ToolsetShell: arbitrary shell execution (powerful; non-default).
	ToolsetShell = inventory.ToolsetMetadata{
		ID:          "shell",
		Description: "Execute arbitrary zsh, bash or AppleScript. Powerful and potentially destructive; disabled by default.",
		Icon:        "terminal",
	}
	// ToolsetFilesystem: file operations (non-default).
	ToolsetFilesystem = inventory.ToolsetMetadata{
		ID:          "filesystem",
		Description: "Read, write, copy, move, trash, delete, list, and search files. Disabled by default.",
		Icon:        "file-directory",
	}
	// ToolsetWeb: web scraping (non-default).
	ToolsetWeb = inventory.ToolsetMetadata{
		ID:          "web",
		Description: "Fetch and extract web page content. Disabled by default.",
		Icon:        "globe",
	}
	// ToolsetDiagnostics: system diagnostics for support workflows (non-default).
	ToolsetDiagnostics = inventory.ToolsetMetadata{
		ID: "diagnostics",
		Description: "System diagnostics: OS/hardware inventory, launchd service control, unified-log queries, and " +
			"network inspection, for support and troubleshooting. Disabled by default.",
		Icon: "pulse",
	}
	// ToolsetTesting: assertions and evidence capture for QA (non-default).
	ToolsetTesting = inventory.ToolsetMetadata{
		ID:          "testing",
		Description: "UI test assertions and evidence capture, for authoring and running automated tests. Disabled by default.",
		Icon:        "beaker",
	}
	// ToolsetPlanning: propose a sequence of tool calls for whole-plan
	// adjudication, then apply it (non-default). Opt-in because it is a distinct
	// way of working — the agent proposes, the plan is reviewed and adjudicated as
	// a whole, then executed — rather than an extra tool for the usual loop.
	ToolsetPlanning = inventory.ToolsetMetadata{
		ID: "planning",
		Description: "Propose a whole sequence of tool calls as a reviewable plan, adjudicated up front, then " +
			"apply it. Disabled by default.",
		Icon: "checklist",
	}
	// ToolsetPackages: software install/removal via Homebrew, installer and
	// softwareupdate (non-default). Opt-in and in no persona: it downloads and runs
	// installers from the network, outside the egress proxy, so it stays off the
	// default surface until an operator deliberately asks for it.
	ToolsetPackages = inventory.ToolsetMetadata{
		ID: "packages",
		Description: "Install, remove, list, and search software via Homebrew, the macOS installer, and " +
			"softwareupdate. Downloads run installers from the network, outside the egress proxy. Disabled " +
			"by default and in no persona.",
		Icon: "package",
	}
	// ToolsetCredentials: use of credentials installed at init (non-default).
	// Enabled automatically when --credentials-file is supplied; there is nothing
	// to use without it.
	ToolsetCredentials = inventory.ToolsetMetadata{
		ID: "credentials",
		Description: "Sign-in using credentials supplied to the server at startup and held in the login " +
			"keychain. Secrets can be injected into fields but never read back. Disabled by default; " +
			"enabled automatically with --credentials-file.",
		Icon: "key",
	}
)

// Persona is a named preset that resolves to a toolset selection and a
// read-only default. Personas make the toolset engine's persona goal concrete:
// each is simply a fixed WithToolsets configuration a caller can select with a
// single flag, rather than enumerating toolsets by hand.
type Persona struct {
	// ID is the persona's selector value (e.g. "first-line-support").
	ID string
	// Description explains who the persona is for.
	Description string
	// Toolsets is the toolset selection this persona enables (values accepted by
	// Builder.WithToolsets, including "all"/"default").
	Toolsets []string
	// ReadOnly is the persona's default read-only stance. A caller may still
	// override it explicitly.
	ReadOnly bool
	// Instructions is guidance injected into the MCP server's Instructions so
	// the agent adopts this persona's workflow and mindset.
	Instructions string
}

// Personas is the registry of built-in personas. The IDs and toolset
// memberships match windows-mcp-server's exactly; only the wording is macOS.
var Personas = map[string]Persona{
	"first-line-support": {
		ID:          "first-line-support",
		Description: "1st-line support engineer: perceive and drive the desktop, manage apps and system state, run diagnostics, and control launchd services.",
		// system-admin is explicit here: this persona's own instructions name
		// editing preference domains as part of the job, so the split must not
		// silently take that away. The QA and business-user personas do not get it.
		Toolsets: []string{"screen", "interaction", "apps", "system", "system-admin", "shell", "diagnostics"},
		ReadOnly: false,
		Instructions: "You are assisting a 1st-line support engineer troubleshooting a Mac. " +
			"Diagnose before you act: take a Snapshot to see the desktop, and use SystemInfo, Process, and Service " +
			"to gather state before making changes. Use Shell for deeper diagnostics. Before any destructive or " +
			"disruptive action (killing a process, stopping a launchd service, writing a preference domain with " +
			"Defaults), state what you will do and why. Report findings and the steps you took in clear, " +
			"non-jargon language the end user can follow.",
	},
	"qa-test-engineer": {
		ID:          "qa-test-engineer",
		Description: "QA test engineer: full desktop automation for authoring and running UI tests, with assertions, evidence capture, filesystem, and web access.",
		Toolsets:    []string{"screen", "interaction", "apps", "system", "filesystem", "web", "testing"},
		ReadOnly:    false,
		Instructions: "You are automating and verifying UI tests as a QA engineer. Work deterministically: take a " +
			"Snapshot before each interaction and target elements by their label rather than raw coordinates, since " +
			"labels are stable across display scale, appearance, and window position. Verify every expected outcome " +
			"with Assert or WaitFor, and treat a failed Assert as a test failure to report — do not silently continue. " +
			"Capture evidence with CaptureEvidence at key steps and on failure. Re-Snapshot after the UI changes.",
	},
	"business-user": {
		ID: "business-user",
		Description: "Business end-user & user-journey testing: drive everyday applications and browse the web through " +
			"the real UI, verify outcomes, and capture evidence — without shell, preference, or file-system access.",
		Toolsets: []string{"screen", "interaction", "apps", "web", "testing"},
		ReadOnly: false,
		Instructions: "You are driving a Mac as a business end-user, to complete everyday tasks and to test " +
			"user journeys through the real application UI. Work one observable step at a time: take a Snapshot, act on " +
			"an element by its label (Click, Type, or the accessibility actions Invoke/SetValue/Toggle/Select), then " +
			"verify the result with Assert or WaitFor before moving on — treat a failed Assert as a failed journey step " +
			"and report it. Prefer the accessibility actions (Invoke/SetValue) over raw Click/Type where available: " +
			"they are more reliable and do not depend on window focus. Capture evidence with CaptureEvidence at key " +
			"steps and on failure. You have no shell, preference, or file-system access — stay within the open " +
			"applications and browser. Note: the login window, the lock screen, Touch ID and password sheets for " +
			"privileged operations, and privacy (TCC) consent dialogs cannot be automated; assume you are already " +
			"in an unlocked, signed-in session with the needed permissions granted. Explain each step in plain " +
			"language and confirm before anything a user might not expect (submitting forms, deleting content, " +
			"closing apps).",
	},
}

// LookupPersona returns the named persona and whether it exists.
func LookupPersona(id string) (Persona, bool) {
	p, ok := Personas[id]
	return p, ok
}

// PersonaIDs lists the persona selectors, for completion and `personas`.
func PersonaIDs() []string {
	out := make([]string, 0, len(Personas))
	for id := range Personas {
		out = append(out, id)
	}
	return out
}
