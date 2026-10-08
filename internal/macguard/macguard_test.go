//go:build darwin && (amd64 || arm64)

package macguard

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/deploymenttheory/agentweave-harness/guardrails/egress"
)

// fakeRunner records every argv and answers from a table keyed on the first
// two words, so a test can drive the whole pfctl/networksetup sequence
// without touching the machine.
type fakeRunner struct {
	calls   [][]string
	answers map[string]string
	fail    map[string]error
}

func (f *fakeRunner) run(_ context.Context, argv []string) (string, string, error) {
	f.calls = append(f.calls, append([]string(nil), argv...))
	key := strings.Join(argv[:min(2, len(argv))], " ")
	if err, ok := f.fail[key]; ok {
		return "", "", err
	}
	if argv[0] == "pfctl" && len(argv) > 1 && argv[1] == "-E" {
		return "", "pf enabled\nToken : 4242\n", nil
	}
	if out, ok := f.answers[key]; ok {
		return out, "", nil
	}
	if len(argv) > 2 {
		if out, ok := f.answers[strings.Join(argv[:2], " ")+" "+argv[2]]; ok {
			return out, "", nil
		}
	}
	return "", "", nil
}

func (f *fakeRunner) joined() string {
	var b strings.Builder
	for _, c := range f.calls {
		b.WriteString(strings.Join(c, " "))
		b.WriteString("\n")
	}
	return b.String()
}

func elevated() bool { return true }

func TestRenderRulesScopedTierIsUIDScoped(t *testing.T) {
	rules := RenderRules(RuleSpec{ProxyPort: 8181, ScopedUID: 501})
	if !strings.Contains(rules, "block drop out quick user 501") {
		t.Errorf("scoped tier must block the console uid:\n%s", rules)
	}
	if !strings.Contains(rules, "pass out quick on lo0 all") {
		t.Errorf("scoped tier must keep loopback so the proxy is reachable:\n%s", rules)
	}
	if strings.Contains(rules, "block drop out all") {
		t.Errorf("scoped tier must not block other users:\n%s", rules)
	}
}

// TestGlobalAllowRulesCoverTheMachineEssentials pins the exception set: a
// default-deny machine still needs DHCP, DNS, time and loopback, and nothing
// else. Each is scoped to the daemon's own uid so none is a route for the
// workload.
func TestGlobalAllowRulesCoverTheMachineEssentials(t *testing.T) {
	rules := RenderRules(RuleSpec{ProxyPort: 8181, ScopedUID: -1, GlobalBlock: true, AllowPorts: []int{443}})
	lines := strings.Split(rules, "\n")
	first := ""
	for _, l := range lines {
		if l != "" && !strings.HasPrefix(l, "#") {
			first = l
			break
		}
	}
	if first != "block drop out all" {
		t.Errorf("the block must come first so the quick passes win for what they match; got %q", first)
	}
	for _, want := range []string{
		"pass out quick on lo0 all",
		"port 68 to any port 67",
		"port 53 user _mdnsresponder",
		"port 123 user _timed",
		"pass out quick proto tcp to any port { 443 } user root",
	} {
		if !strings.Contains(rules, want) {
			t.Errorf("global rules missing %q:\n%s", want, rules)
		}
	}
	if RuleCount(rules) != 6 {
		t.Errorf("rule count = %d, want 6 (block + 4 essentials + proxy):\n%s", RuleCount(rules), rules)
	}
	// Without allow ports the proxy owner's rule is unbounded, as on Windows.
	open := RenderRules(RuleSpec{ScopedUID: -1, GlobalBlock: true})
	if !strings.Contains(open, "pass out quick proto tcp to any user root") {
		t.Errorf("no allow ports should mean no port clause:\n%s", open)
	}
}

func TestSuspendedRulesKeepOnlyTheBlock(t *testing.T) {
	s := RenderSuspended(RuleSpec{ScopedUID: -1, GlobalBlock: true})
	if RuleCount(s) != 1 || !strings.Contains(s, "block drop out all") {
		t.Errorf("suspended global set must be the bare block:\n%s", s)
	}
	if strings.Contains(s, "pass") {
		t.Errorf("suspended set must carry no allow rule:\n%s", s)
	}
}

