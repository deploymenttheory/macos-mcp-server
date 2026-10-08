//go:build darwin && (amd64 || arm64)

package macdesktop

import (
	"context"
	"errors"
	"sync"
	"time"
	"unsafe"

	ebipurego "github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"

	"github.com/deploymenttheory/go-bindings-macosplatform/bindings/frameworks/corefoundation"
	"github.com/deploymenttheory/go-bindings-macosplatform/bindings/frameworks/coregraphics"
	"github.com/deploymenttheory/go-bindings-macosplatform/bindings/frameworks/hiservices"
	"github.com/deploymenttheory/go-bindings-macosplatform/bindings/runtime/obj"
)

// This file is the journey recorder: an event tap on the session's input
// stream, with each click resolved to the element under it and each keystroke
// to a character (with the field's password state) or a named key.
//
// The tap is listen-only — it observes, it never modifies or swallows an
// event — and it needs the Accessibility grant, the same one synthetic input
// needs. The run loop that services it is the main thread's, which the
// `journey record` command pumps.

// DefaultRecordStopKey is the virtual key that ends a recording (F9), and
// DefaultRecordMarkKey the one that marks an assertion (F8). Both are consumed,
// not recorded, so pressing either never appears in the journey.
const (
	DefaultRecordStopKey = vkF9
	DefaultRecordMarkKey = vkF8
)

// doubleClickWindow and doubleClickSlop are the thresholds for merging two
// clicks into a double-click.
const (
	doubleClickWindow = 500 * time.Millisecond
	doubleClickSlop   = 5
)

// RecordedInput is one enriched user action the recorder emits. It is
// engine-level and holds no journey types; the caller maps it to a journey event.
type RecordedInput struct {
	Kind    string      // "click" | "char" | "key" | "assert"
	X, Y    int         // click point, or the cursor position at a mark
	Element ElementInfo // the element under a click or mark
	// State is what that element can do and what it currently holds, read at
	// the same hit-test so a click can be recorded as the verb it meant.
	State  ElementState
	Button string // left | right | middle
	Double bool   // a double-click
	Char   rune   // a typed character
	Secure bool   // the char was typed into a password field → redact
	Key    string // a named non-text key (Enter, Tab, …)
}

// Errors the recorder returns.
var (
	ErrEventTap            = errors.New("could not install the input event tap; is Accessibility granted?")
	ErrRecordingInProgress = errors.New("a recording is already in progress")
)

// tapState is the recorder's shared state, reached from the C callback through
// a package-level slot because a C function pointer cannot carry a closure.
type tapState struct {
	events chan rawInput
}

type rawInput struct {
	kind    string // "down" | "key"
	x, y    int
	button  string
	keycode uint16
	flags   coregraphics.CGEventFlags
	char    rune
	at      time.Time
}

var (
	activeTapMu sync.Mutex
	activeTap   *tapState
	tapCallback uintptr
	tapOnce     sync.Once
)

// tapHandler is the CGEventTapCallBack. It must return the event to pass it
// on; a listen-only tap ignores the return value but the contract stands.
func tapHandler(_ uintptr, typ uint32, event uintptr, _ uintptr) uintptr {
	activeTapMu.Lock()
	st := activeTap
	activeTapMu.Unlock()
	if st == nil || event == 0 {
		return event
	}
	ev := coregraphics.CGEventRef{Object: obj.WrapUnmanaged(objc.ID(event))}
	var in rawInput
	in.at = time.Now()
	switch coregraphics.CGEventType(typ) {
	case coregraphics.KCGEventLeftMouseDown, coregraphics.KCGEventRightMouseDown, coregraphics.KCGEventOtherMouseDown:
		p := coregraphics.CGEventGetLocation(ev)
		in.kind, in.x, in.y = "down", int(p.X), int(p.Y)
		switch coregraphics.CGEventType(typ) {
		case coregraphics.KCGEventRightMouseDown:
			in.button = "right"
		case coregraphics.KCGEventOtherMouseDown:
			in.button = "middle"
		default:
			in.button = "left"
		}
	case coregraphics.KCGEventKeyDown:
		if coregraphics.CGEventGetIntegerValueField(ev, coregraphics.KCGKeyboardEventAutorepeat) != 0 {
			return event
		}
		in.kind = "key"
		in.keycode = uint16(
			coregraphics.CGEventGetIntegerValueField(ev, coregraphics.KCGKeyboardEventKeycode),
		) //nolint:gosec // keycodes are 16-bit
		in.flags = coregraphics.CGEventGetFlags(ev)
		if n, unit := coregraphics.CGEventKeyboardGetUnicodeString(ev, 1); n > 0 {
			in.char = rune(unit)
		}
	default:
		return event
	}
	select {
	case st.events <- in:
	default: // a slow consumer drops input rather than stalling the tap
	}
	return event
}

