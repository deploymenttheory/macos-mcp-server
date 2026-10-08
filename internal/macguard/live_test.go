//go:build darwin && (amd64 || arm64)

package macguard

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/deploymenttheory/agentweave-harness/guardrails/egress"
)

// The live suites load real pf rules and need root. They have separate
// opt-ins so someone running the scoped test never makes their machine
// default-deny by accident.
const (
	scopedTestEnv      = "MACOS_MCP_SCOPED_TEST"
	globalBlockTestEnv = "MACOS_MCP_GLOBAL_BLOCK_TEST"
)

func requireOptIn(t *testing.T, env string) *Enforcer {
	t.Helper()
	if strings.TrimSpace(os.Getenv(env)) != "1" {
		t.Skipf("set %s=1 (as root) to run the tests that load real pf rules", env)
	}
	if !Elevated() {
		t.Fatalf("%s=1 was set but this process is not root", env)
	}
	return &Enforcer{StateDir: t.TempDir()}
}

func TestScopedLive(t *testing.T) {
	e := requireOptIn(t, scopedTestEnv)
	undo, err := e.Apply(egress.EnforceSpec{ProxyAddr: "127.0.0.1:8181", Applications: []string{"/Applications/Safari.app"}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	rules, err := e.Observe(context.Background(), EgressAnchor)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) == 0 {
		t.Error("pf reports no rules in the anchor after Apply")
	}
	if err := undo(); err != nil {
		t.Fatalf("undo: %v", err)
	}
	rules, _ = e.Observe(context.Background(), EgressAnchor)
	if len(rules) != 0 {
		t.Errorf("rules remain after undo: %v", rules)
	}
	if n, err := e.Recover(); err != nil || n != 0 {
		t.Errorf("Recover after a clean undo = %d, %v; want 0, nil", n, err)
	}
}

func TestGlobalBlockLive(t *testing.T) {
	e := requireOptIn(t, globalBlockTestEnv)
	undo, err := e.Apply(egress.EnforceSpec{ProxyAddr: "127.0.0.1:8181", GlobalBlock: true, AllowPorts: []int{443}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	defer func() { _ = undo() }()
	rules, err := e.Observe(context.Background(), EgressAnchor)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(rules, "\n")
	if !strings.Contains(joined, "block drop out all") {
		t.Errorf("pf does not report the global block:\n%s", joined)
	}
	if err := e.Suspend(); err != nil {
		t.Fatal(err)
	}
	rules, _ = e.Observe(context.Background(), EgressAnchor)
	if strings.Contains(strings.Join(rules, "\n"), "pass") {
		t.Errorf("allow rules survive Suspend:\n%s", strings.Join(rules, "\n"))
	}
}
