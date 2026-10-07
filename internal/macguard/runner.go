//go:build darwin && (amd64 || arm64)

// Package macguard holds the macOS actuators behind the guardrail stack: the
// pf-based egress enforcer, the system proxy setter, and the containment
// actuator (network isolation, process kill, lock, shutdown). Everything here
// shells out argv-only through an injectable runner so the rule rendering and
// the state handling are testable without touching pf.
package macguard

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/deploymenttheory/macos-mcp-server/internal/clirunner"
)

// Runner executes argv and returns its stdout and stderr. The default runs
// through clirunner; tests substitute a recorder.
type Runner func(ctx context.Context, argv []string) (stdout, stderr string, err error)

// commandTimeout bounds every pfctl/networksetup call. These finish in
// milliseconds; a hang means the tool is prompting, which argv-only
// invocation never answers.
const commandTimeout = 20 * time.Second

// defaultRunner shells out through clirunner, which rebuilds PATH and withholds
// the server's own secrets from the child.
func defaultRunner(ctx context.Context, argv []string) (string, string, error) {
	res, err := clirunner.Run(ctx, argv, clirunner.Options{Timeout: commandTimeout})
	if err != nil {
		return res.Stdout, res.Stderr, fmt.Errorf("%s: %w", strings.Join(argv, " "), err)
	}
	return res.Stdout, res.Stderr, nil
}

// Elevated reports whether the process runs as root. pf and networksetup both
// require it; there is no partial grant.
func Elevated() bool { return os.Geteuid() == 0 }

// ConsoleUID is the uid owning the console session: the user whose
// applications the scoped egress tier blocks. It is read from /dev/console,
// which the login window chowns to the console user at login.
func ConsoleUID() (int, error) {
	info, err := os.Stat("/dev/console")
	if err != nil {
		return 0, fmt.Errorf("console user: %w", err)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, ErrNoConsoleUser
	}
	return int(st.Uid), nil
}

// ErrNoConsoleUser reports that the console owner could not be determined.
var ErrNoConsoleUser = errors.New("could not determine the console user")

// DefaultStateDir is where machine-wide guardrail state lives. The rules are
// machine-wide, so what records them has to be too.
const DefaultStateDir = "/Library/Application Support/MacOSMCP"

// stateDirOr returns dir, or the default when empty.
func stateDirOr(dir string) string {
	if dir != "" {
		return dir
	}
	return DefaultStateDir
}

// ensureDir creates the state directory root-only.
func ensureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create state dir %s: %w", dir, err)
	}
	return nil
}

// writeFileAtomic writes content to path via a temp file and rename, so a
// crash mid-write never leaves a half-written state document.
func writeFileAtomic(path string, content []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, content, perm); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename %s: %w", filepath.Base(tmp), err)
	}
	return nil
}
