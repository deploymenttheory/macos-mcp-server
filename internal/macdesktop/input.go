//go:build darwin && (amd64 || arm64)

package macdesktop

import (
	"errors"
	"fmt"
	"time"
	"unicode/utf16"
	"unsafe"

	"github.com/deploymenttheory/go-bindings-macosplatform/bindings/frameworks/corefoundation"
	"github.com/deploymenttheory/go-bindings-macosplatform/bindings/frameworks/coregraphics"
)

// Limits on a single synthetic-input call.
//
// The engine serialises every operation onto one thread, so a call that runs
// for minutes stops the whole desktop surface for that long. The limits are
// set well above real usage: they exist to bound a runaway or hostile call,
// not to shape ordinary ones.
const (
	// MaxTypeChars bounds one Type call.
	MaxTypeChars = 10000
	// MaxBatchItems bounds the point and edit counts of the batch tools.
	MaxBatchItems = 100
	// maxWheelClicks bounds a single Scroll call.
	maxWheelClicks = 1000
	// maxCoordinate bounds a screen coordinate: no display space is this large,
	// and a value past it is a malformed argument rather than a point.
	maxCoordinate = 1 << 16
	// unicodeChunk is the most UTF-16 units one keyboard event may carry.
	unicodeChunk = 20
)

// Errors the input operations return.
var (
	ErrInputTooLarge        = errors.New("input exceeds the per-call limit")
	ErrCoordinateOutOfRange = errors.New("coordinate is outside the display coordinate space")
	ErrUnknownKey           = errors.New("unknown key")
	ErrEventRefused         = errors.New("the window server refused the event; is Accessibility granted?")
)

// screenPoint validates a tool-supplied point.
func screenPoint(x, y int) (corefoundation.CGPoint, error) {
	if x < -maxCoordinate || x > maxCoordinate || y < -maxCoordinate || y > maxCoordinate {
		return corefoundation.CGPoint{}, fmt.Errorf("%w: (%d,%d)", ErrCoordinateOutOfRange, x, y)
	}
	return corefoundation.CGPoint{X: float64(x), Y: float64(y)}, nil
}

// eventSource is the HID-state source every synthetic event is created from,
// so the events carry the same modifier and button state a real device would.
func eventSource() coregraphics.CGEventSourceRef {
	return coregraphics.CGEventSourceCreate(coregraphics.KCGEventSourceStateHIDSystemState)
}

// post sends one event to the HID event tap and releases it.
func post(ev coregraphics.CGEventRef) error {
	if ev.IsNil() {
		return ErrEventRefused
	}
	coregraphics.CGEventPost(coregraphics.KCGHIDEventTap, ev)
	ev.Release()
	return nil
}

// moveTo moves the cursor and tells the window server about it, so the target
// window sees a motion event before any click.
func moveTo(src coregraphics.CGEventSourceRef, p corefoundation.CGPoint) error {
	ev := coregraphics.CGEventCreateMouseEvent(src, coregraphics.KCGEventMouseMoved, p, coregraphics.KCGMouseButtonLeft)
	return post(ev)
}

// mouseButton maps a button name to its event types and CG button.
func mouseButton(button string) (down, up coregraphics.CGEventType, b coregraphics.CGMouseButton) {
	switch button {
	case "right":
		return coregraphics.KCGEventRightMouseDown, coregraphics.KCGEventRightMouseUp, coregraphics.KCGMouseButtonRight
	case "middle":
		return coregraphics.KCGEventOtherMouseDown, coregraphics.KCGEventOtherMouseUp, coregraphics.KCGMouseButtonCenter
	default:
		return coregraphics.KCGEventLeftMouseDown, coregraphics.KCGEventLeftMouseUp, coregraphics.KCGMouseButtonLeft
	}
}

// clickAt presses and releases a button at p. clickState numbers the click in
// a multi-click sequence (1 single, 2 double), which is how the target tells a
// double-click from two singles. Main thread, inside a Do job.
func clickAt(
	src coregraphics.CGEventSourceRef,
	p corefoundation.CGPoint,
	button string,
	clickState int,
	flags coregraphics.CGEventFlags,
) error {
	downT, upT, b := mouseButton(button)
	for _, t := range []coregraphics.CGEventType{downT, upT} {
		ev := coregraphics.CGEventCreateMouseEvent(src, t, p, b)
		if ev.IsNil() {
			return ErrEventRefused
		}
		coregraphics.CGEventSetIntegerValueField(ev, coregraphics.KCGMouseEventClickState, int64(clickState))
		if flags != 0 {
			coregraphics.CGEventSetFlags(ev, flags)
		}
		if err := post(ev); err != nil {
			return err
		}
	}
	return nil
}

