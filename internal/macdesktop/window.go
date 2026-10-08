//go:build darwin && (amd64 || arm64)

package macdesktop

import (
	"errors"
	"fmt"
	"strings"

	"github.com/deploymenttheory/go-bindings-macosplatform/bindings/frameworks/appkit"
	"github.com/deploymenttheory/go-bindings-macosplatform/bindings/frameworks/corefoundation"
	"github.com/deploymenttheory/go-bindings-macosplatform/bindings/frameworks/hiservices"
)

// WindowInfo describes a top-level window.
type WindowInfo struct {
	// Handle identifies the window within a snapshot: the accessibility element's
	// address, stable for as long as the application keeps the window.
	Handle       uintptr
	Title        string
	App          string
	Rect         Rect
	ProcessID    int
	Minimized    bool
	IsForeground bool

	// element is the retained accessibility element for the window. Released
	// when the snapshot is replaced. Main-thread use only.
	element hiservices.AXUIElementRef
}

// ErrNoWindow reports a window lookup that matched nothing.
var ErrNoWindow = errors.New("no window matched")

// topLevelWindows lists every window of every regular (Dock-visible)
// application, with the frontmost application's focused window marked. Titles
// come from the accessibility tree, which needs only the Accessibility grant;
// the window server's titles would need Screen Recording as well. Main thread.
func (d *Desktop) topLevelWindows() ([]WindowInfo, error) {
	if !d.Accessible() {
		return nil, ErrAccessibilityDenied
	}
	ws := appkit.SharedWorkspace()
	front := ws.FrontmostApplication()
	frontPID := -1
	if front != nil {
		frontPID = front.ProcessIdentifier()
	}

	var out []WindowInfo
	for _, app := range ws.RunningApplications() {
		if app.ActivationPolicy() != appkit.ApplicationActivationPolicyRegular {
			continue
		}
		pid := app.ProcessIdentifier()
		appEl := hiservices.AXUIElementCreateApplication(pid)
		if appEl.IsNil() {
			continue
		}
		hiservices.AXUIElementSetMessagingTimeout(appEl, axMessagingTimeout)
		var focused hiservices.AXUIElementRef
		if pid == frontPID {
			focused, _ = axElement(appEl, axFocusedWindow)
		}
		name := app.LocalizedName()
		for _, w := range axElements(appEl, axWindows) {
			info := WindowInfo{
				Handle:    elementHandle(w),
				Title:     axString(w, axTitle),
				App:       name,
				ProcessID: pid,
				element:   w,
			}
			info.Minimized, _ = axBool(w, axMinimized)
			if p, ok := axPoint(w, axPosition); ok {
				if s, ok := axSize(w, axSizeAttr); ok {
					info.Rect = Rect{
						Left:   int(p.X),
						Top:    int(p.Y),
						Right:  int(p.X + s.Width),
						Bottom: int(p.Y + s.Height),
					}
				}
			}
			if !focused.IsNil() && w.IsEqual(focused) {
				info.IsForeground = true
			}
			if info.Title == "" {
				info.Title = name
			}
			out = append(out, info)
		}
		if !focused.IsNil() {
			focused.Release()
		}
		appEl.Release()
	}
	return out, nil
}

// elementHandle derives a stable identity for an element within a snapshot.
func elementHandle(el hiservices.AXUIElementRef) uintptr {
	return uintptr(idOf(el))
}

// foregroundWindowInfo picks the focused window out of an already-listed set.
func (d *Desktop) foregroundWindowInfo(windows []WindowInfo) (WindowInfo, bool) {
	for _, w := range windows {
		if w.IsForeground {
			return w, true
		}
	}
	return WindowInfo{}, false
}

// TopLevelWindows lists the top-level windows. The returned WindowInfos carry
// no retained elements; use Snapshot for a state that keeps them.
func (d *Desktop) TopLevelWindows() ([]WindowInfo, error) {
	var out []WindowInfo
	err := d.Do(func() error {
		ws, e := d.topLevelWindows()
		if e != nil {
			return e
		}
		for i := range ws {
			ws[i].element.Release()
			ws[i].element = zeroElement()
		}
		out = ws
		return nil
	})
	return out, err
}

