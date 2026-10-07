//go:build darwin && (amd64 || arm64)

package macdesktop

import (
	"context"
	"os"
	"runtime"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/deploymenttheory/go-bindings-macosplatform/bindings/frameworks/corefoundation"
	"github.com/deploymenttheory/go-bindings-macosplatform/bindings/frameworks/iokit"
	"github.com/deploymenttheory/go-bindings-macosplatform/bindings/frameworks/systemconfiguration"
	"github.com/deploymenttheory/macos-mcp-server/internal/clirunner"
)

// SystemInfo is a snapshot of OS and hardware inventory.
type SystemInfo struct {
	Hostname      string     `json:"hostname"`
	ComputerName  string     `json:"computer_name"`
	OSCaption     string     `json:"os"`
	OSVersion     string     `json:"os_version"`
	BuildNumber   string     `json:"build"`
	OSArch        string     `json:"arch"`
	LastBoot      string     `json:"last_boot"`
	Model         string     `json:"model"`
	Serial        string     `json:"serial"`
	HardwareUUID  string     `json:"hardware_uuid"`
	CPUName       string     `json:"cpu"`
	PhysicalCores uint32     `json:"physical_cores"`
	LogicalCPUs   uint32     `json:"logical_cpus"`
	TotalMemoryMB uint64     `json:"total_memory_mb"`
	FreeMemoryMB  uint64     `json:"free_memory_mb"`
	SIPEnabled    *bool      `json:"sip_enabled,omitempty"`
	FileVaultOn   *bool      `json:"filevault_on,omitempty"`
	MDMEnrolled   *bool      `json:"mdm_enrolled,omitempty"`
	MDMServer     string     `json:"mdm_server,omitempty"`
	Disks         []DiskInfo `json:"disks"`
}

// DiskInfo describes one mounted volume.
type DiskInfo struct {
	Mount   string  `json:"mount"`
	Device  string  `json:"device"`
	Type    string  `json:"type"`
	SizeGB  float64 `json:"size_gb"`
	FreeGB  float64 `json:"free_gb"`
	UsedPct int     `json:"used_pct"`
}

// HostFacts are the identity facts the guardrail probes read.
type HostFacts struct {
	Hostname     string
	ComputerName string
	Serial       string
	HardwareUUID string
	Model        string
	OSCaption    string
	PartOfDomain bool
	Domain       string
}

// GetSystemInfo collects OS, hardware, memory and disk inventory. The kernel,
// IOKit and SystemConfiguration answer in-process; SIP, FileVault and MDM
// enrollment come from their CLIs because no public API reports them.
func (d *Desktop) GetSystemInfo(ctx context.Context) (SystemInfo, error) {
	si := SystemInfo{OSArch: runtime.GOARCH}
	si.Hostname, _ = os.Hostname()
	si.ComputerName = computerName()
	si.OSVersion = sysctlString("kern.osproductversion")
	si.BuildNumber = sysctlString("kern.osversion")
	si.OSCaption = "macOS " + si.OSVersion + " (" + si.BuildNumber + ")"
	si.Model = sysctlString("hw.model")
	si.CPUName = sysctlString("machdep.cpu.brand_string")
	si.PhysicalCores = sysctlUint32("hw.physicalcpu")
	si.LogicalCPUs = sysctlUint32("hw.logicalcpu")
	if mem, err := unix.SysctlUint64("hw.memsize"); err == nil {
		si.TotalMemoryMB = mem / (1024 * 1024)
	}
	if free, err := unix.SysctlUint32("vm.page_free_count"); err == nil {
		pageSize := uint64(os.Getpagesize()) //nolint:gosec // a page size is a small positive integer
		si.FreeMemoryMB = uint64(free) * pageSize / (1024 * 1024)
	}
	if tv, err := unix.SysctlTimeval("kern.boottime"); err == nil {
		si.LastBoot = time.Unix(tv.Sec, int64(tv.Usec)*1000).Format(time.RFC3339)
	}
	si.Serial, si.HardwareUUID = platformIdentity()
	si.Disks = mountedDisks()

	// CLI-backed facts are best-effort: absent rather than wrong.
	if out, err := clirunner.Run(
		ctx,
		[]string{"csrutil", "status"},
		clirunner.Options{Timeout: 5 * time.Second},
	); err == nil {
		enabled := strings.Contains(strings.ToLower(out.Stdout), "enabled")
		si.SIPEnabled = &enabled
	}
	if out, err := clirunner.Run(
		ctx,
		[]string{"fdesetup", "status"},
		clirunner.Options{Timeout: 5 * time.Second},
	); err == nil {
		on := strings.Contains(out.Stdout, "FileVault is On")
		si.FileVaultOn = &on
	}
	if out, err := clirunner.Run(
		ctx,
		[]string{"profiles", "status", "-type", "enrollment"},
		clirunner.Options{Timeout: 10 * time.Second},
	); err == nil {
		enrolled, server := ParseEnrollment(out.Stdout)
		si.MDMEnrolled = &enrolled
		si.MDMServer = server
	}
	return si, nil
}