// Click moves the cursor to (x,y) and clicks. button is "left", "right", or
// "middle"; clicks of 0 hovers (move only), 1 single-clicks, 2 double-clicks.
func (d *Desktop) Click(x, y int, button string, clicks int) error {
	p, err := screenPoint(x, y)
	if err != nil {
		return err
	}
	return d.Do(func() error {
		src := eventSource()
		defer src.Release()
		if err := moveTo(src, p); err != nil {
			return err
		}
		if clicks <= 0 {
			return nil // hover only
		}
		time.Sleep(10 * time.Millisecond)
		for i := 1; i <= clicks; i++ {
			if err := clickAt(src, p, button, i, 0); err != nil {
				return err
			}
			if i < clicks {
				time.Sleep(60 * time.Millisecond)
			}
		}
		return nil
	})
}

// ClickMany left-clicks a series of points in order. When holdCmd is true,
// Command is held for the whole sequence (multi-select).
func (d *Desktop) ClickMany(points [][2]int, holdCmd bool) error {
	if len(points) == 0 {
		return nil
	}
	if len(points) > MaxBatchItems {
		return fmt.Errorf("%w: %d points (limit %d)", ErrInputTooLarge, len(points), MaxBatchItems)
	}
	pts := make([]corefoundation.CGPoint, len(points))
	for i, pt := range points {
		p, err := screenPoint(pt[0], pt[1])
		if err != nil {
			return err
		}
		pts[i] = p
	}
	return d.Do(func() error {
		src := eventSource()
		defer src.Release()
		var flags coregraphics.CGEventFlags
		if holdCmd {
			flags = coregraphics.KCGEventFlagMaskCommand
			if err := post(coregraphics.CGEventCreateKeyboardEvent(src, vkCommand, true)); err != nil {
				return err
			}
			defer func() { _ = post(coregraphics.CGEventCreateKeyboardEvent(src, vkCommand, false)) }()
		}
		for i, p := range pts {
			if err := moveTo(src, p); err != nil {
				return err
			}
			time.Sleep(10 * time.Millisecond)
			if err := clickAt(src, p, "left", 1, flags); err != nil {
				return err
			}
			if i < len(pts)-1 {
				time.Sleep(80 * time.Millisecond)
			}
		}
		return nil
	})
}

// MoveCursor moves the cursor to (x,y) without clicking.
func (d *Desktop) MoveCursor(x, y int) error {
	p, err := screenPoint(x, y)
	if err != nil {
		return err
	}
	return d.Do(func() error {
		src := eventSource()
		defer src.Release()
		return moveTo(src, p)
	})
}

// TypeText types Unicode text at the current focus. Newlines and tabs are sent
// as Return and Tab keys; everything else is sent as Unicode keyboard events,
// which are layout-independent.
func (d *Desktop) TypeText(text string) error {
	if len(text) > MaxTypeChars {
		return fmt.Errorf("%w: %d characters (limit %d)", ErrInputTooLarge, len(text), MaxTypeChars)
	}
	return d.Do(func() error {
		src := eventSource()
		defer src.Release()
		var chunk []uint16
		flush := func() error {
			if len(chunk) == 0 {
				return nil
			}
			for _, down := range []bool{true, false} {
				ev := coregraphics.CGEventCreateKeyboardEvent(src, 0, down)
				if ev.IsNil() {
					return ErrEventRefused
				}
				coregraphics.CGEventKeyboardSetUnicodeString(ev, len(chunk), unsafe.Pointer(&chunk[0]))
				if err := post(ev); err != nil {
					return err
				}
			}
			chunk = chunk[:0]
			time.Sleep(3 * time.Millisecond)
			return nil
		}
		for _, r := range text {
			switch r {
			case '\n':
				if err := flush(); err != nil {
					return err
				}
				if err := tapKey(src, vkReturn, 0); err != nil {
					return err
				}
			case '\t':
				if err := flush(); err != nil {
					return err
				}
				if err := tapKey(src, vkTab, 0); err != nil {
					return err
				}
			case '\r':
				// ignore; handled by \n
			default:
				units := utf16.Encode([]rune{r})
				if len(chunk)+len(units) > unicodeChunk {
					if err := flush(); err != nil {
						return err
					}
				}
				chunk = append(chunk, units...)
			}
		}
		return flush()
	})
}

