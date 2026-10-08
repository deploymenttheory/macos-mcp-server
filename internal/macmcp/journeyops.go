//go:build darwin && (amd64 || arm64)

package macmcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/deploymenttheory/agentweave-harness/guardrails/audit"
	"github.com/deploymenttheory/agentweave-harness/guardrails/policy"
	"github.com/deploymenttheory/agentweave-harness/guardrails/signals"
	"github.com/deploymenttheory/macos-mcp-server/internal/macdesktop"
	"github.com/deploymenttheory/macos-mcp-server/pkg/macos"
	"github.com/deploymenttheory/mcp-server-core/journeys"
	"github.com/deploymenttheory/mcp-server-core/runtime"
	"github.com/deploymenttheory/mcp-server-core/toolkit"
)

// errNoOutputPath reports a record request with no --out destination.
var errNoOutputPath = errors.New("an output path is required (--out)")

// RunJourney compiles a journey file to a plan and executes it against the
// live desktop, through the same planner Apply uses: every step is
// policy-evaluated, audited as plan.step, and fail-stopped on the first
// failure. A failed assertion is a failed step, which is what makes a journey
// a test.
func RunJourney(ctx context.Context, cfg Config, path string) (runtime.JourneyReport, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // an operator-supplied journey path
	if err != nil {
		return runtime.JourneyReport{}, fmt.Errorf("read journey %s: %w", path, err)
	}
	logger, cleanup, err := runtime.NewLogger(cfg.LogFile)
	if err != nil {
		return runtime.JourneyReport{}, fmt.Errorf("logger: %w", err)
	}
	defer cleanup()

	reg := runtime.NewGuardrailRegistry(envNames, logger)
	devicePolicy, err := runtime.LoadPolicy(cfg.PolicyConfig, reg, logger)
	if err != nil {
		return runtime.JourneyReport{}, fmt.Errorf("policy: %w", err)
	}
	cfg.EnforceHTTPS = devicePolicy.EnforceHTTPS
	// A journey's assertions and evidence compile to the testing tools, so that
	// toolset must be served whatever else the selection is.
	cfg.Toolsets = runtime.WithToolset(cfg.Toolsets, string(macos.ToolsetTesting.ID))

	sessionStamp := runtime.SessionStamp()
	auditKey := runtime.ResolveAuditKey(envNames, devicePolicy.Transparency.AuditDestination, logger)
	dest, err := audit.OpenDestination(devicePolicy.Transparency.AuditDestination, sessionStamp, auditKey)
	if err != nil {
		return runtime.JourneyReport{}, fmt.Errorf("audit log: %w", err)
	}
	auditLog := audit.NewAuditLog(dest, audit.WithHMACKey(auditKey))
	defer func() { _ = auditLog.Close() }()

	dsk, err := macdesktop.New(logger, macdesktop.Options{SecurityOverlay: devicePolicy.Transparency.Banner})
	if err != nil {
		return runtime.JourneyReport{}, fmt.Errorf("failed to start desktop engine: %w", err)
	}
	defer func() { _ = dsk.Close() }()

	envFn := func() *signals.Env { return guardrailEnv(cfg, dsk, logger) }
	engine := policy.NewEngine(devicePolicy, reg, nil, envFn)

	inv, _, err := buildInventory(cfg, false)
	if err != nil {
		return runtime.JourneyReport{}, fmt.Errorf("build inventory: %w", err)
	}
	engine.SetIndex(runtime.NewToolIndex(ctx, inv))

	evidenceRoot := devicePolicy.Transparency.EvidenceDir
	deps := macos.NewBaseDeps(dsk, logger, nil)
	deps.WithEnforceHTTPS(cfg.EnforceHTTPS).
		WithProtectedPaths(guardrailPaths(cfg, devicePolicy)).
		WithEvidenceDir(runtime.EvidenceSubdir(evidenceRoot, "evidence"))

	rep, err := runtime.RunJourney(ctx, raw, path, runtime.JourneyRun{
		Engine: engine, Inventory: inv, Deps: deps,
		EvidenceSink: deps, ReadRegister: deps,
		SetPlanner: func(p toolkit.Planner) { deps.WithPlanner(p) },
		AuditLog:   auditLog, SessionStamp: sessionStamp, EvidenceRoot: evidenceRoot,
		ServiceName: ServerName, Version: cfg.Version, Logger: logger,
	})
	if err != nil {
		return runtime.JourneyReport{}, fmt.Errorf("journey: %w", err)
	}
	return rep, nil
}