// findWindowByTitle returns the first window whose title or application name
// contains the given substring (case-insensitive), with its element retained
// for the caller. Main thread only.
func (d *Desktop) findWindowByTitle(substr string) (WindowInfo, bool, error) {
	windows, err := d.topLevelWindows()
	if err != nil {
		return WindowInfo{}, false, err
	}
	needle := strings.ToLower(strings.TrimSpace(substr))
	var match WindowInfo
	found := false
	for _, w := range windows {
		if !found &&
			(strings.Contains(strings.ToLower(w.Title), needle) || strings.Contains(strings.ToLower(w.App), needle)) {
			match, found = w, true
			continue
		}
		w.element.Release()
	}
	return match, found, nil
}

// ActivateWindow brings the first window matching titleSubstr to the
// foreground. Returns the activated window.
func (d *Desktop) ActivateWindow(titleSubstr string) (WindowInfo, error) {
	var result WindowInfo
	err := d.Do(func() error {
		w, ok, err := d.findWindowByTitle(titleSubstr)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%w: %q", ErrNoWindow, titleSubstr)
		}
		defer w.element.Release()
		if app := appkit.RunningApplicationWithProcessIdentifier(w.ProcessID); app != nil {
			app.Unhide()
			app.ActivateWith(activateOptions)
		}
		if w.Minimized {
			_ = axSetBool(w.element, axMinimized, false)
		}
		_ = axPerform(w.element, axRaiseAction)
		_ = axSetBool(w.element, axMain, true)
		w.IsForeground = true
		w.element = zeroElement()
		result = w
		return nil
	})
	if err != nil {
		return WindowInfo{}, err
	}
	return result, nil
}

// ResizeWindow moves and resizes the first window matching titleSubstr.
func (d *Desktop) ResizeWindow(titleSubstr string, x, y, width, height int) (WindowInfo, error) {
	var result WindowInfo
	err := d.Do(func() error {
		w, ok, err := d.findWindowByTitle(titleSubstr)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%w: %q", ErrNoWindow, titleSubstr)
		}
		defer w.element.Release()
		if width <= 0 || height <= 0 {
			return fmt.Errorf("%w: window size must be positive", ErrCoordinateOutOfRange)
		}
		if w.Minimized {
			_ = axSetBool(w.element, axMinimized, false)
		}
		if err := axSetPoint(w.element, axPosition, corefoundation.CGPoint{X: float64(x), Y: float64(y)}); err != nil {
			return fmt.Errorf("move window: %w", err)
		}
		if err := axSetSize(
			w.element,
			axSizeAttr,
			corefoundation.CGSize{Width: float64(width), Height: float64(height)},
		); err != nil {
			return fmt.Errorf("resize window: %w", err)
		}
		w.Rect = Rect{Left: x, Top: y, Right: x + width, Bottom: y + height}
		w.element = zeroElement()
		result = w
		return nil
	})
	if err != nil {
		return WindowInfo{}, err
	}
	return result, nil
}

// CloseWindow closes the first window matching titleSubstr through its close
// button, which lets the application ask about unsaved changes.
func (d *Desktop) CloseWindow(titleSubstr string) (WindowInfo, error) {
	var result WindowInfo
	err := d.Do(func() error {
		w, ok, err := d.findWindowByTitle(titleSubstr)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%w: %q", ErrNoWindow, titleSubstr)
		}
		defer w.element.Release()
		for _, child := range axElements(w.element, axChildren) {
			if axString(child, axSubrole) == axSubroleCloseButton {
				perr := axPerform(child, axPressAction)
				child.Release()
				if perr != nil {
					return fmt.Errorf("close window: %w", perr)
				}
				w.element = zeroElement()
				result = w
				return nil
			}
			child.Release()
		}
		return fmt.Errorf("%w: %q has no close button", ErrNoWindow, w.Title)
	})
	if err != nil {
		return WindowInfo{}, err
	}
	return result, nil
}