// tapKey presses and releases one virtual key with the given modifier flags.
func tapKey(src coregraphics.CGEventSourceRef, vk uint16, flags coregraphics.CGEventFlags) error {
	for _, down := range []bool{true, false} {
		ev := coregraphics.CGEventCreateKeyboardEvent(src, vk, down)
		if ev.IsNil() {
			return ErrEventRefused
		}
		if flags != 0 {
			coregraphics.CGEventSetFlags(ev, flags)
		}
		if err := post(ev); err != nil {
			return err
		}
	}
	return nil
}

// boundWheelClicks clamps the requested notch count into the usable range.
func boundWheelClicks(n int) int {
	switch {
	case n < 1:
		return 1
	case n > maxWheelClicks:
		return maxWheelClicks
	default:
		return n
	}
}

// scrollDeltas maps a direction name to the vertical and horizontal line
// deltas of one notch. Positive vertical scrolls up (content moves down),
// matching the trackpad's natural reading of the wheel.
func scrollDeltas(direction string) (vertical, horizontal int32) {
	switch direction {
	case "up":
		return 1, 0
	case "left":
		return 0, 1
	case "right":
		return 0, -1
	default: // "down" and anything unrecognised
		return -1, 0
	}
}

// Scroll moves the cursor to (x,y) and scrolls the wheel. direction is "up",
// "down", "left", or "right"; wheelClicks is the number of wheel notches.
func (d *Desktop) Scroll(x, y, wheelClicks int, direction string) error {
	p, err := screenPoint(x, y)
	if err != nil {
		return err
	}
	wheelClicks = boundWheelClicks(wheelClicks)
	return d.Do(func() error {
		src := eventSource()
		defer src.Release()
		if err := moveTo(src, p); err != nil {
			return err
		}
		v, h := scrollDeltas(direction)
		for range wheelClicks {
			ev := coregraphics.CGEventCreateScrollWheelEvent2(src, coregraphics.KCGScrollEventUnitLine, 2, v, h, 0)
			if ev.IsNil() {
				return ErrEventRefused
			}
			coregraphics.CGEventSetLocation(ev, p)
			if err := post(ev); err != nil {
				return err
			}
			time.Sleep(10 * time.Millisecond)
		}
		return nil
	})
}

// modifierFlag maps a modifier virtual key to its event flag.
func modifierFlag(vk uint16) coregraphics.CGEventFlags {
	switch vk {
	case vkCommand, vkRightCommand:
		return coregraphics.KCGEventFlagMaskCommand
	case vkControl:
		return coregraphics.KCGEventFlagMaskControl
	case vkOption:
		return coregraphics.KCGEventFlagMaskAlternate
	case vkShift:
		return coregraphics.KCGEventFlagMaskShift
	case vkFunction:
		return coregraphics.KCGEventFlagMaskSecondaryFn
	}
	return 0
}

// SendShortcut presses a key chord, e.g. []string{"cmd","shift","4"}.
// Modifiers are pressed first, in order, and released in reverse after the
// key; the non-modifier keys carry the accumulated modifier flags, which is
// what applications read.
func (d *Desktop) SendShortcut(keys []string) error {
	if len(keys) == 0 {
		return fmt.Errorf("%w: no keys given", ErrUnknownKey)
	}
	var mods []uint16
	var plain []uint16
	var flags coregraphics.CGEventFlags
	for _, k := range keys {
		vk, isMod, ok := keyNameToVK(k)
		if !ok {
			return fmt.Errorf("%w: %q", ErrUnknownKey, k)
		}
		if isMod {
			mods = append(mods, vk)
			flags |= modifierFlag(vk)
		} else {
			plain = append(plain, vk)
		}
	}
	return d.Do(func() error {
		src := eventSource()
		defer src.Release()
		for _, m := range mods {
			ev := coregraphics.CGEventCreateKeyboardEvent(src, m, true)
			if err := post(ev); err != nil {
				return err
			}
		}
		for _, k := range plain {
			if err := tapKey(src, k, flags); err != nil {
				return err
			}
		}
		for i := len(mods) - 1; i >= 0; i-- {
			ev := coregraphics.CGEventCreateKeyboardEvent(src, mods[i], false)
			if err := post(ev); err != nil {
				return err
			}
		}
		return nil
	})
}