// RecordJourney captures a human's interaction with the desktop and writes it
// as a journey file: each click resolved to a UI element, each keystroke to a
// character (redacting password fields), compiled through journeys.Emit into
// a reviewable draft to confirm and add assertions to.
//
// Recording ends when the user presses the stop key (F9) or ctx is cancelled.
func RecordJourney(ctx context.Context, cfg Config, name, out string) (journeys.Journey, error) {
	if out == "" {
		return journeys.Journey{}, errNoOutputPath
	}
	logger, cleanup, err := runtime.NewLogger(cfg.LogFile)
	if err != nil {
		return journeys.Journey{}, fmt.Errorf("logger: %w", err)
	}
	defer cleanup()

	dsk, err := macdesktop.New(logger, macdesktop.Options{})
	if err != nil {
		return journeys.Journey{}, fmt.Errorf("failed to start desktop engine: %w", err)
	}
	defer func() { _ = dsk.Close() }()

	var events []journeys.Event
	err = dsk.RecordInput(ctx, macdesktop.DefaultRecordStopKey, func(in macdesktop.RecordedInput) {
		if e, ok := mapRecordedInput(in); ok {
			events = append(events, e)
		}
	})
	if err != nil {
		return journeys.Journey{}, fmt.Errorf("record input: %w", err)
	}

	journey := journeys.Emit(name, events)
	blob, err := json.MarshalIndent(journey, "", "  ")
	if err != nil {
		return journeys.Journey{}, fmt.Errorf("render journey: %w", err)
	}
	// Owner-only: a journey is not meant to hold a secret, but that rests on the
	// recorder's redaction being right.
	if err := os.WriteFile(out, blob, 0o600); err != nil {
		return journeys.Journey{}, fmt.Errorf("write journey %s: %w", out, err)
	}
	return journey, nil
}

// mapRecordedInput converts one engine-level recorded action into a journey
// event. It reports ok=false for an action that produces no event.
func mapRecordedInput(in macdesktop.RecordedInput) (journeys.Event, bool) {
	switch in.Kind {
	case "click":
		return elementEvent(journeys.EventClick, in), true
	case "assert":
		return elementEvent(journeys.EventAssert, in), true
	case "char":
		return journeys.Event{Kind: journeys.EventChar, Char: in.Char, Secure: in.Secure}, true
	case "key":
		return journeys.Event{Kind: journeys.EventKey, Key: in.Key}, true
	default:
		return journeys.Event{}, false
	}
}

func elementEvent(kind journeys.EventKind, in macdesktop.RecordedInput) journeys.Event {
	return journeys.Event{
		Kind: kind,
		X:    in.X, Y: in.Y,
		AutomationID: in.Element.AutomationID,
		Name:         in.Element.Name,
		ControlType:  in.Element.ControlType,
		Button:       in.Button,
		Double:       in.Double,
		Facts: journeys.ElementFacts{
			Value:             in.State.Value,
			Checked:           in.State.Checked,
			Selected:          in.State.Selected,
			Enabled:           in.State.Enabled,
			Expanded:          in.State.Expanded,
			HasValue:          in.State.HasValue,
			HasToggle:         in.State.HasToggle,
			HasSelection:      in.State.HasSelection,
			HasInvoke:         in.State.HasInvoke,
			HasExpandCollapse: in.State.HasExpandCollapse,
		},
	}
}
