//go:build darwin && (amd64 || arm64)

package macmcp

import "testing"

// TestParseAPFSVolumesKeepsUserData pins the FileVault reading: Data-role
// and roleless volumes are reported, system-reserved ones are not, and the
// FileVault flag is carried per volume.
func TestParseAPFSVolumesKeepsUserData(t *testing.T) {
	raw := `{"Containers":[{"Volumes":[
	  {"Name":"Preboot","FileVault":false,"Roles":["Preboot"]},
	  {"Name":"Recovery","FileVault":false,"Roles":["Recovery"]},
	  {"Name":"Macintosh HD","FileVault":true,"Roles":["System"]},
	  {"Name":"Macintosh HD - Data","FileVault":true,"Roles":["Data"]},
	  {"Name":"Scratch","FileVault":false,"Roles":[]}
	]}]}`
	vols, err := ParseAPFSVolumes(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(vols) != 2 {
		t.Fatalf("volumes = %+v, want the Data volume and the roleless one", vols)
	}
	if vols[0].Mount != "Macintosh HD - Data" || !vols[0].Protected {
		t.Errorf("data volume = %+v", vols[0])
	}
	if vols[1].Mount != "Scratch" || vols[1].Protected {
		t.Errorf("scratch volume = %+v", vols[1])
	}
}
