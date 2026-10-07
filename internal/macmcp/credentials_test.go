//go:build darwin && (amd64 || arm64)

package macmcp

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/macos-mcp-server/internal/macdesktop"
)

// writeCredFile writes a credentials document into a per-test directory with
// the 0600 mode the loader requires — which is also the shape a real operator
// should use.
func writeCredFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "creds.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadCredentialsFileValid(t *testing.T) {
	path := writeCredFile(t, `{
	  "credentials": [
	    {"name":"corp-sso","target":"login.example.com","username":"svc@example.com","secret":"s3cr3t"},
	    {"name":"api","target":"api.internal","secret":"tok","type":"generic","persist":"session"}
	  ]
	}`)

	entries, err := loadCredentialsFile(path)
	if err != nil {
		t.Fatalf("loadCredentialsFile: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(entries))
	}
	if entries[0].Name != "corp-sso" || entries[0].Target != "login.example.com" {
		t.Errorf("unexpected first entry: %+v", entries[0])
	}
	if string(entries[0].Secret) != "s3cr3t" {
		t.Errorf("secret not decoded: %q", entries[0].Secret)
	}
	if entries[0].Username != "svc@example.com" {
		t.Errorf("username not decoded: %q", entries[0].Username)
	}
}

func TestLoadCredentialsFileRejects(t *testing.T) {
	for name, tc := range map[string]struct {
		body    string
		wantSub string
	}{
		"no entries":       {`{"credentials":[]}`, "no entries"},
		"missing name":     {`{"credentials":[{"target":"t","secret":"s"}]}`, "name is required"},
		"missing target":   {`{"credentials":[{"name":"n","secret":"s"}]}`, "target is required"},
		"missing secret":   {`{"credentials":[{"name":"n","target":"t"}]}`, "secret is required"},
		"duplicate name":   {`{"credentials":[{"name":"n","target":"a","secret":"s"},{"name":"n","target":"b","secret":"s"}]}`, "duplicate name"},
		"duplicate target": {`{"credentials":[{"name":"a","target":"t","secret":"s"},{"name":"b","target":"t","secret":"s"}]}`, "duplicate target"},
		"durable persist":  {`{"credentials":[{"name":"n","target":"t","secret":"s","persist":"local_machine"}]}`, "not supported"},
		"windows type":     {`{"credentials":[{"name":"n","target":"t","secret":"s","type":"domain_password"}]}`, "not supported"},
		"malformed json":   {`{"credentials":`, "parse credentials file"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadCredentialsFile(writeCredFile(t, tc.body))
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error %q should mention %q", err, tc.wantSub)
			}
		})
	}
}

func TestLoadCredentialsFileMissingPath(t *testing.T) {
	if _, err := loadCredentialsFile(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Error("a missing file must be an error")
	}
}

func TestLoadCredentialsFileRejectsDirectory(t *testing.T) {
	if _, err := loadCredentialsFile(t.TempDir()); !errors.Is(err, ErrCredentialsFileDir) {
		t.Errorf("a directory must be rejected, got %v", err)
	}
}

func TestLoadCredentialsFileRejectsOversized(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.json")
	if err := os.WriteFile(path, make([]byte, credentialFileMaxSize+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadCredentialsFile(path); !errors.Is(err, ErrCredentialsFileSize) {
		t.Errorf("oversized file should be rejected with the size error, got %v", err)
	}
}

// TestLoadCredentialsFileRejectsBroadMode is the macOS counterpart of the
// Windows DACL check: a group- or world-readable document is already
// disclosed, so startup refuses it and says what to do.
func TestLoadCredentialsFileRejectsBroadMode(t *testing.T) {
	for _, mode := range []os.FileMode{0o640, 0o604, 0o644, 0o660, 0o666} {
		path := filepath.Join(t.TempDir(), "creds.json")
		if err := os.WriteFile(path, []byte(`{"credentials":[{"name":"n","target":"t","secret":"s"}]}`), mode); err != nil {
			t.Fatal(err)
		}
		// umask may have narrowed the mode; force it.
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		_, err := loadCredentialsFile(path)
		if !errors.Is(err, ErrCredentialsFileBroadlyReadable) {
			t.Errorf("mode %04o: want ErrCredentialsFileBroadlyReadable, got %v", mode, err)
		}
		if err != nil && !strings.Contains(err.Error(), "chmod 600") {
			t.Errorf("mode %04o: the refusal should say how to fix it, got %v", mode, err)
		}
	}
	for _, mode := range []os.FileMode{0o600, 0o400, 0o700} {
		path := filepath.Join(t.TempDir(), "creds.json")
		if err := os.WriteFile(path, []byte(`{"credentials":[{"name":"n","target":"t","secret":"s"}]}`), mode); err != nil {
			t.Fatal(err)
		}
		if _, err := loadCredentialsFile(path); err != nil {
			t.Errorf("mode %04o should be accepted, got %v", mode, err)
		}
	}
}

// TestSecretBytesDecoding covers both decode paths: the unescaped fast path
// that avoids ever materialising a Go string, and the escaped fallback.
func TestSecretBytesDecoding(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`"plain"`, "plain"},
		{`"with spaces and $ymbols"`, "with spaces and $ymbols"},
		{`"esc\"aped"`, `esc"aped`},
		{`"tab\there"`, "tab\there"},
		{`"unicode é"`, "unicode é"},
		{`"back\\slash"`, `back\slash`},
	} {
		var s secretBytes
		if err := json.Unmarshal([]byte(tc.in), &s); err != nil {
			t.Errorf("%s: %v", tc.in, err)
			continue
		}
		if string(s) != tc.want {
			t.Errorf("%s decoded to %q, want %q", tc.in, s, tc.want)
		}
	}
}

