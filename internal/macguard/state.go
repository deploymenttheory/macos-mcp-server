//go:build darwin && (amd64 || arm64)

package macguard

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// The recovery state exists because pf rules outlive the process that made
// them. A session killed with SIGKILL, a machine powered off mid-run, or a
// panic past the deferred cleanup all leave the anchor loaded with nothing
// left to explain it — and the console user stays blocked with no server
// running to proxy them. So the state is written down before any rule is
// loaded, and the next start removes whatever the file names.
const stateFileName = "egress-state.json"

// EnforcementState is what a future run needs to undo this one.
type EnforcementState struct {
	// PID and Listen are diagnostic: an operator finding rules on a machine
	// wants to know what put them there.
	PID    int    `json:"pid"`
	Listen string `json:"listen"`
	// Anchors lists every anchor this run loaded.
	Anchors []string `json:"anchors"`
	// RuleCount is how many rules were loaded, reported by Recover.
	RuleCount int `json:"rule_count"`
	// PFToken is the pfctl -E reference token to release, when this run
	// enabled pf. Zero means pf was already enabled and must be left alone.
	PFToken string `json:"pf_token,omitempty"`
	// GlobalBlock and ScopedUID record the tier, for the audit of a recovery.
	GlobalBlock bool `json:"global_block,omitempty"`
	ScopedUID   int  `json:"scoped_uid,omitempty"`
	// SystemProxy is the per-service proxy configuration to put back.
	SystemProxy []SavedProxy `json:"system_proxy,omitempty"`
}

func statePath(dir string) string { return filepath.Join(stateDirOr(dir), stateFileName) }

// writeState records the rules about to be loaded. It is called before the
// first pfctl call, never after: a crash between writing and loading leaves a
// file naming an anchor that is empty, and flushing an empty anchor is a
// no-op. The reverse order would leave real rules with nothing recording them.
func writeState(dir string, st EnforcementState) error {
	if err := ensureDir(stateDirOr(dir)); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("encode egress state: %w", err)
	}
	return writeFileAtomic(statePath(dir), raw, 0o600)
}

// readState returns the recorded state, or found=false when there is none.
func readState(dir string) (EnforcementState, bool, error) {
	raw, err := os.ReadFile(statePath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return EnforcementState{}, false, nil
	}
	if err != nil {
		return EnforcementState{}, false, fmt.Errorf("read egress state: %w", err)
	}
	var st EnforcementState
	if err := json.Unmarshal(raw, &st); err != nil {
		return EnforcementState{}, false, fmt.Errorf("decode egress state: %w", err)
	}
	return st, true, nil
}

// clearState removes the record once its rules are gone.
func clearState(dir string) error {
	if err := os.Remove(statePath(dir)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove egress state: %w", err)
	}
	return nil
}
