//go:build darwin && (amd64 || arm64)

package macdesktop

import (
	"strings"
)

// DesktopState is the captured perception of the desktop returned by Snapshot.
// It is also stored on the engine so that label-based interaction (Click/Type
// by label) can resolve labels to screen coordinates.
type DesktopState struct {
	// Foreground is the current foreground window (zero value if none).
	Foreground WindowInfo
	// Windows lists visible, titled top-level windows.
	Windows []WindowInfo
	// Interactive lists the labeled interactive elements, in label order.
	Interactive []LabeledElement
	// TreeText is the human-readable semantic UI tree.
	TreeText string
}

// SnapshotOptions controls what a Snapshot captures.
type SnapshotOptions struct {
	// AllWindows, when true, walks every visible window's tree (subject to the
	// global node budget). When false, only the foreground window is walked,
	// which is faster and usually sufficient.
	AllWindows bool
}

// Snapshot captures the current desktop state: windows, and the labeled
// interactive UI tree of the foreground window (and optionally all windows).
// It stores the result for subsequent label-based interaction.
func (d *Desktop) Snapshot(opts SnapshotOptions) (*DesktopState, error) {
	if !d.Accessible() {
		return nil, ErrAccessibilityDenied
	}
	var state *DesktopState
	err := d.Do(func() error {
		windows, e := d.topLevelWindows()
		if e != nil {
			return e
		}
		fg, _ := d.foregroundWindowInfo(windows)

		targets := selectTargets(windows, opts.AllWindows)

		tb := &treeBuilder{}
		for _, w := range targets {
			if w.element.IsNil() {
				continue
			}
			tb.visitWindow(w.element, w)
			if tb.nodes >= maxTreeNodes {
				break
			}
		}

		state = &DesktopState{
			Foreground:  fg,
			Windows:     windows,
			Interactive: tb.interactive,
			TreeText:    strings.Join(tb.lines, "\n"),
		}

		// Swap in the new state and release the previous snapshot's retained
		// element references — on the main thread, where they were created.
		d.stateMu.Lock()
		old := d.lastState
		d.lastState = state
		d.stateMu.Unlock()
		releaseStateElements(old)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return state, nil
}

// releaseStateElements releases the retained element references held by a
// snapshot. Must run on the main thread.
func releaseStateElements(s *DesktopState) {
	if s == nil {
		return
	}
	for i := range s.Interactive {
		if !s.Interactive[i].element.IsNil() {
			s.Interactive[i].element.Release()
			s.Interactive[i].element = zeroElement()
		}
	}
	for i := range s.Windows {
		if !s.Windows[i].element.IsNil() {
			s.Windows[i].element.Release()
			s.Windows[i].element = zeroElement()
		}
	}
}

// selectTargets orders windows for traversal: the primary window first, then
// (when allWindows) the remaining non-minimized windows. De-duplication keys on
// the window Handle rather than the IsForeground flag, because when there is no
// foreground window the fallback picks a window that is by definition not
// foreground.
func selectTargets(windows []WindowInfo, allWindows bool) []WindowInfo {
	var primary *WindowInfo
	for i := range windows {
		if windows[i].IsForeground {
			primary = &windows[i]
			break
		}
	}

	var targets []WindowInfo
	if primary != nil {
		targets = append(targets, *primary)
	} else if len(windows) > 0 {
		for i := range windows {
			if !windows[i].Minimized {
				targets = append(targets, windows[i])
				primary = &windows[i]
				break
			}
		}
	}
	if !allWindows {
		return targets
	}
	for i := range windows {
		if windows[i].Minimized {
			continue
		}
		if primary != nil && windows[i].Handle == primary.Handle {
			continue
		}
		targets = append(targets, windows[i])
	}
	return targets
}

// CoordinatesForLabel resolves a label from the most recent Snapshot to a click
// point. It is safe to call from any goroutine.
func (d *Desktop) CoordinatesForLabel(label int) (x, y int, ok bool) {
	d.stateMu.Lock()
	defer d.stateMu.Unlock()
	if d.lastState == nil {
		return 0, 0, false
	}
	for _, e := range d.lastState.Interactive {
		if e.Label == label {
			return e.CenterX, e.CenterY, true
		}
	}
	return 0, 0, false
}

// LastState returns the most recent Snapshot, or nil if none has been taken.
func (d *Desktop) LastState() *DesktopState {
	d.stateMu.Lock()
	defer d.stateMu.Unlock()
	return d.lastState
}
