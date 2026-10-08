//go:build darwin && (amd64 || arm64)

package macdesktop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/deploymenttheory/macos-mcp-server/internal/clirunner"
	"github.com/deploymenttheory/macos-mcp-server/internal/plist"
)

// launchd is the service manager and the scheduler. There is no public API for
// its bootstrap operations (ServiceManagement only manages a bundled app's own
// jobs), so the engine drives launchctl(1) through the argv-only runner and
// parses its output.

// ServiceInfo describes one launchd job.
type ServiceInfo struct {
	Label  string `json:"label"`
	Domain string `json:"domain"`
	PID    int    `json:"pid,omitempty"`
	// State is running, loaded (not running) or the last exit status.
	State      string `json:"state"`
	LastExit   int    `json:"last_exit"`
	Program    string `json:"program,omitempty"`
	Plist      string `json:"plist,omitempty"`
	Disabled   bool   `json:"disabled"`
	RunAtLoad  bool   `json:"run_at_load"`
	KeepAlive  bool   `json:"keep_alive"`
	Scheduled  string `json:"scheduled,omitempty"`
	DomainKind string `json:"domain_kind"`
}

// Errors the launchd operations return.
var (
	ErrServiceNotFound  = errors.New("no launchd job matched")
	ErrBadLabel         = errors.New("invalid launchd label")
	ErrSystemDomain     = errors.New("the system domain needs root")
	ErrBadTrigger       = errors.New("invalid trigger")
	ErrBadServiceAction = errors.New("unknown service action")
)

// launchdDomain names the target domain for launchctl: gui/<uid> for the
// console user, system for daemons. The system domain needs root; refusing
// early gives the model a reason rather than a launchctl error code.
func launchdDomain(system bool) (string, error) {
	if system {
		if os.Geteuid() != 0 {
			return "", ErrSystemDomain
		}
		return "system", nil
	}
	return fmt.Sprintf("gui/%d", os.Getuid()), nil
}

// labelPattern bounds what a label may contain: reverse-DNS style names.
var labelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,200}$`)

// ValidLabel reports whether s is an acceptable launchd label.
func ValidLabel(s string) bool { return labelPattern.MatchString(s) }

// ListServices lists the jobs in the domain, optionally filtered by a label
// substring. It reads `launchctl list`, whose three columns are pid, last
// exit status and label.
func (d *Desktop) ListServices(ctx context.Context, system bool, filter string) ([]ServiceInfo, error) {
	domain, err := launchdDomain(system)
	if err != nil {
		return nil, err
	}
	res, err := clirunner.Run(ctx, []string{"launchctl", "list"}, clirunner.Options{Timeout: 15 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("launchctl list: %w", err)
	}
	needle := strings.ToLower(strings.TrimSpace(filter))
	var out []ServiceInfo
	for _, s := range ParseLaunchctlList(res.Stdout) {
		if needle != "" && !strings.Contains(strings.ToLower(s.Label), needle) {
			continue
		}
		s.Domain = domain
		s.DomainKind = domainKind(system)
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out, nil
}

func domainKind(system bool) string {
	if system {
		return "system"
	}
	return "user"
}

// ParseLaunchctlList parses `launchctl list` output.
func ParseLaunchctlList(out string) []ServiceInfo {
	var jobs []ServiceInfo
	for i, line := range strings.Split(out, "\n") {
		if i == 0 || strings.TrimSpace(line) == "" { // header: PID Status Label
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		s := ServiceInfo{Label: fields[2]}
		if pid, err := strconv.Atoi(fields[0]); err == nil && pid > 0 {
			s.PID = pid
			s.State = "running"
		} else {
			s.State = "loaded"
		}
		if code, err := strconv.Atoi(fields[1]); err == nil {
			s.LastExit = code
			if s.PID == 0 && code != 0 {
				s.State = "exited " + fields[1]
			}
		}
		jobs = append(jobs, s)
	}
	return jobs
}

// GetService inspects one job through `launchctl print`.
func (d *Desktop) GetService(ctx context.Context, system bool, label string) (ServiceInfo, error) {
	if !ValidLabel(label) {
		return ServiceInfo{}, fmt.Errorf("%w: %q", ErrBadLabel, label)
	}
	domain, err := launchdDomain(system)
	if err != nil {
		return ServiceInfo{}, err
	}
	res, err := clirunner.Run(
		ctx,
		[]string{"launchctl", "print", domain + "/" + label},
		clirunner.Options{Timeout: 15 * time.Second},
	)
	if err != nil {
		if strings.Contains(res.Stderr, "Could not find service") || res.ExitCode == 113 {
			return ServiceInfo{}, fmt.Errorf("%w: %s in %s", ErrServiceNotFound, label, domain)
		}
		return ServiceInfo{}, fmt.Errorf("launchctl print: %w", err)
	}
	s := ParseLaunchctlPrint(res.Stdout)
	s.Label, s.Domain, s.DomainKind = label, domain, domainKind(system)
	return s, nil
}

// ParseLaunchctlPrint reads the fields the tool reports out of `launchctl
// print` output, which is a loosely structured "key = value" dump.
func ParseLaunchctlPrint(out string) ServiceInfo {
	var s ServiceInfo
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), " = ")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.TrimSpace(k) {
		case "state":
			s.State = v
		case "pid":
			s.PID, _ = strconv.Atoi(v)
		case "last exit code":
			s.LastExit, _ = strconv.Atoi(strings.Fields(v)[0])
		case "program":
			s.Program = v
		case "path":
			s.Plist = v
		case "run at load":
			s.RunAtLoad = v == "1" || v == "true"
		case "keepalive", "keep alive":
			s.KeepAlive = v == "1" || v == "true"
		}
	}
	if s.State == "" {
		if s.PID > 0 {
			s.State = "running"
		} else {
			s.State = "loaded"
		}
	}
	return s
}

