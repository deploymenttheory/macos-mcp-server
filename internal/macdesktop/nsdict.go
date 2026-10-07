//go:build darwin && (amd64 || arm64)

package macdesktop

import (
	"github.com/ebitengine/purego/objc"

	"github.com/deploymenttheory/go-bindings-macosplatform/bindings/runtime/obj"
	"github.com/deploymenttheory/go-bindings-macosplatform/bindings/runtime/purego"
)

// nsDictionaryNumber reads an NSNumber value out of an NSDictionary by key,
// returning 0 when absent. It sends the messages directly because the generic
// Object the SDK hands back for a dictionary has no typed accessor.
func nsDictionaryNumber(dict obj.Object, key string) int64 {
	if dict == nil {
		return 0
	}
	k := purego.NSString(key)
	v := obj.ID(dict).Send(objc.RegisterName("objectForKey:"), k)
	if v == 0 {
		return 0
	}
	r := v.Send(objc.RegisterName("longLongValue"))
	ll := int64(r) //nolint:gosec // the selector returns a long long in the register
	return ll
}
