//go:build darwin && (amd64 || arm64)

// Keychain integration.
//
// Credentials supplied to the server at init are installed into the calling
// user's login keychain as generic passwords, so anything running in that user
// context can consume them the normal way, and removed again on every
// shutdown path.
//
// The plaintext secret is confined to this file. It is read back from the
// keychain only inside InjectCredential, converted straight to keystrokes as
// UTF-16 units, and the intermediate buffers are zeroed. No function here
// returns a secret to a caller, so a secret can never reach a tool result, the
// audit log, or the model's context.

package macdesktop

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"

	"github.com/deploymenttheory/go-bindings-macosplatform/bindings/frameworks/coregraphics"
	"github.com/deploymenttheory/go-bindings-macosplatform/opinionated/tools/keychain"
)

// credMaxSecretSize bounds a secret; the keychain accepts far more, but a
// credential this long is a mistake, not a password.
const credMaxSecretSize = 4096

// Errors the credential store returns.
var (
	ErrCredentialNotFound = errors.New("credential not found")
	ErrCredentialEmpty    = errors.New("credential has an empty secret")
	ErrSecretTooLarge     = errors.New("secret exceeds the size limit")
	ErrSecretNotUTF8      = errors.New("secret is not valid UTF-8")
	ErrUnknownCredType    = errors.New("unknown credential type")
	ErrUnknownPersist     = errors.New("unknown persistence")
)

// CredentialType is the keychain item class.
type CredentialType string

const (
	// CredentialGeneric is a generic password: an application-defined
	// credential, the right class for app, web and API secrets. The default.
	CredentialGeneric CredentialType = "generic"
)

// Readable reports whether the secret can be read back by this process. Every
// class the macOS store supports can; the method exists so the tool layer and
// the Windows server read the same.
func (t CredentialType) Readable() bool { return t == CredentialGeneric || t == "" }

// Valid reports whether the type is one this store supports.
func (t CredentialType) Valid() bool { return t.Readable() }

// CredentialPersist controls how long the credential is retained.
type CredentialPersist string

const (
	// PersistSession removes the credential when the session ends. The only
	// supported value: everything this server installs is removed on exit.
	PersistSession CredentialPersist = "session"
)

// CredentialSpec is one credential to install. Secret is a byte slice rather
// than a string so the caller can zero it after installation.
type CredentialSpec struct {
	Name     string
	Target   string
	Username string
	Comment  string
	Secret   []byte
	Type     CredentialType
	Persist  CredentialPersist
}

// Point is a screen location in points.
type Point struct{ X, Y int }

// serviceFor is the keychain service a target is stored under. Namespaced so
// the server's items are distinguishable from the user's own, and so removal
// on exit cannot touch anything the server did not add.
const servicePrefix = "com.deploymenttheory.macos-mcp-server:"

func serviceFor(target string) string { return servicePrefix + target }

// WriteCredential installs a credential into the login keychain. An existing
// item under the same target and username is updated.
func (d *Desktop) WriteCredential(spec CredentialSpec) error {
	if !spec.Type.Valid() {
		return fmt.Errorf("%w: %q (want generic)", ErrUnknownCredType, spec.Type)
	}
	if spec.Persist != "" && spec.Persist != PersistSession {
		return fmt.Errorf("%w: %q (want session)", ErrUnknownPersist, spec.Persist)
	}
	if len(spec.Secret) == 0 {
		return ErrCredentialEmpty
	}
	if len(spec.Secret) > credMaxSecretSize {
		return fmt.Errorf("%w: %d bytes (limit %d)", ErrSecretTooLarge, len(spec.Secret), credMaxSecretSize)
	}
	if !utf8.Valid(spec.Secret) {
		return ErrSecretNotUTF8
	}
	item := keychain.GenericPassword{
		Service: serviceFor(spec.Target),
		Account: spec.Username,
		Label:   "macos-mcp-server: " + spec.Name,
		Secret:  spec.Secret,
	}
	if _, found, err := keychain.ReadGenericPassword(item.Service, item.Account); err == nil && found {
		if err := keychain.UpdateGenericPassword(item); err != nil {
			return fmt.Errorf("update keychain item: %w", err)
		}
		return nil
	}
	if err := keychain.CreateGenericPassword(item); err != nil {
		return fmt.Errorf("create keychain item: %w", err)
	}
	return nil
}

// DeleteCredential removes an installed credential.
func (d *Desktop) DeleteCredential(target, username string) error {
	if err := keychain.DeleteGenericPassword(serviceFor(target), username); err != nil {
		return fmt.Errorf("delete keychain item: %w", err)
	}
	return nil
}

// CredentialPresent reports whether the credential is in the keychain.
func (d *Desktop) CredentialPresent(target, username string) (bool, error) {
	_, found, err := keychain.ReadGenericPassword(serviceFor(target), username)
	if err != nil {
		return false, fmt.Errorf("read keychain item: %w", err)
	}
	return found, nil
}

