//go:build darwin && (amd64 || arm64)

package macdesktop

import (
	"errors"
	"testing"

	"github.com/deploymenttheory/go-bindings-macosplatform/opinionated/tools/grandcentraldispatch/mainthread"
)

// TestDoRunsOnTheMainThread pins the engine's central constraint.
func TestDoRunsOnTheMainThread(t *testing.T) {
	d, err := New(nil, Options{})
	if err != nil {
		t.Skipf("engine unavailable: %v", err)
	}
	defer d.Close()
	var onMain bool
	if err := d.Do(func() error { onMain = mainthread.IsMain(); return nil }); err != nil {
		t.Fatal(err)
	}
	if !onMain {
		t.Fatal("Do must run its job on the main thread")
	}
}

func TestDoRecoversPanics(t *testing.T) {
	d, err := New(nil, Options{})
	if err != nil {
		t.Skipf("engine unavailable: %v", err)
	}
	defer d.Close()
	err = d.Do(func() error { panic("boom") })
	if err == nil {
		t.Fatal("a panicking job must surface as an error, not kill the engine")
	}
	if err := d.Do(func() error { return nil }); err != nil {
		t.Fatalf("the engine must keep working after a recovered panic: %v", err)
	}
	if !d.Alive() {
		t.Fatal("engine should report alive")
	}
}

func TestClosedEngineRefusesWork(t *testing.T) {
	d, err := New(nil, Options{})
	if err != nil {
		t.Skipf("engine unavailable: %v", err)
	}
	_ = d.Close()
	if err := d.Do(func() error { return nil }); !errors.Is(err, ErrEngineClosed) {
		t.Fatalf("want ErrEngineClosed, got %v", err)
	}
}

// TestSnapshotLive walks the real desktop. Skips without Accessibility.
func TestSnapshotLive(t *testing.T) {
	d := requireDesktop(t)
	state, err := d.Snapshot(SnapshotOptions{})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	t.Logf("foreground=%q windows=%d interactive=%d", state.Foreground.Title, len(state.Windows), len(state.Interactive))
	if len(state.Windows) == 0 {
		t.Skip("no windows on this desktop")
	}
	if state.TreeText == "" {
		t.Error("a desktop with windows should render a tree")
	}
	if d.LastState() != state {
		t.Error("LastState must return the snapshot just taken")
	}
}

func TestDisplaysLive(t *testing.T) {
	d, err := New(nil, Options{})
	if err != nil {
		t.Skipf("engine unavailable: %v", err)
	}
	defer d.Close()
	ds, err := d.Displays()
	if err != nil {
		t.Fatal(err)
	}
	if len(ds) == 0 {
		t.Skip("no displays (headless)")
	}
	primary := 0
	for _, disp := range ds {
		if disp.Primary {
			primary++
		}
		if disp.Bounds.Empty() {
			t.Errorf("display %d has empty bounds", disp.Index)
		}
	}
	if primary != 1 {
		t.Errorf("want exactly one primary display, got %d", primary)
	}
}

func TestProcessListLive(t *testing.T) {
	d, err := New(nil, Options{})
	if err != nil {
		t.Skipf("engine unavailable: %v", err)
	}
	defer d.Close()
	procs, err := d.ProcessList("pid", 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range procs {
		if p.PID == 1 && p.Name == "launchd" {
			found = true
		}
	}
	if !found {
		t.Error("launchd (pid 1) must be in the process list")
	}
	if _, err := d.ProcessKill(0, "no-such-process-xyz", false); !errors.Is(err, ErrProcessNotFound) {
		t.Errorf("killing nothing should report ErrProcessNotFound, got %v", err)
	}
}
