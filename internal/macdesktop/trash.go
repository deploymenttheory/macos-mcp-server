//go:build darwin && (amd64 || arm64)

package macdesktop

import (
	"fmt"

	"github.com/deploymenttheory/go-bindings-macosplatform/bindings/frameworks/foundation"
)

// TrashItem moves a file or directory to the user's Trash, where it can be
// recovered — the default for the FileSystem tool's delete, since a
// permanent delete is a different class of action.
func TrashItem(path string) error {
	fm := foundation.DefaultManager()
	if err := fm.TrashItemAtURLResultingItemURL(foundation.FileURLWithPath(path), ""); err != nil {
		return fmt.Errorf("trash %s: %w", path, err)
	}
	return nil
}
