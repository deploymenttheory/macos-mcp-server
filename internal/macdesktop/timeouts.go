//go:build darwin && (amd64 || arm64)

package macdesktop

import "time"

const (
	// aliveProbeTimeout bounds the liveness probe.
	aliveProbeTimeout = 2 * time.Second
	// axMessagingTimeout is how long one accessibility request may wait on the
	// target application before the engine gives up on it. Per application, so
	// a hung app costs one timeout, not the whole snapshot.
	axMessagingTimeout = 0.5
	// screenshotTimeout bounds a ScreenCaptureKit capture.
	screenshotTimeout = 10 * time.Second
)

func contextTimeout(d time.Duration) <-chan time.Time { return time.After(d) }
