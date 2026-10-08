//go:build darwin && (amd64 || arm64)

// Init-time credential provisioning.
//
// Credentials are supplied only at startup, via --credentials-file. They are
// deliberately not accepted as command-line flags: argv is readable by any
// process on the machine (ps, /proc-alikes, launchd's process table), so a
// secret on the command line is a secret disclosed.
//
// The loaded entries are installed into the calling user's login keychain and,
// by default, removed again on every shutdown path — normal exit and kill-switch
// trip alike — so a session leaves no credential residue.
//
// Audit records names, targets, and usernames. It never records a secret: this
// file is the only place in the server that holds plaintext, and it hands it
// straight to the desktop engine.
package macmcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/deploymenttheory/agentweave-harness/guardrails/audit"
	"github.com/deploymenttheory/macos-mcp-server/internal/macdesktop"
	"github.com/deploymenttheory/mcp-server-core/toolkit"
)

// credentialFileMaxSize bounds the credentials document, so a mistyped path at
// a huge file cannot be read into memory.
const credentialFileMaxSize = 1 << 20 // 1 MiB

// Credential-file errors.
var (
	ErrNoCredentials       = errors.New("credentials file contains no entries")
	ErrCredentialsFileDir  = errors.New("credentials file is a directory")
	ErrCredentialsFileSize = errors.New("credentials file exceeds the size limit")
	ErrCredentialInvalid   = errors.New("invalid credential entry")
)

// credentialsDocument is the --credentials-file schema.
type credentialsDocument struct {
	Credentials []credentialEntry `json:"credentials"`
}

// credentialEntry is one credential to install.
//
//nolint:tagliatelle // the document schema is shared with the Windows server
type credentialEntry struct {
	// Name is the handle the agent uses to refer to this credential. Required,
	// unique, and never secret.
	Name string `json:"name"`
	// Target is the keychain target (host, URL, or app identifier). It becomes
	// the namespaced keychain service name.
	Target string `json:"target"`
	// Username is stored as the keychain account.
	Username string `json:"username,omitempty"`
	// Comment is an optional note; kept for schema parity, not stored.
	Comment string `json:"comment,omitempty"`
	// Secret is the plaintext. Decoded into a byte slice rather than a string
	// so it can be wiped after installation.
	Secret secretBytes `json:"secret"`
	// Type is "generic" (the only class the macOS store supports).
	Type macdesktop.CredentialType `json:"type,omitempty"`
	// Persist is "session" (the only supported value).
	Persist macdesktop.CredentialPersist `json:"persist,omitempty"`
	// AllowUnmaskedTarget permits injection into a control that does not
	// report itself as a secure text field. Defaults to false: injection
	// normally requires a confirmed password field, because the agent chooses
	// where the keystrokes land and an unmasked destination puts the secret in
	// reach of Screenshot, GetText and the clipboard — defeating the guarantee
	// that a secret may be used but never read.
	//
	// Set it only for a destination that genuinely cannot report itself as
	// secure, such as a terminal or some Electron and Java applications. It is
	// per credential and in the document precisely so the exception is an
	// operator decision, narrow, and reviewable.
	AllowUnmaskedTarget bool `json:"allow_unmasked_target,omitempty"`
}

// secretBytes holds a JSON string as wipeable bytes.
//
// Go strings are immutable and cannot be zeroed, so the common case — a secret
// with no JSON escapes — is unquoted directly into a byte slice and never
// becomes a string. Escaped values fall back to the standard decoder, which
// does allocate a string; that is documented as best-effort rather than
// silently pretended away.
type secretBytes []byte

func (s *secretBytes) UnmarshalJSON(b []byte) error {
	if len(b) >= 2 && b[0] == '"' && b[len(b)-1] == '"' {
		if body := b[1 : len(b)-1]; !bytes.ContainsRune(body, '\\') {
			*s = bytes.Clone(body)
			return nil
		}
	}
	var str string
	if err := json.Unmarshal(b, &str); err != nil {
		return fmt.Errorf("decode secret: %w", err)
	}
	*s = []byte(str)
	return nil
}

// installedCredential is the non-secret record of something this server
// installed, retained so shutdown can remove exactly what it added and the
// Credentials tool can list what is available.
type installedCredential struct {
	Name                string
	Target              string
	Username            string
	Type                macdesktop.CredentialType
	Persist             macdesktop.CredentialPersist
	AllowUnmaskedTarget bool
}

