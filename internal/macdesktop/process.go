//go:build darwin && (amd64 || arm64)

package macdesktop

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/deploymenttheory/go-bindings-macosplatform/bindings/frameworks/appkit"
	"github.com/deploymenttheory/go-bindings-macosplatform/bindings/libraries/libproc"
)

// ProcessInfo describes one running process.
type ProcessInfo struct {
	PID            int     `json:"pid"`
	ParentPID      int     `json:"ppid"`
	UID            uint32  `json:"uid"`
	Name           string  `json:"name"`
	ExecutablePath string  `json:"path,omitempty"`
	ResidentKB     uint64  `json:"resident_kb"`
	CPUSeconds     float64 `json:"cpu_seconds"`
	Threads        int     `json:"threads"`
}

// procTaskInfo mirrors struct proc_taskinfo from <sys/proc_info.h>.
type procTaskInfo struct {
	VirtualSize      uint64
	ResidentSize     uint64
	TotalUser        uint64 // nanoseconds
	TotalSystem      uint64
	ThreadsUser      uint64
	ThreadsSystem    uint64
	Policy           int32
	Faults           int32
	Pageins          int32
	CowFaults        int32
	MessagesSent     int32
	MessagesReceived int32
	SyscallsMach     int32
	SyscallsUnix     int32
	Csw              int32
	Threadnum        int32
	Numrunning       int32
	Priority         int32
}

// procPidTaskInfo is the PROC_PIDTASKINFO flavor.
const procPidTaskInfo = 4

// Errors the process operations return.
var (
	ErrProcessNotFound = errors.New("no process matched")
	ErrPartialKill     = errors.New("some processes could not be terminated")
)

// ProcessList returns running processes, sorted by sortBy ("memory", "name",
// or "pid") and limited to at most limit entries (0 = no limit). It needs no
// engine thread: the kernel answers directly.
func (d *Desktop) ProcessList(sortBy string, limit int) ([]ProcessInfo, error) {
	kps, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, fmt.Errorf("kern.proc.all: %w", err)
	}
	procs := make([]ProcessInfo, 0, len(kps))
	for i := range kps {
		kp := &kps[i]
		pid := int(kp.Proc.P_pid)
		if pid <= 0 {
			continue
		}
		p := ProcessInfo{
			PID:       pid,
			ParentPID: int(kp.Eproc.Ppid),
			UID:       kp.Eproc.Ucred.Uid,
			Name:      cString(kp.Proc.P_comm[:]),
		}
		if path := pidPath(pid); path != "" {
			p.ExecutablePath = path
			if base := path[strings.LastIndex(path, "/")+1:]; len(base) > len(p.Name) {
				p.Name = base // P_comm is truncated to 16 bytes
			}
		}
		var ti procTaskInfo
		pid32 := int32(pid) //nolint:gosec // a pid_t is 32 bits
		if n := libproc.Pidinfo(
			pid32,
			procPidTaskInfo,
			0,
			unsafe.Pointer(&ti),
			int32(unsafe.Sizeof(ti)),
		); n == int32(
			unsafe.Sizeof(ti),
		) {
			p.ResidentKB = ti.ResidentSize / 1024
			p.CPUSeconds = float64(ti.TotalUser+ti.TotalSystem) / 1e9
			p.Threads = int(ti.Threadnum)
		}
		procs = append(procs, p)
	}
	switch sortBy {
	case "name":
		sort.Slice(
			procs,
			func(i, j int) bool { return strings.ToLower(procs[i].Name) < strings.ToLower(procs[j].Name) },
		)
	case "pid":
		sort.Slice(procs, func(i, j int) bool { return procs[i].PID < procs[j].PID })
	default:
		sort.Slice(procs, func(i, j int) bool { return procs[i].ResidentKB > procs[j].ResidentKB })
	}
	if limit > 0 && len(procs) > limit {
		procs = procs[:limit]
	}
	return procs, nil
}

// pidPath reads a process's executable path through libproc; "" for a process
// this user may not inspect.
func pidPath(pid int) string {
	buf := make([]byte, 4096) // PROC_PIDPATHINFO_MAXSIZE
	n := libproc.Pidpath(
		int32(pid),
		unsafe.Pointer(&buf[0]),
		uint32(len(buf)),
	) //nolint:gosec // a pid_t is 32 bits and the buffer is 4 KiB
	if n <= 0 {
		return ""
	}
	return string(buf[:n])
}

func cString(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}

// ProcessKill terminates processes by pid or by name substring. A regular
// application is asked to quit through AppKit first, so it can save; anything
// else gets SIGTERM, then SIGKILL after a grace period when force is set.
// Without root only the caller's own processes can be signalled; the kernel's
// refusal is reported, not hidden.
func (d *Desktop) ProcessKill(pid int, nameSubstr string, force bool) (int, error) {
	procs, err := d.ProcessList("pid", 0)
	if err != nil {
		return 0, err
	}
	needle := strings.ToLower(strings.TrimSpace(nameSubstr))
	killed := 0
	var errs []string
	for _, p := range procs {
		switch {
		case pid != 0 && p.PID != pid:
			continue
		case pid == 0 && (needle == "" || !strings.Contains(strings.ToLower(p.Name), needle)):
			continue
		}
		if p.PID == syscall.Getpid() {
			continue // never the server itself
		}
		if err := terminate(p.PID, force); err != nil {
			errs = append(errs, fmt.Sprintf("%d (%s): %v", p.PID, p.Name, err))
			continue
		}
		killed++
	}
	if killed == 0 && len(errs) == 0 {
		return 0, ErrProcessNotFound
	}
	if len(errs) > 0 {
		return killed, fmt.Errorf("%w: %d terminated; failed: %s", ErrPartialKill, killed, strings.Join(errs, "; "))
	}
	return killed, nil
}

// terminate ends one process. Main-thread AppKit for applications, signals
// for everything else.
func terminate(pid int, force bool) error {
	if app := appkit.RunningApplicationWithProcessIdentifier(pid); app != nil && !force {
		if app.Terminate() {
			return nil
		}
	}
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	if err := syscall.Kill(pid, sig); err != nil {
		return fmt.Errorf("kill %d: %w", pid, err)
	}
	if force {
		return nil
	}
	// Give it a moment; escalate only when asked (force) — a TERM the process
	// ignores is reported as still running by the next list.
	time.Sleep(50 * time.Millisecond)
	return nil
}
