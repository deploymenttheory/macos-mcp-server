//go:build darwin && (amd64 || arm64)

// Package macdesktop is the macOS desktop engine: the accessibility tree,
// synthetic input, screenshots, windows and applications, the clipboard and
// processes, reached through go-bindings-macosplatform. It is the counterpart
// of windows-mcp-server's internal/desktop.
//
// The engine's central constraint is the main-thread rule. AppKit, the
// accessibility API and event posting expect the process main thread, and the
// SDK's @MainActor wrappers dispatch there. Every engine operation that touches
// them goes through Desktop.Do, which serialises callers and runs the work on
// the main thread via mainthread.Do. The process must keep that thread
// serviced — the stdio command hands it to mainthread.DispatchMain, and tests
// pump it from TestMain — or Do blocks forever.
package macdesktop

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"sync/atomic"

	"github.com/deploymenttheory/go-bindings-macosplatform/bindings/frameworks/hiservices"
	"github.com/deploymenttheory/go-bindings-macosplatform/opinionated/tools/grandcentraldispatch/mainthread"
)

// Options configures the engine.
type Options struct {
	// Overlay enables visual-feedback overlays (a highlight around the active
	// window, a flash at click points) for screen capture and recording.
	Overlay bool
	// SecurityOverlay starts the overlay manager even when Overlay is false, so
	// the security-event banner can be shown regardless of the decorative
	// overlay setting. Set by the security layer.
	SecurityOverlay bool
	// Record, when its Dir is set, records the whole session to a video file.
	Record RecorderOptions
}

// RecorderOptions configures session recording (landing in a later milestone;
// carried here so the server's configuration surface matches the Windows one).
type RecorderOptions struct {
	// Dir is the output directory for session recordings. Empty disables it.
	Dir string
	// FPS is the capture rate (default 4).
	FPS int
	// Codec is h264 or hevc.
	Codec string
	// Stamp is the session stamp the recording file is named after.
	Stamp string
}

// Errors the engine lifecycle returns.
var (
	ErrEngineClosed      = errors.New("desktop engine is closed")
	ErrEnginePanic       = errors.New("desktop engine panic")
	ErrSystemWideElement = errors.New("accessibility: could not create the system-wide element")
)

// Desktop is the engine. One instance per process.
type Desktop struct {
	logger *slog.Logger
	opts   Options

	// doMu serialises Do: every AX/CGEvent/AppKit operation runs one at a time,
	// on the main thread, exactly as the Windows engine's STA thread does.
	doMu sync.Mutex

	// systemWide is the accessibility system-wide element, the root for
	// focused-application and element-at-point queries.
	systemWide hiservices.AXUIElementRef

	stateMu   sync.Mutex
	lastState *DesktopState

	bannerMu sync.Mutex
	banner   string

	closed atomic.Bool
}

// New starts the engine. It returns once the accessibility entry points have
// been created. It does not require the Accessibility grant: without it the
// engine still serves screenshots, displays, windows and processes, and the
// tree calls report the missing grant per call.
func New(logger *slog.Logger, opts Options) (*Desktop, error) {
	if logger == nil {
		logger = slog.Default()
	}
	d := &Desktop{logger: logger, opts: opts}
	err := d.Do(func() error {
		d.systemWide = hiservices.AXUIElementCreateSystemWide()
		if d.systemWide.IsNil() {
			return ErrSystemWideElement
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if hiservices.AXIsProcessTrusted() == 0 {
		logger.Warn("Accessibility is not granted: Snapshot, Click-by-label and synthetic input will fail " +
			"until it is (System Settings > Privacy & Security > Accessibility, or `permissions request`)")
	}
	return d, nil
}

// Do runs fn on the main thread, serialised with every other engine operation.
// It is the only path to AppKit, the accessibility API and event posting; it is
// never called from outside this package, and it must never be re-entered from
// inside a job (doMu is not reentrant).
func (d *Desktop) Do(fn func() error) error {
	if d.closed.Load() {
		return ErrEngineClosed
	}
	d.doMu.Lock()
	defer d.doMu.Unlock()
	var err error
	mainthread.Do(func() { err = safeCall(fn) })
	return err
}

// safeCall runs fn, converting a panic into an error so one bad call cannot
// take the engine — and with it the main thread — down.
func safeCall(fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%w: %v\n%s", ErrEnginePanic, r, debug.Stack())
		}
	}()
	return fn()
}

// Close releases the engine. Idempotent.
func (d *Desktop) Close() error {
	if d.closed.Swap(true) {
		return nil
	}
	d.stateMu.Lock()
	old := d.lastState
	d.lastState = nil
	d.stateMu.Unlock()
	releaseStateElements(old)
	return nil
}

// Logger returns the engine's logger.
func (d *Desktop) Logger() *slog.Logger { return d.logger }

// Alive reports whether the engine is still answering. The harness heartbeat
// carries it: a wedged main thread is the one fault the MCP wire cannot show.
func (d *Desktop) Alive() bool {
	if d.closed.Load() {
		return false
	}
	done := make(chan struct{})
	go func() { _ = d.Do(func() error { return nil }); close(done) }()
	select {
	case <-done:
		return true
	case <-contextTimeout(aliveProbeTimeout):
		return false
	}
}

// Accessible reports whether this process holds the Accessibility grant.
func (d *Desktop) Accessible() bool { return hiservices.AXIsProcessTrusted() != 0 }

// ShowSecurityBanner shows the security-event banner. Until the overlay lands
// it is recorded and logged, so the kill ladder's transparency step has a
// record even where it has no pixels.
func (d *Desktop) ShowSecurityBanner(text string) {
	d.bannerMu.Lock()
	d.banner = text
	d.bannerMu.Unlock()
	d.logger.Warn("SECURITY BANNER", "text", text)
}

// DismissSecurityBanner clears the banner.
func (d *Desktop) DismissSecurityBanner() {
	d.bannerMu.Lock()
	d.banner = ""
	d.bannerMu.Unlock()
}

// Banner returns the current banner text, for the status surface.
func (d *Desktop) Banner() string {
	d.bannerMu.Lock()
	defer d.bannerMu.Unlock()
	return d.banner
}

// Notify raises a user notification. Best-effort: a failure is logged, never
// returned, because the callers are the guardrail paths that must not fail on
// presentation.
func (d *Desktop) Notify(ctx context.Context, title, message string) {
	if err := notify(ctx, title, message); err != nil {
		d.logger.Warn("notification failed", "error", err)
	}
}
