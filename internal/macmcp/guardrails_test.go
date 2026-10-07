//go:build darwin && (amd64 || arm64)

package macmcp

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/deploymenttheory/agentweave-harness/guardrails/audit"
	"github.com/deploymenttheory/agentweave-harness/guardrails/egress"
	"github.com/deploymenttheory/agentweave-harness/guardrails/policy"
	"github.com/deploymenttheory/agentweave-harness/guardrails/policy/policytest"
	"github.com/deploymenttheory/agentweave-harness/guardrails/signals"

	"github.com/deploymenttheory/mcp-server-core/runtime"

	"github.com/deploymenttheory/macos-mcp-server/internal/macguard"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// TestGuardrailEnvCarriesEnforceHTTPS proves the setting reaches the guardrail
// checks, which is what lets remote-policy refuse a plaintext endpoint.
func TestGuardrailEnvCarriesEnforceHTTPS(t *testing.T) {
	if env := guardrailEnv(Config{EnforceHTTPS: true}, nil, nil); !env.EnforceHTTPS {
		t.Error("guardrailEnv must propagate EnforceHTTPS")
	}
	if env := guardrailEnv(Config{}, nil, nil); env.EnforceHTTPS {
		t.Error("guardrailEnv must not set EnforceHTTPS when off")
	}
}

// TestServerDefaultPolicyIsAuditOnly is this repo's pin of the never-refuse
// default, driven through the same registry the server builds: a destructive
// call is admitted with every signal the default names failing.
func TestServerDefaultPolicyIsAuditOnly(t *testing.T) {
	reg := runtime.NewGuardrailRegistry(envNames, discardLogger())
	p, err := runtime.LoadPolicy("", reg, discardLogger())
	if err != nil {
		t.Fatalf("LoadPolicy with no document: %v", err)
	}
	if p.Mode != policy.ModeAuditOnly {
		t.Fatalf("default mode = %q, want %q", p.Mode, policy.ModeAuditOnly)
	}
	if p.Egress.Enabled {
		t.Error("default policy enables the egress proxy; the default must not alter the device's networking")
	}
	states := map[string]signals.Status{}
	for _, id := range p.SignalIDs() {
		states[id] = signals.Fail
	}
	facts := policy.ToolFacts{Name: "Shell", Toolset: "shell", Destructive: true}
	eng := policytest.NewEngine(p, policytest.StaticIndex{"Shell": facts}, states)
	v := eng.Evaluate(context.Background(), policy.Subject{
		Scope: policy.ScopeCall, Method: "tools/call", Facts: facts,
	})
	if !v.Allowed() {
		t.Fatalf("default policy refused a call (severity %v); the default must never refuse", v.Severity)
	}
}

// TestShippedPolicyExamplesValidate pins that every example in policy/examples
// loads against this build's signal set, so a harness bump that renames a
// signal fails here rather than at an operator's first start.
func TestShippedPolicyExamplesValidate(t *testing.T) {
	entries, err := os.ReadDir("../../policy/examples")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		n++
		path := "../../policy/examples/" + e.Name()
		if _, err := ValidatePolicy(Config{PolicyConfig: path}); err != nil {
			t.Errorf("%s: %v", e.Name(), err)
		}
	}
	if n == 0 {
		t.Fatal("no policy examples found")
	}
}

// TestShippedPolicyFixturesPass runs the fixture files the way CI does.
func TestShippedPolicyFixturesPass(t *testing.T) {
	entries, err := os.ReadDir("../../policy/examples/tests")
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			paths = append(paths, "../../policy/examples/tests/"+e.Name())
		}
	}
	reports, err := TestPolicy(paths)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range reports {
		for _, c := range r.Cases {
			if !c.OK {
				t.Errorf("%s: %s: %s", r.Fixture, c.Name, c.Detail)
			}
		}
	}
}

func egressTestPolicy() *policy.Policy {
	return &policy.Policy{
		Version: 1,
		Egress: policy.EgressPolicy{
			Enabled: true,
			Listen:  "127.0.0.1:0",
			Allow:   policy.StringSet{"api.example.com"},
		},
	}
}

// TestHarnessModeSkipsLocalProxyOnlyWhenPortAnnounced pins the boundary with
// the pf enforcer plugged in: no announced port means a local listener;
// an announced one means none and delegation — never both, never neither.
func TestHarnessModeSkipsLocalProxyOnlyWhenPortAnnounced(t *testing.T) {
	log := audit.NewAuditLog(nil)
	svc, cleanup, _, err := provisionEgress(
		context.Background(), egressTestPolicy(), log, discardLogger(), runtime.HarnessEgress{})
	if err != nil {
		t.Fatalf("standalone provisioning failed: %v", err)
	}
	if svc == nil {
		t.Fatal("no announced port, but the local proxy was not started")
	}
	cleanup()

	svc, cleanup, suspend, err := provisionEgress(
		context.Background(), egressTestPolicy(), log, discardLogger(), runtime.HarnessEgress{
			Port: 48123, Executable: "/usr/local/bin/agentweave-harness",
		})
	if err != nil {
		t.Fatalf("delegated provisioning failed: %v", err)
	}
	if svc != nil {
		t.Fatal("port announced, but a local proxy was started anyway")
	}
	suspend()
	cleanup()
}

// TestDelegatedEgressStillRefusesUnelevatedEnforcement pins that delegation
// does not weaken the refusal: a policy naming applications still needs root
// for the pf work, whichever process serves the proxy.
func TestDelegatedEgressStillRefusesUnelevatedEnforcement(t *testing.T) {
	if macguard.Elevated() {
		t.Skip("running as root; the refusal under test cannot fire")
	}
	pol := egressTestPolicy()
	pol.Egress.Applications = policy.StringSet{"/Applications/Safari.app"}
	_, _, _, err := provisionEgress(
		context.Background(), pol, audit.NewAuditLog(nil), discardLogger(), runtime.HarnessEgress{Port: 48123})
	if err == nil {
		t.Fatal("unelevated server accepted an enforcement-demanding policy under delegation")
	}
	if !errors.Is(err, egress.ErrNotElevated) {
		t.Errorf("want ErrNotElevated, got %v", err)
	}
}
