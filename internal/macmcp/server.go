//go:build darwin && (amd64 || arm64)

// Package macmcp wires the macOS automation engine and tool inventory into an
// MCP server and runs it over a transport. It is the bootstrap layer between
// the cobra CLI (cmd/macos-mcp-server) and the domain package (pkg/macos),
// composed from the shared mcp-server-core runtime.
package macmcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/deploymenttheory/agentweave-harness/guardrails/audit"
	"github.com/deploymenttheory/agentweave-harness/guardrails/contain"
	"github.com/deploymenttheory/agentweave-harness/guardrails/enforce"
	"github.com/deploymenttheory/agentweave-harness/guardrails/policy"
	"github.com/deploymenttheory/agentweave-harness/guardrails/signals"
	"github.com/deploymenttheory/agentweave-harness/guardrails/status"
	"github.com/deploymenttheory/agentweave-harness/guardrails/telemetry"
	"github.com/deploymenttheory/agentweave-harness/guardrails/watch"
	"github.com/deploymenttheory/agentweave-harness/wire"
	"github.com/deploymenttheory/macos-mcp-server/internal/macdesktop"
	"github.com/deploymenttheory/macos-mcp-server/internal/macguard"
	"github.com/deploymenttheory/macos-mcp-server/pkg/macos"
	"github.com/deploymenttheory/mcp-server-core/inventory"
	"github.com/deploymenttheory/mcp-server-core/runtime"
	"github.com/deploymenttheory/mcp-server-core/surface"
)

// ServerName and ServerTitle identify this server in server/discover.
const (
	ServerName  = "macos-mcp-server"
	ServerTitle = "macOS MCP Server"
	// EnvPrefix is the prefix of every environment variable this server reads.
	EnvPrefix = "MACOS_MCP_"
)

// Config controls how the server is assembled and which tools it exposes.
type Config struct {
	// Version is reported in the MCP server implementation info.
	Version string

	// Persona, if set, selects a built-in preset (see macos.Personas) that
	// determines the toolset selection and read-only default. Explicit Toolsets
	// / ReadOnly settings override the persona.
	Persona string

	// Toolsets is the toolset selection (values accepted by
	// Builder.WithToolsets, including "all"/"default"). When nil and no persona
	// is set, the default toolsets are used.
	Toolsets []string
	// Tools is an additive allow-list of individual tools that bypass toolset
	// filtering.
	Tools []string
	// ExcludeTools is a deny-list applied last.
	ExcludeTools []string
	// ReadOnly, when true, exposes only read-only tools.
	ReadOnly bool
	// readOnlySet records whether ReadOnly was explicitly provided, so a persona
	// default is not silently overridden by the zero value.
	readOnlySet bool

	// LogFile, if set, directs debug logs to this file; otherwise info-level
	// logs go to stderr. stdout is reserved for the MCP stdio transport.
	LogFile string

	// PolicyConfig is the path to the device-policy document. Empty uses the
	// embedded default, which evaluates every declared signal, records every
	// verdict, and refuses nothing.
	PolicyConfig string

	// EnforceHTTPS blocks plaintext http:// targets. It is set from the policy
	// document at startup, not from a flag.
	EnforceHTTPS bool

	// --- Presentation and capture (not policy) ---
	Overlay     bool   // decorative window hue and click flash
	RecordFPS   int    // session recording frame rate
	RecordCodec string // session recording codec

	// --- Credentials ---
	// CredentialsFile is a JSON document of credentials to install into the
	// login keychain at init. Enabling this also enables the "credentials"
	// toolset.
	CredentialsFile string
}

// SetReadOnly records an explicit read-only choice (distinguishing it from the
// zero value so it can override a persona default).
func (c *Config) SetReadOnly(v bool) {
	c.ReadOnly = v
	c.readOnlySet = true
}

// Errors the startup path returns.
var (
	// ErrPersonaNeedsSession reports a persona requested with no graphical
	// session to drive. Personas are desktop-automation presets, so this is a
	// configuration error rather than a policy denial.
	ErrPersonaNeedsSession = errors.New(
		"persona requires a graphical login session, but the process has none")
	// ErrUnknownPersona reports a persona id that is not registered.
	ErrUnknownPersona = errors.New("unknown persona")
)

