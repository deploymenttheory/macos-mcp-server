//go:build darwin && (amd64 || arm64)

package macdesktop

import (
	"fmt"

	"github.com/deploymenttheory/go-bindings-macosplatform/bindings/frameworks/hiservices"
)

// Action-based operations act on an accessibility element directly through its
// actions and settable attributes (AXPress, AXValue, AXSelected, AXExpanded)
// rather than by synthesizing mouse/keyboard input. This is the reliable path
// preferred by RPA: it does not depend on the window being focused, unoccluded,
// or on-screen. Click/Type remain as the fallback for controls that expose no
// suitable action.

// zeroElement is the nil element handle.
func zeroElement() hiservices.AXUIElementRef { return hiservices.AXUIElementRef{} }

// elementForLabel returns the retained element for a snapshot label, or a nil
// handle. Main-thread use only.
func (d *Desktop) elementForLabel(label int) hiservices.AXUIElementRef {
	d.stateMu.Lock()
	defer d.stateMu.Unlock()
	if d.lastState == nil {
		return zeroElement()
	}
	for i := range d.lastState.Interactive {
		if d.lastState.Interactive[i].Label == label {
			return d.lastState.Interactive[i].element
		}
	}
	return zeroElement()
}

// withLabel resolves the labeled element and passes it to fn on the main thread.
func (d *Desktop) withLabel(label int, fn func(hiservices.AXUIElementRef) error) error {
	return d.Do(func() error {
		el := d.elementForLabel(label)
		if el.IsNil() {
			return fmt.Errorf("%w: %d", ErrLabelNotFound, label)
		}
		return fn(el)
	})
}

// InvokeLabel activates the labeled control (AXPress: buttons, links, menu items).
func (d *Desktop) InvokeLabel(label int) error {
	return d.withLabel(label, func(el hiservices.AXUIElementRef) error {
		if err := axPerform(el, axPressAction); err != nil {
			return fmt.Errorf("element [%d] does not support invoke: %w", label, err)
		}
		return nil
	})
}

// SetValueLabel sets the labeled control's value directly (text fields),
// without synthesizing keystrokes.
func (d *Desktop) SetValueLabel(label int, value string) error {
	return d.withLabel(label, func(el hiservices.AXUIElementRef) error {
		if err := axSetString(el, axValue, value); err != nil {
			return fmt.Errorf("element [%d] does not support set_value: %w", label, err)
		}
		return nil
	})
}

// ToggleLabel toggles the labeled control (checkboxes, switches). macOS has
// no toggle action distinct from press; a press on a checkbox is a toggle.
func (d *Desktop) ToggleLabel(label int) error {
	return d.withLabel(label, func(el hiservices.AXUIElementRef) error {
		if err := axPerform(el, axPressAction); err != nil {
			return fmt.Errorf("element [%d] does not support toggle: %w", label, err)
		}
		return nil
	})
}

// SelectLabel selects the labeled item (list rows, radio buttons, tabs).
func (d *Desktop) SelectLabel(label int) error {
	return d.withLabel(label, func(el hiservices.AXUIElementRef) error {
		if err := axSetBool(el, axSelected, true); err != nil {
			// Tabs and radio buttons select on press.
			if perr := axPerform(el, axPressAction); perr != nil {
				return fmt.Errorf("element [%d] does not support select: %w", label, err)
			}
		}
		return nil
	})
}

// ExpandLabel / CollapseLabel operate disclosure state (pop-up buttons,
// outline rows, disclosure triangles).
func (d *Desktop) ExpandLabel(label int) error {
	return d.withLabel(label, func(el hiservices.AXUIElementRef) error {
		if err := axSetBool(el, axExpanded, true); err != nil {
			if perr := axPerform(el, axPressAction); perr != nil {
				return fmt.Errorf("element [%d] does not support expand: %w", label, err)
			}
		}
		return nil
	})
}

// CollapseLabel collapses the labeled element.
func (d *Desktop) CollapseLabel(label int) error {
	return d.withLabel(label, func(el hiservices.AXUIElementRef) error {
		if err := axSetBool(el, axExpanded, false); err != nil {
			return fmt.Errorf("element [%d] does not support collapse: %w", label, err)
		}
		return nil
	})
}

// ReadLabel returns the labeled element's current name and value, for
// verification and assertions.
func (d *Desktop) ReadLabel(label int) (name, value string, err error) {
	err = d.withLabel(label, func(el hiservices.AXUIElementRef) error {
		role := axString(el, axRole)
		name = elementName(el, role)
		if role != axRoleSecureTextField {
			value = axString(el, axValue)
		}
		return nil
	})
	return name, value, err
}

// ElementState is what an assertion can read about one element. It is a snapshot
// of the properties that carry state, taken together on the main thread so the
// fields describe one consistent moment rather than several.
//
// The Has* flags distinguish "false" from "the control does not have this kind of
// state": asserting that a Button is unchecked should report that a Button has no
// toggle state, not that it is unchecked.
type ElementState struct {
	Name        string
	ControlType string
	Value       string
	Enabled     bool
	Checked     bool
	Selected    bool
	Focused     bool
	Expanded    bool

	HasValue          bool
	HasToggle         bool
	HasSelection      bool
	HasInvoke         bool
	HasExpandCollapse bool
}

// ElementState reads the current state of a labeled element.
func (d *Desktop) ElementState(label int) (ElementState, error) {
	var st ElementState
	err := d.withLabel(label, func(el hiservices.AXUIElementRef) error {
		st = readElementState(el)
		return nil
	})
	return st, err
}

// readElementState reads everything an assertion or the recorder can ask about
// one element. Main thread.
func readElementState(el hiservices.AXUIElementRef) ElementState {
	var st ElementState
	if el.IsNil() {
		return st
	}
	role := axString(el, axRole)
	subrole := axString(el, axSubrole)
	st.Name = elementName(el, role)
	st.ControlType = controlTypeFor(role, subrole)
	if enabled, ok := axBool(el, axEnabled); ok {
		st.Enabled = enabled
	} else {
		st.Enabled = true
	}
	st.Focused, _ = axBool(el, axFocused)

	// Value: text-bearing controls carry it as a string; toggles carry it as a
	// number (0/1/2) which the description renders.
	switch st.ControlType {
	case "CheckBox", "RadioButton":
		v, _ := axCopy(el, axValue)
		if v != nil {
			st.HasToggle = true
			st.Checked = cfToString(v) == "1"
			v.Release()
		}
	case "Edit", "ComboBox", "Slider", "Spinner", "Text":
		if role != axRoleSecureTextField {
			v, _ := axCopy(el, axValue)
			if v != nil {
				st.HasValue = true
				st.Value = cfToString(v)
				v.Release()
			}
		}
	}
	if sel, ok := axBool(el, axSelected); ok {
		st.HasSelection, st.Selected = true, sel
	}
	if exp, ok := axBool(el, axExpanded); ok {
		st.HasExpandCollapse, st.Expanded = true, exp
	}
	st.HasInvoke = isInteractiveControlType(st.ControlType) && st.ControlType != "Edit"
	return st
}
