# Plan and apply

The plan model (whole-plan adjudication, derived targets, the change manifest,
`Apply`'s per-step evaluation and fail-stop) belongs to the
[agentweave-harness](https://github.com/deploymenttheory/agentweave-harness)
module, and its reference lives with that code.

**See [`docs/plan-and-apply.md`](https://github.com/deploymenttheory/agentweave-harness/blob/main/docs/plan-and-apply.md) in the agentweave-harness repository.**

This server imports those packages unchanged for its in-process stack, so the
behaviour described there applies here: the `Plan` and `Apply` tools in the
`planning` toolset are the harness's, the wiring is `mcp-server-core/runtime`,
and a [journey](journeys.md) compiles to the same plan document and runs through
the same executor. What this repository adds is the macOS side only: the tools a
plan step can name (`Shell`, `Defaults`, `LaunchdJob`, `FileSystem` and the rest
of the manifest) and the posture probes and actuators the policy engine consults
when it adjudicates one; see [policy configuration](policy-config.md) for the
macOS reading of each signal.