// ControlService starts, stops, restarts, enables or disables a job.
func (d *Desktop) ControlService(ctx context.Context, system bool, label, action string) (string, error) {
	if !ValidLabel(label) {
		return "", fmt.Errorf("%w: %q", ErrBadLabel, label)
	}
	domain, err := launchdDomain(system)
	if err != nil {
		return "", err
	}
	target := domain + "/" + label
	var argv []string
	switch action {
	case "start":
		argv = []string{"launchctl", "kickstart", target}
	case "restart":
		argv = []string{"launchctl", "kickstart", "-k", target}
	case "stop":
		argv = []string{"launchctl", "kill", "SIGTERM", target}
	case "enable":
		argv = []string{"launchctl", "enable", target}
	case "disable":
		argv = []string{"launchctl", "disable", target}
	default:
		return "", fmt.Errorf("%w: %q", ErrBadServiceAction, action)
	}
	res, err := clirunner.Run(ctx, argv, clirunner.Options{Timeout: 30 * time.Second})
	if err != nil {
		if strings.Contains(res.Stderr, "Could not find service") {
			return "", fmt.Errorf("%w: %s in %s", ErrServiceNotFound, label, domain)
		}
		return "", fmt.Errorf("launchctl %s: %w", action, err)
	}
	return fmt.Sprintf("%s: %s", label, pastTense(action)), nil
}

func pastTense(action string) string {
	switch action {
	case "stop":
		return "stopped"
	case "start", "restart":
		return action + "ed"
	default:
		return action + "d"
	}
}

// JobSpec describes a launchd job the server registers.
type JobSpec struct {
	Label       string
	Program     string
	Arguments   []string
	Trigger     string // login | daily | weekly | interval | once
	Time        string // HH:mm for daily/weekly/once
	Weekday     int    // 0-6 for weekly (0 = Sunday)
	IntervalSec int    // for interval
	Description string
	System      bool
}

// jobsDir is where the server keeps the jobs it registers.
func jobsDir(system bool) (string, error) {
	if system {
		return "/Library/LaunchDaemons", nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home directory: %w", err)
	}
	return filepath.Join(home, "Library", "LaunchAgents"), nil
}

