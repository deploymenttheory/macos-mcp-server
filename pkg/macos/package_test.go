//go:build darwin && (amd64 || arm64)

package macos

import "testing"

// TestPackageRefusesRemoteInstallers pins the local-only rule for .pkg
// installs: a URL, a network path, a mounted volume, a relative path or a
// non-.pkg file is refused before installer(8) ever sees it.
func TestPackageRefusesRemoteInstallers(t *testing.T) {
	rejected := []string{
		"http://attacker.example/p.pkg",
		"https://attacker.example/p.pkg",
		"//attacker/share/p.pkg",
		"/Volumes/Share/p.pkg",
		"/net/host/p.pkg",
		"relative/path.pkg",
		"/Users/me/Downloads/thing.dmg",
	}
	for _, pkg := range rejected {
		if err := requireLocalPkg(pkg); err == nil {
			t.Errorf("%s must be refused", pkg)
		}
	}
	if err := requireLocalPkg("/Users/me/Downloads/thing.pkg"); err != nil {
		t.Errorf("a local absolute .pkg should be accepted, got %v", err)
	}
}

// TestPackageCommandIsArgvOnly checks that caller input lands in its own argv
// element behind "--", so a name starting with a dash cannot become a flag.
func TestPackageCommandIsArgvOnly(t *testing.T) {
	argv, _, err := packageCommand("install", map[string]any{"id": "--force", "cask": true})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"brew", "install", "--cask", "--", "--force"}
	if len(argv) != len(want) {
		t.Fatalf("argv = %v, want %v", argv, want)
	}
	for i := range want {
		if argv[i] != want[i] {
			t.Fatalf("argv = %v, want %v", argv, want)
		}
	}
	if _, _, err := packageCommand("install", map[string]any{}); err == nil {
		t.Error("install without id or pkg must fail")
	}
}
