# Journeys as code

> **Status: implemented.** Schema version 2 is what the build accepts. Version 1
> documents are rejected and there is no converter; see [Version 2](#version-2).
> The journey format, validator and compiler are the shared ones from
> `mcp-server-core`, so a document written for windows-mcp-server is the same
> document here. The one part still on the roadmap is the assertion-authoring
> workflow in [recording a journey](journey-recording.md).

A **journey** is a named sequence of user actions, each with assertions about the
resulting screen and evidence to capture (a login smoke test, an expense-claim
regression), written as JSON and run deterministically, rather than as a prose
script a human follows by hand.

This is the authoring guide. The vocabularies it uses are defined in the
[journey taxonomy](journey-taxonomy.md), which is normative; what a run records is
defined in [journey evidence](journey-evidence.md).

A journey **compiles to a [plan](plan-and-apply.md)** and runs through the same
executor `Apply` uses: every step is evaluated by the policy engine, audited, and
**fail-stopped** on the first failure. A failed assertion is a failed step; the
run stops there and reports it, exactly as a test runner would.

## A document

The shipped example, `journeys/examples/textedit-smoke.json`, is the smallest
useful journey: open TextEdit, type a line, check it landed.

```jsonc
{
  "version": 2,
  "name": "textedit-smoke",
  "description": "Open a text editor, type a line, and verify it appears.",
  "steps": [
    {
      "name": "open the editor",
      "verb": "open_app",
      "app": "TextEdit",
      "assertions": [
        { "subject": "window.title", "operator": "contains", "expected": "Untitled",
          "message": "the editor window is in the foreground",
          "wait": { "timeout": 15 } }
      ],
      "evidence": [ "editor opened" ]
    },
    {
      "name": "type a line",
      "verb": "type_text",
      "text": "hello from a macos-mcp journey",
      "assertions": [
        { "subject": "screen.text", "operator": "contains",
          "expected": "hello from a macos-mcp journey",
          "message": "the typed text is present in the document" }
      ],
      "evidence": [ "text typed" ]
    }
  ],
  "expected_evidence": [ "editor opened", "text typed" ]
}
```

A journey against a real application uses the rest of the vocabulary: targeted
steps, control operations and a `read` whose result is asserted on.

```jsonc
{
  "version": 2,
  "name": "expenses-submit",
  "description": "Submit an expense claim and confirm the reference number.",
  "steps": [
    {
      "name": "open the expenses app",
      "verb": "open_app",
      "app": "Contoso Expenses",
      "assertions": [
        { "subject": "window.title", "operator": "contains", "expected": "Expenses",
          "wait": { "timeout": 20 } }
      ],
      "evidence": [ "app opened" ]
    },
    {
      "name": "enter the amount",
      "verb": "set_value",
      "target": { "name": "Amount", "control_type": "Edit" },
      "value": "126.40"
    },
    {
      "name": "submit the claim",
      "verb": "invoke",
      "target": { "name": "Submit", "control_type": "Button" },
      "assertions": [
        { "subject": "element", "target": { "name": "Reference", "control_type": "Text" },
          "operator": "exists", "wait": { "timeout": 30 },
          "message": "the confirmation panel appears" }
      ]
    },
    {
      "name": "read the reference",
      "verb": "read",
      "target": { "name": "Reference", "control_type": "Text" },
      "assertions": [
        { "subject": "result.text", "operator": "matches", "expected": "EXP-[0-9]{6}",
          "message": "the reference is well-formed" }
      ],
      "evidence": [ "claim submitted" ]
    }
  ],
  "expected_evidence": [ "claim submitted" ]
}
```

- **`verb`** and its parameters: the action, from the closed set in
  [section 3 of the taxonomy](journey-taxonomy.md#3-verbs). A journey never names
  an MCP tool; the compiler decides which call expresses the verb.
- **`target`**: which element or window, per
  [section 4](journey-taxonomy.md#4-selectors). Exact and unambiguous by default.
- **`assertions`**: conditions checked *after* the action, as
  `subject` x `operator` x `expected` per [section 5](journey-taxonomy.md#5-assertions).
  A failing one fails the step and stops the journey.
- **`evidence`**: captions for evidence to capture at that point.
- **`expected_evidence`**: labels a passing run must produce. Checked at validate
  time (some step must produce each) *and* after the run (the run must actually
  have captured each).

Unknown fields are rejected, so a typo in a key fails at `validate`.

### Control types and the accessibility tree

Selectors and assertions name controls by the **control type vocabulary the
taxonomy defines** (`Button`, `Edit`, `CheckBox`, `ListItem`, ...), which is the
UI Automation vocabulary windows-mcp-server uses. On macOS the engine maps each
accessibility role onto that vocabulary (`AXButton` is a `Button`, `AXTextField`
and `AXTextArea` are `Edit`, `AXPopUpButton` is a `ComboBox`, `AXRow` is a
`ListItem`), so a journey written against the shared vocabulary runs on either
platform. The full mapping is in
[section 4.4 of the taxonomy](journey-taxonomy.md#44-control-types-on-macos).

A `Snapshot` renders the tree as one line per element, and the names and control
types on those lines are what a selector matches:

```
Window: "Untitled" [pid 4821] (focused)
  [0] Edit "" (640,412)
  Group "Toolbar"
    [1] ComboBox "Font" (212,88)
    [2] CheckBox "Bold" (301,88)
```

`automation_id` is the element's accessibility identifier (`AXIdentifier`), the
value a developer sets through `accessibilityIdentifier`. Most Apple-framework
controls carry none; applications built for testing often set one on every
control, and it is the most stable key a selector can use.

### Keyboard shortcuts

`press_keys` chords use the Mac modifier names: `cmd` or `command`, `option` or
`alt`, `ctrl`, `shift`, joined with `+` (`cmd+s`, `cmd+shift+4`). **`ctrl` means
the Control key, not Command.** A journey written on Windows is not translated: a
`press_keys` of `ctrl+c` presses Control+C on the Mac, which copies nothing. The
Windows modifier names `win` and `windows` are aliases for Command, so a chord
*written* with them lands on the Command key, but the meaning of the chord is
still the Mac's (`win+r` is Command+R, not the Run dialog). Review every
`press_keys` step in a journey ported from Windows.

## Writing one that does not flake

Four habits, each of which the taxonomy makes cheap:

**Assert, don't sleep.** Put a `wait` on the assertion that states what you are
waiting *for*, rather than a `pause` step guessing how long it takes. The run
record then tells you it took 14.6 seconds of its 15-second budget, which is a
warning you can act on before it becomes a failure.

```jsonc
// prefer
{ "subject": "element.enabled", "target": { "name": "Submit" },
  "operator": "is_true", "wait": { "timeout": 15 } }

// over
{ "verb": "pause", "seconds": 5 }
```

**Target by name, and let ambiguity fail.** `occurrence` defaults to `unique`, so
a selector matching two controls fails and names both, rather than picking one.
When you genuinely mean the second one, say `"occurrence": 1`; the document then
records that you knew.

**Prefer `set_value` and `invoke` over `type_text` and `click`.** They go through
accessibility actions and settable attributes (`AXPress`, `AXValue`) rather than
synthetic input, so they do not depend on window focus and cannot be stolen by a
notification sliding in mid-run. Fall back to the input verbs only for controls
that expose no action.

**Assert what you read, not just what you see.** `read` puts a control's text into
the run's register; a `result.text` assertion checks it. That is how you verify a
generated reference number, a total, or a status string, rather than checking that
*something* appeared.

## Running

```sh
# Offline: parse, validate, compile to a plan. No desktop needed; put this in CI.
macos-mcp-server journey validate journeys/examples/textedit-smoke.json

# Live: run against the real desktop and report pass/fail. Exits 1 on failure.
macos-mcp-server journey run journeys/examples/textedit-smoke.json --policy-config policy.json
macos-mcp-server journey run journeys/examples/textedit-smoke.json --json   # for CI
```

Every flag is also an environment variable with the `MACOS_MCP_` prefix
(`MACOS_MCP_POLICY_CONFIG`), which is how a launchd job or a CI step configures
it.

`validate` checks everything in
[section 8 of the taxonomy](journey-taxonomy.md#8-what-journey-validate-checks-with-no-desktop):
verbs, parameters, selectors, the subject/operator matrix, regex compilation,
evidence expectations, and reports every problem at once. It needs no Mac and no
desktop (the journey package is pure Go), so a broken journey fails in CI rather
than on the machine that was going to run it.

`run` requires a graphical login session with the Accessibility and Screen
Recording grants (`macos-mcp-server permissions check` reports them); on a machine
that cannot host UI automation, such as an SSH session with no console, it errors
rather than reporting a spurious pass. The whole run is recorded on the audit
chain (`journey.started`, a `plan.step` per action and assertion, and
`journey.finished` with the counts) and produces the run record described in
[journey evidence](journey-evidence.md).

The testing toolset is served automatically for a journey run, so a journey's
assertions and evidence work regardless of the rest of the toolset selection.

## Don't write it, record it

Almost nothing above is meant to be typed by hand. Forty steps of JSON is not an
authoring experience, and a journey vocabulary that only a determined author can
produce would get used twice and abandoned. The recorder is the **primary author**:

```sh
macos-mcp-server journey record --out journeys/expenses-submit.json --name expenses-submit
# do the task; F8 marks an assertion; F9 stops
```

It installs a listen-only event tap on the session's input and captures what you
do, resolving each click to the accessibility element under it, picking the most
stable selector available, inferring the verb from what that element supports, and
**redacting secure text fields**, whose keystrokes are never written to the file.

The result is a **reviewable draft**, because a recorder captures actions and only
you know intent. Assertions are where that shows most: a perfect capture of a
human submitting an expense claim proves the clicks landed and notices nothing
about the total being wrong. There are three ways a recording acquires
assertions: marking them as you go, accepting proposals from what changed after
each action, and freezing a golden run. They are the subject of
[recording a journey](journey-recording.md), along with why the recorder is a CLI
verb and never an MCP tool.

Recording needs a graphical session and the Accessibility grant. Typed
characters are read from the keyboard event's own Unicode string, so any layout
records the character it actually produced.

## Relationship to plans

A journey **is** a pre-authored plan plus interleaved assertions and evidence, so
it inherits every property of [plan-and-apply](plan-and-apply.md): whole-plan
adjudication before anything runs, per-step live policy evaluation at execution,
fail-stop, and abandonment on a kill-switch trip. A journey step that hits a
policy `deny`, or a `hold` that is not approved, refuses and stops the run, the
same as any plan step.

The division of labour between the two is deliberate:

| | Journey | Plan |
|---|---|---|
| Vocabulary | closed verb set | any served tool |
| Arguments | typed per verb | untyped `args` map |
| Written by | a person, or the recorder | usually the agent |
| Reach | derived from the document, always complete | derived where derivable; `Shell` is undeclarable |
| For | user journeys through a UI | anything else |

Anything needing a shell, a preference domain or the filesystem is a plan. That
boundary is what lets a journey promise that every step is an attested verb.

## Version 2

Version 1 documents are **not** accepted, and there is no conversion. A v1 step
named an MCP tool and carried an untyped `args` map, which is exactly what the
taxonomy replaces; converting one mechanically would produce a v2 document that
still could not be checked. Rewrite the journeys you have against
[section 3 of the taxonomy](journey-taxonomy.md#3-verbs), or re-record them.
