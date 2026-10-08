//go:build darwin && (amd64 || arm64)

package macdesktop

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"time"
	"unsafe"

	"github.com/ebitengine/purego/objc"

	"github.com/deploymenttheory/go-bindings-macosplatform/bindings/frameworks/corefoundation"
	"github.com/deploymenttheory/go-bindings-macosplatform/bindings/frameworks/coregraphics"
	"github.com/deploymenttheory/go-bindings-macosplatform/bindings/frameworks/imageio"
	"github.com/deploymenttheory/go-bindings-macosplatform/bindings/frameworks/screencapturekit"
	"github.com/deploymenttheory/go-bindings-macosplatform/bindings/runtime/obj"
	"github.com/deploymenttheory/macos-mcp-server/internal/clirunner"
)

// maxScreenshotWidth bounds the encoded image width; larger captures are
// downscaled by an integer factor so the PNG stays a reasonable size.
const maxScreenshotWidth = 1920

// Errors the capture path returns.
var (
	ErrScreenRecordingDenied = errors.New("screen recording is not granted to this process")
	ErrNoDisplays            = errors.New("no displays")
	ErrCaptureFailed         = errors.New("screen capture failed")
	ErrCaptureTimedOut       = errors.New("screen capture timed out")
	ErrEncodeFailed          = errors.New("png encode failed")
)

// Screenshot captures every display as one PNG. It returns the PNG bytes, the
// encoded image size, and the factor that converts an image pixel back to a
// screen point (Snapshot coordinates): multiply image coordinates by it.
//
// Deliberately not routed through Do: ScreenCaptureKit is asynchronous and
// completes on its own queue, and a capture must not hold the engine's
// serialised thread for the duration.
func (d *Desktop) Screenshot() (pngData []byte, width, height int, pointsPerPixel float64, err error) {
	if !coregraphics.CGPreflightScreenCaptureAccess() {
		return nil, 0, 0, 0, ErrScreenRecordingDenied
	}
	bounds := unionDisplayBounds()
	if bounds.Size.Width == 0 || bounds.Size.Height == 0 {
		return nil, 0, 0, 0, ErrNoDisplays
	}

	img, err := captureRect(bounds)
	if err != nil {
		d.logger.Debug("ScreenCaptureKit capture failed, falling back to screencapture(1)", "error", err)
		img, err = captureWithCLI()
		if err != nil {
			return nil, 0, 0, 0, err
		}
	}

	pixelWidth := img.Bounds().Dx()
	denom := 1
	for pixelWidth/denom > maxScreenshotWidth {
		denom++
	}
	if denom > 1 {
		img = downscale(img, denom)
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return nil, 0, 0, 0, fmt.Errorf("png encode: %w", err)
	}
	// pixels per point is the capture width over the point width; the caller
	// multiplies image coordinates by points-per-image-pixel.
	pixelsPerPoint := float64(pixelWidth) / bounds.Size.Width
	ppp := float64(denom) / pixelsPerPoint
	return out.Bytes(), img.Bounds().Dx(), img.Bounds().Dy(), ppp, nil
}

// unionDisplayBounds is the rectangle covering every active display, in points.
func unionDisplayBounds() corefoundation.CGRect {
	ids := make([]uint32, maxDisplays)
	_, count := coregraphics.CGGetActiveDisplayList(maxDisplays, unsafe.Pointer(&ids[0]))
	var u corefoundation.CGRect
	for i, id := range ids[:count] {
		b := coregraphics.CGDisplayBounds(id)
		if i == 0 {
			u = b
			continue
		}
		minX, minY := min(u.Origin.X, b.Origin.X), min(u.Origin.Y, b.Origin.Y)
		maxX := max(u.Origin.X+u.Size.Width, b.Origin.X+b.Size.Width)
		maxY := max(u.Origin.Y+u.Size.Height, b.Origin.Y+b.Size.Height)
		u = corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: minX, Y: minY},
			Size:   corefoundation.CGSize{Width: maxX - minX, Height: maxY - minY},
		}
	}
	return u
}

