//go:build darwin && (amd64 || arm64)

package macdesktop

import (
	"github.com/deploymenttheory/go-bindings-macosplatform/bindings/frameworks/hiservices"
)

// Rect is a screen rectangle in global display coordinates: points, with the
// origin at the top-left of the main display (the space CGEvent, the
// accessibility API and CGDisplayBounds all share).
type Rect struct {
	Left, Top, Right, Bottom int
}

// Width returns the rectangle width.
func (r Rect) Width() int { return r.Right - r.Left }

// Height returns the rectangle height.
func (r Rect) Height() int { return r.Bottom - r.Top }

// Empty reports whether the rectangle has no area.
func (r Rect) Empty() bool { return r.Width() <= 0 || r.Height() <= 0 }

// Center returns the rectangle's center point.
func (r Rect) Center() (x, y int) {
	return r.Left + r.Width()/2, r.Top + r.Height()/2
}

// ElementInfo is a snapshot of an accessibility element's key properties, read
// off the element while it is alive so the handle can then be released.
//
// ControlType is the platform-neutral name the journey vocabulary and the
// Windows server use (Button, Edit, CheckBox, ...), mapped from the AX role;
// Role keeps the raw macOS role for anyone who needs it.
type ElementInfo struct {
	Name         string
	ControlType  string
	Role         string
	Subrole      string
	AutomationID string
	Description  string
	Value        string
	Rect         Rect
	Enabled      bool
	Offscreen    bool
	ProcessID    int
	// IsPassword marks a field that masks its input (an AXSecureTextField). The
	// journey recorder reads it to redact keystrokes typed into such a field,
	// and credential injection requires it.
	IsPassword bool
}

// readElementInfo reads the commonly-needed properties of an element. It
// tolerates individual attribute failures (not every element implements every
// attribute) by leaving the corresponding field at its zero value. Main thread.
func readElementInfo(el hiservices.AXUIElementRef) ElementInfo {
	var info ElementInfo
	if el.IsNil() {
		return info
	}
	info.Role = axString(el, axRole)
	info.Subrole = axString(el, axSubrole)
	info.ControlType = controlTypeFor(info.Role, info.Subrole)
	info.Name = elementName(el, info.Role)
	info.AutomationID = axString(el, axIdentifier)
	info.Description = axString(el, axDescription)
	if info.Role != axRoleSecureTextField {
		info.Value = axString(el, axValue)
	}
	if enabled, ok := axBool(el, axEnabled); ok {
		info.Enabled = enabled
	} else {
		info.Enabled = true // an element without the attribute is not disabled
	}
	if p, ok := axPoint(el, axPosition); ok {
		if s, ok := axSize(el, axSizeAttr); ok {
			info.Rect = Rect{
				Left: int(p.X), Top: int(p.Y),
				Right: int(p.X + s.Width), Bottom: int(p.Y + s.Height),
			}
		}
	}
	info.Offscreen = info.Rect.Empty()
	info.ProcessID = axPID(el)
	info.IsPassword = info.Role == axRoleSecureTextField
	return info
}

// elementName picks the accessible name the way a screen reader would: the
// title, else the description, else — for static text and values that *are*
// the content — the value, else the help text.
func elementName(el hiservices.AXUIElementRef, role string) string {
	if t := axString(el, axTitle); t != "" {
		return t
	}
	if d := axString(el, axDescription); d != "" {
		return d
	}
	switch role {
	case axRoleStaticText, axRoleTextField, axRoleTextArea, axRoleLink, axRoleMenuItem, axRoleHeading, axRoleCell:
		if v := axString(el, axValue); v != "" {
			return v
		}
	}
	return axString(el, axHelp)
}

