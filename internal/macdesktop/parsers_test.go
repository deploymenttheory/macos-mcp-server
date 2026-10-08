//go:build darwin && (amd64 || arm64)

package macdesktop

import (
	"errors"
	"strings"
	"testing"
)

func TestParseLaunchctlList(t *testing.T) {
	out := "PID\tStatus\tLabel\n" +
		"312\t0\tcom.apple.Finder\n" +
		"-\t0\tcom.example.idle\n" +
		"-\t78\tcom.example.crashed\n"
	jobs := ParseLaunchctlList(out)
	if len(jobs) != 3 {
		t.Fatalf("got %d jobs", len(jobs))
	}
	if jobs[0].PID != 312 || jobs[0].State != "running" {
		t.Errorf("running job: %+v", jobs[0])
	}
	if jobs[1].State != "loaded" {
		t.Errorf("idle job: %+v", jobs[1])
	}
	if jobs[2].State != "exited 78" || jobs[2].LastExit != 78 {
		t.Errorf("crashed job: %+v", jobs[2])
	}
}

func TestParseLaunchctlPrint(t *testing.T) {
	out := `com.example.agent = {
	active count = 1
	path = /Users/me/Library/LaunchAgents/com.example.agent.plist
	state = running
	program = /usr/local/bin/agent
	pid = 4242
	last exit code = 0
	run at load = 1
}`
	s := ParseLaunchctlPrint(out)
	if s.State != "running" || s.PID != 4242 || s.Program != "/usr/local/bin/agent" || !s.RunAtLoad ||
		!strings.HasSuffix(s.Plist, ".plist") {
		t.Errorf("parsed = %+v", s)
	}
}

func TestJobPlistTriggers(t *testing.T) {
	job, err := JobPlist(JobSpec{Label: "com.example.daily", Program: "/usr/bin/say", Arguments: []string{"hi"}, Trigger: "daily", Time: "07:30"})
	if err != nil {
		t.Fatal(err)
	}
	cal := job["StartCalendarInterval"].(map[string]any)
	if cal["Hour"] != 7 || cal["Minute"] != 30 {
		t.Errorf("calendar = %v", cal)
	}
	if args := job["ProgramArguments"].([]string); len(args) != 2 || args[0] != "/usr/bin/say" {
		t.Errorf("args = %v", args)
	}
	if _, err := JobPlist(JobSpec{Label: "bad label!", Program: "x", Trigger: "login"}); !errors.Is(err, ErrBadLabel) {
		t.Errorf("bad label: %v", err)
	}
	if _, err := JobPlist(JobSpec{Label: "com.x", Program: "x", Trigger: "interval", IntervalSec: 5}); !errors.Is(err, ErrBadTrigger) {
		t.Errorf("short interval: %v", err)
	}
	if _, err := JobPlist(JobSpec{Label: "com.x", Program: "x", Trigger: "daily", Time: "25:00"}); !errors.Is(err, ErrBadTrigger) {
		t.Errorf("bad time: %v", err)
	}
	if _, err := JobPlist(JobSpec{Label: "com.x", Program: "x", Trigger: "hourly"}); !errors.Is(err, ErrBadTrigger) {
		t.Errorf("unknown trigger: %v", err)
	}
}

func TestLogPredicateQuotesValues(t *testing.T) {
	p := LogPredicate(LogQuery{Subsystem: `com.apple."x"`, Process: `a\b`, Level: "error", Contains: "oops"})
	want := `subsystem == "com.apple.\"x\"" AND process == "a\\b" AND messageType == error AND eventMessage CONTAINS[c] "oops"`
	if p != want {
		t.Errorf("predicate =\n%s\nwant\n%s", p, want)
	}
	if LogPredicate(LogQuery{}) != "" {
		t.Error("an empty query has no predicate")
	}
}

func TestParseLogNDJSON(t *testing.T) {
	out := `{"timestamp":"2026-10-07 10:00:00.000000+0100","messageType":"Error","subsystem":"com.apple.x","process":"foo","processID":12,"eventMessage":"bad   thing\nhappened"}
not json
{"timestamp":"2026-10-07 10:00:01.000000+0100","messageType":"Default","processImagePath":"/usr/libexec/bar","processID":13,"eventMessage":"ok"}`
	entries := ParseLogNDJSON(out, 10)
	if len(entries) != 2 {
		t.Fatalf("got %d entries", len(entries))
	}
	if entries[0].Message != "bad thing happened" || entries[0].Process != "foo" {
		t.Errorf("entry 0 = %+v", entries[0])
	}
	if entries[1].Process != "bar" {
		t.Errorf("process from image path: %+v", entries[1])
	}
	if got := ParseLogNDJSON(out, 1); len(got) != 1 || got[0].Process != "bar" {
		t.Errorf("max keeps the newest: %+v", got)
	}
}

func TestParseEnrollment(t *testing.T) {
	enrolled, server := ParseEnrollment("Enrolled via DEP: Yes\nMDM enrollment: Yes (User Approved)\nMDM server: https://mdm.example.com/mdm\n")
	if !enrolled || server != "https://mdm.example.com/mdm" {
		t.Errorf("got %v %q", enrolled, server)
	}
	if e, _ := ParseEnrollment("MDM enrollment: No\n"); e {
		t.Error("not enrolled")
	}
}

func TestWifiDevice(t *testing.T) {
	out := "Hardware Port: Ethernet\nDevice: en5\nEthernet Address: a\n\nHardware Port: Wi-Fi\nDevice: en0\nEthernet Address: b\n"
	if got := wifiDevice(out); got != "en0" {
		t.Errorf("got %q", got)
	}
}

func TestLiveSystemInfo(t *testing.T) {
	d, err := New(nil, Options{})
	if err != nil {
		t.Skipf("engine unavailable: %v", err)
	}
	defer d.Close()
	si, err := d.GetSystemInfo(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if si.OSVersion == "" || si.Model == "" || si.TotalMemoryMB == 0 || si.LogicalCPUs == 0 {
		t.Errorf("incomplete system info: %+v", si)
	}
	if len(si.Disks) == 0 {
		t.Error("no volumes")
	}
	if _, err := d.PreferenceGet("com.apple.finder", "no-such-key-xyz"); !errors.Is(err, ErrPreferenceNotFound) {
		t.Errorf("missing key: %v", err)
	}
	if doms := d.PreferenceDomains(); len(doms) == 0 {
		t.Error("no preference domains")
	}
	if ad, err := d.Adapters(); err != nil || len(ad) == 0 {
		t.Errorf("adapters: %v (%d)", err, len(ad))
	}
}
