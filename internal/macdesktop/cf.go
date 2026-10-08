//go:build darwin && (amd64 || arm64)

package macdesktop

import (
	"errors"
	"fmt"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego/objc"

	"github.com/deploymenttheory/go-bindings-macosplatform/bindings/frameworks/corefoundation"
	"github.com/deploymenttheory/go-bindings-macosplatform/bindings/frameworks/hiservices"
	"github.com/deploymenttheory/go-bindings-macosplatform/bindings/runtime/obj"
	"github.com/deploymenttheory/go-bindings-macosplatform/bindings/runtime/purego"
)

// The accessibility attribute and action names. Apple's headers define these
// as string literals (#define kAXRoleAttribute CFSTR("AXRole")), not exported
// symbols, so the SDK cannot bind them and they are spelled here. The values
// are the documented wire strings and have been stable since Mac OS X 10.2.
const (
	axRole               = "AXRole"
	axSubrole            = "AXSubrole"
	axRoleDescription    = "AXRoleDescription"
	axTitle              = "AXTitle"
	axDescription        = "AXDescription"
	axValue              = "AXValue"
	axHelp               = "AXHelp"
	axIdentifier         = "AXIdentifier"
	axEnabled            = "AXEnabled"
	axFocused            = "AXFocused"
	axSelected           = "AXSelected"
	axExpanded           = "AXExpanded"
	axMinimized          = "AXMinimized"
	axMain               = "AXMain"
	axPosition           = "AXPosition"
	axSizeAttr           = "AXSize"
	axChildren           = "AXChildren"
	axWindows            = "AXWindows"
	axFocusedWindow      = "AXFocusedWindow"
	axFocusedApplication = "AXFocusedApplication"
	axFocusedUIElement   = "AXFocusedUIElement"
	axParent             = "AXParent"
	axWindow             = "AXWindow"
	axTopLevelUIElement  = "AXTopLevelUIElement"
	axMenuBar            = "AXMenuBar"

	axPressAction           = "AXPress"
	axRaiseAction           = "AXRaise"
	axShowMenuAction        = "AXShowMenu"
	axScrollToVisibleAction = "AXScrollToVisible"
	axConfirmAction         = "AXConfirm"
	axCancelAction          = "AXCancel"
)

// cfStrings caches the CFString for each attribute/action name: they are
// created once and never released, like the constants they stand in for.
var (
	cfStringsMu sync.Mutex
	cfStrings   = map[string]corefoundation.CFStringRef{}
)

// cfStr returns the CFString for name, creating and caching it.
func cfStr(name string) corefoundation.CFStringRef {
	cfStringsMu.Lock()
	defer cfStringsMu.Unlock()
	if s, ok := cfStrings[name]; ok {
		return s
	}
	s := corefoundation.CFStringCreateWithCString(corefoundation.CFAllocatorRef{}, name,
		int(corefoundation.KCFStringEncodingUTF8))
	cfStrings[name] = s
	return s
}

// cfTemp creates a CFString the caller releases when done (an attribute value
// to set, a one-off key).
func cfTemp(s string) corefoundation.CFStringRef {
	return corefoundation.CFStringCreateWithCString(corefoundation.CFAllocatorRef{}, s,
		int(corefoundation.KCFStringEncodingUTF8))
}

// AX errors the engine distinguishes.
var (
	ErrAccessibilityDenied = errors.New("accessibility is not granted to this process")
	ErrAXUnsupported       = errors.New("the element does not support this attribute or action")
	ErrAXFailed            = errors.New("accessibility request failed")
)

// axErr converts an AXError to a Go error, nil for success.
func axErr(code hiservices.AXError) error {
	switch code {
	case hiservices.KAXErrorSuccess:
		return nil
	case hiservices.KAXErrorAPIDisabled:
		return ErrAccessibilityDenied
	case hiservices.KAXErrorAttributeUnsupported, hiservices.KAXErrorActionUnsupported,
		hiservices.KAXErrorNotImplemented, hiservices.KAXErrorNoValue:
		return fmt.Errorf("%w (%s)", ErrAXUnsupported, code)
	default:
		return fmt.Errorf("%w: %s", ErrAXFailed, code)
	}
}

// axCopy copies one attribute value. The result is +1 (a Copy function), so it
// is adopted; nil when the attribute is absent or the call failed.
func axCopy(el hiservices.AXUIElementRef, attr string) (obj.Object, hiservices.AXError) {
	if el.IsNil() {
		return nil, hiservices.KAXErrorInvalidUIElement
	}
	var out uintptr
	code := hiservices.AXUIElementCopyAttributeValue(el, cfStr(attr), unsafe.Pointer(&out))
	if code != hiservices.KAXErrorSuccess || out == 0 {
		return nil, code
	}
	return obj.Adopt(objc.ID(out)), code
}

// axString reads a string-valued attribute, "" when absent or not a string.
func axString(el hiservices.AXUIElementRef, attr string) string {
	v, _ := axCopy(el, attr)
	if v == nil {
		return ""
	}
	defer v.Release()
	return cfToString(v)
}

