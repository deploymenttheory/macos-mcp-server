//go:build darwin && (amd64 || arm64)

package macdesktop

import (
	"fmt"
	"strings"

	"github.com/deploymenttheory/go-bindings-macosplatform/bindings/frameworks/hiservices"
)

// LabeledElement is an interactive element assigned a stable label within a
// snapshot, together with its click point (the center of its bounding rect).
type LabeledElement struct {
	Label   int
	Info    ElementInfo
	CenterX int
	CenterY int

	// element is the live accessibility element, retained so that action-based
	// operations (Invoke/SetValue/Toggle/...) can act on it directly rather than
	// via synthetic input. It is released when the snapshot is replaced (see
	// releaseStateElements). Main-thread use only.
	element hiservices.AXUIElementRef
}

const (
	// maxTreeNodes bounds the total number of elements visited across a whole
	// snapshot, so a pathological UI cannot make traversal unbounded.
	maxTreeNodes = 2500
	// maxTreeDepth bounds recursion depth.
	maxTreeDepth = 40
)

// treeBuilder accumulates the labeled interactive elements and a semantic tree
// rendering across one or more windows.
type treeBuilder struct {
	interactive []LabeledElement
	lines       []string
	nodes       int
}

// visitWindow renders one window's subtree, appending to the builder. The
// window element is not released here (the caller owns it).
func (tb *treeBuilder) visitWindow(window hiservices.AXUIElementRef, w WindowInfo) {
	fg := ""
	if w.IsForeground {
		fg = " (focused)"
	}
	min := ""
	if w.Minimized {
		min = " (minimized)"
	}
	tb.lines = append(tb.lines, fmt.Sprintf("Window: %q [pid %d]%s%s", w.Title, w.ProcessID, fg, min))
	tb.visitChildren(window, 1)
}

func (tb *treeBuilder) visitChildren(parent hiservices.AXUIElementRef, depth int) {
	if depth > maxTreeDepth || tb.nodes >= maxTreeNodes {
		return
	}
	for _, child := range axElements(parent, axChildren) {
		if tb.nodes >= maxTreeNodes {
			child.Release()
			break
		}
		tb.visit(child, depth)
	}
}

// visit renders one element and recurses. It takes ownership of el: the
// element is either retained in the labeled list or released here.
func (tb *treeBuilder) visit(el hiservices.AXUIElementRef, depth int) {
	tb.nodes++
	info := readElementInfo(el)
	indent := strings.Repeat("  ", depth)

	interactive := isInteractiveControlType(info.ControlType) && info.Enabled && !info.Offscreen
	switch {
	case interactive:
		label := len(tb.interactive)
		cx, cy := info.Rect.Center()
		tb.interactive = append(tb.interactive, LabeledElement{
			Label: label, Info: info, CenterX: cx, CenterY: cy, element: el,
		})
		tb.lines = append(tb.lines,
			fmt.Sprintf("%s[%d] %s %q (%d,%d)", indent, label, info.ControlType, info.Name, cx, cy))
	case info.Name != "":
		// Informative/structural node with a name: include for context.
		tb.lines = append(tb.lines, fmt.Sprintf("%s%s %q", indent, info.ControlType, info.Name))
	}

	tb.visitChildren(el, depth+1)
	if !interactive {
		el.Release()
	}
}