// captureRect captures a display-space rectangle through ScreenCaptureKit's
// screenshot manager and decodes it to a Go image.
func captureRect(rect corefoundation.CGRect) (image.Image, error) {
	type result struct {
		img uintptr
		err uintptr
	}
	ch := make(chan result, 1)
	screencapturekit.CaptureImageInRectCompletionHandler(rect, func(img unsafe.Pointer, err unsafe.Pointer) {
		ch <- result{img: uintptr(img), err: uintptr(err)}
	})
	select {
	case r := <-ch:
		if r.img == 0 {
			msg := "capture returned no image"
			if r.err != 0 {
				msg = obj.Wrap(objc.ID(r.err)).Description()
			}
			return nil, fmt.Errorf("%w: %s", ErrCaptureFailed, msg)
		}
		// The completion handler's image is +0 for the duration of the callback;
		// Wrap retains it for us.
		cg := coregraphics.CGImageRef{Object: obj.Wrap(objc.ID(r.img))}
		defer cg.Release()
		pngBytes, err := encodeCGImagePNG(cg)
		if err != nil {
			return nil, err
		}
		img, err := png.Decode(bytes.NewReader(pngBytes))
		if err != nil {
			return nil, fmt.Errorf("decode captured png: %w", err)
		}
		return img, nil
	case <-time.After(screenshotTimeout):
		return nil, ErrCaptureTimedOut
	}
}

// encodeCGImagePNG renders a CGImage as PNG bytes through ImageIO.
func encodeCGImagePNG(img coregraphics.CGImageRef) ([]byte, error) {
	data := corefoundation.CFDataCreateMutable(corefoundation.CFAllocatorRef{}, 0)
	if data.IsNil() {
		return nil, fmt.Errorf("%w: CFDataCreateMutable", ErrEncodeFailed)
	}
	defer data.Release()
	dest := imageio.CGImageDestinationCreateWithData(data, cfStr("public.png"), 1, corefoundation.CFDictionaryRef{})
	if dest.IsNil() {
		return nil, fmt.Errorf("%w: CGImageDestinationCreateWithData", ErrEncodeFailed)
	}
	defer dest.Release()
	imageio.CGImageDestinationAddImage(dest, img, corefoundation.CFDictionaryRef{})
	if !imageio.CGImageDestinationFinalize(dest) {
		return nil, fmt.Errorf("%w: CGImageDestinationFinalize", ErrEncodeFailed)
	}
	n := corefoundation.CFDataGetLength(corefoundation.CFDataRef(data))
	p := corefoundation.CFDataGetBytePtr(corefoundation.CFDataRef(data))
	if n == 0 || p == nil {
		return nil, fmt.Errorf("%w: empty png data", ErrEncodeFailed)
	}
	out := make([]byte, n)
	copy(out, unsafe.Slice((*byte)(p), n))
	return out, nil
}

// captureWithCLI is the fallback: screencapture(1) writes a PNG to a temporary
// file. It needs the same Screen Recording grant.
func captureWithCLI() (image.Image, error) {
	dir, err := os.MkdirTemp("", "macos-mcp-shot-")
	if err != nil {
		return nil, fmt.Errorf("temp dir: %w", err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "shot.png")
	ctx, cancel := context.WithTimeout(context.Background(), screenshotTimeout)
	defer cancel()
	if _, err := clirunner.Run(
		ctx,
		[]string{"screencapture", "-x", "-t", "png", path},
		clirunner.Options{},
	); err != nil {
		return nil, fmt.Errorf("screencapture: %w", err)
	}
	f, err := os.Open(path) //nolint:gosec // a path this function just created
	if err != nil {
		return nil, fmt.Errorf("read capture: %w", err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("decode capture: %w", err)
	}
	return img, nil
}

// downscale shrinks img by an integer factor with box averaging.
func downscale(img image.Image, factor int) image.Image {
	b := img.Bounds()
	w, h := b.Dx()/factor, b.Dy()/factor
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	n := uint32(factor * factor) //nolint:gosec // factor is a small positive integer
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var r, g, bl, a uint32
			for dy := 0; dy < factor; dy++ {
				for dx := 0; dx < factor; dx++ {
					pr, pg, pb, pa := img.At(b.Min.X+x*factor+dx, b.Min.Y+y*factor+dy).RGBA()
					r += pr >> 8
					g += pg >> 8
					bl += pb >> 8
					a += pa >> 8
				}
			}
			i := out.PixOffset(x, y)
			// Each channel is an average of 8-bit samples, so it fits a byte.
			out.Pix[i+0] = uint8(r / n)  //nolint:gosec // see above
			out.Pix[i+1] = uint8(g / n)  //nolint:gosec // see above
			out.Pix[i+2] = uint8(bl / n) //nolint:gosec // see above
			out.Pix[i+3] = uint8(a / n)  //nolint:gosec // see above
		}
	}
	return out
}
