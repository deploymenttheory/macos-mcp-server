//go:build darwin && (amd64 || arm64)

package macmcp

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

// Credentials-file permission errors.
var (
	// ErrCredentialsFileBroadlyReadable reports a credentials file whose mode
	// grants read access to the group or to others.
	ErrCredentialsFileBroadlyReadable = errors.New("credentials file is readable by other users")
	// ErrCredentialsFileNotOwned reports a credentials file owned by a different
	// user than the one running the server.
	ErrCredentialsFileNotOwned = errors.New("credentials file is not owned by the current user")
)

// checkCredentialsFilePerms refuses a credentials file that anyone but the
// calling user can read. Unlike Windows, the Unix mode bits here are real: the
// document must be 0600 (or stricter) and owned by the effective user, which
// is also what an operator creating it with umask 077 gets by default.
func checkCredentialsFilePerms(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat credentials file: %w", err)
	}
	if perm := info.Mode().Perm(); perm&(fs.ModePerm^0o700) != 0 {
		return fmt.Errorf("%w: %q is mode %04o; chmod 600 it", ErrCredentialsFileBroadlyReadable, path, perm)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil // not a Unix stat; nothing more to check
	}
	if euid := os.Geteuid(); int(st.Uid) != euid {
		return fmt.Errorf("%w: %q is owned by uid %d, the server runs as uid %d",
			ErrCredentialsFileNotOwned, path, st.Uid, euid)
	}
	return nil
}
