//go:build darwin && (amd64 || arm64)

package macdesktop

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/ebitengine/purego/objc"

	"github.com/deploymenttheory/go-bindings-macosplatform/bindings/frameworks/corefoundation"
	"github.com/deploymenttheory/go-bindings-macosplatform/bindings/frameworks/systemconfiguration"
	"github.com/deploymenttheory/go-bindings-macosplatform/bindings/runtime/obj"
	"github.com/deploymenttheory/macos-mcp-server/internal/clirunner"
)

// AdapterInfo describes one network interface.
type AdapterInfo struct {
	Name      string   `json:"name"`
	Device    string   `json:"device"`
	Type      string   `json:"type"`
	MAC       string   `json:"mac,omitempty"`
	Up        bool     `json:"up"`
	Addresses []string `json:"addresses,omitempty"`
}

// Adapters lists the network interfaces SystemConfiguration knows, joined with
// the addresses the kernel reports.
func (d *Desktop) Adapters() ([]AdapterInfo, error) {
	addrs := map[string][]string{}
	up := map[string]bool{}
	if ifs, err := net.Interfaces(); err == nil {
		for _, ifc := range ifs {
			up[ifc.Name] = ifc.Flags&net.FlagUp != 0
			if as, err := ifc.Addrs(); err == nil {
				for _, a := range as {
					addrs[ifc.Name] = append(addrs[ifc.Name], a.String())
				}
			}
		}
	}
	arr := systemconfiguration.SCNetworkInterfaceCopyAll()
	if arr.IsNil() {
		return nil, fmt.Errorf("%w: SCNetworkInterfaceCopyAll", ErrAXFailed)
	}
	defer arr.Release()
	n := corefoundation.CFArrayGetCount(arr)
	out := make([]AdapterInfo, 0, n)
	for i := 0; i < n; i++ {
		p := corefoundation.CFArrayGetValueAtIndex(arr, i)
		if p == nil {
			continue
		}
		ifc := systemconfiguration.SCNetworkInterfaceRef{Object: obj.WrapUnmanaged(objc.ID(uintptr(p)))}
		dev := cfGetString(systemconfiguration.SCNetworkInterfaceGetBSDName(ifc))
		info := AdapterInfo{
			Name:      cfGetString(systemconfiguration.SCNetworkInterfaceGetLocalizedDisplayName(ifc)),
			Device:    dev,
			Type:      cfGetString(systemconfiguration.SCNetworkInterfaceGetInterfaceType(ifc)),
			MAC:       cfGetString(systemconfiguration.SCNetworkInterfaceGetHardwareAddressString(ifc)),
			Up:        up[dev],
			Addresses: addrs[dev],
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Device < out[j].Device })
	return out, nil
}

// cfGetString reads a +0 CFString (a Get function's result) without
// releasing it.
func cfGetString(s corefoundation.CFStringRef) string {
	if s.IsNil() {
		return ""
	}
	return cfToString(s)
}

// DNSConfig is the resolver configuration, from scutil.
func (d *Desktop) DNSConfig(ctx context.Context) (string, error) {
	res, err := clirunner.Run(ctx, []string{"scutil", "--dns"}, clirunner.Options{Timeout: 10 * time.Second})
	if err != nil {
		return "", fmt.Errorf("scutil --dns: %w", err)
	}
	return summariseScutilDNS(res.Stdout), nil
}

// summariseScutilDNS keeps the resolver blocks' nameservers and search
// domains, which is what a diagnosis needs out of scutil's verbose dump.
func summariseScutilDNS(out string) string {
	var b strings.Builder
	for _, line := range strings.Split(out, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "resolver #"), strings.HasPrefix(t, "nameserver["),
			strings.HasPrefix(t, "search domain["), strings.HasPrefix(t, "domain "),
			strings.HasPrefix(t, "if_index"), strings.HasPrefix(t, "flags"):
			b.WriteString(t)
			b.WriteByte('\n')
		}
	}
	return strings.TrimSpace(b.String())
}

// ProxyConfig is the system proxy configuration, from scutil.
func (d *Desktop) ProxyConfig(ctx context.Context) (string, error) {
	res, err := clirunner.Run(ctx, []string{"scutil", "--proxy"}, clirunner.Options{Timeout: 10 * time.Second})
	if err != nil {
		return "", fmt.Errorf("scutil --proxy: %w", err)
	}
	return strings.TrimSpace(res.Stdout), nil
}

// RouteConfig is the default route and gateway, from route(8).
func (d *Desktop) RouteConfig(ctx context.Context) (string, error) {
	res, err := clirunner.Run(
		ctx,
		[]string{"route", "-n", "get", "default"},
		clirunner.Options{Timeout: 10 * time.Second},
	)
	if err != nil {
		return "", fmt.Errorf("route get default: %w", err)
	}
	return strings.TrimSpace(res.Stdout), nil
}

// WiFiStatus reports the Wi-Fi interface and its network, from networksetup.
// CoreWLAN's SSID needs the Location grant on current macOS; networksetup
// does not.
func (d *Desktop) WiFiStatus(ctx context.Context) (string, error) {
	ports, err := clirunner.Run(
		ctx,
		[]string{"networksetup", "-listallhardwareports"},
		clirunner.Options{Timeout: 10 * time.Second},
	)
	if err != nil {
		return "", fmt.Errorf("networksetup: %w", err)
	}
	dev := wifiDevice(ports.Stdout)
	if dev == "" {
		return "no Wi-Fi hardware port", nil
	}
	res, err := clirunner.Run(
		ctx,
		[]string{"networksetup", "-getairportnetwork", dev},
		clirunner.Options{Timeout: 10 * time.Second},
	)
	if err != nil {
		return "", fmt.Errorf("networksetup -getairportnetwork: %w", err)
	}
	power, _ := clirunner.Run(
		ctx,
		[]string{"networksetup", "-getairportpower", dev},
		clirunner.Options{Timeout: 10 * time.Second},
	)
	return strings.TrimSpace(dev + ": " + res.Output() + "\n" + power.Output()), nil
}

// wifiDevice finds the Wi-Fi device name in `networksetup -listallhardwareports`.
func wifiDevice(out string) string {
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "Hardware Port: Wi-Fi") && i+1 < len(lines) {
			if _, v, ok := strings.Cut(lines[i+1], "Device: "); ok {
				return strings.TrimSpace(v)
			}
		}
	}
	return ""
}

// TestConnection pings a host (ICMP via ping(8)) or, with a port, opens a TCP
// connection. It reaches the network directly, outside the egress proxy.
func (d *Desktop) TestConnection(ctx context.Context, host string, port int) (string, error) {
	if port > 0 {
		addr := net.JoinHostPort(host, fmt.Sprint(port))
		start := time.Now()
		conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", addr)
		if err != nil {
			return fmt.Sprintf("TCP %s: failed (%v)", addr, err), nil
		}
		_ = conn.Close()
		return fmt.Sprintf("TCP %s: open (%s)", addr, time.Since(start).Round(time.Millisecond)), nil
	}
	res, err := clirunner.Run(
		ctx,
		[]string{"ping", "-c", "3", "-W", "2000", host},
		clirunner.Options{Timeout: 15 * time.Second},
	)
	if err != nil {
		return fmt.Sprintf("ping %s: failed\n%s", host, strings.TrimSpace(res.Stdout+res.Stderr)), nil
	}
	return strings.TrimSpace(res.Stdout), nil
}