// loadCredentialsFile reads and validates the credentials document.
//
// The file's mode and owner are checked first (see checkCredentialsFilePerms):
// if anyone but the calling user can read it, the secrets are already
// disclosed and startup fails rather than proceeding.
func loadCredentialsFile(path string) ([]credentialEntry, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat credentials file: %w", err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%w: %q", ErrCredentialsFileDir, path)
	}
	if info.Size() > credentialFileMaxSize {
		return nil, fmt.Errorf("%w: %q is %d bytes (limit %d)",
			ErrCredentialsFileSize, path, info.Size(), credentialFileMaxSize)
	}
	if err := checkCredentialsFilePerms(path); err != nil {
		return nil, err
	}

	raw, err := os.ReadFile(path) //nolint:gosec // the operator names this path deliberately
	if err != nil {
		return nil, fmt.Errorf("read credentials file: %w", err)
	}
	defer wipe(raw)

	// A UTF-8 BOM is tolerated, as the policy loader does: a document authored
	// on Windows and copied over commonly carries one. Trimmed in place rather
	// than re-sliced so the wipe above still covers every byte that was read.
	body := bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})

	var doc credentialsDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("parse credentials file: %w", err)
	}
	if len(doc.Credentials) == 0 {
		return nil, fmt.Errorf("%w: %q", ErrNoCredentials, path)
	}

	seenNames := map[string]bool{}
	seenTargets := map[string]bool{}
	for i := range doc.Credentials {
		e := &doc.Credentials[i]
		e.Name = strings.TrimSpace(e.Name)
		e.Target = strings.TrimSpace(e.Target)

		switch {
		case e.Name == "":
			return nil, fmt.Errorf("%w: credential %d: name is required", ErrCredentialInvalid, i)
		case e.Target == "":
			return nil, fmt.Errorf("%w: credential %q: target is required", ErrCredentialInvalid, e.Name)
		case len(e.Secret) == 0:
			return nil, fmt.Errorf("%w: credential %q: secret is required", ErrCredentialInvalid, e.Name)
		case seenNames[e.Name]:
			return nil, fmt.Errorf("%w: credential %q: duplicate name", ErrCredentialInvalid, e.Name)
		case seenTargets[e.Target]:
			return nil, fmt.Errorf("%w: credential %q: duplicate target %q", ErrCredentialInvalid, e.Name, e.Target)
		case !e.Type.Valid():
			return nil, fmt.Errorf("%w: credential %q: type %q is not supported; only %q is",
				ErrCredentialInvalid, e.Name, e.Type, macdesktop.CredentialGeneric)
		case e.Persist != "" && e.Persist != macdesktop.PersistSession:
			// Session-scoped only, by design: everything this server installs
			// is removed again on every shutdown path, so a durable persistence
			// setting would be silently overridden. Install durable credentials
			// out of band.
			return nil, fmt.Errorf("%w: credential %q: persist %q is not supported; only %q is, "+
				"because installed credentials are removed when the session ends",
				ErrCredentialInvalid, e.Name, e.Persist, macdesktop.PersistSession)
		}
		seenNames[e.Name] = true
		seenTargets[e.Target] = true
	}
	return doc.Credentials, nil
}

// installCredentials writes each entry into the keychain and returns the
// non-secret records of what was installed. Every secret is wiped before
// returning, whether or not installation succeeded.
//
// A failure part-way through returns the entries installed so far alongside
// the error, so the caller can still clean them up.
func installCredentials(
	dsk *macdesktop.Desktop,
	entries []credentialEntry,
	auditLog *audit.AuditLog,
	logger *slog.Logger,
) ([]installedCredential, error) {
	installed := make([]installedCredential, 0, len(entries))

	defer func() {
		for i := range entries {
			wipe(entries[i].Secret)
		}
	}()

	for i := range entries {
		e := &entries[i]
		spec := macdesktop.CredentialSpec{
			Name:     e.Name,
			Target:   e.Target,
			Username: e.Username,
			Comment:  e.Comment,
			Secret:   e.Secret,
			Type:     e.Type,
			Persist:  e.Persist,
		}
		if err := dsk.WriteCredential(spec); err != nil {
			return installed, fmt.Errorf("install credential %q: %w", e.Name, err)
		}
		rec := installedCredential{
			Name: e.Name, Target: e.Target, Username: e.Username,
			Type: defaultType(e.Type), Persist: defaultPersist(e.Persist),
			AllowUnmaskedTarget: e.AllowUnmaskedTarget,
		}
		installed = append(installed, rec)
		if logger != nil {
			logger.Info("credential installed",
				"name", rec.Name, "target", rec.Target, "username", rec.Username,
				"type", string(rec.Type), "persist", string(rec.Persist))
		}
	}

	if auditLog != nil && len(installed) > 0 {
		// Names, targets and usernames only — never a secret.
		_, _ = auditLog.Append("credentials.installed", map[string]any{
			"count":   len(installed),
			"entries": auditView(installed),
		})
	}
	return installed, nil
}