// eventMask selects the event types the tap observes.
func eventMask(types ...coregraphics.CGEventType) uint64 {
	var m uint64
	for _, t := range types {
		m |= 1 << uint64(t)
	}
	return m
}

// RecordInput installs the event tap and streams enriched events to out until
// ctx is cancelled or the stop key is pressed. It runs the caller's out on the
// recorder goroutine; out must not block for long.
//
// The main run loop must be pumped while this runs (see cmd's journey record):
// the tap's source is added to the main run loop.
func (d *Desktop) RecordInput(ctx context.Context, stopKey uint16, out func(RecordedInput)) error {
	return d.RecordInputWithMark(ctx, stopKey, DefaultRecordMarkKey, out)
}

// RecordInputWithMark is RecordInput with an explicit assertion-mark key.
func (d *Desktop) RecordInputWithMark(ctx context.Context, stopKey, markKey uint16, out func(RecordedInput)) error {
	if !d.Accessible() {
		return ErrAccessibilityDenied
	}
	st := &tapState{events: make(chan rawInput, 256)}
	activeTapMu.Lock()
	if activeTap != nil {
		activeTapMu.Unlock()
		return ErrRecordingInProgress
	}
	activeTap = st
	activeTapMu.Unlock()
	defer func() {
		activeTapMu.Lock()
		activeTap = nil
		activeTapMu.Unlock()
	}()
	tapOnce.Do(func() { tapCallback = ebipurego.NewCallback(tapHandler) })

	var (
		port   obj.Object
		source corefoundation.CFRunLoopSourceRef
	)
	err := d.Do(func() error {
		mask := eventMask(coregraphics.KCGEventLeftMouseDown, coregraphics.KCGEventRightMouseDown,
			coregraphics.KCGEventOtherMouseDown, coregraphics.KCGEventKeyDown)
		port = coregraphics.CGEventTapCreate(coregraphics.KCGSessionEventTap, coregraphics.KCGHeadInsertEventTap,
			coregraphics.KCGEventTapOptionListenOnly, mask, unsafe.Pointer(tapCallback), nil)
		if port == nil {
			return ErrEventTap
		}
		source = corefoundation.CFMachPortCreateRunLoopSource(corefoundation.CFAllocatorRef{},
			corefoundation.CFMachPortRef{Object: port}, 0)
		if source.IsNil() {
			return ErrEventTap
		}
		corefoundation.CFRunLoopAddSource(corefoundation.CFRunLoopGetMain(), source,
			unsafe.Pointer(corefoundation.KCFRunLoopCommonModes()))
		coregraphics.CGEventTapEnable(port, true)
		return nil
	})
	if err != nil {
		return err
	}
	defer func() {
		_ = d.Do(func() error {
			coregraphics.CGEventTapEnable(port, false)
			corefoundation.CFRunLoopRemoveSource(corefoundation.CFRunLoopGetMain(), source,
				unsafe.Pointer(corefoundation.KCFRunLoopCommonModes()))
			corefoundation.CFMachPortInvalidate(corefoundation.CFMachPortRef{Object: port})
			source.Release()
			port.Release()
			return nil
		})
	}()

	var lastAt time.Time
	var lastX, lastY int
	for {
		select {
		case <-ctx.Done():
			return nil
		case in := <-st.events:
			switch in.kind {
			case "down":
				info, state := d.inspectPoint(in.x, in.y)
				double := in.at.Sub(lastAt) <= doubleClickWindow &&
					absInt(in.x-lastX) <= doubleClickSlop && absInt(in.y-lastY) <= doubleClickSlop
				lastAt, lastX, lastY = in.at, in.x, in.y
				out(
					RecordedInput{
						Kind:    "click",
						X:       in.x,
						Y:       in.y,
						Element: info,
						State:   state,
						Button:  in.button,
						Double:  double,
					},
				)
			case "key":
				if stopKey != 0 && in.keycode == stopKey {
					return nil
				}
				if markKey != 0 && in.keycode == markKey {
					// Marked where the pointer is, not where focus is: the author is
					// pointing at the thing they mean.
					x, y := cursorPoint()
					info, state := d.inspectPoint(x, y)
					out(RecordedInput{Kind: "assert", X: x, Y: y, Element: info, State: state})
					continue
				}
				if name, ok := namedKeyFor(in.keycode); ok {
					out(RecordedInput{Kind: "key", Key: name})
					continue
				}
				if in.flags&(coregraphics.KCGEventFlagMaskCommand|coregraphics.KCGEventFlagMaskControl) != 0 {
					// A chord is a shortcut, not text; the emitter has no chord event
					// yet, so it is recorded as the key name for the author to review.
					if name, ok := letterFor(in.keycode); ok {
						out(RecordedInput{Kind: "key", Key: chordName(in.flags, name)})
					}
					continue
				}
				if in.char == 0 || in.char < 0x20 {
					continue
				}
				// Fail closed: a keystroke whose destination cannot be established
				// is treated as secret.
				secure := true
				_ = d.Do(func() error { secure = d.focusedIsPassword(); return nil })
				out(RecordedInput{Kind: "char", Char: in.char, Secure: secure})
			}
		}
	}
}

