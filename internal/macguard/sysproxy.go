//go:build darwin && (amd64 || arm64)

package macguard

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// SavedProxy is one network service's HTTP and HTTPS proxy settings as found
// before the policy pointed them at the egress proxy, so they can be put back
// exactly — an operator whose service already had a corporate proxy does not
// get that quietly undone.
type SavedProxy struct {
	Service string     `json:"service"`
	Web     ProxyState `json:"web"`
	Secure  ProxyState `json:"secure"`
}

// ProxyState is what networksetup -getwebproxy reports.
type ProxyState struct {
	Enabled bool   `json:"enabled"`
	Server  string `json:"server,omitempty"`
	Port    int    `json:"port,omitempty"`
}

// ErrNoNetworkServices reports that networksetup listed nothing to configure.
var ErrNoNetworkServices = errors.New("no enabled network services to configure")

// listNetworkServices parses `networksetup -listallnetworkservices`, skipping
// the header line and services marked disabled with a leading asterisk.
func listNetworkServices(out string) []string {
	var services []string
	for i, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || (i == 0 && strings.HasPrefix(line, "An asterisk")) {
			continue
		}
		if strings.HasPrefix(line, "*") {
			continue
		}
		services = append(services, line)
	}
	return services
}

// parseProxyState parses the Enabled/Server/Port block networksetup prints.
func parseProxyState(out string) ProxyState {
	var st ProxyState
	for _, line := range strings.Split(out, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(key) {
		case "Enabled":
			st.Enabled = strings.EqualFold(value, "yes")
		case "Server":
			st.Server = value
		case "Port":
			st.Port, _ = strconv.Atoi(value)
		}
	}
	return st
}

// setSystemProxy points every enabled network service's HTTP and HTTPS proxy
// at addr and returns what it replaced. It stops at the first failure and
// returns what it changed so far, so the caller can restore that much.
func setSystemProxy(ctx context.Context, run Runner, addr string) ([]SavedProxy, error) {
	host, portStr, err := splitHostPort(addr)
	if err != nil {
		return nil, err
	}
	out, _, err := run(ctx, []string{"networksetup", "-listallnetworkservices"})
	if err != nil {
		return nil, err
	}
	services := listNetworkServices(out)
	if len(services) == 0 {
		return nil, ErrNoNetworkServices
	}
	var saved []SavedProxy
	for _, svc := range services {
		web, _, err := run(ctx, []string{"networksetup", "-getwebproxy", svc})
		if err != nil {
			return saved, err
		}
		secure, _, err := run(ctx, []string{"networksetup", "-getsecurewebproxy", svc})
		if err != nil {
			return saved, err
		}
		saved = append(saved, SavedProxy{Service: svc, Web: parseProxyState(web), Secure: parseProxyState(secure)})
		if _, _, err := run(ctx, []string{"networksetup", "-setwebproxy", svc, host, portStr}); err != nil {
			return saved, err
		}
		if _, _, err := run(ctx, []string{"networksetup", "-setsecurewebproxy", svc, host, portStr}); err != nil {
			return saved, err
		}
	}
	return saved, nil
}

// restoreSystemProxy puts each service back the way setSystemProxy found it.
// Best-effort across services: one failure does not stop the rest.
func restoreSystemProxy(ctx context.Context, run Runner, saved []SavedProxy) error {
	errs := make([]error, 0, 2*len(saved))
	for _, s := range saved {
		errs = append(errs,
			restoreOne(ctx, run, s.Service, "-setwebproxy", "-setwebproxystate", s.Web),
			restoreOne(ctx, run, s.Service, "-setsecurewebproxy", "-setsecurewebproxystate", s.Secure),
		)
	}
	return errors.Join(errs...)
}

func restoreOne(ctx context.Context, run Runner, svc, setVerb, stateVerb string, st ProxyState) error {
	if st.Enabled && st.Server != "" {
		_, _, err := run(ctx, []string{"networksetup", setVerb, svc, st.Server, strconv.Itoa(st.Port)})
		return err
	}
	if st.Server != "" {
		// Keep the remembered server, just switch the proxy off as it was.
		if _, _, err := run(ctx, []string{"networksetup", setVerb, svc, st.Server, strconv.Itoa(st.Port)}); err != nil {
			return err
		}
	}
	_, _, err := run(ctx, []string{"networksetup", stateVerb, svc, "off"})
	return err
}

func splitHostPort(addr string) (string, string, error) {
	host, port, ok := strings.Cut(addr, ":")
	if !ok || host == "" || port == "" {
		return "", "", fmt.Errorf("%w: %q", ErrBadProxyAddr, addr)
	}
	if _, err := strconv.Atoi(port); err != nil {
		return "", "", fmt.Errorf("%w: %q", ErrBadProxyAddr, addr)
	}
	return host, port, nil
}

// ErrBadProxyAddr reports a proxy address that is not host:port.
var ErrBadProxyAddr = errors.New("proxy address must be host:port")
