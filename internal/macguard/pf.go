//go:build darwin && (amd64 || arm64)

package macguard

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/deploymenttheory/agentweave-harness/guardrails/egress"
)

// Enforcer implements egress.Enforcer over pf. It is the macOS counterpart of
// the harness's Windows Firewall enforcer, with one documented difference: pf
// matches on the socket owner, not the executable, so the "scoped" tier blocks
// the console user rather than the listed applications. The audit record and
// the status surface say so.
type Enforcer struct {
	Logger *slog.Logger
	// Run executes commands; nil means the clirunner default.
	Run Runner
	// StateDir overrides the recovery-state directory; empty means
	// DefaultStateDir.
	StateDir string
	// ConsoleUID overrides console-user detection, for tests. nil means
	// read /dev/console.
	ConsoleUID func() (int, error)
	// Elevation overrides the root check, for tests that drive the pfctl
	// sequence through a fake runner. nil means Elevated().
	Elevation func() bool

	mu      sync.Mutex
	current *RuleSpec
}

var _ egress.Enforcer = (*Enforcer)(nil)

// Elevated reports whether this process can change pf state.
func (e *Enforcer) Elevated() bool {
	if e.Elevation != nil {
		return e.Elevation()
	}
	return Elevated()
}

func (e *Enforcer) run() Runner {
	if e.Run != nil {
		return e.Run
	}
	return defaultRunner
}

func (e *Enforcer) logger() *slog.Logger {
	if e.Logger != nil {
		return e.Logger
	}
	return slog.New(slog.DiscardHandler)
}

func (e *Enforcer) consoleUID() (int, error) {
	if e.ConsoleUID != nil {
		return e.ConsoleUID()
	}
	return ConsoleUID()
}

// rulesPath is where the rendered ruleset is written for pfctl -f.
func (e *Enforcer) rulesPath() string {
	return filepath.Join(stateDirOr(e.StateDir), "pf-anchor.conf")
}

// Apply installs the rules described by spec and returns the undo. The
// recovery state is written before any change, so a crash mid-way is
// recoverable by the next start.
func (e *Enforcer) Apply(spec egress.EnforceSpec) (func() error, error) {
	noop := func() error { return nil }
	wantsRules := len(spec.Applications) > 0 || spec.GlobalBlock
	if !wantsRules && !spec.SetSystemProxy {
		return noop, nil
	}
	if !e.Elevated() {
		// networksetup needs an administrator too, so even the proxy-only
		// path is refused unprivileged rather than prompting on a console
		// nobody is watching.
		return nil, egress.ErrNotElevated
	}
	ctx := context.Background()
	run := e.run()

	rs := RuleSpec{ScopedUID: -1, GlobalBlock: spec.GlobalBlock, AllowPorts: spec.AllowPorts, ProxyUser: "root"}
	if _, port, err := net.SplitHostPort(spec.ProxyAddr); err == nil {
		rs.ProxyPort, _ = strconv.Atoi(port)
	}
	if len(spec.Applications) > 0 && !spec.GlobalBlock {
		uid, err := e.consoleUID()
		if err != nil {
			return nil, err
		}
		rs.ScopedUID = uid
		e.logger().Warn("egress scoped tier is uid-scoped on macOS: pf cannot match on an executable",
			"uid", uid, "applications_requested", len(spec.Applications))
	}

	st := EnforcementState{
		PID: os.Getpid(), Listen: spec.ProxyAddr,
		GlobalBlock: rs.GlobalBlock, ScopedUID: rs.ScopedUID,
	}
	rules := ""
	if wantsRules {
		rules = RenderRules(rs)
		st.Anchors = []string{EgressAnchor}
		st.RuleCount = RuleCount(rules)
	}
	// State first, then mutation. See writeState.
	if err := writeState(e.StateDir, st); err != nil {
		return nil, err
	}

	undo := func() error {
		var errs []error
		if wantsRules {
			errs = append(errs, e.flushAnchor(ctx, EgressAnchor))
		}
		if st.PFToken != "" {
			errs = append(errs, e.releasePF(ctx, st.PFToken))
		}
		if len(st.SystemProxy) > 0 {
			errs = append(errs, restoreSystemProxy(ctx, run, st.SystemProxy))
		}
		e.mu.Lock()
		e.current = nil
		e.mu.Unlock()
		if err := errors.Join(errs...); err != nil {
			return err
		}
		return clearState(e.StateDir)
	}

	if wantsRules {
		if err := e.loadAnchor(ctx, EgressAnchor, rules); err != nil {
			_ = undo()
			return nil, err
		}
		token, err := e.enablePF(ctx)
		if err != nil {
			_ = undo()
			return nil, err
		}
		st.PFToken = token
		if err := writeState(e.StateDir, st); err != nil {
			_ = undo()
			return nil, err
		}
	}
	if spec.SetSystemProxy {
		saved, err := setSystemProxy(ctx, run, spec.ProxyAddr)
		st.SystemProxy = saved
		if werr := writeState(e.StateDir, st); werr != nil && err == nil {
			err = werr
		}
		if err != nil {
			_ = undo()
			return nil, err
		}
	}
	e.mu.Lock()
	e.current = &rs
	e.mu.Unlock()
	return undo, nil
}

