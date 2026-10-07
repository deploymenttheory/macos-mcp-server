//go:build darwin && (amd64 || arm64)

package macdesktop

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ebitengine/purego/objc"

	"github.com/deploymenttheory/go-bindings-macosplatform/bindings/frameworks/appkit"
	"github.com/deploymenttheory/go-bindings-macosplatform/bindings/frameworks/foundation"
	"github.com/deploymenttheory/go-bindings-macosplatform/bindings/runtime/obj"
	"github.com/deploymenttheory/macos-mcp-server/internal/clirunner"
)

// Errors the application operations return.
var (
	ErrAppNotFound     = errors.New("application not found")
	ErrNotAnAppBundle  = errors.New("not an application bundle; use LaunchExecutable for a program by path")
	ErrAppNotRunning   = errors.New("application is not running")
	ErrLaunchTimedOut  = errors.New("application did not finish launching in time")
	ErrQuitRefused     = errors.New("the application refused to quit")
	launchTimeout      = 20 * time.Second
	appSearchLocations = []string{
		"/Applications",
		"/System/Applications",
		"/System/Applications/Utilities",
		"/Applications/Utilities",
	}
)

// idOf returns the Objective-C/CF identity of a handle.
func idOf(o obj.Object) objc.ID { return obj.ID(o) }

// ResolveApp turns an application name, bundle identifier or .app path into
// the bundle path. A URL is reported as such so the caller can gate it.
func ResolveApp(name string) (path string, isURL bool, err error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", false, fmt.Errorf("%w: empty name", ErrAppNotFound)
	}
	if strings.Contains(name, "://") {
		return name, true, nil
	}
	ws := appkit.SharedWorkspace()
	if strings.HasSuffix(name, ".app") && filepath.IsAbs(name) {
		if _, err := os.Stat(name); err != nil {
			return "", false, fmt.Errorf("%w: %s", ErrAppNotFound, name)
		}
		return name, false, nil
	}
	if filepath.IsAbs(name) {
		// A path that is not an .app bundle is a program; that is LaunchExecutable's
		// job, under the shell toolset, and it is refused here so the apps toolset
		// cannot become a way to run arbitrary binaries.
		return "", false, fmt.Errorf("%w: %s", ErrNotAnAppBundle, name)
	}
	if strings.Contains(name, ".") && !strings.Contains(name, " ") {
		if u := ws.URLForApplicationWithBundleIdentifier(name); u != "" {
			return fileURLToPath(u), false, nil
		}
	}
	base := strings.TrimSuffix(name, ".app")
	for _, dir := range appSearchLocations {
		candidate := filepath.Join(dir, base+".app")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, false, nil
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidate := filepath.Join(home, "Applications", base+".app")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, false, nil
		}
	}
	if p := ws.FullPathForApplication(name); p != "" {
		return p, false, nil
	}
	return "", false, fmt.Errorf("%w: %q (try the bundle identifier or the full .app path)", ErrAppNotFound, name)
}

// fileURLToPath strips a file:// URL to its path.
func fileURLToPath(u string) string {
	p := strings.TrimPrefix(u, "file://")
	p = strings.TrimSuffix(p, "/")
	if strings.Contains(p, "%") {
		if dec, err := url.PathUnescape(p); err == nil {
			p = dec
		}
	}
	return p
}

// LaunchApp starts an application by name, bundle identifier or .app path and
// activates it. A URL-shaped name opens in the default handler. It returns the
// resolved target.
func (d *Desktop) LaunchApp(ctx context.Context, name string) (string, error) {
	target, isURL, err := ResolveApp(name)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, launchTimeout)
	defer cancel()
	ws := appkit.SharedWorkspace()
	cfg := appkit.NewWorkspaceOpenConfiguration()
	if isURL {
		if _, err := ws.OpenURLConfiguration(ctx, target, cfg); err != nil {
			return "", fmt.Errorf("open %s: %w", target, err)
		}
		return target, nil
	}
	app, err := ws.OpenApplicationAtURLConfiguration(ctx, foundation.FileURLWithPath(target), cfg)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return "", fmt.Errorf("%w: %s", ErrLaunchTimedOut, target)
		}
		return "", fmt.Errorf("launch %s: %w", target, err)
	}
	if app != nil {
		app.ActivateWith(activateOptions)
	}
	return target, nil
}

// runningApp finds a running regular application by name, bundle id or title
// substring.
func runningApp(name string) (*appkit.RunningApplication, error) {
	needle := strings.ToLower(strings.TrimSpace(name))
	for _, app := range appkit.SharedWorkspace().RunningApplications() {
		if app.ActivationPolicy() != appkit.ApplicationActivationPolicyRegular {
			continue
		}
		if strings.EqualFold(app.BundleIdentifier(), needle) ||
			strings.Contains(strings.ToLower(app.LocalizedName()), needle) {
			return app, nil
		}
	}
	return nil, fmt.Errorf("%w: %q", ErrAppNotRunning, name)
}

// HideApp hides (or unhides) the named running application.
func (d *Desktop) HideApp(name string, hide bool) (string, error) {
	var label string
	err := d.Do(func() error {
		app, err := runningApp(name)
		if err != nil {
			return err
		}
		label = app.LocalizedName()
		if hide {
			app.Hide()
		} else {
			app.Unhide()
			app.ActivateWith(activateOptions)
		}
		return nil
	})
	return label, err
}

// QuitApp asks the named application to quit; force kills it.
func (d *Desktop) QuitApp(name string, force bool) (string, error) {
	var label string
	err := d.Do(func() error {
		app, err := runningApp(name)
		if err != nil {
			return err
		}
		label = app.LocalizedName()
		ok := false
		if force {
			ok = app.ForceTerminate()
		} else {
			ok = app.Terminate()
		}
		if !ok {
			return fmt.Errorf("%w: %q", ErrQuitRefused, label)
		}
		return nil
	})
	return label, err
}

// LaunchExecutable starts an executable directly (detached), returning its PID.
func (d *Desktop) LaunchExecutable(path string, args []string, cwd string) (int, error) {
	if _, err := os.Stat(path); err != nil {
		return 0, fmt.Errorf("executable not found: %w", err)
	}
	// Deliberately not exec.CommandContext: a launched program must outlive the
	// tool call that started it.
	cmd := exec.Command(path, args...) //nolint:noctx,gosec // detached by design; path is operator-controlled by policy
	cmd.Env = clirunner.Environment()
	if cwd != "" {
		cmd.Dir = cwd
	}
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("failed to start executable: %w", err)
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	return pid, nil
}

// activateOptions is the activation request every activate call makes. The
// ignoringOtherApps option has had no effect since macOS 14 — the system
// decides whether an activation request is honoured — so the default options
// are the honest ones.
const activateOptions appkit.ApplicationActivationOptions = 0
