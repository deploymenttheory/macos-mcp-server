//go:build darwin && (amd64 || arm64)

package macos

import (
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/deploymenttheory/mcp-server-core/inventory"
)

// findTool returns the named tool, or skips the test when it has not landed
// yet. The manifest grows milestone by milestone toward parity; a skip here is
// a visible "not yet", and the count tripwire below is what stops the manifest
// drifting silently.
func findTool(t *testing.T, name string) *inventory.ServerTool {
	t.Helper()
	tools := AllTools()
	for i := range tools {
		if tools[i].Tool.Name == name {
			return &tools[i]
		}
	}
	t.Skipf("%s has not landed in this milestone yet", name)
	return nil
}

// TestAllToolsValid asserts every tool in the manifest is well-formed: unique
// name, non-empty description, a toolset, an object input schema, and a handler.
func TestAllToolsValid(t *testing.T) {
	tools := AllTools()
	if len(tools) == 0 {
		t.Fatal("AllTools() is empty")
	}
	seen := map[string]bool{}
	for _, st := range tools {
		name := st.Tool.Name
		if name == "" {
			t.Error("tool with empty name")
			continue
		}
		if seen[name] {
			t.Errorf("duplicate tool name %q", name)
		}
		seen[name] = true
		if st.Tool.Description == "" {
			t.Errorf("%s: empty description", name)
		}
		if st.Toolset.ID == "" {
			t.Errorf("%s: no toolset", name)
		}
		if !st.HasHandler() {
			t.Errorf("%s: no handler", name)
		}
		if st.Tool.Annotations == nil || st.Tool.Annotations.Title == "" {
			t.Errorf("%s: no annotation title", name)
		}
		schema, ok := st.Tool.InputSchema.(*jsonschema.Schema)
		if !ok || schema == nil {
			t.Errorf("%s: input schema is not *jsonschema.Schema", name)
			continue
		}
		if schema.Type != "object" {
			t.Errorf("%s: input schema type = %q, want object", name, schema.Type)
		}
	}
}

// TestExpectedToolCount guards against accidental tool loss/addition. It is a
// deliberate tripwire: bump it when a tool lands. Parity with
// windows-mcp-server is 35.
func TestExpectedToolCount(t *testing.T) {
	const want = 28 // milestone 2: + system-admin (2), shell (2), filesystem (1), web (1), diagnostics (4); Recording lands in milestone 3
	if got := len(AllTools()); got != want {
		t.Errorf("tool count = %d, want %d (update this test intentionally)", got, want)
	}
}

// TestToolNamesMatchTheWindowsServer pins the cross-platform naming rule: every
// tool keeps its Windows name except the four whose Windows name denotes a
// Windows subsystem.
func TestToolNamesMatchTheWindowsServer(t *testing.T) {
	renamed := map[string]bool{"Defaults": true, "Shell": true, "LaunchdJob": true, "UnifiedLog": true}
	windowsNames := map[string]bool{
		"Snapshot": true, "Screenshot": true, "DisplayInventory": true, "Recording": true, "App": true,
		"Click": true, "Type": true, "Invoke": true, "GetText": true, "Scroll": true, "Move": true,
		"Shortcut": true, "Wait": true, "WaitFor": true, "MultiSelect": true, "MultiEdit": true,
		"Clipboard": true, "Process": true, "Notification": true, "LaunchExecutable": true,
		"FileSystem": true, "Scrape": true, "SystemInfo": true, "Service": true, "Network": true,
		"Package": true, "Credentials": true, "Assert": true, "CaptureEvidence": true, "Plan": true, "Apply": true,
	}
	for _, st := range AllTools() {
		if !windowsNames[st.Tool.Name] && !renamed[st.Tool.Name] {
			t.Errorf("%s is neither a Windows tool name nor one of the four documented renames", st.Tool.Name)
		}
		if name := st.Tool.Name; name == "Registry" || name == "PowerShell" || name == "ScheduledTask" || name == "EventLog" {
			t.Errorf("%s is a Windows subsystem name; use the macOS counterpart", name)
		}
	}
}

