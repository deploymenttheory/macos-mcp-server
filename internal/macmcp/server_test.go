//go:build darwin && (amd64 || arm64)

package macmcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/mcp-server-core/mcpspec"
)

func schemaDir() string { return filepath.Join("..", "..", "schema") }

// TestServedSurfaceValidatesAgainstTheNewestRevision is the offline pre-flight:
// every wire object this server serves validates against the newest vendored
// schema, so `go test` catches a broken tool schema without Node.
func TestServedSurfaceValidatesAgainstTheNewestRevision(t *testing.T) {
	m, err := mcpspec.LoadManifest(schemaDir())
	if err != nil {
		t.Fatal(err)
	}
	spec, err := mcpspec.Load(schemaDir(), m.Newest())
	if err != nil {
		t.Fatal(err)
	}
	got, err := CaptureSurface(context.Background(), Config{Toolsets: []string{"all"}, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if got.NegotiatedVersion != m.Newest() {
		t.Errorf("negotiated %q, newest vendored is %q", got.NegotiatedVersion, m.Newest())
	}
	var payload struct {
		Tools []json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(got.ToolsListResult, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Tools) == 0 {
		t.Fatal("no tools served")
	}
	for _, raw := range payload.Tools {
		var named struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(raw, &named)
		if err := spec.ValidateJSON("Tool", raw); err != nil {
			t.Errorf("tool %q does not validate: %v", named.Name, err)
		}
	}
	if err := spec.ValidateJSON("ListToolsResult", got.ToolsListResult); err != nil {
		t.Errorf("tools/list result: %v", err)
	}
	if err := spec.ValidateJSON("ServerCapabilities", got.Capabilities); err != nil {
		t.Errorf("capabilities: %v", err)
	}
	if def, ok := spec.FirstPresent("DiscoverResult", "InitializeResult"); ok {
		if err := spec.ValidateJSON(def, got.HandshakeResult); err != nil {
			t.Errorf("handshake (%s): %v", def, err)
		}
	}
}

// TestGuardrailToolsAlwaysServed pins that GuardrailStatus and Kill survive
// --exclude-tools: they are registered outside the inventory.
func TestGuardrailToolsAlwaysServed(t *testing.T) {
	got, err := CaptureSurface(context.Background(), Config{ExcludeTools: []string{"GuardrailStatus", "Kill"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"GuardrailStatus", "Kill"} {
		if !strings.Contains(string(got.ToolsListResult), `"`+name+`"`) {
			t.Errorf("%s must be served despite --exclude-tools", name)
		}
	}
}

func TestPersonaSelectsToolsets(t *testing.T) {
	inv, instr, err := buildInventory(Config{Persona: "business-user"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if instr == "" {
		t.Error("persona instructions must be returned")
	}
	ids := map[string]bool{}
	for _, ts := range inv.EnabledToolsets() {
		ids[string(ts.ID)] = true
	}
	for _, want := range []string{"screen", "interaction", "apps"} {
		if !ids[want] {
			t.Errorf("business-user must enable %s", want)
		}
	}
	if ids["system"] {
		t.Error("business-user must not enable system")
	}
	if _, _, err := buildInventory(Config{Persona: "nobody"}, false); err == nil {
		t.Error("unknown persona must be refused")
	}
}

func TestNoSessionDropsAutomationToolsets(t *testing.T) {
	inv, _, err := buildInventory(Config{Toolsets: []string{"all"}}, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, ts := range inv.EnabledToolsets() {
		if ts.ID == "screen" || ts.ID == "interaction" || ts.ID == "apps" {
			t.Errorf("no-session mode must not serve %s", ts.ID)
		}
	}
}
