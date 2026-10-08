//go:build darwin && (amd64 || arm64)

package macdesktop

import (
	"unsafe"

	"github.com/deploymenttheory/go-bindings-macosplatform/bindings/frameworks/appkit"
	"github.com/deploymenttheory/go-bindings-macosplatform/bindings/frameworks/coregraphics"
)

// DisplayInfo describes one connected display.
type DisplayInfo struct {
	Index   int    `json:"index"`
	ID      uint32 `json:"id"`
	Name    string `json:"name,omitempty"`
	Primary bool   `json:"primary"`
	// Bounds is the display rectangle in global coordinates (points).
	Bounds Rect `json:"bounds"`
	// WorkArea is the usable area excluding the menu bar and Dock.
	WorkArea Rect `json:"work_area"`
	// PixelWidth and PixelHeight are the backing resolution.
	PixelWidth  int `json:"pixel_width"`
	PixelHeight int `json:"pixel_height"`
	// Scale is the backing scale factor (2 on Retina displays).
	Scale float64 `json:"scale"`
}

// maxDisplays bounds the enumeration.
const maxDisplays = 16

// Displays enumerates the active displays.
func (d *Desktop) Displays() ([]DisplayInfo, error) {
	var out []DisplayInfo
	err := d.Do(func() error {
		out = displays()
		return nil
	})
	return out, err
}

// displays reads the display list. Main thread (NSScreen).
func displays() []DisplayInfo {
	ids := make([]uint32, maxDisplays)
	result, count := coregraphics.CGGetActiveDisplayList(maxDisplays, unsafe.Pointer(&ids[0]))
	if result != coregraphics.KCGErrorSuccess || count == 0 {
		return nil
	}
	ids = ids[:count]

	// NSScreen carries the names, scale and visible frames, keyed by display
	// id in its device description; its coordinates are bottom-left based, so
	// they are converted to the top-left global space everything else uses.
	screens := map[uint32]*appkit.Screen{}
	var mainHeight float64
	for _, s := range appkit.Screens() {
		id := screenDisplayID(s)
		screens[id] = s
		if coregraphics.CGDisplayIsMain(id) != 0 {
			mainHeight = s.Frame().Size.Height
		}
	}

	out := make([]DisplayInfo, 0, len(ids))
	for i, id := range ids {
		b := coregraphics.CGDisplayBounds(id)
		info := DisplayInfo{
			Index:   i,
			ID:      id,
			Primary: coregraphics.CGDisplayIsMain(id) != 0,
			Bounds: Rect{
				Left: int(b.Origin.X), Top: int(b.Origin.Y),
				Right: int(b.Origin.X + b.Size.Width), Bottom: int(b.Origin.Y + b.Size.Height),
			},
			PixelWidth:  coregraphics.CGDisplayPixelsWide(id),
			PixelHeight: coregraphics.CGDisplayPixelsHigh(id),
			Scale:       1,
		}
		info.WorkArea = info.Bounds
		if s, ok := screens[id]; ok {
			info.Name = s.LocalizedName()
			info.Scale = s.BackingScaleFactor()
			vf := s.VisibleFrame()
			// Flip: NSScreen's y grows upward from the main display's bottom edge.
			top := mainHeight - (vf.Origin.Y + vf.Size.Height)
			info.WorkArea = Rect{
				Left: int(vf.Origin.X), Top: int(top),
				Right: int(vf.Origin.X + vf.Size.Width), Bottom: int(top + vf.Size.Height),
			}
		}
		out = append(out, info)
	}
	return out
}

// screenDisplayID reads the CGDirectDisplayID out of a screen's device
// description ("NSScreenNumber").
func screenDisplayID(s *appkit.Screen) uint32 {
	desc := s.DeviceDescription()
	if desc == nil {
		return 0
	}
	// NSDictionary -> objectForKey: through the generic Object description is
	// brittle; the frame match below is the fallback when the key is absent.
	n := nsDictionaryNumber(desc, "NSScreenNumber")
	if n != 0 {
		return uint32(n) //nolint:gosec // a CGDirectDisplayID is 32 bits; NSScreenNumber carries one
	}
	// Fall back to matching the display whose bounds share the screen's origin.
	ids := make([]uint32, maxDisplays)
	_, count := coregraphics.CGGetActiveDisplayList(maxDisplays, unsafe.Pointer(&ids[0]))
	f := s.Frame()
	for _, id := range ids[:count] {
		b := coregraphics.CGDisplayBounds(id)
		if b.Size.Width == f.Size.Width && b.Size.Height == f.Size.Height && b.Origin.X == f.Origin.X {
			return id
		}
	}
	return 0
}
