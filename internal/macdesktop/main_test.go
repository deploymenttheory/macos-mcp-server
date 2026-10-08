//go:build darwin && (amd64 || arm64)

package macdesktop

import (
	"os"
	"runtime"
	"testing"

	"github.com/deploymenttheory/go-bindings-macosplatform/opinionated/tools/grandcentraldispatch/mainthread"
)

func init() { runtime.LockOSThread() }

// TestMain keeps the main thread serviced while the tests run on a goroutine:
// Desktop.Do dispatches to the main queue, and a test binary's main thread is
// otherwise idle in the testing harness, which would deadlock every engine
// call. This is the test-side half of the main-thread rule.
func TestMain(m *testing.M) {
	done := make(chan int, 1)
	go func() { done <- m.Run() }()
	for {
		select {
		case code := <-done:
			os.Exit(code)
		default:
			mainthread.PumpMainRunLoop(0.05)
		}
	}
}

// requireDesktop skips a test that needs the live engine on a machine that
// cannot host it (no Accessibility grant, or no console session).
func requireDesktop(t *testing.T) *Desktop {
	t.Helper()
	d, err := New(nil, Options{})
	if err != nil {
		t.Skipf("engine unavailable: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if !d.Accessible() {
		t.Skip("needs the Accessibility grant")
	}
	return d
}