// axBool reads a boolean attribute. ok is false when the attribute is absent.
func axBool(el hiservices.AXUIElementRef, attr string) (value, ok bool) {
	v, _ := axCopy(el, attr)
	if v == nil {
		return false, false
	}
	defer v.Release()
	if corefoundation.CFGetTypeID(v) != corefoundation.CFBooleanGetTypeID() {
		return false, false
	}
	return corefoundation.CFBooleanGetValue(corefoundation.CFBooleanRef{Object: v}) != 0, true
}

// axPoint and axSize read the AXValue-wrapped geometry attributes.
func axPoint(el hiservices.AXUIElementRef, attr string) (corefoundation.CGPoint, bool) {
	var p corefoundation.CGPoint
	v, _ := axCopy(el, attr)
	if v == nil {
		return p, false
	}
	defer v.Release()
	ok := hiservices.AXValueGetValue(
		hiservices.AXValueRef{Object: v},
		hiservices.KAXValueTypeCGPoint,
		unsafe.Pointer(&p),
	)
	return p, ok != 0
}

func axSize(el hiservices.AXUIElementRef, attr string) (corefoundation.CGSize, bool) {
	var s corefoundation.CGSize
	v, _ := axCopy(el, attr)
	if v == nil {
		return s, false
	}
	defer v.Release()
	ok := hiservices.AXValueGetValue(
		hiservices.AXValueRef{Object: v},
		hiservices.KAXValueTypeCGSize,
		unsafe.Pointer(&s),
	)
	return s, ok != 0
}

// axElement reads an element-valued attribute (the focused window, the parent).
// The caller owns the returned element.
func axElement(el hiservices.AXUIElementRef, attr string) (hiservices.AXUIElementRef, bool) {
	v, _ := axCopy(el, attr)
	if v == nil {
		return hiservices.AXUIElementRef{}, false
	}
	return hiservices.AXUIElementRef{Object: v}, true
}

// axElements reads an array-valued attribute of elements (children, windows).
// Each returned element is retained for the caller.
func axElements(el hiservices.AXUIElementRef, attr string) []hiservices.AXUIElementRef {
	v, _ := axCopy(el, attr)
	if v == nil {
		return nil
	}
	defer v.Release()
	if corefoundation.CFGetTypeID(v) != corefoundation.CFArrayGetTypeID() {
		return nil
	}
	arr := corefoundation.CFArrayRef{Object: v}
	n := corefoundation.CFArrayGetCount(arr)
	out := make([]hiservices.AXUIElementRef, 0, n)
	for i := 0; i < n; i++ {
		p := corefoundation.CFArrayGetValueAtIndex(arr, i)
		if p == nil {
			continue
		}
		// Array members are +0: Wrap retains our own reference.
		out = append(out, hiservices.AXUIElementRef{Object: obj.Wrap(objc.ID(uintptr(p)))})
	}
	return out
}

// axPerform performs a named action on an element.
func axPerform(el hiservices.AXUIElementRef, action string) error {
	return axErr(hiservices.AXUIElementPerformAction(el, cfStr(action)))
}

// axSetString sets a string attribute.
func axSetString(el hiservices.AXUIElementRef, attr, value string) error {
	s := cfTemp(value)
	defer s.Release()
	return axErr(hiservices.AXUIElementSetAttributeValue(el, cfStr(attr), s))
}

// axSetBool sets a boolean attribute.
func axSetBool(el hiservices.AXUIElementRef, attr string, value bool) error {
	b := corefoundation.KCFBooleanFalse()
	if value {
		b = corefoundation.KCFBooleanTrue()
	}
	return axErr(hiservices.AXUIElementSetAttributeValue(el, cfStr(attr), b))
}

// axSetPoint and axSetSize set the AXValue-wrapped geometry attributes.
func axSetPoint(el hiservices.AXUIElementRef, attr string, p corefoundation.CGPoint) error {
	v := hiservices.AXValueCreate(hiservices.KAXValueTypeCGPoint, unsafe.Pointer(&p))
	if v.IsNil() {
		return ErrAXFailed
	}
	defer v.Release()
	return axErr(hiservices.AXUIElementSetAttributeValue(el, cfStr(attr), v))
}

func axSetSize(el hiservices.AXUIElementRef, attr string, s corefoundation.CGSize) error {
	v := hiservices.AXValueCreate(hiservices.KAXValueTypeCGSize, unsafe.Pointer(&s))
	if v.IsNil() {
		return ErrAXFailed
	}
	defer v.Release()
	return axErr(hiservices.AXUIElementSetAttributeValue(el, cfStr(attr), v))
}

// axPID reads the owning process of an element.
func axPID(el hiservices.AXUIElementRef) int {
	code, pid := hiservices.AXUIElementGetPid(el)
	if code != hiservices.KAXErrorSuccess {
		return 0
	}
	return pid
}

// cfToString renders a CF value as text: strings as themselves, booleans and
// numbers via their description, anything else via its description.
func cfToString(v obj.Object) string {
	if v == nil {
		return ""
	}
	if corefoundation.CFGetTypeID(v) == corefoundation.CFStringGetTypeID() {
		return purego.GoString(obj.ID(v))
	}
	return v.Description()
}