// namedKeyFor maps a virtual key to the key name the journey vocabulary uses,
// for the non-text keys.
func namedKeyFor(vk uint16) (string, bool) {
	switch vk {
	case vkReturn, vkKeypadEnter:
		return "Enter", true
	case vkTab:
		return "Tab", true
	case vkEscape:
		return "Escape", true
	case vkDelete:
		return "Backspace", true
	case vkForwardDelete:
		return "Delete", true
	case vkHome:
		return "Home", true
	case vkEnd:
		return "End", true
	case vkPageUp:
		return "PageUp", true
	case vkPageDown:
		return "PageDown", true
	case vkUpArrow:
		return "Up", true
	case vkDownArrow:
		return "Down", true
	case vkLeftArrow:
		return "Left", true
	case vkRightArrow:
		return "Right", true
	}
	return "", false
}

// letterFor maps a letter/digit virtual key back to its character.
func letterFor(vk uint16) (string, bool) {
	for c, code := range letterKeys {
		if code == vk {
			return string(c), true
		}
	}
	return "", false
}

// chordName renders a modifier chord in the Shortcut tool's syntax.
func chordName(flags coregraphics.CGEventFlags, key string) string {
	var parts []string
	if flags&coregraphics.KCGEventFlagMaskCommand != 0 {
		parts = append(parts, "cmd")
	}
	if flags&coregraphics.KCGEventFlagMaskControl != 0 {
		parts = append(parts, "ctrl")
	}
	if flags&coregraphics.KCGEventFlagMaskAlternate != 0 {
		parts = append(parts, "option")
	}
	if flags&coregraphics.KCGEventFlagMaskShift != 0 {
		parts = append(parts, "shift")
	}
	parts = append(parts, key)
	out := parts[0]
	for _, p := range parts[1:] {
		out += "+" + p
	}
	return out
}

// inspectPoint resolves the element under a point and reads its state, both on
// the main thread and both from the same element.
func (d *Desktop) inspectPoint(x, y int) (ElementInfo, ElementState) {
	var (
		info  ElementInfo
		state ElementState
	)
	_ = d.Do(func() error {
		info, state = d.elementAtPoint(x, y)
		return nil
	})
	return info, state
}

// elementAtPoint hit-tests the system-wide element. Main thread.
func (d *Desktop) elementAtPoint(x, y int) (ElementInfo, ElementState) {
	var out uintptr
	code := hiservices.AXUIElementCopyElementAtPosition(d.systemWide, float32(x), float32(y), unsafe.Pointer(&out))
	if code != hiservices.KAXErrorSuccess || out == 0 {
		return ElementInfo{}, ElementState{}
	}
	el := hiservices.AXUIElementRef{Object: obj.Adopt(objc.ID(out))}
	defer el.Release()
	return readElementInfo(el), readElementState(el)
}

// focusedIsPassword reports whether the focused element masks its input.
// Main thread.
func (d *Desktop) focusedIsPassword() bool {
	app, ok := axElement(d.systemWide, axFocusedApplication)
	if !ok {
		return true // fail closed
	}
	defer app.Release()
	el, ok := axElement(app, axFocusedUIElement)
	if !ok {
		return true
	}
	defer el.Release()
	return axString(el, axRole) == axRoleSecureTextField
}

// cursorPoint reads the current pointer position in points.
func cursorPoint() (int, int) {
	ev := coregraphics.CGEventCreate(coregraphics.CGEventSourceRef{})
	if ev.IsNil() {
		return 0, 0
	}
	defer ev.Release()
	p := coregraphics.CGEventGetLocation(ev)
	return int(p.X), int(p.Y)
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
