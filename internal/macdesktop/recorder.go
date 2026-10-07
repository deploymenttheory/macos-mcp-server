//go:build darwin && (amd64 || arm64)

package macdesktop

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/deploymenttheory/macos-mcp-server/internal/clirunner"
)

// Session recording captures the whole session as a video file plus a JSONL
// marker log, so every session is tracked regardless of persona.
//
// The capture is screencapture(1)'s video mode, driven as a child process:
// it writes H.264 QuickTime straight to disk with the system's own encoder,
// needs the same Screen Recording grant everything else does, and finalises
// its file on SIGINT. ScreenCaptureKit's recording output is the in-process
// alternative; the SDK's idiomatic layer does not yet expose the stream
// initialiser it needs, so the CLI is the honest path for now.

// RecordingStatus describes an active recording.
type RecordingStatus struct {
	Recording   bool    `json:"recording"`
	VideoPath   string  `json:"video_path"`
	MarkersPath string  `json:"markers_path"`
	DurationSec float64 `json:"duration_sec"`
	Codec       string  `json:"codec"`
}

// Errors the recorder returns. ErrRecordingOff is the "not configured" answer,
// so a caller distinguishes off from failed.
var (
	ErrRecorderStart = errors.New("could not start the session recorder")
	ErrRecordingOff  = errors.New("session recording is not configured")
)

// recorder owns the screencapture child and the marker log.
type recorder struct {
	cmd     *exec.Cmd
	video   string
	markers string
	codec   string
	started time.Time

	mu    sync.Mutex
	marks *os.File
	done  bool
}

// recorderFinalizeTimeout bounds how long Close waits for the encoder to
// finish its file after SIGINT.
const recorderFinalizeTimeout = 15 * time.Second

// startRecorder begins a recording per opts. Returns nil when recording is
// off (no Dir).
func startRecorder(opts RecorderOptions) (*recorder, error) {
	if opts.Dir == "" {
		return nil, ErrRecordingOff
	}
	if err := os.MkdirAll(opts.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRecorderStart, err)
	}
	stamp := opts.Stamp
	if stamp == "" {
		stamp = time.Now().Format("20060102-150405")
	}
	codec := opts.Codec
	if codec == "" {
		codec = "h264"
	}
	video := filepath.Join(opts.Dir, "session-"+stamp+".mov")
	markers := filepath.Join(opts.Dir, "session-"+stamp+".markers.jsonl")

	exe, err := clirunner.LookPath("screencapture")
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRecorderStart, err)
	}
	// -v video, -x no sound, -k show clicks. The recording runs until SIGINT.
	argv := []string{"-v", "-x", "-k", video}
	cmd := exec.Command(exe, argv...) //nolint:gosec,noctx // argv-only; outlives any request context
	cmd.Env = clirunner.Environment()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRecorderStart, err)
	}
	marks, err := os.OpenFile(markers, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		_ = cmd.Process.Signal(syscall.SIGINT)
		return nil, fmt.Errorf("%w: markers: %w", ErrRecorderStart, err)
	}
	r := &recorder{cmd: cmd, video: video, markers: markers, codec: codec, started: time.Now(), marks: marks}
	r.writeMark("recording.started", "")
	return r, nil
}

// writeMark appends one marker.
func (r *recorder) writeMark(kind, label string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.marks == nil {
		return
	}
	entry := map[string]any{
		"t":     time.Since(r.started).Seconds(),
		"time":  time.Now().UTC().Format(time.RFC3339Nano),
		"kind":  kind,
		"label": label,
	}
	if b, err := json.Marshal(entry); err == nil {
		_, _ = r.marks.Write(append(b, '\n'))
	}
}

// status reports the recording.
func (r *recorder) status() RecordingStatus {
	return RecordingStatus{
		Recording: true, VideoPath: r.video, MarkersPath: r.markers,
		DurationSec: time.Since(r.started).Seconds(), Codec: r.codec,
	}
}

// close stops the encoder and waits for it to finalise. Idempotent.
func (r *recorder) close() {
	r.mu.Lock()
	if r.done {
		r.mu.Unlock()
		return
	}
	r.done = true
	r.mu.Unlock()
	r.writeMark("recording.stopped", "")
	_ = r.cmd.Process.Signal(syscall.SIGINT)
	waited := make(chan struct{})
	go func() { _ = r.cmd.Wait(); close(waited) }()
	select {
	case <-waited:
	case <-time.After(recorderFinalizeTimeout):
		_ = r.cmd.Process.Kill()
	}
	r.mu.Lock()
	if r.marks != nil {
		_ = r.marks.Close()
		r.marks = nil
	}
	r.mu.Unlock()
}

// RecordingStatus reports the session recording, and whether one is active.
func (d *Desktop) RecordingStatus() (RecordingStatus, bool) {
	if d.rec == nil {
		return RecordingStatus{}, false
	}
	return d.rec.status(), true
}

// MarkRecording adds a labelled marker to the timeline. It reports false when
// the session is not being recorded.
func (d *Desktop) MarkRecording(label string) bool {
	if d.rec == nil {
		return false
	}
	d.rec.writeMark("mark", label)
	return true
}

// FinalizeRecording stops the recorder synchronously. Idempotent; Close calls it.
func (d *Desktop) FinalizeRecording() {
	if d.rec != nil {
		d.rec.close()
	}
}