// ParseEnrollment reads `profiles status -type enrollment` output.
func ParseEnrollment(out string) (enrolled bool, server string) {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "MDM enrollment:"):
			v := strings.TrimSpace(strings.TrimPrefix(line, "MDM enrollment:"))
			enrolled = strings.HasPrefix(strings.ToLower(v), "yes")
		case strings.HasPrefix(line, "MDM server:"):
			server = strings.TrimSpace(strings.TrimPrefix(line, "MDM server:"))
		}
	}
	return enrolled, server
}

// HostFacts returns the identity facts for the guardrail probes.
func (d *Desktop) HostFacts(ctx context.Context) HostFacts {
	hf := HostFacts{ComputerName: computerName(), Model: sysctlString("hw.model")}
	hf.Hostname, _ = os.Hostname()
	hf.Serial, hf.HardwareUUID = platformIdentity()
	hf.OSCaption = "macOS " + sysctlString("kern.osproductversion") + " (" + sysctlString("kern.osversion") + ")"
	if out, err := clirunner.Run(
		ctx,
		[]string{"dsconfigad", "-show"},
		clirunner.Options{Timeout: 5 * time.Second},
	); err == nil {
		for _, line := range strings.Split(out.Stdout, "\n") {
			if k, v, ok := strings.Cut(line, "="); ok && strings.TrimSpace(k) == "Active Directory Domain" {
				hf.Domain = strings.TrimSpace(v)
				hf.PartOfDomain = hf.Domain != ""
			}
		}
	}
	return hf
}

func sysctlString(name string) string {
	v, err := unix.Sysctl(name)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(v)
}

func sysctlUint32(name string) uint32 {
	v, err := unix.SysctlUint32(name)
	if err != nil {
		return 0
	}
	return v
}

// computerName is the user-visible name from SystemConfiguration.
func computerName() string {
	name, _ := systemconfiguration.SCDynamicStoreCopyComputerName(systemconfiguration.SCDynamicStoreRef{})
	if name.IsNil() {
		return ""
	}
	defer name.Release()
	return cfToString(name)
}

// platformIdentity reads the serial number and hardware UUID from the
// IOPlatformExpertDevice registry entry.
func platformIdentity() (serial, uuid string) {
	matching := iokit.IOServiceMatching("IOPlatformExpertDevice")
	if matching.IsNil() {
		return "", ""
	}
	entry := iokit.IOServiceGetMatchingService(
		int(iokit.KIOMainPortDefault()),
		corefoundation.CFDictionaryRef(matching),
	)
	if entry == 0 {
		return "", ""
	}
	defer iokit.IOObjectRelease(entry)
	read := func(key string) string {
		v := iokit.IORegistryEntryCreateCFProperty(entry, cfStr(key), corefoundation.CFAllocatorRef{}, 0)
		if v == nil {
			return ""
		}
		defer v.Release()
		return cfToString(v)
	}
	return read("IOPlatformSerialNumber"), read("IOPlatformUUID")
}

// mountedDisks lists the mounted volumes with their capacity.
func mountedDisks() []DiskInfo {
	n, err := unix.Getfsstat(nil, unix.MNT_NOWAIT)
	if err != nil || n <= 0 {
		return nil
	}
	buf := make([]unix.Statfs_t, n)
	n, err = unix.Getfsstat(buf, unix.MNT_NOWAIT)
	if err != nil {
		return nil
	}
	out := make([]DiskInfo, 0, n)
	for _, fs := range buf[:n] {
		fstype := cString(fs.Fstypename[:])
		switch fstype {
		case "devfs", "autofs", "nullfs", "lifs":
			continue
		}
		mount := cString(fs.Mntonname[:])
		// Hide the system's internal volumes; the data volume carries the user's
		// capacity and the sealed system volume is read-only.
		if strings.HasPrefix(mount, "/System/Volumes/") && mount != "/System/Volumes/Data" {
			continue
		}
		const gb = 1024 * 1024 * 1024
		size := float64(fs.Blocks) * float64(fs.Bsize) / gb
		free := float64(fs.Bavail) * float64(fs.Bsize) / gb
		used := 0
		if size > 0 {
			used = int((size - free) / size * 100)
		}
		out = append(out, DiskInfo{
			Mount: mount, Device: cString(fs.Mntfromname[:]), Type: fstype,
			SizeGB: size, FreeGB: free, UsedPct: used,
		})
	}
	return out
}
