//go:build darwin && (amd64 || arm64)

package macmcp

import (
	"context"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/deploymenttheory/agentweave-harness/wire"
	"github.com/deploymenttheory/mcp-server-core/runtime"
)

// dialHarness connects to the harness control channel. The bootstrap contract
// names a unix socket path on macOS (a named pipe on Windows).
func dialHarness(addr string) (io.ReadWriteCloser, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", addr)
	if err != nil {
		return nil, fmt.Errorf("harness: dial %s: %w", addr, err)
	}
	return conn, nil
}

// attachHarness dials the control channel and completes the handshake.
func attachHarness(
	pipe, token, serverVersion, sessionStamp string,
	deps runtime.ServantDeps,
) (*runtime.HarnessServant, wire.HelloAck, error) {
	s, ack, err := runtime.AttachHarness(dialHarness, pipe, token, serverVersion, sessionStamp, deps)
	if err != nil {
		return nil, wire.HelloAck{}, fmt.Errorf("attach: %w", err)
	}
	return s, ack, nil
}

// installedCredentialNames lists what the servant announces to the harness:
// names only, never a target or a secret.
func installedCredentialNames(creds []installedCredential) []string {
	names := make([]string, 0, len(creds))
	for _, c := range creds {
		names = append(names, c.Name)
	}
	return names
}
