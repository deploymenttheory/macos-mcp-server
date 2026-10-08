//go:build darwin && (amd64 || arm64)

package macdesktop

import (
	"errors"

	"github.com/deploymenttheory/go-bindings-macosplatform/bindings/frameworks/appkit"
)

// ErrClipboardWrite reports a pasteboard write the system refused.
var ErrClipboardWrite = errors.New("could not write to the pasteboard")

// ClipboardGet returns the general pasteboard's text, "" when it holds none.
func (d *Desktop) ClipboardGet() (string, error) {
	var text string
	err := d.Do(func() error {
		text = appkit.GeneralPasteboard().StringForType(appkit.NSPasteboardTypeString())
		return nil
	})
	return text, err
}

// ClipboardSet replaces the general pasteboard's contents with text.
func (d *Desktop) ClipboardSet(text string) error {
	return d.Do(func() error {
		pb := appkit.GeneralPasteboard()
		pb.ClearContents()
		if !pb.SetStringForType(text, appkit.NSPasteboardTypeString()) {
			return ErrClipboardWrite
		}
		return nil
	})
}