// JobPlist renders the launchd property list for a spec.
func JobPlist(spec JobSpec) (map[string]any, error) {
	if !ValidLabel(spec.Label) {
		return nil, fmt.Errorf("%w: %q", ErrBadLabel, spec.Label)
	}
	args := append([]string{spec.Program}, spec.Arguments...)
	job := map[string]any{
		"Label":            spec.Label,
		"ProgramArguments": args,
	}
	if spec.Description != "" {
		job["Comment"] = spec.Description
	}
	hour, minute, err := parseClock(spec.Time)
	if err != nil {
		return nil, err
	}
	switch spec.Trigger {
	case "login":
		job["RunAtLoad"] = true
	case "daily":
		job["StartCalendarInterval"] = map[string]any{"Hour": hour, "Minute": minute}
	case "weekly":
		if spec.Weekday < 0 || spec.Weekday > 6 {
			return nil, fmt.Errorf("%w: weekday %d", ErrBadTrigger, spec.Weekday)
		}
		job["StartCalendarInterval"] = map[string]any{"Hour": hour, "Minute": minute, "Weekday": spec.Weekday}
	case "interval":
		if spec.IntervalSec < 60 {
			return nil, fmt.Errorf("%w: interval must be at least 60 seconds", ErrBadTrigger)
		}
		job["StartInterval"] = spec.IntervalSec
	case "once":
		// launchd has no one-shot calendar trigger; the job runs at the next
		// matching time and is left loaded. Documented in the tool description.
		job["StartCalendarInterval"] = map[string]any{"Hour": hour, "Minute": minute}
	default:
		return nil, fmt.Errorf("%w: %q (login, daily, weekly, interval, once)", ErrBadTrigger, spec.Trigger)
	}
	return job, nil
}

func parseClock(s string) (hour, minute int, err error) {
	if s == "" {
		return 9, 0, nil
	}
	h, m, ok := strings.Cut(s, ":")
	if !ok {
		return 0, 0, fmt.Errorf("%w: time %q is not HH:mm", ErrBadTrigger, s)
	}
	hour, err1 := strconv.Atoi(h)
	minute, err2 := strconv.Atoi(m)
	if err1 != nil || err2 != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, 0, fmt.Errorf("%w: time %q is not HH:mm", ErrBadTrigger, s)
	}
	return hour, minute, nil
}

// CreateJob writes the job's plist and bootstraps it.
func (d *Desktop) CreateJob(ctx context.Context, spec JobSpec) (string, error) {
	job, err := JobPlist(spec)
	if err != nil {
		return "", err
	}
	domain, err := launchdDomain(spec.System)
	if err != nil {
		return "", err
	}
	dir, err := jobsDir(spec.System)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	path := filepath.Join(dir, spec.Label+".plist")
	raw, err := plist.Marshal(job)
	if err != nil {
		return "", fmt.Errorf("encode job: %w", err)
	}
	perm := os.FileMode(0o644) //nolint:gosec // launchd requires the plist to be world-readable
	if err := os.WriteFile(path, raw, perm); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	// Re-registering an existing label needs a bootout first; a failure there
	// just means it was not loaded.
	_, _ = clirunner.Run(
		ctx,
		[]string{"launchctl", "bootout", domain + "/" + spec.Label},
		clirunner.Options{Timeout: 15 * time.Second},
	)
	if _, err := clirunner.Run(
		ctx,
		[]string{"launchctl", "bootstrap", domain, path},
		clirunner.Options{Timeout: 30 * time.Second},
	); err != nil {
		return path, fmt.Errorf("launchctl bootstrap: %w", err)
	}
	return path, nil
}

// DeleteJob boots a job out and removes its plist when the plist is one of
// the server's locations.
func (d *Desktop) DeleteJob(ctx context.Context, system bool, label string) error {
	if !ValidLabel(label) {
		return fmt.Errorf("%w: %q", ErrBadLabel, label)
	}
	domain, err := launchdDomain(system)
	if err != nil {
		return err
	}
	dir, err := jobsDir(system)
	if err != nil {
		return err
	}
	_, bootErr := clirunner.Run(
		ctx,
		[]string{"launchctl", "bootout", domain + "/" + label},
		clirunner.Options{Timeout: 15 * time.Second},
	)
	path := filepath.Join(dir, label+".plist")
	rmErr := os.Remove(path)
	if bootErr != nil && rmErr != nil {
		return fmt.Errorf("%w: %s", ErrServiceNotFound, label)
	}
	return nil
}
