//go:build darwin && (amd64 || arm64)

package macdesktop

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/deploymenttheory/macos-mcp-server/internal/clirunner"
)

// notify posts a user notification through System Events' `display
// notification`. UserNotifications.framework needs a bundled application with
// an identifier to post under, which a bare binary is not; AppleScript posts
// under Script Editor's identity, which is the honest attribution for a
// notification the model authored.
func notify(ctx context.Context, title, message string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	script := fmt.Sprintf("display notification %s with title %s",
		AppleScriptString(message), AppleScriptString(title))
	_, err := clirunner.RunScript(ctx, "osascript", script, clirunner.Options{Timeout: 15 * time.Second})
	if err != nil {
		return fmt.Errorf("display notification: %w", err)
	}
	return nil
}

// AppleScriptString renders s as an AppleScript string literal. AppleScript
// strings escape only the backslash and the double quote; everything else,
// including newlines, is literal.
func AppleScriptString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// PostNotification raises a user notification and reports failure, for the
// Notification tool. Notify is the best-effort variant the guardrails use.
func (d *Desktop) PostNotification(ctx context.Context, title, message string) error {
	return notify(ctx, title, message)
}