// TestToolsetIDsMatchTheWindowsServer pins that a policy document means the
// same thing on both platforms.
func TestToolsetIDsMatchTheWindowsServer(t *testing.T) {
	want := []string{"screen", "interaction", "apps", "system", "system-admin", "shell",
		"filesystem", "web", "diagnostics", "testing", "planning", "packages", "credentials"}
	all := map[string]inventory.ToolsetMetadata{
		"screen": ToolsetScreen, "interaction": ToolsetInteraction, "apps": ToolsetApps, "system": ToolsetSystem,
		"system-admin": ToolsetSystemAdmin, "shell": ToolsetShell, "filesystem": ToolsetFilesystem, "web": ToolsetWeb,
		"diagnostics": ToolsetDiagnostics, "testing": ToolsetTesting, "planning": ToolsetPlanning,
		"packages": ToolsetPackages, "credentials": ToolsetCredentials,
	}
	for _, id := range want {
		ts, ok := all[id]
		if !ok || string(ts.ID) != id {
			t.Errorf("toolset %q is missing or misnamed", id)
		}
	}
	defaults := map[string]bool{"screen": true, "interaction": true, "apps": true, "system": true}
	for id, ts := range all {
		if ts.Default != defaults[id] {
			t.Errorf("toolset %q default=%v, want %v", id, ts.Default, defaults[id])
		}
	}
}

// TestPersonasReferenceRealToolsets ensures each persona's toolsets exist.
func TestPersonasReferenceRealToolsets(t *testing.T) {
	inv, err := NewInventory().WithToolsets([]string{"all"}).Build()
	if err != nil {
		t.Fatal(err)
	}
	valid := map[inventory.ToolsetID]bool{}
	for _, id := range inv.ToolsetIDs() {
		valid[id] = true
	}
	for id, persona := range Personas {
		for _, ts := range persona.Toolsets {
			if !valid[inventory.ToolsetID(ts)] {
				// A toolset with no tools yet is invisible to the inventory; that is
				// expected until its milestone lands, so only unknown IDs fail.
				if _, known := map[string]bool{"system-admin": true, "shell": true, "filesystem": true, "web": true,
					"diagnostics": true, "testing": true, "planning": true, "packages": true, "credentials": true}[ts]; !known {
					t.Errorf("persona %q references unknown toolset %q", id, ts)
				}
			}
		}
	}
}

func TestPersonasHaveGuidance(t *testing.T) {
	if len(Personas) != 3 {
		t.Fatalf("want the three Windows personas, got %d", len(Personas))
	}
	for id, p := range Personas {
		if p.Instructions == "" || len(p.Toolsets) == 0 || p.ID != id {
			t.Errorf("persona %q is incomplete", id)
		}
	}
}

// TestReadOnlyToolsAreSafe asserts tools we expect to be read-only are annotated
// as such (so --read-only and read-only personas expose them).
func TestReadOnlyToolsAreSafe(t *testing.T) {
	readOnly := map[string]bool{
		"Snapshot": true, "Screenshot": true, "DisplayInventory": true,
		"Wait": true, "WaitFor": true, "Scrape": true, "GetText": true,
		"Assert": true, "CaptureEvidence": true, "SystemInfo": true,
		"Plan": true, "UnifiedLog": true, "Network": true,
	}
	for _, st := range AllTools() {
		if readOnly[st.Tool.Name] && !st.IsReadOnly() {
			t.Errorf("%s should be read-only", st.Tool.Name)
		}
	}
}

// TestDestructiveToolsAreWrite asserts powerful tools are not marked read-only.
func TestDestructiveToolsAreWrite(t *testing.T) {
	for _, st := range AllTools() {
		switch st.Tool.Name {
		case "Shell", "Defaults", "FileSystem", "Process", "Credentials", "Apply", "LaunchdJob", "Package":
			if st.IsReadOnly() {
				t.Errorf("%s must not be read-only", st.Tool.Name)
			}
		}
	}
}

