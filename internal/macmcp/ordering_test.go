//go:build darwin && (amd64 || arm64)

package macmcp

import (
	"context"
	"encoding/json"
	"testing"
)

// TestToolsListOrderIsDeterministic pins the SHOULD protocol 2026-07-28 added:
// tools/list in a deterministic order, so clients can cache the manifest. The
// order is stable because the inventory keeps tools in a slice and the SDK
// sorts by name; a map anywhere in the registration path would reintroduce
// randomised iteration.
func TestToolsListOrderIsDeterministic(t *testing.T) {
	names := func() []string {
		captured, err := CaptureSurface(context.Background(), Config{Toolsets: []string{"all"}})
		if err != nil {
			t.Fatal(err)
		}
		var payload struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		}
		if err := json.Unmarshal(captured.ToolsListResult, &payload); err != nil {
			t.Fatal(err)
		}
		out := make([]string, len(payload.Tools))
		for i, tool := range payload.Tools {
			out[i] = tool.Name
		}
		return out
	}
	first := names()
	if len(first) == 0 {
		t.Fatal("no tools served")
	}
	for i := 0; i < 3; i++ {
		next := names()
		if len(next) != len(first) {
			t.Fatalf("capture %d served %d tools, want %d", i, len(next), len(first))
		}
		for j := range first {
			if next[j] != first[j] {
				t.Fatalf("tools/list order is not deterministic: position %d was %q, now %q", j, first[j], next[j])
			}
		}
	}
}
