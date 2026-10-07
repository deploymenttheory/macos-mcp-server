//go:build darwin && (amd64 || arm64)

package macguard

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/deploymenttheory/agentweave-harness/guardrails/contain"
)

// Actuator implements contain.SystemActuator for macOS: the kill ladder's
// isolate → kill-procs → lock → shutdown rungs.
type Actuator struct {
	Logger   *slog.Logger
	Run      Runner
	StateDir string
	// Kill sends a signal to a pid; nil means syscall.Kill. Injected so the
	// process rung is testable without killing anything.
	Kill func(pid int, sig syscall.Signal) error
	// GracePeriod is how long a process gets after SIGTERM before SIGKILL.
	GracePeriod time.Duration
	// Elevation overrides the root check, for tests. nil means Elevated().
	Elevation func() bool
}

var _ contain.SystemActuator = (*Actuator)(nil)

// NewActuator returns the macOS actuator with the production runner.
func NewActuator(logger *slog.Logger) *Actuator {
	return &Actuator{Logger: logger, GracePeriod: 3 * time.Second}
}

func (a *Actuator) run() Runner {
	if a.Run != nil {
		return a.Run
	}
	return defaultRunner
}

func (a *Actuator) logger() *slog.Logger {
	if a.Logger != nil {
		return a.Logger
	}
	return slog.New(slog.DiscardHandler)
}

// Elevated reports whether the process runs as root.
func (a *Actuator) Elevated() bool {
	if a.Elevation != nil {
		return a.Elevation()
	}
	return Elevated()
}

// IsolateNetwork blocks all non-loopback traffic through a dedicated pf
// anchor and returns the restore. The state record is written first so a
// crash during containment is recoverable by the next start's Recover; the
// observed rules are what pfctl reads back, not what was asked.
func (a *Actuator) IsolateNetwork() (func() error, []string, error) {
	if !a.Elevated() {
		return nil, nil, ErrNotElevated
	}
	ctx := context.Background()
	enf := &Enforcer{Logger: a.Logger, Run: a.Run, StateDir: a.StateDir, Elevation: a.Elevation}

	rules := RenderIsolation()
	st, found, err := readState(a.StateDir)
	if err != nil {
		return nil, nil, err
	}
	if !found {
		st = EnforcementState{PID: os.Getpid()}
	}
	st.Anchors = appendUnique(st.Anchors, IsolateAnchor)
	st.RuleCount += RuleCount(rules)
	if err := writeState(a.StateDir, st); err != nil {
		return nil, nil, err
	}
	if err := enf.loadAnchor(ctx, IsolateAnchor, rules); err != nil {
		return nil, nil, err
	}
	token := st.PFToken
	if token == "" {
		if token, err = enf.enablePF(ctx); err != nil {
			_ = enf.flushAnchor(ctx, IsolateAnchor)
			return nil, nil, err
		}
		st.PFToken = token
		_ = writeState(a.StateDir, st)
	}
	observed, oerr := enf.Observe(ctx, IsolateAnchor)
	if oerr != nil {
		observed = []string{"read-error: " + oerr.Error()}
	}
	restore := func() error {
		var errs []error
		errs = append(errs, enf.flushAnchor(ctx, IsolateAnchor))
		cur, found, rerr := readState(a.StateDir)
		if rerr != nil {
			return errors.Join(append(errs, rerr)...)
		}
		if !found {
			return errors.Join(errs...)
		}
		cur.Anchors = removeString(cur.Anchors, IsolateAnchor)
		cur.RuleCount -= RuleCount(rules)
		if len(cur.Anchors) == 0 && len(cur.SystemProxy) == 0 {
			if cur.PFToken != "" {
				errs = append(errs, enf.releasePF(ctx, cur.PFToken))
			}
			errs = append(errs, clearState(a.StateDir))
			return errors.Join(errs...)
		}
		return errors.Join(append(errs, writeState(a.StateDir, cur))...)
	}
	return restore, observed, nil
}

