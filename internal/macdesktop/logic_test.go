//go:build darwin && (amd64 || arm64)

package macdesktop

import (
	"errors"
	"image"
	"image/color"
	"testing"
)

func TestKeyNameToVK(t *testing.T) {
	for name, want := range map[string]uint16{
		"a": vkA, "Z": vkZ, "5": vk5, "enter": vkReturn, "Return": vkReturn, "esc": vkEscape,
		"tab": vkTab, "space": vkSpace, "backspace": vkDelete, "delete": vkForwardDelete,
		"f12": vkF12, "up": vkUpArrow, ",": vkComma,
	} {
		vk, mod, ok := keyNameToVK(name)
		if !ok || mod || vk != want {
			t.Errorf("%q = (%#x, mod=%v, ok=%v), want %#x", name, vk, mod, ok, want)
		}
	}
	for name, want := range map[string]uint16{"cmd": vkCommand, "win": vkCommand, "ctrl": vkControl, "alt": vkOption, "shift": vkShift} {
		vk, mod, ok := keyNameToVK(name)
		if !ok || !mod || vk != want {
			t.Errorf("modifier %q = (%#x, mod=%v, ok=%v), want %#x", name, vk, mod, ok, want)
		}
	}
	if _, _, ok := keyNameToVK("nosuchkey"); ok {
		t.Error("unknown key must not resolve")
	}
}

func TestControlTypeMapping(t *testing.T) {
	for _, tc := range []struct{ role, subrole, want string }{
		{axRoleButton, "", "Button"},
		{axRoleTextField, "", "Edit"},
		{axRoleSecureTextField, "", "Edit"},
		{axRoleCheckBox, "", "CheckBox"},
		{axRoleRadioButton, axSubroleTab, "TabItem"},
		{axRoleCheckBox, axSubroleSwitch, "CheckBox"},
		{axRoleRow, axSubroleOutlineRow, "TreeItem"},
		{axRolePopUpButton, "", "ComboBox"},
		{axRoleWebArea, "", "Document"},
		{"AXSomethingNew", "", "Custom"},
		{"", "", "Unknown"},
	} {
		if got := controlTypeFor(tc.role, tc.subrole); got != tc.want {
			t.Errorf("controlTypeFor(%q,%q) = %q, want %q", tc.role, tc.subrole, got, tc.want)
		}
	}
	for _, ct := range []string{"Button", "Edit", "CheckBox", "MenuItem", "TabItem", "TreeItem"} {
		if !isInteractiveControlType(ct) {
			t.Errorf("%s should be interactive", ct)
		}
	}
	if isInteractiveControlType("Text") || isInteractiveControlType("Window") {
		t.Error("static text and windows are not interactive")
	}
}

func fakeState() *DesktopState {
	return &DesktopState{Interactive: []LabeledElement{
		{Label: 0, Info: ElementInfo{Name: "Save", ControlType: "Button"}},
		{Label: 1, Info: ElementInfo{Name: "Save As…", ControlType: "MenuItem"}},
		{Label: 2, Info: ElementInfo{Name: "save", ControlType: "Button", AutomationID: "save-secondary"}},
		{Label: 3, Info: ElementInfo{Name: "Cancel", ControlType: "Button"}},
	}}
}

