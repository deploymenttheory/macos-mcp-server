//go:build darwin && (amd64 || arm64)

package macos

import "github.com/deploymenttheory/mcp-server-core/inventory"

// AllTools returns the full manifest of macOS automation tools with their
// toolset membership. This is the single source of truth for what the server
// can expose; the inventory Builder filters it down per toolset selection,
// read-only mode, persona, and allow/deny lists.
//
// Tools are grouped by toolset for readability. The list grows milestone by
// milestone toward parity with windows-mcp-server's 35.
func AllTools() []inventory.ServerTool {
	return []inventory.ServerTool{
		// screen toolset
		Snapshot(),
		Screenshot(),
		DisplayInventory(),
		Recording(),

		// apps toolset
		App(),

		// interaction toolset
		Click(),
		Type(),
		Invoke(),
		GetText(),
		Scroll(),
		Move(),
		Shortcut(),
		Wait(),
		WaitFor(),
		MultiSelect(),
		MultiEdit(),

		// system toolset
		Clipboard(),
		Process(),
		Notification(),

		// system-admin toolset (opt-in: persists across reboots)
		Defaults(),
		LaunchdJob(),

		// shell toolset
		Shell(),
		LaunchExecutable(),

		// filesystem toolset
		FileSystem(),

		// web toolset
		Scrape(),

		// diagnostics toolset (1st-line support)
		SystemInfo(),
		Service(),
		UnifiedLog(),
		Network(),

		// testing toolset (QA)
		Assert(),
		CaptureEvidence(),

		// planning toolset (propose a plan, apply it)
		Plan(),
		Apply(),
	}
}

// ToolToolsets maps each tool's name to its toolset ID. GuardrailStatus and
// Kill are absent because they belong to no toolset and are served
// unconditionally.
func ToolToolsets() map[string]string {
	tools := AllTools()
	m := make(map[string]string, len(tools))
	for _, t := range tools {
		m[t.Tool.Name] = string(t.Toolset.ID)
	}
	return m
}

// NewInventory builds an inventory Builder seeded with the full tool manifest.
// Callers apply persona/toolset/read-only configuration and call Build.
func NewInventory() *inventory.Builder {
	return inventory.NewBuilder().
		SetTools(AllTools()).
		SetFixedResources(AllResources()).
		SetPrompts(AllPrompts())
}