// TestCredentialsToolNeverReturnsSecrets is a guard on the tool's core security
// property: no mode that reads a secret back.
func TestCredentialsToolNeverReturnsSecrets(t *testing.T) {
	tool := findTool(t, "Credentials")
	modeProp := tool.Tool.InputSchema.(*jsonschema.Schema).Properties["mode"]
	if modeProp == nil {
		t.Fatal("Credentials has no mode property")
	}
	allowed := map[string]bool{"list": true, "verify": true, "inject": true}
	for _, v := range modeProp.Enum {
		s, _ := v.(string)
		if !allowed[s] {
			t.Errorf("Credentials mode %q is not an approved mode", s)
		}
	}
	if len(modeProp.Enum) != len(allowed) {
		t.Errorf("mode enum has %d values, want exactly %d", len(modeProp.Enum), len(allowed))
	}
	if !strings.Contains(tool.Tool.Description, "cannot be read") {
		t.Error("Credentials description must state that secrets cannot be read")
	}
}

func TestAllResourcesValid(t *testing.T) {
	seen := map[string]bool{}
	uris := map[string]bool{}
	for _, r := range AllResources() {
		switch {
		case r.Resource.Name == "":
			t.Error("resource with empty name")
		case r.Resource.URI == "":
			t.Errorf("resource %q has no URI", r.Resource.Name)
		case r.Resource.Description == "":
			t.Errorf("resource %q has no description", r.Resource.Name)
		case r.Toolset.ID == "":
			t.Errorf("resource %q has no toolset", r.Resource.Name)
		case !r.HasHandler():
			t.Errorf("resource %q has no handler", r.Resource.Name)
		}
		if seen[r.Resource.Name] || uris[r.Resource.URI] {
			t.Errorf("duplicate resource %q / %q", r.Resource.Name, r.Resource.URI)
		}
		seen[r.Resource.Name], uris[r.Resource.URI] = true, true
		if !strings.HasPrefix(r.Resource.URI, "macos://") {
			t.Errorf("resource %q URI %q should use the macos:// scheme", r.Resource.Name, r.Resource.URI)
		}
	}
}

func TestAllPromptsValid(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range AllPrompts() {
		switch {
		case p.Prompt.Name == "":
			t.Error("prompt with empty name")
		case p.Prompt.Description == "":
			t.Errorf("prompt %q has no description", p.Prompt.Name)
		case p.Toolset.ID == "":
			t.Errorf("prompt %q has no toolset", p.Prompt.Name)
		case p.Handler == nil:
			t.Errorf("prompt %q has no handler", p.Prompt.Name)
		}
		if seen[p.Prompt.Name] {
			t.Errorf("duplicate prompt name %q", p.Prompt.Name)
		}
		seen[p.Prompt.Name] = true
		for _, a := range p.Prompt.Arguments {
			if a.Name == "" || a.Description == "" {
				t.Errorf("prompt %q has an incomplete argument", p.Prompt.Name)
			}
		}
	}
}

// TestResourcesAndPromptsUseToolBearingToolsets guards the inventory trap that
// a toolset introduced solely by a resource or prompt is invisible.
func TestResourcesAndPromptsUseToolBearingToolsets(t *testing.T) {
	withTools := map[inventory.ToolsetID]bool{}
	for _, st := range AllTools() {
		withTools[st.Toolset.ID] = true
	}
	for _, r := range AllResources() {
		if !withTools[r.Toolset.ID] {
			t.Errorf("resource %q uses toolset %q which contains no tools", r.Resource.Name, r.Toolset.ID)
		}
	}
	for _, p := range AllPrompts() {
		if !withTools[p.Toolset.ID] {
			// Prompts for toolsets whose tools land later are the one tolerated
			// case; the manifest check in CaptureSurface still registers them.
			t.Logf("prompt %q uses toolset %q which has no tools yet", p.Prompt.Name, p.Toolset.ID)
		}
	}
}