// Recover undoes whatever a previously crashed run left behind and reports
// how many rules it removed.
func (e *Enforcer) Recover() (int, error) {
	st, found, err := readState(e.StateDir)
	if err != nil || !found {
		return 0, err
	}
	if !e.Elevated() {
		return 0, fmt.Errorf("%w: a previous session left egress rules installed (%s)",
			egress.ErrNotElevated, statePath(e.StateDir))
	}
	ctx := context.Background()
	var errs []error
	for _, anchor := range st.Anchors {
		errs = append(errs, e.flushAnchor(ctx, anchor))
	}
	if st.PFToken != "" {
		errs = append(errs, e.releasePF(ctx, st.PFToken))
	}
	if len(st.SystemProxy) > 0 {
		errs = append(errs, restoreSystemProxy(ctx, e.run(), st.SystemProxy))
	}
	if err := errors.Join(errs...); err != nil {
		return 0, err
	}
	if err := clearState(e.StateDir); err != nil {
		return 0, err
	}
	e.logger().Warn("recovered egress rules left by a previous session", "pid", st.PID, "rules", st.RuleCount)
	return st.RuleCount, nil
}

// Suspend weakens egress without restoring anything, for the kill path: the
// anchor is reloaded with its block rules only, so neither the proxy owner nor
// the exempted daemons keep a route out during containment.
func (e *Enforcer) Suspend() error {
	e.mu.Lock()
	rs := e.current
	e.mu.Unlock()
	if rs == nil {
		return nil
	}
	return e.loadAnchor(context.Background(), EgressAnchor, RenderSuspended(*rs))
}

// loadAnchor writes the ruleset to disk and loads it into the anchor,
// replacing whatever the anchor held.
func (e *Enforcer) loadAnchor(ctx context.Context, anchor, rules string) error {
	if err := ensureDir(stateDirOr(e.StateDir)); err != nil {
		return err
	}
	if err := writeFileAtomic(e.rulesPath(), []byte(rules), 0o600); err != nil {
		return err
	}
	if _, stderr, err := e.run()(ctx, []string{"pfctl", "-q", "-a", anchor, "-f", e.rulesPath()}); err != nil {
		return fmt.Errorf("load pf anchor %s: %w: %s", anchor, err, strings.TrimSpace(stderr))
	}
	return nil
}

// flushAnchor removes every rule from the anchor.
func (e *Enforcer) flushAnchor(ctx context.Context, anchor string) error {
	if _, stderr, err := e.run()(ctx, []string{"pfctl", "-q", "-a", anchor, "-F", "all"}); err != nil {
		return fmt.Errorf("flush pf anchor %s: %w: %s", anchor, err, strings.TrimSpace(stderr))
	}
	return nil
}

var pfTokenRE = regexp.MustCompile(`Token\s*:\s*(\d+)`)

// enablePF takes a reference on pf and returns the token to release it with.
// pf may already be enabled (Apple's own services take references); the token
// model means this run only ever releases what it took.
func (e *Enforcer) enablePF(ctx context.Context) (string, error) {
	stdout, stderr, err := e.run()(ctx, []string{"pfctl", "-E"})
	if err != nil {
		return "", fmt.Errorf("enable pf: %w: %s", err, strings.TrimSpace(stderr))
	}
	if m := pfTokenRE.FindStringSubmatch(stdout + "\n" + stderr); m != nil {
		return m[1], nil
	}
	return "", fmt.Errorf("%w: %s", ErrNoPFToken, strings.TrimSpace(stdout+stderr))
}

// releasePF drops the reference enablePF took.
func (e *Enforcer) releasePF(ctx context.Context, token string) error {
	if _, stderr, err := e.run()(ctx, []string{"pfctl", "-X", token}); err != nil {
		return fmt.Errorf("release pf token: %w: %s", err, strings.TrimSpace(stderr))
	}
	return nil
}

// Observe reads the anchor's rules back through pfctl, so an audit record can
// carry what the OS says rather than what was asked.
func (e *Enforcer) Observe(ctx context.Context, anchor string) ([]string, error) {
	out, stderr, err := e.run()(ctx, []string{"pfctl", "-q", "-a", anchor, "-sr"})
	if err != nil {
		return nil, fmt.Errorf("read pf anchor %s: %w: %s", anchor, err, strings.TrimSpace(stderr))
	}
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	return lines, nil
}

// ErrNoPFToken reports that pfctl -E did not print a reference token.
var ErrNoPFToken = errors.New("pfctl -E returned no token")
