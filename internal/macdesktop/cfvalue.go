//go:build darwin && (amd64 || arm64)

package macdesktop

import (
	"encoding/base64"
	"fmt"
	"time"
	"unsafe"

	"github.com/ebitengine/purego/objc"

	"github.com/deploymenttheory/go-bindings-macosplatform/bindings/frameworks/corefoundation"
	"github.com/deploymenttheory/go-bindings-macosplatform/bindings/runtime/obj"
)

// cfEpoch is the CFAbsoluteTime origin (2001-01-01 UTC).
var cfEpoch = time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)

// cfToGo converts a property-list value to its Go shape: string, bool,
// int64/float64, []any, map[string]any, []byte (as base64 text under a
// marker) or time.Time. Anything else comes back as its description.
func cfToGo(v obj.Object) any {
	if v == nil {
		return nil
	}
	switch corefoundation.CFGetTypeID(v) {
	case corefoundation.CFStringGetTypeID():
		return cfToString(v)
	case corefoundation.CFBooleanGetTypeID():
		return corefoundation.CFBooleanGetValue(corefoundation.CFBooleanRef{Object: v}) != 0
	case corefoundation.CFNumberGetTypeID():
		n := corefoundation.CFNumberRef{Object: v}
		if corefoundation.CFNumberIsFloatType(n) != 0 {
			var f float64
			corefoundation.CFNumberGetValue(n, corefoundation.KCFNumberDoubleType, unsafe.Pointer(&f))
			return f
		}
		var i int64
		corefoundation.CFNumberGetValue(n, corefoundation.KCFNumberSInt64Type, unsafe.Pointer(&i))
		return i
	case corefoundation.CFArrayGetTypeID():
		arr := corefoundation.CFArrayRef{Object: v}
		n := corefoundation.CFArrayGetCount(arr)
		out := make([]any, 0, n)
		for i := 0; i < n; i++ {
			p := corefoundation.CFArrayGetValueAtIndex(arr, i)
			if p == nil {
				out = append(out, nil)
				continue
			}
			out = append(out, cfToGo(obj.WrapUnmanaged(objc.ID(uintptr(p)))))
		}
		return out
	case corefoundation.CFDictionaryGetTypeID():
		dict := corefoundation.CFDictionaryRef{Object: v}
		n := corefoundation.CFDictionaryGetCount(dict)
		out := make(map[string]any, n)
		if n == 0 {
			return out
		}
		keys := make([]uintptr, n)
		vals := make([]uintptr, n)
		corefoundation.CFDictionaryGetKeysAndValues(dict, unsafe.Pointer(&keys[0]), unsafe.Pointer(&vals[0]))
		for i := 0; i < n; i++ {
			k := cfToString(obj.WrapUnmanaged(objc.ID(keys[i])))
			if vals[i] == 0 {
				out[k] = nil
				continue
			}
			out[k] = cfToGo(obj.WrapUnmanaged(objc.ID(vals[i])))
		}
		return out
	case corefoundation.CFDataGetTypeID():
		d := corefoundation.CFDataRef{Object: v}
		n := corefoundation.CFDataGetLength(d)
		p := corefoundation.CFDataGetBytePtr(d)
		if n == 0 || p == nil {
			return map[string]any{"data": ""}
		}
		raw := make([]byte, n)
		copy(raw, unsafe.Slice((*byte)(p), n))
		return map[string]any{"data": base64.StdEncoding.EncodeToString(raw)}
	case corefoundation.CFDateGetTypeID():
		secs := corefoundation.CFDateGetAbsoluteTime(corefoundation.CFDateRef{Object: v})
		return cfEpoch.Add(time.Duration(secs * float64(time.Second)))
	default:
		return v.Description()
	}
}

// goToCF converts a scalar Go value to a property-list value the caller owns.
// Structured values (arrays, dictionaries) are written through the defaults
// CLI with a plist fragment instead; see Defaults.Set.
func goToCF(v any) (obj.Object, error) {
	switch x := v.(type) {
	case string:
		return cfTemp(x), nil
	case bool:
		if x {
			return corefoundation.KCFBooleanTrue(), nil
		}
		return corefoundation.KCFBooleanFalse(), nil
	case int:
		i := int64(x)
		return corefoundation.CFNumberCreate(
			corefoundation.CFAllocatorRef{},
			corefoundation.KCFNumberSInt64Type,
			unsafe.Pointer(&i),
		), nil
	case int64:
		i := x
		return corefoundation.CFNumberCreate(
			corefoundation.CFAllocatorRef{},
			corefoundation.KCFNumberSInt64Type,
			unsafe.Pointer(&i),
		), nil
	case float64:
		f := x
		return corefoundation.CFNumberCreate(
			corefoundation.CFAllocatorRef{},
			corefoundation.KCFNumberDoubleType,
			unsafe.Pointer(&f),
		), nil
	default:
		return nil, fmt.Errorf("%w: %T", ErrUnsupportedPreferenceValue, v)
	}
}