// KillProcesses terminates processes whose executable name matches, SIGTERM
// first and SIGKILL after the grace period. Only processes this user may
// signal are affected; the rest are reported as errors.
func (a *Actuator) KillProcesses(names []string) []error {
	var errs []error
	kill := a.Kill
	if kill == nil {
		kill = syscall.Kill
	}
	grace := a.GracePeriod
	if grace <= 0 {
		grace = 3 * time.Second
	}
	ctx := context.Background()
	var pids []int
	for _, name := range names {
		found, err := a.pidsNamed(ctx, name)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		pids = append(pids, found...)
	}
	for _, pid := range pids {
		if pid == os.Getpid() {
			continue
		}
		if err := kill(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
			errs = append(errs, fmt.Errorf("terminate pid %d: %w", pid, err))
		}
	}
	if len(pids) > 0 {
		deadline := time.Now().Add(grace)
		for time.Now().Before(deadline) {
			alive := false
			for _, pid := range pids {
				if err := kill(pid, 0); err == nil {
					alive = true
					break
				}
			}
			if !alive {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		for _, pid := range pids {
			if err := kill(pid, 0); err == nil {
				if err := kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
					errs = append(errs, fmt.Errorf("kill pid %d: %w", pid, err))
				}
			}
		}
	}
	return errs
}

// pidsNamed resolves an executable name to pids with pgrep -x, which matches
// the whole process name rather than a substring.
func (a *Actuator) pidsNamed(ctx context.Context, name string) ([]int, error) {
	name = strings.TrimSuffix(name, ".app")
	out, _, err := a.run()(ctx, []string{"pgrep", "-x", name})
	if err != nil {
		// pgrep exits 1 when nothing matches; that is not an error here.
		if strings.TrimSpace(out) == "" {
			return nil, nil
		}
		return nil, err
	}
	var pids []int
	for _, f := range strings.Fields(out) {
		if pid, err := strconv.Atoi(f); err == nil {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}

// LockWorkstation locks the interactive session. The Lock Screen shortcut
// through System Events needs no privilege; display sleep is the fallback,
// which locks when the screen saver requires a password.
func (a *Actuator) LockWorkstation() error {
	ctx := context.Background()
	_, _, err := a.run()(ctx, []string{
		"osascript", "-e",
		`tell application "System Events" to keystroke "q" using {control down, command down}`,
	})
	if err == nil {
		return nil
	}
	a.logger().Warn("lock via System Events failed; falling back to display sleep", "error", err)
	if _, _, err2 := a.run()(ctx, []string{"pmset", "displaysleepnow"}); err2 != nil {
		return fmt.Errorf("lock workstation: %w", errors.Join(err, err2))
	}
	return nil
}

// Shutdown halts the machine after delay. Root uses shutdown(8); otherwise
// System Events is asked, which may prompt the console user.
func (a *Actuator) Shutdown(reason string, delay time.Duration) error {
	ctx := context.Background()
	a.logger().Error("shutdown requested by the kill ladder", "reason", reason, "delay", delay)
	if a.Elevated() {
		when := "now"
		if delay > 0 {
			mins := int((delay + time.Minute - 1) / time.Minute)
			when = "+" + strconv.Itoa(max(mins, 1))
		}
		if _, stderr, err := a.run()(ctx, []string{"shutdown", "-h", when, reason}); err != nil {
			return fmt.Errorf("shutdown: %w: %s", err, strings.TrimSpace(stderr))
		}
		return nil
	}
	if delay > 0 {
		time.Sleep(delay)
	}
	if _, stderr, err := a.run()(
		ctx,
		[]string{"osascript", "-e", `tell application "System Events" to shut down`},
	); err != nil {
		return fmt.Errorf("shutdown via System Events: %w: %s", err, strings.TrimSpace(stderr))
	}
	return nil
}

// ErrNotElevated reports a containment action that needs root.
var ErrNotElevated = errors.New("network isolation requires root")

func appendUnique(list []string, s string) []string {
	for _, v := range list {
		if v == s {
			return list
		}
	}
	return append(list, s)
}

func removeString(list []string, s string) []string {
	out := list[:0]
	for _, v := range list {
		if v != s {
			out = append(out, v)
		}
	}
	return out
}