func TestEnforcementStateRoundTrips(t *testing.T) {
	dir := t.TempDir()
	in := EnforcementState{PID: 42, Listen: "127.0.0.1:8181", Anchors: []string{EgressAnchor},
		RuleCount: 3, PFToken: "99", GlobalBlock: true,
		SystemProxy: []SavedProxy{{Service: "Wi-Fi", Web: ProxyState{Enabled: true, Server: "proxy.corp", Port: 3128}}}}
	if err := writeState(dir, in); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(statePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("state file mode = %04o, want 0600", info.Mode().Perm())
	}
	out, found, err := readState(dir)
	if err != nil || !found {
		t.Fatalf("readState: found=%v err=%v", found, err)
	}
	if out.PID != 42 || out.PFToken != "99" || len(out.SystemProxy) != 1 || out.SystemProxy[0].Web.Port != 3128 {
		t.Errorf("state did not round-trip: %+v", out)
	}
	if err := clearState(dir); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := readState(dir); found {
		t.Error("state should be gone after clearState")
	}
}

func TestCorruptStateIsReportedNotFatal(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(statePath(dir), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	e := &Enforcer{StateDir: dir, Elevation: elevated, Run: (&fakeRunner{}).run}
	if _, err := e.Recover(); err == nil {
		t.Error("a corrupt state file must be reported")
	}
}

func TestRecoverWithNoStateDoesNothing(t *testing.T) {
	fr := &fakeRunner{}
	e := &Enforcer{StateDir: t.TempDir(), Elevation: elevated, Run: fr.run}
	n, err := e.Recover()
	if err != nil || n != 0 {
		t.Errorf("Recover() = %d, %v; want 0, nil", n, err)
	}
	if len(fr.calls) != 0 {
		t.Errorf("no state must mean no pfctl calls, got %v", fr.calls)
	}
}

// TestApplyWritesStateBeforeAnyChange pins the ordering the recovery depends
// on: the state file exists before the first pfctl call, names the anchor,
// and the undo flushes the anchor, releases the token and clears the file.
func TestApplyWritesStateBeforeAnyChange(t *testing.T) {
	dir := t.TempDir()
	fr := &fakeRunner{}
	var stateSeenAtFirstCall bool
	fr2 := &fakeRunner{}
	run := func(ctx context.Context, argv []string) (string, string, error) {
		if len(fr2.calls) == 0 {
			_, found, _ := readState(dir)
			stateSeenAtFirstCall = found
		}
		return fr2.run(ctx, argv)
	}
	_ = fr
	e := &Enforcer{StateDir: dir, Elevation: elevated, Run: run, ConsoleUID: func() (int, error) { return 501, nil }}
	undo, err := e.Apply(egress.EnforceSpec{ProxyAddr: "127.0.0.1:8181", Applications: []string{"/Applications/Safari.app"}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !stateSeenAtFirstCall {
		t.Error("the state file must exist before the first pfctl call")
	}
	st, found, _ := readState(dir)
	if !found || st.PFToken != "4242" || st.ScopedUID != 501 || st.Anchors[0] != EgressAnchor {
		t.Errorf("state after Apply = %+v", st)
	}
	calls := fr2.joined()
	if !strings.Contains(calls, "pfctl -q -a "+EgressAnchor+" -f ") || !strings.Contains(calls, "pfctl -E") {
		t.Errorf("Apply must load the anchor then enable pf:\n%s", calls)
	}
	rules, _ := os.ReadFile(filepath.Join(dir, "pf-anchor.conf"))
	if !strings.Contains(string(rules), "user 501") {
		t.Errorf("rendered rules not written for pfctl -f:\n%s", rules)
	}

	if err := e.Suspend(); err != nil {
		t.Fatal(err)
	}
	rules, _ = os.ReadFile(filepath.Join(dir, "pf-anchor.conf"))
	if strings.Contains(string(rules), "pass") {
		t.Errorf("Suspend must drop the allow rules:\n%s", rules)
	}

	if err := undo(); err != nil {
		t.Fatalf("undo: %v", err)
	}
	calls = fr2.joined()
	if !strings.Contains(calls, "-F all") || !strings.Contains(calls, "pfctl -X 4242") {
		t.Errorf("undo must flush the anchor and release the token:\n%s", calls)
	}
	if _, found, _ := readState(dir); found {
		t.Error("undo must clear the state file")
	}
}

func TestApplyRefusesWithoutElevation(t *testing.T) {
	e := &Enforcer{StateDir: t.TempDir(), Elevation: func() bool { return false }, Run: (&fakeRunner{}).run}
	if _, err := e.Apply(egress.EnforceSpec{GlobalBlock: true}); !errors.Is(err, egress.ErrNotElevated) {
		t.Errorf("want ErrNotElevated, got %v", err)
	}
	if _, err := e.Apply(egress.EnforceSpec{SetSystemProxy: true, ProxyAddr: "127.0.0.1:1"}); !errors.Is(err, egress.ErrNotElevated) {
		t.Errorf("networksetup needs an administrator too; want ErrNotElevated, got %v", err)
	}
	undo, err := e.Apply(egress.EnforceSpec{ProxyAddr: "127.0.0.1:1"})
	if err != nil || undo == nil {
		t.Errorf("proxy-only with nothing to enforce must be a no-op, got %v", err)
	}
}

func TestRecoverUndoesWhatTheFileNames(t *testing.T) {
	dir := t.TempDir()
	st := EnforcementState{PID: 1, Anchors: []string{EgressAnchor, IsolateAnchor}, RuleCount: 5, PFToken: "7",
		SystemProxy: []SavedProxy{{Service: "Wi-Fi", Web: ProxyState{Enabled: false}, Secure: ProxyState{Enabled: false}}}}
	if err := writeState(dir, st); err != nil {
		t.Fatal(err)
	}
	fr := &fakeRunner{}
	e := &Enforcer{StateDir: dir, Elevation: elevated, Run: fr.run}
	n, err := e.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Errorf("recovered = %d, want 5", n)
	}
	calls := fr.joined()
	for _, want := range []string{
		"pfctl -q -a " + EgressAnchor + " -F all",
		"pfctl -q -a " + IsolateAnchor + " -F all",
		"pfctl -X 7",
		"networksetup -setwebproxystate Wi-Fi off",
		"networksetup -setsecurewebproxystate Wi-Fi off",
	} {
		if !strings.Contains(calls, want) {
			t.Errorf("Recover missing %q:\n%s", want, calls)
		}
	}
	if _, found, _ := readState(dir); found {
		t.Error("Recover must clear the state file")
	}
}

func TestSystemProxyParsing(t *testing.T) {
	services := listNetworkServices("An asterisk (*) denotes that a network service is disabled.\nWi-Fi\n*Bluetooth PAN\nThunderbolt Bridge\n")
	if len(services) != 2 || services[0] != "Wi-Fi" || services[1] != "Thunderbolt Bridge" {
		t.Errorf("services = %v", services)
	}
	st := parseProxyState("Enabled: Yes\nServer: proxy.corp\nPort: 3128\nAuthenticated Proxy Enabled: 0\n")
	if !st.Enabled || st.Server != "proxy.corp" || st.Port != 3128 {
		t.Errorf("state = %+v", st)
	}
	if parseProxyState("Enabled: No\nServer: \nPort: 0\n").Enabled {
		t.Error("No must parse as disabled")
	}
}

func TestSystemProxyRoundTrips(t *testing.T) {
	fr := &fakeRunner{answers: map[string]string{
		"networksetup -listallnetworkservices": "An asterisk (*) denotes that a network service is disabled.\nWi-Fi\n",
		"networksetup -getwebproxy":            "Enabled: Yes\nServer: proxy.corp\nPort: 3128\n",
		"networksetup -getsecurewebproxy":      "Enabled: No\nServer: \nPort: 0\n",
	}}
	saved, err := setSystemProxy(context.Background(), fr.run, "127.0.0.1:8181")
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) != 1 || !saved[0].Web.Enabled || saved[0].Web.Server != "proxy.corp" {
		t.Errorf("saved = %+v", saved)
	}
	calls := fr.joined()
	if !strings.Contains(calls, "networksetup -setwebproxy Wi-Fi 127.0.0.1 8181") ||
		!strings.Contains(calls, "networksetup -setsecurewebproxy Wi-Fi 127.0.0.1 8181") {
		t.Errorf("set calls missing:\n%s", calls)
	}
	fr.calls = nil
	if err := restoreSystemProxy(context.Background(), fr.run, saved); err != nil {
		t.Fatal(err)
	}
	calls = fr.joined()
	if !strings.Contains(calls, "networksetup -setwebproxy Wi-Fi proxy.corp 3128") {
		t.Errorf("the corporate proxy must be put back, not switched off:\n%s", calls)
	}
	if !strings.Contains(calls, "networksetup -setsecurewebproxystate Wi-Fi off") {
		t.Errorf("the previously-off HTTPS proxy must be switched off again:\n%s", calls)
	}
	if err := restoreSystemProxy(context.Background(), fr.run, nil); err != nil {
		t.Errorf("restore of nothing must be a no-op, got %v", err)
	}
}

func TestIsolateNetworkLoadsItsOwnAnchorAndRestores(t *testing.T) {
	dir := t.TempDir()
	fr := &fakeRunner{answers: map[string]string{
		"pfctl -q": "block drop out all\nblock drop in all\npass quick on lo0 all\n",
	}}
	a := &Actuator{StateDir: dir, Elevation: elevated, Run: fr.run}
	restore, observed, err := a.IsolateNetwork()
	if err != nil {
		t.Fatalf("IsolateNetwork: %v", err)
	}
	if len(observed) != 3 {
		t.Errorf("observed = %v, want the 3 rules pfctl read back", observed)
	}
	st, found, _ := readState(dir)
	if !found || st.PFToken != "4242" || len(st.Anchors) != 1 || st.Anchors[0] != IsolateAnchor {
		t.Errorf("state = %+v", st)
	}
	if err := restore(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fr.joined(), "pfctl -q -a "+IsolateAnchor+" -F all") || !strings.Contains(fr.joined(), "pfctl -X 4242") {
		t.Errorf("restore must flush and release:\n%s", fr.joined())
	}
	if _, found, _ := readState(dir); found {
		t.Error("restore of the only anchor must clear the state")
	}
	if _, _, err := (&Actuator{Elevation: func() bool { return false }}).IsolateNetwork(); !errors.Is(err, ErrNotElevated) {
		t.Errorf("unelevated isolation must refuse, got %v", err)
	}
}

func TestKillProcessesEscalatesTermToKill(t *testing.T) {
	fr := &fakeRunner{answers: map[string]string{"pgrep -x": "1234\n"}}
	var signals []syscall.Signal
	alive := true
	a := &Actuator{Run: fr.run, GracePeriod: 50 * 1e6, Kill: func(pid int, sig syscall.Signal) error {
		if pid != 1234 {
			t.Errorf("unexpected pid %d", pid)
		}
		if sig == 0 {
			if alive {
				return nil
			}
			return syscall.ESRCH
		}
		signals = append(signals, sig)
		if sig == syscall.SIGKILL {
			alive = false
		}
		return nil
	}}
	if errs := a.KillProcesses([]string{"evil"}); len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(signals) != 2 || signals[0] != syscall.SIGTERM || signals[1] != syscall.SIGKILL {
		t.Errorf("signals = %v, want TERM then KILL", signals)
	}
	if !strings.Contains(fr.joined(), "pgrep -x evil") {
		t.Errorf("name must be matched exactly:\n%s", fr.joined())
	}
}
