//go:build darwin && (amd64 || arm64)

package macdesktop

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/deploymenttheory/go-bindings-macosplatform/bindings/frameworks/corefoundation"
	"github.com/deploymenttheory/macos-mcp-server/internal/clirunner"
	"github.com/deploymenttheory/macos-mcp-server/internal/plist"
)

// Preference domains are what `defaults` calls them: a bundle identifier, or
// the global domain. CFPreferences reads and writes them in-process, which is
// what makes the managed check possible — a profile-forced value reports as
// forced, and the engine refuses to write over it instead of writing a value
// the profile immediately shadows.

// GlobalDomain is the name `defaults` gives the global preference domain.
const GlobalDomain = "NSGlobalDomain"

// Errors the preference operations return.
var (
	ErrPreferenceManaged = errors.New(
		"the preference is managed by a configuration profile and cannot be changed here",
	)
	ErrPreferenceNotFound         = errors.New("no such preference")
	ErrUnsupportedPreferenceValue = errors.New("unsupported preference value type")
)

// PreferenceValue is one read preference.
type PreferenceValue struct {
	Domain  string `json:"domain"`
	Key     string `json:"key"`
	Value   any    `json:"value"`
	Managed bool   `json:"managed"`
}

// domainRef maps a domain name to its CFPreferences application id.
func domainRef(domain string) corefoundation.CFStringRef {
	if domain == "" || strings.EqualFold(domain, GlobalDomain) || domain == ".GlobalPreferences" {
		return corefoundation.CFStringRef{Object: corefoundation.KCFPreferencesAnyApplication()}
	}
	return cfStr(domain)
}

// PreferenceGet reads one key from a domain.
func (d *Desktop) PreferenceGet(domain, key string) (PreferenceValue, error) {
	pv := PreferenceValue{Domain: domain, Key: key}
	app := domainRef(domain)
	k := cfTemp(key)
	defer k.Release()
	v := corefoundation.CFPreferencesCopyAppValue(k, app)
	if v.IsNil() {
		return pv, fmt.Errorf("%w: %s %s", ErrPreferenceNotFound, domain, key)
	}
	defer v.Release()
	pv.Value = cfToGo(v)
	pv.Managed = corefoundation.CFPreferencesAppValueIsForced(k, app) != 0
	return pv, nil
}

// PreferenceList reads every key in a domain for the current user.
func (d *Desktop) PreferenceList(domain string) ([]PreferenceValue, error) {
	app := domainRef(domain)
	keys := corefoundation.CFPreferencesCopyKeyList(app,
		corefoundation.CFStringRef{Object: corefoundation.KCFPreferencesCurrentUser()},
		corefoundation.CFStringRef{Object: corefoundation.KCFPreferencesAnyHost()})
	if keys.IsNil() {
		return nil, nil
	}
	defer keys.Release()
	raw, _ := cfToGo(keys).([]any)
	names := make([]string, 0, len(raw))
	for _, k := range raw {
		if s, ok := k.(string); ok {
			names = append(names, s)
		}
	}
	sort.Strings(names)
	out := make([]PreferenceValue, 0, len(names))
	for _, name := range names {
		pv, err := d.PreferenceGet(domain, name)
		if err != nil {
			continue
		}
		out = append(out, pv)
	}
	return out, nil
}

// PreferenceSet writes one key. Scalars go through CFPreferences; structured
// values are written through the defaults CLI as a plist fragment, which is
// the one path that accepts them without hand-building CF collections.
func (d *Desktop) PreferenceSet(ctx context.Context, domain, key string, value any) error {
	app := domainRef(domain)
	k := cfTemp(key)
	defer k.Release()
	if corefoundation.CFPreferencesAppValueIsForced(k, app) != 0 {
		return fmt.Errorf("%w: %s %s", ErrPreferenceManaged, domain, key)
	}
	switch value.(type) {
	case []any, []string, map[string]any:
		frag, err := plist.MarshalValue(value)
		if err != nil {
			return fmt.Errorf("encode value: %w", err)
		}
		cliDomain := domain
		if domain == "" {
			cliDomain = GlobalDomain
		}
		if _, err := clirunner.Run(
			ctx,
			[]string{"defaults", "write", cliDomain, key, string(frag)},
			clirunner.Options{},
		); err != nil {
			return fmt.Errorf("defaults write: %w", err)
		}
		return nil
	}
	cf, err := goToCF(value)
	if err != nil {
		return err
	}
	defer cf.Release()
	corefoundation.CFPreferencesSetAppValue(k, corefoundation.CFPropertyListRef{Object: cf}, app)
	if corefoundation.CFPreferencesAppSynchronize(app) == 0 {
		return fmt.Errorf("%w: synchronize failed for %s", ErrAXFailed, domain)
	}
	return nil
}

// PreferenceDelete removes one key.
func (d *Desktop) PreferenceDelete(domain, key string) error {
	app := domainRef(domain)
	k := cfTemp(key)
	defer k.Release()
	if corefoundation.CFPreferencesAppValueIsForced(k, app) != 0 {
		return fmt.Errorf("%w: %s %s", ErrPreferenceManaged, domain, key)
	}
	corefoundation.CFPreferencesSetAppValue(k, corefoundation.CFPropertyListRef{}, app)
	corefoundation.CFPreferencesAppSynchronize(app)
	return nil
}

// PreferenceDomains lists the domains with preferences for the current user.
func (d *Desktop) PreferenceDomains() []string {
	arr := corefoundation.CFPreferencesCopyApplicationList(
		corefoundation.CFStringRef{Object: corefoundation.KCFPreferencesCurrentUser()},
		corefoundation.CFStringRef{Object: corefoundation.KCFPreferencesAnyHost()})
	if arr.IsNil() {
		return nil
	}
	defer arr.Release()
	raw, _ := cfToGo(arr).([]any)
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