// ErrInjectTargetNotMasked is returned when a credential injection cannot be
// confirmed to be landing in a control that masks its input.
var ErrInjectTargetNotMasked = errors.New("refusing to inject the credential")

const injectRemedy = ` Set "allow_unmasked_target": true on this credential if the destination ` +
	`genuinely cannot report itself as a secure text field (a terminal, some Electron ` +
	`and Java applications); otherwise target the password field itself.`

// InjectRemedy names the documented escape hatch, for the tool layer to append
// to a refusal.
func InjectRemedy() string { return injectRemedy }

// requireMaskedFocus confirms the focused element is a secure text field.
// Main thread, immediately before the keystrokes.
func (d *Desktop) requireMaskedFocus() error {
	app, ok := axElement(d.systemWide, axFocusedApplication)
	if !ok {
		return fmt.Errorf("%w: no focused application", ErrInjectTargetNotMasked)
	}
	defer app.Release()
	el, ok := axElement(app, axFocusedUIElement)
	if !ok {
		return fmt.Errorf("%w: no focused element", ErrInjectTargetNotMasked)
	}
	defer el.Release()
	if role := axString(el, axRole); role != axRoleSecureTextField {
		return fmt.Errorf("%w: the focused element is %s, not a secure text field",
			ErrInjectTargetNotMasked, controlTypeFor(role, axString(el, axSubrole)))
	}
	return nil
}

// InjectCredential types the stored secret at the current focus, optionally
// clicking a target first so the click and the keystrokes are one serialised
// operation. It returns how many UTF-16 units were typed — never the secret.
func (d *Desktop) InjectCredential(target, username string, at *Point, allowUnmasked bool) (int, error) {
	var typed int
	err := d.Do(func() error {
		src := eventSource()
		defer src.Release()
		if at != nil {
			p, err := screenPoint(at.X, at.Y)
			if err != nil {
				return err
			}
			if err := moveTo(src, p); err != nil {
				return err
			}
			if err := clickAt(src, p, "left", 1, 0); err != nil {
				return err
			}
		}
		// The destination is checked on the main thread, after the click and
		// immediately before the keystrokes — so what is verified is what
		// receives them.
		if !allowUnmasked {
			if err := d.requireMaskedFocus(); err != nil {
				return err
			}
		}
		units, err := readSecretUnits(target, username)
		if err != nil {
			return err
		}
		defer zeroUnits(units)
		for i := 0; i < len(units); i += unicodeChunk {
			end := min(i+unicodeChunk, len(units))
			chunk := units[i:end]
			for _, down := range []bool{true, false} {
				ev := coregraphics.CGEventCreateKeyboardEvent(src, 0, down)
				if ev.IsNil() {
					return ErrEventRefused
				}
				coregraphics.CGEventKeyboardSetUnicodeString(ev, len(chunk), unsafe.Pointer(&chunk[0]))
				if err := post(ev); err != nil {
					return err
				}
			}
			typed += len(chunk)
		}
		return nil
	})
	return typed, err
}

// readSecretUnits reads the secret and returns it as UTF-16 code units — never
// as a string, so a refactor cannot hand it to a caller. The caller zeroes it.
func readSecretUnits(target, username string) ([]uint16, error) {
	item, found, err := keychain.ReadGenericPassword(serviceFor(target), username)
	if err != nil {
		return nil, fmt.Errorf("read keychain item: %w", err)
	}
	if !found {
		return nil, fmt.Errorf("%w: %s", ErrCredentialNotFound, target)
	}
	defer zeroBytes(item.Secret)
	if len(item.Secret) == 0 {
		return nil, ErrCredentialEmpty
	}
	runes := []rune(string(item.Secret))
	units := utf16.Encode(runes)
	for i := range runes {
		runes[i] = 0
	}
	return units, nil
}

func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

func zeroUnits(u []uint16) {
	for i := range u {
		u[i] = 0
	}
}

// RecoverCredentials removes every keychain item this server installed in an
// earlier session that never got to remove it (a crash past the deferred
// cleanup, a SIGKILL). The namespaced service prefix is what makes this safe:
// nothing but this server writes items under it. It runs before each
// install, the way the egress enforcer recovers its rules.
func (d *Desktop) RecoverCredentials() (int, error) {
	items, err := keychain.ListGenericPasswords()
	if err != nil {
		return 0, fmt.Errorf("list keychain items: %w", err)
	}
	removed := 0
	var errs []error
	for _, it := range items {
		if !strings.HasPrefix(it.Service, servicePrefix) {
			continue
		}
		if err := keychain.DeleteGenericPassword(it.Service, it.Account); err != nil {
			errs = append(errs, fmt.Errorf("delete %s: %w", it.Service, err))
			continue
		}
		removed++
	}
	return removed, errors.Join(errs...)
}