// nonAutomationToolsets is the toolset set permitted when the server runs
// without a graphical session (a LaunchDaemon, an ssh session): nothing that
// drives the desktop.
var nonAutomationToolsets = []string{"system", "shell", "filesystem", "diagnostics", "web"}

// ErrStartupDenied reports a device that did not meet the startup-scoped rules
// of the active policy.
var ErrStartupDenied = errors.New("device policy denied startup")

// ErrPersonaToolBypass reports a --tools entry that escapes the active
// persona's toolset selection via the additional-tools bypass. A persona is a
// documented surface guarantee, so this is a configuration error: compose the
// surface explicitly with --toolsets, or drop the tool.
var ErrPersonaToolBypass = errors.New("--tools escapes the persona's toolsets")

// RunStdio builds the server and serves the MCP protocol over stdio until the
// context is cancelled or the client disconnects.
//
// The order below is the order windows-mcp-server pinned: policy first (it
// names everything else), then the audit chain, the engine, startup admission,
// the tool surface, credentials, the harness attach, egress, the kill ladder,
// the planner, the middleware chain, registration, the guardrail tools and
// baselines, the in-flight monitor, the status endpoint, and only then the
// transport.
//
//nolint:gocyclo,cyclop,maintidx,funlen,gocognit // the wiring order is the contract
func RunStdio(ctx context.Context, cfg Config) error {
	logger, cleanup, err := runtime.NewLogger(cfg.LogFile)
	if err != nil {
		return fmt.Errorf("logger: %w", err)
	}
	defer cleanup()

	// The policy is loaded before anything else it configures. It names the
	// audit destination, the heartbeat cadence, whether the session is
	// recorded and where — so a bad document must fail before any of that is
	// stood up, and certainly before a desktop engine exists.
	reg := runtime.NewGuardrailRegistry(envNames, logger)
	devicePolicy, err := runtime.LoadPolicy(cfg.PolicyConfig, reg, logger)
	if err != nil {
		return fmt.Errorf("policy: %w", err)
	}
	// Carried onto Config because it has to reach the tool dependencies and
	// the guardrail Env, neither of which holds a policy.
	cfg.EnforceHTTPS = devicePolicy.EnforceHTTPS

	// --- Hash-chained audit log (built early so startup is recorded) ---
	// One session stamp, shared with the recorder, so the audit file and the
	// recording correlate by name — the correlation an evidence bundle relies
	// on. Keyed by default: an unkeyed chain is tamper-evident but not
	// unforgeable.
	sessionStamp := runtime.SessionStamp()
	auditKey := runtime.ResolveAuditKey(envNames, devicePolicy.Transparency.AuditDestination, logger)
	dest, err := audit.OpenDestination(devicePolicy.Transparency.AuditDestination, sessionStamp, auditKey)
	if err != nil {
		return fmt.Errorf("audit log: %w", err)
	}
	auditLog := audit.NewAuditLog(dest, audit.WithHMACKey(auditKey))
	// sealAtExit is populated later, once the planner exists and the closing
	// posture is captured. It runs from inside the audit-close defer, so it
	// fires after the chain is sealed and (defers being LIFO) after the
	// recorder is finalised — both are inputs to the bundle.
	var sealAtExit func()
	defer func() {
		_ = auditLog.Close()
		if sealAtExit != nil {
			sealAtExit()
		}
	}()
	_, _ = auditLog.Append("server.started", map[string]any{"version": cfg.Version, "session": sessionStamp})

	// Off-box anchoring of the chain head, if the policy asks for it. Never
	// gates startup.
	stopAnchor := startAnchor(ctx, devicePolicy.Transparency.Anchor, auditLog, logger)
	defer stopAnchor()

	perms := CheckPermissions(ctx)
	for _, w := range perms.Warnings {
		logger.Warn(w)
	}

	// The engine. Overlay is the decorative hue and click flash, a display
	// choice that stays a flag; SecurityOverlay starts the overlay manager
	// without the decoration so the kill banner has somewhere to draw.
	dsk, err := macdesktop.New(logger, macdesktop.Options{
		Overlay:         cfg.Overlay,
		SecurityOverlay: devicePolicy.Transparency.Banner,
		Record: macdesktop.RecorderOptions{
			Dir:   devicePolicy.Transparency.RecordingDir,
			FPS:   cfg.RecordFPS,
			Codec: cfg.RecordCodec,
			Stamp: sessionStamp,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to start desktop engine: %w", err)
	}
	defer func() { _ = dsk.Close() }()
	envFn := func() *signals.Env { return guardrailEnv(cfg, dsk, logger) }
	// The index is supplied once the manifest exists; a startup decision has
	// no tool to resolve.
	engine := policy.NewEngine(devicePolicy, reg, nil, envFn)
	holder := &runtime.DecisionHolder{}

	probe := envFn().Sys
	runContext := probe.RunContext()

	// Startup admission. Rules scoped "startup" are evaluated once, before any
	// tool surface is assembled, so a refused device never gets as far as
	// registering tools or provisioning credentials.
	startup := engine.Evaluate(ctx, policy.StartupSubject())
	decision := engine.DecisionFrom(startup, probe.DeviceIdentity(), runContext)
	holder.Set(decision)
	_, _ = auditLog.Append("policy.decided", decision)

	// No graphical session (a LaunchDaemon, an ssh login) means no desktop to
	// drive, so the automation toolsets are dropped regardless of what was
	// asked for. Detected, not declared.
	noSession := !perms.ConsoleSession
	if cfg.Persona != "" && noSession {
		return fmt.Errorf("%w: %q", ErrPersonaNeedsSession, cfg.Persona)
	}

	if !startup.Allowed() {
		signals.LogDecision(logger, "deny", decision)
		_, _ = auditLog.Append("policy.denied", decision.Reasons)
		_ = auditLog.Flush()
		dsk.ShowSecurityBanner("STARTUP BLOCKED — device did not meet policy")
		dsk.Notify(ctx, "macOS MCP: startup blocked", "Device did not meet policy: "+startup.Reason())
		return fmt.Errorf("%w: %s", ErrStartupDenied, startup.Reason())
	}
	signals.LogDecision(logger, "admit", decision)

	// The tool surface is resolved before credentials are provisioned, so the
	// exposure check sees exactly the toolsets that will be served.
	inv, personaInstructions, err := buildInventory(cfg, noSession)
	if err != nil {
		return err
	}
	for _, unknown := range inv.UnrecognizedToolsets() {
		logger.Warn("unrecognized toolset requested", "toolset", unknown)
	}
	if noSession {
		logger.Warn("no graphical session: desktop-automation toolsets disabled")
	}

	// Record the resolved tool surface, so an incident review can see exactly
	// what was served under which persona and selection.
	enabledToolsetIDs := surface.ToolsetIDs(inv.EnabledToolsets())
	_, _ = auditLog.Append("server.configured", map[string]any{
		"persona":               cfg.Persona,
		"toolsets":              enabledToolsetIDs,
		"unrecognized_toolsets": inv.UnrecognizedToolsets(),
		"additional_tools":      cfg.Tools,
		"excluded_tools":        cfg.ExcludeTools,
		"read_only":             cfg.ReadOnly,
		"credentials_file":      cfg.CredentialsFile != "",
	})

	// A persona is a documented guarantee about the served surface. --tools
	// bypasses toolset membership, so combined with a persona it would
	// silently widen that guarantee. Refuse it.
	if cfg.Persona != "" {
		if outside := surface.ToolsOutsidePersona(
			cfg.Tools,
			inv.EnabledToolsets(),
			macos.ToolToolsets(),
		); len(
			outside,
		) > 0 {
			_, _ = auditLog.Append("tools.persona_bypass.denied", map[string]any{
				"persona": cfg.Persona,
				"tools":   outside,
			})
			_ = auditLog.Flush()
			return fmt.Errorf("%w: %v are outside the %q persona's toolsets; select --toolsets "+
				"explicitly instead of a persona, or drop those tools", ErrPersonaToolBypass, outside, cfg.Persona)
		}
	}

	// --- Init-time credentials ---
	if err := refuseCredentialExposure(cfg, inv, devicePolicy, auditLog, logger); err != nil {
		return err
	}
	// Provisioned only after admission and the exposure check, so a denied
	// startup never installs credentials, and removed again on every shutdown
	// path.
	installedCreds, cleanupCreds, err := provisionCredentials(dsk, cfg, auditLog, logger)
	if err != nil {
		return err
	}
	defer cleanupCreds()

	deps := macos.NewBaseDeps(dsk, logger, nil)
	deps.WithCredentials(credentialInfos(installedCreds)).
		WithEnforceHTTPS(cfg.EnforceHTTPS).
		WithProtectedPaths(guardrailPaths(cfg, devicePolicy))

	// Built by the same function the conformance host uses, so the surface the
	// official suite is measured against is the surface this binary serves.
	s := newSurface(cfg, inv, personaInstructions, deps)
	server := s.Server

	// --- Out-of-band kill switch + tiered action executor ---
	runCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	startedAt := time.Now()
	actuator := macguard.NewActuator(logger)

	// --- agentweave-harness control channel (servant side) ---
	// When the harness spawned this server it left the channel address and a
	// bootstrap token in the environment. Dial back, authenticate, and serve
	// signal evaluation, actuation and liveness for the session. The attach
	// happens before egress provisioning so the ack's effective config can
	// point the OS enforcement at the harness's proxy. An enforce ack sheds
	// the duplicated in-process layers; an observe ack is additive.
	harnessEnforcing := false
	var harnessProxy runtime.HarnessEgress
	var servant *runtime.HarnessServant
	var harnessRestoreMu sync.Mutex
	var harnessRestore func() error
	egressRestore := &runtime.EgressRestoreHolder{}
	if pipe, token := runtime.HarnessAddress(); pipe != "" {
		rungs := runtime.BuildRungs(runtime.RungPrimitives{
			Actuator:     actuator,
			Banner:       dsk.ShowSecurityBanner,
			Seal:         auditLog.Flush,
			Finalize:     func() { _ = dsk.Close() },
			CleanupCreds: cleanupCreds,
			SetRestore: func(r func() error) {
				harnessRestoreMu.Lock()
				harnessRestore = r
				harnessRestoreMu.Unlock()
			},
			Egress:        newEnforcer(logger),
			EgressRestore: egressRestore,
			Logger:        logger,
		})

		sv, ack, derr := attachHarness(pipe, token, cfg.Version, sessionStamp, runtime.ServantDeps{
			Registry:   reg,
			EnvFn:      envFn,
			Rungs:      rungs,
			Alive:      dsk.Alive,
			RunContext: runContext,
			Elevated:   actuator.Elevated(),
			Logger:     logger,
			OnLost: func(cause error) {
				if cause == nil {
					cancel(runtime.ErrHarnessChannelLost)
					return
				}
				cancel(fmt.Errorf("%w: %w", runtime.ErrHarnessChannelLost, cause))
			},
		})
		// The token has done its job. Scrub both bootstrap vars so no tool the
		// agent runs can read the channel credential back.
		_ = os.Unsetenv(runtime.EnvHarnessPipe)
		_ = os.Unsetenv(runtime.EnvHarnessToken)
		if derr != nil {
			return fmt.Errorf("agentweave-harness: %w", derr)
		}
		servant = sv
		harnessEnforcing = ack.Mode == wire.ModeEnforce
		harnessProxy = runtime.HarnessEgress{
			Port:       ack.EffectiveConfig.EgressProxyPort,
			Executable: ack.EffectiveConfig.EgressProxyExecutable,
		}
		logger.Info("attached to agentweave-harness", "mode", ack.Mode, "proto", ack.Proto,
			"local_enforcement", !harnessEnforcing, "egress_proxy_port", harnessProxy.Port)
		_, _ = auditLog.Append("harness.attached", map[string]any{
			"mode": ack.Mode, "local_enforcement": !harnessEnforcing,
			"egress_proxy_port": harnessProxy.Port,
		})
		// Before the servant starts serving and before RegisterAll builds the
		// tool surface, per the wire contract.
		runtime.ApplyEffectiveConfig(ack.EffectiveConfig, &cfg.EnforceHTTPS, deps.BaseDeps,
			guardrailPaths(cfg, devicePolicy), macos.NormalizePath, dsk.ShowSecurityBanner, logger)

		go servant.Serve(runCtx)
		servant.StartHeartbeat(runCtx, runtime.HeartbeatFromAck(ack))
		if names := installedCredentialNames(installedCreds); len(names) > 0 {
			_ = servant.PushCredentialEvent(wire.CredentialInstalled, names)
		}
	}

	// --- Device egress proxy ---
	// Registered before the executor's Restore defer so the deferred stack
	// unwinds in the right order: containment is undone first, then the
	// egress state it was layered over.
	egressSvc, cleanupEgress, suspendEgress, err := provisionEgress(
		runCtx,
		devicePolicy,
		auditLog,
		logger,
		harnessProxy,
	)
	if err != nil {
		return err
	}
	defer cleanupEgress()
	// The server's own reaching-out (Scrape) routes through whichever proxy
	// this session runs, so it is governed by the same allowlist.
	switch {
	case harnessProxy.Announced():
		deps.WithEgressProxy(fmt.Sprintf("127.0.0.1:%d", harnessProxy.Port))
	case egressSvc != nil:
		deps.WithEgressProxy(egressSvc.Addr())
	}

	executor := contain.NewKillExecutor(contain.KillExecutorDeps{
		Config:   runtime.KillPolicyConfig(devicePolicy),
		Actuator: actuator,
		Audit:    auditLog,
		Logger:   logger,
		Banner:   dsk.ShowSecurityBanner,
		Finalize: func() {
			// Revoke credentials before tearing the engine down: containment
			// must not leave session credentials installed on the machine.
			cleanupCreds()
			// Suspend rather than clean up: stop admitting traffic and drop
			// the allow rules that would otherwise outlive isolation, without
			// restoring anything that would countermand the containment.
			suspendEgress()
			_ = dsk.Close() // finalise recording synchronously (idempotent)
		},
		Abort: func(cause error) {
			// Deferred so an in-flight reply flushes before the transport closes.
			time.AfterFunc(300*time.Millisecond, func() { cancel(cause) })
		},
	})
	kill := contain.NewKillSwitch(executor.OnTrip)
	defer func() { _ = executor.Restore() }() // undo network isolation on exit

	// The harness teardown defers, registered here so the LIFO unwind keeps
	// its layering: the servant closes and the isolation restore runs first,
	// then the executor's Restore, then the egress teardown.
	if servant != nil {
		defer func() { _ = egressRestore.Run() }()
		defer func() {
			harnessRestoreMu.Lock()
			r := harnessRestore
			harnessRestoreMu.Unlock()
			if r != nil {
				_ = r()
			}
		}()
		defer func() { _ = servant.Close() }()
	}

	// Out-of-band approvals for on_fail: hold rules. Off unless a webhook is
	// configured. The signing key is an environment secret, never the policy.
	var approver enforce.Approver
	if devicePolicy.Approvals.WebhookURL != "" {
		approver = enforce.NewApprovalClient(enforce.ApprovalConfig{
			WebhookURL:   devicePolicy.Approvals.WebhookURL,
			Timeout:      devicePolicy.Approvals.Timeout.Std(),
			PollInterval: devicePolicy.Approvals.PollInterval.Std(),
			HMACKey:      []byte(os.Getenv(envNames.ApprovalKey())),
			Logger:       logger,
		})
		logger.Info("dual control enabled", "webhook", devicePolicy.Approvals.WebhookURL,
			"timeout", devicePolicy.Approvals.Timeout)
	}

	// Plan-and-apply. Wired after the kill switch so an apply can abandon its
	// remaining steps when containment trips.
	sessionPlanner := runtime.NewPlanner(engine, auditLog, runtime.InventoryRegistry{Inv: inv, Deps: deps},
		func() bool { tripped, _ := kill.Tripped(); return tripped }).
		WithReadRegister(deps)
	if approver != nil {
		sessionPlanner.WithApprovals(approver, sessionStamp, devicePolicy.Approvals.Timeout.Std())
	}
	deps.WithPlanner(sessionPlanner)

	// Kill triggers come from the policy's kill.triggers block. A trigger left
	// off is report-only: still detected and audited, but it contains nothing.
	triggers := devicePolicy.Kill.Triggers
	tripSentinel := runtime.TripFunc("sentinel", triggers.Sentinel, kill, auditLog, logger)
	tripPostureDrift := runtime.TripFunc("posture-drift", triggers.PostureDrift, kill, auditLog, logger)
	tripRugpull := runtime.TripFunc("rugpull", triggers.RugPull, kill, auditLog, logger)
	tripHeartbeat := runtime.TripFunc("heartbeat-gap", triggers.HeartbeatGap, kill, auditLog, logger)
	// A kill verdict needs no trigger switch: the rule that produced it said
	// `on_fail: kill` in this same policy, which is the operator arming it.
	tripPolicy := runtime.TripFunc("policy", true, kill, auditLog, logger)

	// --- Rug-pull detector (baseline pinned after all AddTool) ---
	heartbeat := watch.NewHeartbeat(auditLog)
	rugpull := watch.NewRugPull(tripRugpull, auditLog)

	// OTLP export, off unless a collector endpoint is configured. Not
	// constructed under an enforcing harness: the exporter's spans describe the
	// request path, which the harness then owns.
	var (
		recordDecision      func(subject, severity, mode string)
		telemetryMiddleware mcp.Middleware
	)
	if devicePolicy.Telemetry.Endpoint != "" && !harnessEnforcing {
		tele, terr := telemetry.New(ctx, telemetry.Config{
			Endpoint:    devicePolicy.Telemetry.Endpoint,
			SampleRatio: devicePolicy.Telemetry.SampleRatio,
			Headers:     telemetry.ParseHeaders(os.Getenv(envNames.OTLPHeaders())),
			ServiceName: ServerName,
			Version:     cfg.Version,
		})
		if terr != nil {
			logger.Warn("telemetry disabled: could not start the OTLP exporter", "error", terr)
		} else {
			defer tele.Shutdown(context.WithoutCancel(ctx))
			telemetryMiddleware = tele.Middleware()
			recordDecision = tele.RecordDecision
			logger.Info("telemetry enabled", "endpoint", devicePolicy.Telemetry.Endpoint)
		}
	}

	// Read before the scrub below, like every other environment secret.
	statusToken, err := runtime.ResolveStatusToken(devicePolicy.Transparency, logger)
	if err != nil {
		return fmt.Errorf("status token: %w", err)
	}
	// The evidence export destination, for the same reason.
	exportSink := runtime.ProvisionExport(devicePolicy, auditLog, logger)
	evidenceKeyFile := os.Getenv(envNames.EvidenceKeyFile())

	// Every environment secret has now been read into the component that
	// needs it, so clear them before any tool can run.
	runtime.ScrubSecretEnv(envNames, devicePolicy, logger)

	// Receiving middleware, outermost first, installed in one call so the
	// order below is the order that actually runs: inject-deps and cache
	// hints (the surface's own), then audit, telemetry, rug-pull, policy.
	s.InstallReceiving(runtime.ReceivingChain(
		harnessEnforcing,
		auditLog.Middleware(),
		telemetryMiddleware,
		[]mcp.Middleware{
			rugpull.Middleware(),
			rugpull.PromptMiddleware(),
			rugpull.ResourceMiddleware(),
			rugpull.DiscoverMiddleware(),
		},
		enforce.Middleware(engine, enforce.EnforcerDeps{
			Audit:           auditLog,
			Kill:            tripPolicy,
			RecordDecision:  recordDecision,
			Approver:        approver,
			SessionID:       sessionStamp,
			ApprovalTimeout: devicePolicy.Approvals.Timeout.Std(),
			Logger:          logger,
		}),
	)...)

	// RegisterAll rather than RegisterTools: resources and prompts are part of
	// the served surface too.
	inv.RegisterAll(runCtx, server, deps)

	// Guardrail tools and rug-pull baselines are one block, registered together
	// or not at all. Under an enforcing harness the whole block is shed: the
	// harness injects its own Status/Kill tools and fingerprints every surface
	// from the wire.
	var baselineTools []*mcp.Tool
	snapshot := runtime.SnapshotFn(startedAt, rugpull, heartbeat, auditLog, kill, egressSvc, devicePolicy.Egress,
		runtime.ExportStatus(devicePolicy.Transparency.Export, exportSink))
	if !harnessEnforcing {
		statusTool, statusHandler := status.StatusTool(holder.Get, snapshot, kill)
		server.AddTool(statusTool, statusHandler)
		// The agent-facing Kill tool always stops the session, but only
		// actuates the containment ladder when the policy configures
		// containment: a misbehaving model must not be able to isolate or shut
		// down the device by asking.
		stopSession := executor.StopGracefully
		actions := devicePolicy.Kill.Actions
		if actions.Isolate || actions.Lock || actions.Shutdown || len(actions.KillProcs) > 0 {
			stopSession = kill.Trip
		}
		killTool, killHandler := status.KillTool(stopSession)
		server.AddTool(killTool, killHandler)

		// Pin the rug-pull baselines over the full served surface: tools,
		// prompts, resources and the server/discover advertisement.
		baselineTools = append(surface.MCPTools(runCtx, inv), statusTool, killTool)
		baseHash := rugpull.SetBaseline(baselineTools)
		_, _ = auditLog.Append("tools.pinned", map[string]any{"hash": baseHash, "count": len(baselineTools)})

		basePrompts := surface.MCPPrompts(runCtx, inv)
		promptHash := rugpull.SetPromptBaseline(basePrompts)
		_, _ = auditLog.Append("prompts.pinned", map[string]any{"hash": promptHash, "count": len(basePrompts)})

		baseResources := surface.MCPResources(runCtx, inv)
		resourceHash := rugpull.SetResourceBaseline(baseResources)
		_, _ = auditLog.Append("resources.pinned", map[string]any{"hash": resourceHash, "count": len(baseResources)})

		discoverHash := rugpull.SetDiscoverBaseline(s.Capabilities, s.Instructions)
		_, _ = auditLog.Append("discover.pinned", map[string]any{"hash": discoverHash})
	}

	// The engine can now resolve tools; from here every request is decided
	// against the manifest that is actually served.
	engine.SetIndex(runtime.NewToolIndex(runCtx, inv))

	// --- In-flight: signal refresh, posture drift, sentinel, verifiers ---
	verifiers := []watch.VerifyFunc{
		{Name: "signal-refresh", Run: engine.Refresh, Trip: tripPostureDrift},
		{Name: "heartbeat", Run: heartbeat.Beat, Trip: tripHeartbeat},
	}
	if !harnessEnforcing {
		verifiers = append(verifiers, runtime.RugpullVerifier(rugpull,
			func() []*mcp.Tool { return baselineTools }, tripRugpull))
	}
	watch.StartMonitor(runCtx, watch.MonitorConfig{
		Interval:         devicePolicy.InFlight.Interval.Std(),
		ControlDir:       devicePolicy.InFlight.ControlDir,
		SentinelToken:    runtime.SentinelToken(devicePolicy.InFlight.ControlDir, auditLog, logger),
		TripSentinel:     tripSentinel,
		TripPostureDrift: tripPostureDrift,
		Stopped:          func() bool { tripped, _ := kill.Tripped(); return tripped },
		Logger:           logger,
		Evaluate: func(c context.Context) signals.Decision {
			v := engine.Evaluate(c, policy.StartupSubject())
			d := engine.DecisionFrom(v, probe.DeviceIdentity(), probe.RunContext())
			holder.Set(d)
			return d
		},
		Verify: verifiers,
	})
	if heartbeatInterval := devicePolicy.Transparency.Heartbeat.Std(); heartbeatInterval > 0 {
		heartbeat.StartWatchdog(runCtx, 3*heartbeatInterval, tripHeartbeat)
	}

	// --- Status endpoint (always-on when an address is configured) ---
	if devicePolicy.Transparency.StatusAddr != "" {
		ss := &status.StatusServer{
			Addr:     devicePolicy.Transparency.StatusAddr,
			Token:    statusToken,
			Current:  holder.Get,
			Snapshot: snapshot,
			Kill:     kill,
			Logger:   logger,
		}
		if err := ss.Start(runCtx); err != nil {
			logger.Warn("guardrails status endpoint disabled", "error", err)
		}
	}

	logger.Info("starting macos-mcp-server over stdio",
		"version", cfg.Version,
		"enabled_toolsets", enabledToolsetIDs,
		"policy_mode", string(devicePolicy.Mode),
		"policy_signals", devicePolicy.SignalIDs(),
		"policy_rules", len(devicePolicy.Rules),
		"accessibility", perms.Accessibility,
		"screen_recording", perms.ScreenRecording,
	)

	err = server.Run(runCtx, &mcp.StdioTransport{})

	// The session is over but the engine and desktop are still up. Capture the
	// closing posture now, and arm the evidence seal to fire from the
	// audit-close defer once the chain and recording are finalised.
	if devicePolicy.Transparency.EvidenceDir != "" {
		var posture []byte
		if b, mErr := json.Marshal(snapshot()); mErr == nil {
			posture = b
		}
		plans := sessionPlanner.StoredPlans()
		sealAtExit = func() {
			runtime.AutoSealEvidence(devicePolicy.Transparency, sessionStamp, plans, posture, exportSink,
				evidenceKeyFile, logger)
		}
	}

	if tripped, reason := kill.Tripped(); tripped {
		logger.Error("session terminated by kill switch", "reason", reason)
		return fmt.Errorf("%w: %s", ErrKilled, reason)
	}
	// A requested stop (the Kill tool with the switch unarmed) is a normal
	// shutdown, not a failure.
	if cause := context.Cause(runCtx); errors.Is(cause, contain.ErrSessionStopped) {
		logger.Info("session stopped on request", "cause", cause.Error())
		return nil
	}
	if err != nil {
		return fmt.Errorf("server run: %w", err)
	}
	return nil
}

// ErrKilled reports a session ended by the kill switch.
var ErrKilled = errors.New("session terminated by kill switch")

// the official suite is measured against is the surface the binary serves.
func newSurface(
	cfg Config,
	inv *inventory.Inventory,
	personaInstructions string,
	deps *macos.BaseDeps,
) *surface.Surface {
	return surface.New(
		surface.Config{Name: ServerName, Title: ServerTitle, Version: cfg.Version},
		inv, personaInstructions, deps,
		surface.CompletionHandlerFor(inv, surface.CompletionSources{
			PersonaIDs: macos.PersonaIDs(),
			CommonApps: commonApps,
		}),
	)
}

// commonApps are frequently automated applications, offered as launch
// suggestions. A short curated list rather than an enumeration of installed
// software, which would be slow and leak the machine's inventory.
var commonApps = []string{
	"Calculator", "Finder", "Mail", "Notes", "Preview",
	"Safari", "System Settings", "Terminal", "TextEdit",
}

// buildInventory applies persona, toolset, read-only, and allow/deny
// configuration to the full tool manifest. It also returns the selected
// persona's instructions (empty when no persona is selected).
func buildInventory(cfg Config, noSession bool) (*inventory.Inventory, string, error) {
	toolsets := cfg.Toolsets
	readOnly := cfg.ReadOnly
	var personaInstructions string

	if cfg.Persona != "" {
		persona, ok := macos.LookupPersona(cfg.Persona)
		if !ok {
			return nil, "", fmt.Errorf("%w: %q", ErrUnknownPersona, cfg.Persona)
		}
		personaInstructions = persona.Instructions
		if toolsets == nil {
			toolsets = persona.Toolsets
		}
		if !cfg.readOnlySet {
			readOnly = persona.ReadOnly
		}
	}

	if noSession {
		toolsets = nonAutomationToolsets
	} else if cfg.CredentialsFile != "" {
		toolsets = runtime.WithToolset(toolsets, string(macos.ToolsetCredentials.ID))
	}

	inv, err := macos.NewInventory().
		WithToolsets(toolsets).
		WithReadOnly(readOnly).
		WithTools(cfg.Tools).
		WithExcludeTools(cfg.ExcludeTools).
		WithServerInstructions().
		Build()
	if err != nil {
		return nil, "", fmt.Errorf("failed to build tool inventory: %w", err)
	}
	return inv, personaInstructions, nil
}

// CaptureSurface assembles the tool manifest this configuration would serve,
// runs a real in-process MCP session against it, and returns the wire objects
// a client actually receives. No desktop engine is created: tools/list never
// invokes a handler.
func CaptureSurface(ctx context.Context, cfg Config) (surface.Captured, error) {
	logger := slog.New(slog.DiscardHandler)
	inv, personaInstructions, err := buildInventory(cfg, false)
	if err != nil {
		return surface.Captured{}, fmt.Errorf("build inventory: %w", err)
	}
	deps := macos.NewBaseDeps(nil, logger, nil)
	s := newSurface(cfg, inv, personaInstructions, deps)
	s.InstallReceiving()
	got, err := surface.Capture(ctx, s, inv, deps, cfg.Version)
	if err != nil {
		return surface.Captured{}, fmt.Errorf("capture surface: %w", err)
	}
	return got, nil
}
