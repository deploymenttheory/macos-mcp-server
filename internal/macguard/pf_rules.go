//go:build darwin && (amd64 || arm64)

package macguard

import (
	"fmt"
	"strconv"
	"strings"
)

// Anchor names. They sit under Apple's "com.apple/*" anchor point, which
// /etc/pf.conf evaluates by default, so the system ruleset is never edited.
// The numeric prefix orders them after Apple's own sub-anchors.
const (
	// EgressAnchor holds the egress enforcement rules.
	EgressAnchor = "com.apple/900.deploymenttheory.macosmcp"
	// IsolateAnchor holds the kill-ladder network isolation rules.
	IsolateAnchor = "com.apple/901.deploymenttheory.macosmcp.isolate"
)

// RuleSpec is what the rule renderer is asked to express. It is the
// platform-neutral egress.EnforceSpec reduced to what pf can match on: pf
// filters by socket owner, not by executable, so "applications" becomes the
// console user's uid.
type RuleSpec struct {
	// ProxyPort is the loopback port the proxy listens on. Loopback is passed
	// wholesale, so it only informs the comment.
	ProxyPort int
	// ScopedUID, when non-negative, blocks that user's outbound traffic except
	// loopback. -1 means no scoped tier.
	ScopedUID int
	// GlobalBlock drops all outbound traffic except the machine essentials.
	GlobalBlock bool
	// AllowPorts bounds the proxy owner's own allow rule under GlobalBlock.
	AllowPorts []int
	// ProxyUser is the uid or name whose sockets carry the proxy's outbound
	// traffic under GlobalBlock. The server's own proxy runs as this process,
	// so it is "root" whenever enforcement is possible at all.
	ProxyUser string
}

// essential is one allow rule that keeps a default-deny machine usable.
type essential struct {
	rule string
	why  string
}

// globalEssentials is the machine-usability set applied with global block.
// Every entry is an OS daemon with its own uid, scoped to the ports it needs;
// none is a route an agent's workload can use. Pinned by
// TestGlobalAllowRulesCoverTheMachineEssentials.
var globalEssentials = []essential{
	{"pass out quick on lo0 all", "loopback: the proxy and every local IPC"},
	{
		"pass out quick proto udp from any port 68 to any port 67",
		"DHCP: without it the lease lapses and there is no network at all",
	},
	{
		"pass out quick proto { tcp udp } to any port 53 user _mdnsresponder",
		"DNS via mDNSResponder; without it the machine resolves nothing",
	},
	{"pass out quick proto udp to any port 123 user _timed", "NTP via timed; certificates and tokens rot without time"},
}

// RenderRules produces the anchor ruleset. pf uses last-match semantics, so
// the block comes first and the quick passes after it win for what they match.
func RenderRules(spec RuleSpec) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# macos-mcp-server egress enforcement (anchor %s)\n", EgressAnchor)
	fmt.Fprintf(&b, "# proxy 127.0.0.1:%d\n", spec.ProxyPort)
	switch {
	case spec.GlobalBlock:
		b.WriteString("block drop out all\n")
		for _, e := range globalEssentials {
			fmt.Fprintf(&b, "%s # %s\n", e.rule, e.why)
		}
		b.WriteString(proxyAllowRule(spec))
	case spec.ScopedUID >= 0:
		b.WriteString("pass out quick on lo0 all # loopback to the proxy\n")
		fmt.Fprintf(&b, "block drop out quick user %d # console user: everything but the proxy\n", spec.ScopedUID)
	}
	return b.String()
}

// RenderSuspended produces the ruleset for the kill path: the blocks stay and
// the allow rules go, so nothing keeps a route out during containment.
func RenderSuspended(spec RuleSpec) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# macos-mcp-server egress enforcement, suspended (anchor %s)\n", EgressAnchor)
	switch {
	case spec.GlobalBlock:
		b.WriteString("block drop out all\n")
	case spec.ScopedUID >= 0:
		fmt.Fprintf(&b, "block drop out quick user %d\n", spec.ScopedUID)
	}
	return b.String()
}

// RenderIsolation is the kill ladder's network isolation: nothing in, nothing
// out, loopback kept so local IPC (and the audit flush) survive.
func RenderIsolation() string {
	return "# macos-mcp-server network isolation (anchor " + IsolateAnchor + ")\n" +
		"block drop out all\n" +
		"block drop in all\n" +
		"pass quick on lo0 all\n"
}

// proxyAllowRule grants the proxy owner's sockets a route out, bounded to the
// allowlist ports when the policy names any.
func proxyAllowRule(spec RuleSpec) string {
	user := spec.ProxyUser
	if user == "" {
		user = "root"
	}
	rule := "pass out quick proto tcp to any"
	if len(spec.AllowPorts) > 0 {
		ports := make([]string, 0, len(spec.AllowPorts))
		for _, p := range spec.AllowPorts {
			ports = append(ports, strconv.Itoa(p))
		}
		rule += " port { " + strings.Join(ports, " ") + " }"
	}
	return rule + " user " + user + " # the egress proxy's own route out\n"
}

// RuleCount counts the rules in a rendered set, for the recovery record.
func RuleCount(rules string) int {
	n := 0
	for _, line := range strings.Split(rules, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		n++
	}
	return n
}