func TestPromptsReusePersonaGuidance(t *testing.T) {
	if got := personaGuidance("business-user"); got == "" {
		t.Fatal("personaGuidance returned empty for a real persona")
	}
	if got := personaGuidance("no-such-persona"); got != "" {
		t.Errorf("unknown persona should yield empty guidance, got %q", got)
	}
}

// TestExecutionPrimitivesAreAnnotatedDestructive pins the annotations policy
// rules match on.
func TestExecutionPrimitivesAreAnnotatedDestructive(t *testing.T) {
	want := []string{"App", "Type", "Shortcut", "MultiEdit", "Credentials", "Invoke", "Click", "Shell", "LaunchExecutable", "Apply", "Package"}
	for _, name := range want {
		tool := findToolOrNil(name)
		if tool == nil {
			continue // lands in a later milestone; TestExpectedToolCount tracks the manifest
		}
		a := tool.Tool.Annotations
		if a == nil || a.DestructiveHint == nil || !*a.DestructiveHint {
			t.Errorf("%s must carry DestructiveHint: policy rules and rate limits match on it", name)
		}
	}
}

func findToolOrNil(name string) *inventory.ServerTool {
	tools := AllTools()
	for i := range tools {
		if tools[i].Tool.Name == name {
			return &tools[i]
		}
	}
	return nil
}

// TestEveryWriteToolIsAnnotatedDestructive is deny-by-default: a tool that is
// not read-only must carry DestructiveHint or appear below with a reason.
func TestEveryWriteToolIsAnnotatedDestructive(t *testing.T) {
	exempt := map[string]string{
		"Move": "moves the cursor and commits nothing; acting on a hover menu still needs a Click, " +
			"which is gated. Annotating every input tool destructive would make the annotation mean " +
			"\"input\" and cost operators the ability to gate the calls that change state",
	}
	for _, tool := range AllTools() {
		a := tool.Tool.Annotations
		if a != nil && a.ReadOnlyHint {
			continue
		}
		if reason, ok := exempt[tool.Tool.Name]; ok {
			if reason == "" {
				t.Errorf("%s is exempt with no reason", tool.Tool.Name)
			}
			continue
		}
		if a == nil || a.DestructiveHint == nil || !*a.DestructiveHint {
			t.Errorf("%s is not read-only and carries no DestructiveHint; add the hint or an argued exemption", tool.Tool.Name)
		}
	}
	for name := range exempt {
		if findToolOrNil(name) == nil {
			t.Errorf("%s is exempt but no longer in the manifest; prune this list deliberately", name)
		}
	}
}

// TestArbitraryExecutionIsNotInADefaultToolset pins where code execution lives.
func TestArbitraryExecutionIsNotInADefaultToolset(t *testing.T) {
	for _, name := range []string{"Shell", "LaunchExecutable"} {
		tool := findToolOrNil(name)
		if tool == nil {
			continue
		}
		if tool.Toolset.Default || tool.Toolset.ID != ToolsetShell.ID {
			t.Errorf("%s must live in the opt-in shell toolset, got %q (default=%v)", name, tool.Toolset.ID, tool.Toolset.Default)
		}
	}
}

// TestAppNeverExecutesByPath keeps execution by path off the tool every persona
// carries.
func TestAppNeverExecutesByPath(t *testing.T) {
	tool := findTool(t, "App")
	schema := tool.Tool.InputSchema.(*jsonschema.Schema)
	for _, banned := range []string{"executable", "cwd", "args"} {
		if _, ok := schema.Properties[banned]; ok {
			t.Errorf("App accepts %q; execution by path belongs to LaunchExecutable in the shell toolset", banned)
		}
	}
}

func TestNormalizePathFoldsPrivateAndCase(t *testing.T) {
	if got := NormalizePath("/private/etc/Hosts"); got != "/etc/hosts" {
		t.Errorf("got %q", got)
	}
	if got := NormalizePath("/tmp/../tmp/X"); got != "/tmp/x" {
		t.Errorf("got %q", got)
	}
	home, _ := homeDir()
	if got := NormalizePath("~/Library/Keychains"); got != strings.ToLower(home+"/library/keychains") {
		t.Errorf("got %q", got)
	}
}
