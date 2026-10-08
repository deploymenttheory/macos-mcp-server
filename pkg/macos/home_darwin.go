//go:build darwin && (amd64 || arm64)

package macos

import "os"

func homeDir() (string, error) { return os.UserHomeDir() }