// TestSecretBytesFastPathDoesNotAliasInput proves the unescaped path copies
// rather than aliasing the JSON buffer, which the loader wipes after parsing.
func TestSecretBytesFastPathDoesNotAliasInput(t *testing.T) {
	raw := []byte(`"topsecret"`)
	var s secretBytes
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	wipe(raw)
	if string(s) != "topsecret" {
		t.Errorf("secret aliased the wiped input buffer: %q", s)
	}
}

func TestLoadCredentialsFileWipesFileBuffer(t *testing.T) {
	path := writeCredFile(t, `{"credentials":[{"name":"n","target":"t","secret":"survives"}]}`)
	entries, err := loadCredentialsFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(entries[0].Secret) != "survives" {
		t.Errorf("secret did not survive the file-buffer wipe: %q", entries[0].Secret)
	}
}

func TestCredentialInfosOmitSecretsAndDefault(t *testing.T) {
	infos := credentialInfos([]installedCredential{
		{Name: "a", Target: "t1", Username: "u", Type: macdesktop.CredentialGeneric, Persist: macdesktop.PersistSession},
	})
	if len(infos) != 1 {
		t.Fatalf("want 1, got %d", len(infos))
	}
	if !infos[0].Injectable {
		t.Error("generic credentials should be injectable")
	}
	// Assert on the serialised *keys*: an allowlist is what pins the guarantee
	// — a new field carrying a secret would have to be added here deliberately.
	b, err := json.Marshal(infos)
	if err != nil {
		t.Fatal(err)
	}
	var decoded []map[string]json.RawMessage
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{
		"name": true, "target": true, "username": true,
		"type": true, "persist": true, "present": true, "injectable": true,
		"allow_unmasked_target": true,
	}
	for _, obj := range decoded {
		for k := range obj {
			if !allowed[k] {
				t.Errorf("credential info exposes unexpected field %q: a secret must never be "+
					"serialisable to a tool result", k)
			}
		}
	}
}

// TestAllowUnmaskedTargetDefaultsOffAndPropagates pins the safe default and
// the path the flag has to travel to the tool layer.
func TestAllowUnmaskedTargetDefaultsOffAndPropagates(t *testing.T) {
	infos := credentialInfos([]installedCredential{
		{Name: "strict", Target: "t1", Type: macdesktop.CredentialGeneric, Persist: macdesktop.PersistSession},
		{
			Name: "console", Target: "t2", Type: macdesktop.CredentialGeneric,
			Persist: macdesktop.PersistSession, AllowUnmaskedTarget: true,
		},
	})
	if infos[0].AllowUnmaskedTarget {
		t.Error("a credential that does not ask for it must not allow an unmasked target")
	}
	if !infos[1].AllowUnmaskedTarget {
		t.Error("an explicit allow_unmasked_target must reach the tool layer")
	}
}

func TestCredentialEntryDefaultsToMaskedTargets(t *testing.T) {
	var e credentialEntry
	if err := json.Unmarshal([]byte(`{"name":"a","target":"t","secret":"s"}`), &e); err != nil {
		t.Fatal(err)
	}
	if e.AllowUnmaskedTarget {
		t.Error("allow_unmasked_target must default to false when the document omits it")
	}
}

func TestDefaultTypeAndPersist(t *testing.T) {
	if got := defaultType(""); got != macdesktop.CredentialGeneric {
		t.Errorf("defaultType(\"\") = %q", got)
	}
	if got := defaultPersist(""); got != macdesktop.PersistSession {
		t.Errorf("defaultPersist(\"\") = %q", got)
	}
}

// TestAuditViewHasNoSecret guards the audit path: identifiers only.
func TestAuditViewHasNoSecret(t *testing.T) {
	view := auditView([]installedCredential{
		{Name: "n", Target: "t", Username: "u", Type: macdesktop.CredentialGeneric, Persist: macdesktop.PersistSession},
	})
	b, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(b)), "secret") {
		t.Errorf("audit view must not carry secrets: %s", b)
	}
	for _, want := range []string{"name", "target", "username"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("audit view missing %q: %s", want, b)
		}
	}
}

func TestErrNoCredentialsIsMatchable(t *testing.T) {
	_, err := loadCredentialsFile(writeCredFile(t, `{"credentials":[]}`))
	if !errors.Is(err, ErrNoCredentials) {
		t.Errorf("want ErrNoCredentials, got %v", err)
	}
}

// TestCredentialsDeclareUnmaskedTargets pins the pre-admission scan: it reads
// only the flag, respects the permission check, and reports false for a
// malformed document (the real loader diagnoses that).
func TestCredentialsDeclareUnmaskedTargets(t *testing.T) {
	strict := writeCredFile(t, `{"credentials":[{"name":"a","target":"t","secret":"s"}]}`)
	if credentialsDeclareUnmaskedTargets(strict) {
		t.Error("a strict document must not report an unmasked target")
	}
	optOut := writeCredFile(t, `{"credentials":[{"name":"a","target":"t","secret":"s","allow_unmasked_target":true}]}`)
	if !credentialsDeclareUnmaskedTargets(optOut) {
		t.Error("an opted-out document must report an unmasked target")
	}
	if credentialsDeclareUnmaskedTargets(writeCredFile(t, `{"credentials":`)) {
		t.Error("a malformed document must report false")
	}
	broad := filepath.Join(t.TempDir(), "broad.json")
	if err := os.WriteFile(broad, []byte(`{"credentials":[{"name":"a","target":"t","secret":"s","allow_unmasked_target":true}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(broad, 0o644)
	if credentialsDeclareUnmaskedTargets(broad) {
		t.Error("a broadly readable document must not be scanned")
	}
}
