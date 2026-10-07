//go:build darwin && (amd64 || arm64)

package macmcp

import "testing"

func TestParseCodesign(t *testing.T) {
	developerID := `Executable=/opt/homebrew/bin/macos-mcp-server
Identifier=com.deploymenttheory.macos-mcp-server
Format=Mach-O universal (x86_64 arm64)
CodeDirectory v=20500 size=1234 flags=0x10000(runtime) hashes=30+2 location=embedded
Signature size=9000
Authority=Developer ID Application: Deployment Theory (ABCDE12345)
Authority=Developer ID Certification Authority
Authority=Apple Root CA
Timestamp=7 Oct 2026 at 10:00:00
TeamIdentifier=ABCDE12345
Runtime Version=27.0.0
`
	s := ParseCodesign(developerID)
	if s.Unsigned || s.AdHoc || s.Identifier != "com.deploymenttheory.macos-mcp-server" ||
		s.TeamIdentifier != "ABCDE12345" || !containsStr(s.Authority, "Developer ID Application") {
		t.Fatalf("developer id parse = %+v", s)
	}

	adhoc := `Executable=/tmp/macos-mcp-server
Identifier=macos-mcp-server
Format=Mach-O thin (arm64)
CodeDirectory v=20400 size=500 flags=0x2(adhoc) hashes=10+2 location=embedded
Signature=adhoc
Info.plist=not bound
TeamIdentifier=not set
`
	s = ParseCodesign(adhoc)
	if !s.AdHoc || s.TeamIdentifier != "" || s.Unsigned {
		t.Fatalf("ad-hoc parse = %+v", s)
	}
	if s = ParseCodesign(""); !s.Unsigned {
		t.Fatalf("empty report must read as unsigned: %+v", s)
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// TestPermissionsCheckDoesNotPanic reads the live grants. It asserts nothing
// about their values — a hosted runner grants nothing, a developer Mac
// everything — only that the probe runs to completion wherever it is.
func TestPermissionsCheckDoesNotPanic(t *testing.T) {
	p := CheckPermissions(t.Context())
	t.Logf("%s", p.Describe())
}