// AX roles the engine reasons about.
const (
	axRoleApplication     = "AXApplication"
	axRoleWindow          = "AXWindow"
	axRoleSheet           = "AXSheet"
	axRoleDrawer          = "AXDrawer"
	axRoleButton          = "AXButton"
	axRolePopUpButton     = "AXPopUpButton"
	axRoleMenuButton      = "AXMenuButton"
	axRoleCheckBox        = "AXCheckBox"
	axRoleRadioButton     = "AXRadioButton"
	axRoleRadioGroup      = "AXRadioGroup"
	axRoleTextField       = "AXTextField"
	axRoleSecureTextField = "AXSecureTextField"
	axRoleTextArea        = "AXTextArea"
	axRoleComboBox        = "AXComboBox"
	axRoleStaticText      = "AXStaticText"
	axRoleLink            = "AXLink"
	axRoleImage           = "AXImage"
	axRoleMenuBar         = "AXMenuBar"
	axRoleMenuBarItem     = "AXMenuBarItem"
	axRoleMenu            = "AXMenu"
	axRoleMenuItem        = "AXMenuItem"
	axRoleList            = "AXList"
	axRoleTable           = "AXTable"
	axRoleOutline         = "AXOutline"
	axRoleRow             = "AXRow"
	axRoleCell            = "AXCell"
	axRoleColumn          = "AXColumn"
	axRoleSlider          = "AXSlider"
	axRoleIncrementor     = "AXIncrementor"
	axRoleProgress        = "AXProgressIndicator"
	axRoleScrollBar       = "AXScrollBar"
	axRoleScrollArea      = "AXScrollArea"
	axRoleSplitGroup      = "AXSplitGroup"
	axRoleSplitter        = "AXSplitter"
	axRoleGroup           = "AXGroup"
	axRoleTabGroup        = "AXTabGroup"
	axRoleToolbar         = "AXToolbar"
	axRoleDisclosure      = "AXDisclosureTriangle"
	axRoleHeading         = "AXHeading"
	axRoleWebArea         = "AXWebArea"
	axRoleBrowser         = "AXBrowser"
	axRoleValueIndicator  = "AXValueIndicator"
	axRoleBusyIndicator   = "AXBusyIndicator"
	axRoleHelpTag         = "AXHelpTag"
	axRoleUnknown         = "AXUnknown"

	axSubroleTab         = "AXTabButton"
	axSubroleSwitch      = "AXSwitch"
	axSubroleToggle      = "AXToggle"
	axSubroleOutlineRow  = "AXOutlineRow"
	axSubroleTableRow    = "AXTableRow"
	axSubroleSearchField = "AXSearchField"
	axSubroleCloseButton = "AXCloseButton"
)

// controlTypeFor maps an AX role (and subrole) to the platform-neutral control
// type the journey vocabulary uses. The names are the UI Automation ones so a
// journey or a selector written for the Windows server reads the same here.
func controlTypeFor(role, subrole string) string {
	switch subrole {
	case axSubroleTab:
		return "TabItem"
	case axSubroleSwitch, axSubroleToggle:
		return "CheckBox"
	case axSubroleOutlineRow:
		return "TreeItem"
	case axSubroleTableRow:
		return "DataItem"
	}
	if ct, ok := roleControlTypes[role]; ok {
		return ct
	}
	if role == "" {
		return "Unknown"
	}
	return "Custom"
}

var roleControlTypes = map[string]string{
	axRoleApplication:     "Window",
	axRoleWindow:          "Window",
	axRoleSheet:           "Window",
	axRoleDrawer:          "Pane",
	axRoleButton:          "Button",
	axRolePopUpButton:     "ComboBox",
	axRoleMenuButton:      "SplitButton",
	axRoleCheckBox:        "CheckBox",
	axRoleRadioButton:     "RadioButton",
	axRoleRadioGroup:      "Group",
	axRoleTextField:       "Edit",
	axRoleSecureTextField: "Edit",
	axRoleTextArea:        "Edit",
	axRoleComboBox:        "ComboBox",
	axRoleStaticText:      "Text",
	axRoleLink:            "Hyperlink",
	axRoleImage:           "Image",
	axRoleMenuBar:         "MenuBar",
	axRoleMenuBarItem:     "MenuItem",
	axRoleMenu:            "Menu",
	axRoleMenuItem:        "MenuItem",
	axRoleList:            "List",
	axRoleTable:           "Table",
	axRoleOutline:         "Tree",
	axRoleRow:             "ListItem",
	axRoleCell:            "DataItem",
	axRoleColumn:          "Header",
	axRoleSlider:          "Slider",
	axRoleIncrementor:     "Spinner",
	axRoleProgress:        "ProgressBar",
	axRoleScrollBar:       "ScrollBar",
	axRoleScrollArea:      "Pane",
	axRoleSplitGroup:      "Pane",
	axRoleSplitter:        "Separator",
	axRoleGroup:           "Group",
	axRoleTabGroup:        "Tab",
	axRoleToolbar:         "ToolBar",
	axRoleDisclosure:      "TreeItem",
	axRoleHeading:         "Text",
	axRoleWebArea:         "Document",
	axRoleBrowser:         "Tree",
	axRoleValueIndicator:  "Thumb",
	axRoleBusyIndicator:   "ProgressBar",
	axRoleHelpTag:         "ToolTip",
	axRoleUnknown:         "Custom",
}

// interactiveControlTypes is the set of control types treated as directly
// interactive (clickable/typeable). It matches the Windows server's set so
// labels land on the same kinds of elements on both platforms.
var interactiveControlTypes = map[string]bool{
	"Button":      true,
	"CheckBox":    true,
	"ComboBox":    true,
	"Edit":        true,
	"Hyperlink":   true,
	"ListItem":    true,
	"MenuItem":    true,
	"RadioButton": true,
	"TabItem":     true,
	"SplitButton": true,
	"TreeItem":    true,
	"Slider":      true,
	"Spinner":     true,
	"Menu":        true,
	"DataItem":    true,
}

// isInteractiveControlType reports whether a control type is directly interactive.
func isInteractiveControlType(ct string) bool { return interactiveControlTypes[ct] }
