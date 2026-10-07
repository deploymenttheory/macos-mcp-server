//go:build darwin && (amd64 || arm64)

package macmcp

import (
	"os"
	"runtime"
	"testing"

	"github.com/deploymenttheory/go-bindings-macosplatform/opinionated/tools/grandcentraldispatch/mainthread"
)

func init() { runtime.LockOSThread() }

// TestMain keeps the main thread serviced while the tests run on a goroutine:
// the conformance host test builds a real desktop engine, whose calls
// dispatch to the main queue. This is the test-side half of the main-thread
// rule, the same shape as internal/macdesktop.
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
