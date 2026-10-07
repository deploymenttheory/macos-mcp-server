//go:build darwin && (amd64 || arm64)

package macmcp

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/deploymenttheory/agentweave-harness/guardrails/audit"
	"github.com/deploymenttheory/agentweave-harness/guardrails/policy"
	"github.com/deploymenttheory/macos-mcp-server/internal/clirunner"
)

// anchorTag is the syslog tag the anchor writes under; the unified log
// records it as the process and the message carries the head.
const anchorTag = "macos-mcp-server"

// anchorWriter publishes an audit chain head somewhere the running session
// cannot reach back into.
type anchorWriter interface {
	publish(seq uint64, head string)
}

// startAnchor launches the anchoring loop for a configured destination,
// returning a stop function. The policy's only destination name is
// "eventlog" (the schema is shared with the Windows server); on macOS it
// means the unified log, written through logger(1) so the entry is owned by
// the system log store rather than by this process. Anchoring is
// defence-in-depth: if the writer cannot be opened it degrades to chain-only
// anchoring with a warning, and never fails startup.
func startAnchor(ctx context.Context, p policy.AnchorPolicy, auditLog *audit.AuditLog, logger *slog.Logger) func() {
	if p.Destination == "" {
		return func() {}
	}

	var w anchorWriter
	switch p.Destination {
	case policy.AnchorEventLog:
		w = newUnifiedLogWriter(logger)
	default:
		logger.Warn("audit anchor: unknown destination, anchoring to the chain only",
			"destination", p.Destination)
	}

	loopCtx, cancel := context.WithCancel(ctx)
	go runAnchor(loopCtx, p.Cadence.Std(), auditLog, w)
	return cancel
}

// runAnchor anchors the chain head on every tick until the context is cancelled.
func runAnchor(ctx context.Context, cadence time.Duration, auditLog *audit.AuditLog, w anchorWriter) {
	ticker := time.NewTicker(cadence)
	defer ticker.Stop()
	last := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			last = anchorOnce(auditLog, last, w)
		}
	}
}

// anchorOnce writes one audit.anchored entry naming the current head and
// publishes it off-process, but only when the head has advanced since
// lastHead. Without that guard an idle server would grow its own chain by one
// anchor entry per tick forever. It returns the post-append head, so a quiet
// interval compares equal and is skipped.
func anchorOnce(auditLog *audit.AuditLog, lastHead string, w anchorWriter) string {
	seq, head := auditLog.Head()
	if head == "" || head == lastHead {
		return lastHead
	}
	_, _ = auditLog.Append("audit.anchored", map[string]any{
		"anchored_seq":  seq,
		"anchored_head": head,
	})
	if w != nil {
		w.publish(seq, head)
	}
	_, newHead := auditLog.Head()
	return newHead
}

// unifiedLogWriter publishes heads to the unified log via logger(1).
type unifiedLogWriter struct {
	exe    string
	logger *slog.Logger
}

func newUnifiedLogWriter(logger *slog.Logger) anchorWriter {
	exe, err := clirunner.LookPath("logger")
	if err != nil {
		logger.Warn("audit anchor: logger(1) unavailable, anchoring to the chain only", "error", err)
		return nil
	}
	return &unifiedLogWriter{exe: exe, logger: logger}
}

func (w *unifiedLogWriter) publish(seq uint64, head string) {
	msg := fmt.Sprintf("%s audit anchor: seq=%s head=%s", anchorTag, strconv.FormatUint(seq, 10), head)
	if _, err := clirunner.Run(context.Background(),
		[]string{w.exe, "-t", anchorTag, "-p", "user.notice", msg},
		clirunner.Options{Timeout: 10 * time.Second}); err != nil {
		w.logger.Warn("audit anchor: could not write to the unified log", "error", err)
	}
}