// removeCredentials deletes everything this server installed. It is idempotent
// and best-effort: it is called from normal shutdown and from the kill-switch
// path, so one failure must not prevent the remaining removals.
func removeCredentials(
	dsk *macdesktop.Desktop,
	installed []installedCredential,
	auditLog *audit.AuditLog,
	logger *slog.Logger,
) {
	if len(installed) == 0 {
		return
	}
	var removed, failed int
	for _, c := range installed {
		if err := dsk.DeleteCredential(c.Target, c.Username); err != nil {
			failed++
			if logger != nil {
				logger.Warn("credential removal failed", "name", c.Name, "target", c.Target, "error", err.Error())
			}
			continue
		}
		removed++
	}
	if logger != nil {
		logger.Info("credentials removed", "removed", removed, "failed", failed)
	}
	if auditLog != nil {
		_, _ = auditLog.Append("credentials.removed", map[string]any{"removed": removed, "failed": failed})
	}
}

// credentialInfos converts the install records into the non-secret view the
// Credentials tool serves. Present is left false: the tool checks liveness
// against the store at call time rather than trusting a startup snapshot.
func credentialInfos(installed []installedCredential) []toolkit.CredentialInfo {
	out := make([]toolkit.CredentialInfo, 0, len(installed))
	for _, c := range installed {
		out = append(out, toolkit.CredentialInfo{
			Name:                c.Name,
			Target:              c.Target,
			Username:            c.Username,
			Type:                string(c.Type),
			Persist:             string(c.Persist),
			Injectable:          c.Type.Readable(),
			AllowUnmaskedTarget: c.AllowUnmaskedTarget,
		})
	}
	return out
}

// auditView renders installed credentials for the audit log: identifiers only.
func auditView(installed []installedCredential) []map[string]string {
	out := make([]map[string]string, 0, len(installed))
	for _, c := range installed {
		out = append(out, map[string]string{
			"name": c.Name, "target": c.Target, "username": c.Username,
			"type": string(c.Type), "persist": string(c.Persist),
		})
	}
	return out
}

func defaultType(t macdesktop.CredentialType) macdesktop.CredentialType {
	if t == "" {
		return macdesktop.CredentialGeneric
	}
	return t
}

func defaultPersist(p macdesktop.CredentialPersist) macdesktop.CredentialPersist {
	if p == "" {
		return macdesktop.PersistSession
	}
	return p
}

func wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// provisionCredentials installs the init-time credentials, if any, and returns
// the installed set together with an idempotent remover.
//
// Removal must happen exactly once even though two independent paths call it —
// the normal-exit defer and the kill-switch Finalize hook — hence the
// sync.Once. On error nothing is left installed (a partial install is rolled
// back), so the caller returns without needing the remover.
func provisionCredentials(
	dsk *macdesktop.Desktop,
	cfg Config,
	auditLog *audit.AuditLog,
	logger *slog.Logger,
) ([]installedCredential, func(), error) {
	var installed []installedCredential
	if cfg.CredentialsFile != "" {
		entries, err := loadCredentialsFile(cfg.CredentialsFile)
		if err != nil {
			return nil, nil, err
		}
		installed, err = installCredentials(dsk, entries, auditLog, logger)
		if err != nil {
			removeCredentials(dsk, installed, auditLog, logger) // roll back a partial install
			return nil, nil, err
		}
	}
	var once sync.Once
	return installed, func() {
		once.Do(func() { removeCredentials(dsk, installed, auditLog, logger) })
	}, nil
}