func TestSelectorResolution(t *testing.T) {
	state := fakeState()
	resolve := func(spec SelectorSpec) (int, int, error) {
		pred, err := spec.predicate()
		if err != nil {
			return 0, 0, err
		}
		return resolveAmong(spec, matchState(state, pred))
	}
	// Exact is case-insensitive and therefore ambiguous between "Save" and "save".
	if _, n, err := resolve(SelectorSpec{Name: "Save"}); !errors.Is(err, ErrAmbiguousSelector) || n != 2 {
		t.Errorf("exact Save: want ambiguity over 2, got n=%d err=%v", n, err)
	}
	// Narrowed by control type it is still ambiguous (both Buttons); occurrence picks.
	if label, _, err := resolve(SelectorSpec{Name: "Save", ControlType: "Button", Occurrence: "1"}); err != nil || label != 2 {
		t.Errorf("occurrence 1: got label=%d err=%v", label, err)
	}
	if label, _, err := resolve(SelectorSpec{Name: "Save", Occurrence: OccurrenceFirst}); err != nil || label != 0 {
		t.Errorf("first: got label=%d err=%v", label, err)
	}
	// Exact never widens to "Save As…".
	if _, _, err := resolve(SelectorSpec{Name: "Save As"}); !errors.Is(err, ErrNoMatch) {
		t.Errorf("exact must not fall back to substring: %v", err)
	}
	if label, _, err := resolve(SelectorSpec{Name: "Save As", NameMatch: MatchContains}); err != nil || label != 1 {
		t.Errorf("contains: got label=%d err=%v", label, err)
	}
	if label, _, err := resolve(SelectorSpec{Name: "^Can", NameMatch: MatchMatches}); err != nil || label != 3 {
		t.Errorf("matches: got label=%d err=%v", label, err)
	}
	if label, _, err := resolve(SelectorSpec{AutomationID: "save-secondary"}); err != nil || label != 2 {
		t.Errorf("automation id: got label=%d err=%v", label, err)
	}
	if _, _, err := resolve(SelectorSpec{Name: "Save", Occurrence: "9"}); !errors.Is(err, ErrOccurrenceOutOfRan) {
		t.Errorf("out of range occurrence: %v", err)
	}
	if _, _, err := resolve(SelectorSpec{Name: "Save", NameMatch: "fuzzy"}); !errors.Is(err, ErrBadNameMatch) {
		t.Errorf("bad name_match: %v", err)
	}
}

func TestSelectTargetsDedupes(t *testing.T) {
	ws := []WindowInfo{
		{Handle: 1, Title: "A", Minimized: true},
		{Handle: 2, Title: "B", IsForeground: true},
		{Handle: 3, Title: "C"},
	}
	got := selectTargets(ws, true)
	if len(got) != 2 || got[0].Handle != 2 || got[1].Handle != 3 {
		t.Fatalf("selectTargets = %+v", got)
	}
	noFg := []WindowInfo{{Handle: 1, Minimized: true}, {Handle: 2}, {Handle: 3}}
	got = selectTargets(noFg, true)
	if len(got) != 2 || got[0].Handle != 2 {
		t.Fatalf("fallback primary must not be duplicated: %+v", got)
	}
}

func TestAppleScriptString(t *testing.T) {
	got := AppleScriptString(`say "hi" \ bye`)
	if got != `"say \"hi\" \\ bye"` {
		t.Fatalf("got %s", got)
	}
}

func TestScrollDeltas(t *testing.T) {
	if v, h := scrollDeltas("up"); v != 1 || h != 0 {
		t.Error("up")
	}
	if v, h := scrollDeltas("down"); v != -1 || h != 0 {
		t.Error("down")
	}
	if v, h := scrollDeltas("left"); v != 0 || h != 1 {
		t.Error("left")
	}
	if boundWheelClicks(0) != 1 || boundWheelClicks(99999) != maxWheelClicks {
		t.Error("bounds")
	}
}

func TestDownscale(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.Set(x, y, color.RGBA{R: 200, G: 100, B: 50, A: 255})
		}
	}
	out := downscale(img, 2)
	if out.Bounds().Dx() != 2 || out.Bounds().Dy() != 2 {
		t.Fatalf("size = %v", out.Bounds())
	}
	r, g, b, _ := out.At(0, 0).RGBA()
	if r>>8 != 200 || g>>8 != 100 || b>>8 != 50 {
		t.Fatalf("pixel = %d %d %d", r>>8, g>>8, b>>8)
	}
}

func TestResolveAppRefusesBarePrograms(t *testing.T) {
	if _, _, err := ResolveApp("/bin/ls"); !errors.Is(err, ErrNotAnAppBundle) {
		t.Errorf("a program path must be refused: %v", err)
	}
	if _, isURL, err := ResolveApp("https://example.com"); err != nil || !isURL {
		t.Errorf("a URL must be reported as one: %v", err)
	}
	if _, _, err := ResolveApp(""); !errors.Is(err, ErrAppNotFound) {
		t.Errorf("empty: %v", err)
	}
}

func TestScreenPointBounds(t *testing.T) {
	if _, err := screenPoint(10, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := screenPoint(1<<20, 0); !errors.Is(err, ErrCoordinateOutOfRange) {
		t.Fatalf("want out of range, got %v", err)
	}
}
