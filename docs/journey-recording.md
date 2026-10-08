# Recording a journey

> **Status: mostly implemented.** The selector ladder (section 4), verb inference
> from what the element supports (section 3), the credential placeholder
> (section 7) and **mark-while-recording** (section 5.1) all ship. Still on the
> roadmap: counting selector matches at record time (section 4), proposing
> assertions from a before/after diff (section 5.2), freezing a golden run
> (section 5.3), and wait inference (section 6). Each is marked where it appears.

**Nobody is going to hand-write forty steps of JSON.** A vocabulary that only a
determined author can produce is a vocabulary that gets used twice and then
abandoned, however well specified it is. So the closed taxonomy and the recorder
are one design, not a format and a convenience: the recorder is the **primary
author** of a journey and a human is its **editor**.

That framing has a consequence the [taxonomy](journey-taxonomy.md) is written
under. A verb that cannot be inferred from a click plus what the accessibility
tree reports at that instant is a verb only a human will ever write. Such verbs
are allowed (`set_value` is one, and it is the *best* way to fill a field) but
the ratio matters, and section 3.3 of the taxonomy is where it is tracked.

```sh
macos-mcp-server journey record --out journeys/expenses-submit.json --name expenses-submit
# do the task; F8 marks an assertion; F9 stops
```

The capture is shared with windows-mcp-server above the operating system: the
event stream the Mac engine produces is compiled into a document by the same
emitter in `mcp-server-core`, so the coalescing, the inference and the redaction
rules are one implementation with one test suite. What this page describes that
is particular to macOS is how the events are captured and what the accessibility
tree is asked.

---

## 1. Why this is not an MCP tool

A journey recorder installs an **event tap on the session's input stream**
(`CGEventTapCreate` at the session level, listening for mouse-down and key-down).
That is a keylogger. It is only not a keylogger because of where it sits: a human
at the console starts it deliberately, it runs for the length of one recording,
and the one thing standing between the capture and a password on disk is a
redaction check that fails **closed**: when the accessibility API cannot report
the focused element, the keystroke is treated as secret and dropped.

Exposing that as a tool on the MCP manifest would hand an agent a keylogger with
an argument for how long to run it. So the recorder is a **CLI verb only**,
reachable in `cmd/macos-mcp-server` and the desktop engine
(`internal/macdesktop/journeyrecord.go`) and registered nowhere in `pkg/macos`.
Nothing on the tool surface can start, stop or read a recording.

This is the same reasoning that keeps the conformance HTTP host behind a build
tag: the capability is legitimate, and the way it is reached is the control.

macOS adds a control of its own: the tap needs the **Accessibility** grant, the
same consent synthetic input needs, and it is granted to the signed binary, not
to the user. An unsigned or ad-hoc-signed build re-prompts on every rebuild; see
the signing notes in the repository's `CLAUDE.md`. Without the grant the tap
cannot be created and `record` fails with an error naming it.

Three properties follow, and all should be preserved:

- **The draft is written `0o600`**, not `0o644`. A journey is not meant to hold a
  secret, but that rests entirely on redaction being right.
- **The stop and mark keys are never recorded**, so pressing F9 never appears as
  a step in the journey it ended. The tap is listen-only, so it cannot swallow
  the keystroke the way a Windows hook can: F8 and F9 still reach the frontmost
  application. Almost nothing binds them, which is why they were chosen.
- **The tap does not distinguish synthetic events from hardware ones.** An agent
  session driving the desktop while a recording runs would be captured. Do not
  run both; the recorder is for a human doing the task once.

---

## 2. What the capture already sees

At every click, the recorder hit-tests the point with
`AXUIElementCopyElementAtPosition` on the system-wide element and reads the
element under it. What comes back is more than the current draft uses:

| Property | Source | Used for |
|---|---|---|
| `AXIdentifier` | the developer-assigned accessibility identifier | selector rung 1 (`automation_id`) |
| name | `AXTitle`, `AXDescription` or the labelling element, by role | selector rung 2, and the assertion proposed for an id-targeted mark |
| control type | `AXRole` plus `AXSubrole`, mapped to the shared vocabulary | selector narrowing, and the verb fallback when the element reports no capabilities |
| `AXSecureTextField` role of the **focused** element | | redaction |
| capabilities (value, toggle, selection, expand/collapse, press) | derived from the role and the attributes present (section 3) | **verb inference** and the proposed assertion (section 5.1) |
| current state: `AXValue`, `AXSelected`, `AXExpanded`, `AXEnabled`, `AXFocused` | | the expected value in a proposed assertion |
| frame | `AXPosition` and `AXSize` | the `point` fallback |
| process id | | read, not yet used |

Everything in the table is read in **one pass on the main thread from the same
element**, so the facts describe a single moment: the identifier that keys the
selector and the state that fills the assertion cannot disagree about which
control they came from.

---

## 3. Inferring the verb

A click is not a verb. Clicking a checkbox is `toggle`; clicking a list row is
`select`; clicking a disclosure triangle is `expand`. Recording every one of them
as `click` throws away what the tree already knows, and produces a journey that
drives the UI through synthetic input when an accessibility action was
available.

Windows asks an element which UI Automation patterns it supports. The
accessibility API has no equivalent question, so the Mac engine derives the same
facts from the role and the attributes the element exposes:

| Fact | How macOS establishes it |
|---|---|
| toggle | the control type is `CheckBox` or `RadioButton` and `AXValue` is present; checked when the value is `1` |
| selection | `AXSelected` is present |
| expand/collapse | `AXExpanded` is present; the current value gives the direction |
| press (the Windows Invoke pattern) | the control type is interactive and is not `Edit` |
| value | the control type is `Edit`, `ComboBox`, `Slider`, `Spinner` or `Text` and `AXValue` is present, and the role is not `AXSecureTextField` |

Inference then reads those facts in order of specificity:

| Supports | Verb |
|---|---|
| toggle | `toggle` |
| selection | `select` |
| expand/collapse | `expand` / `collapse`, by current state |
| press | `invoke` |
| value | `click`: focus, for the `type_text` that follows |

The order matters because controls support more than one: a pop-up button
exposes both expand/collapse and a value, and clicking it opens it rather than
typing into it; a table row exposes both selection and press, and clicking it
selects.

Three rules keep this honest:

- **Capabilities beat the control type.** A `Button` whose role reports a toggle
  state is a toggle button, and recording it as `invoke` would produce a step
  that does the right thing by accident and the wrong thing after a redesign.
  The control type is the fallback for an element that reports none of the
  attributes above, which some custom and web-rendered controls do.
- **Fall back to `click`, never guess.** An element supporting no useful
  capability and carrying no informative control type records as `click`. A
  wrong verb is worse than a general one, because a wrong verb changes what the
  run does. A click that resolved to no named element at all (no identifier and
  no name) is always `click`, because a coordinate target cannot be driven
  through an action.
- **`expand` versus `collapse` is read from the current state**, not from the
  click: recording `expand` for an already-open node produces a step that closes
  it on the next run.

A double-click records as `double_click` and a right-click as `right_click`
regardless of capabilities: those are gestures, not actions. Non-click verbs come
from the keyboard: a named key (Return, Tab, Escape, the arrows, Delete, Home and
End, Page Up and Page Down) becomes `press_keys`, and a chord held with Command
or Control becomes `press_keys` with the chord spelled in the `Shortcut` tool's
syntax (`cmd+shift+s`), in the modifier order `cmd`, `ctrl`, `option`, `shift`.
A trailing Return folds into the preceding text step as `submit`. Window
creation and focus changes are not yet observed, so `open_app`, `focus_window`,
`close_window`, `navigate` and `scroll` are added by the reviewer.

---

## 4. Inferring the selector, the one place ambiguity can be resolved

**The recorder is the only component that can pick a selector correctly**, and
this is the strongest argument for recording over hand-authoring.

At the moment of the click it holds something nothing else ever holds: the
**intended element**, the specific object under the pointer, unambiguously. So
it can choose the key for it from what that element actually offers rather than
from what an author remembers it offering.

The ladder, highest available rung first (taxonomy section 4.1):

1. **`automation_id`**, when the application sets an `AXIdentifier`. Survives
   translation.
2. **`name` + `control_type`**, when the element has an accessible name.
3. **`point`**, when it has neither: recorded, and marked non-durable.

The control type is attached to rungs 1 and 2 whenever the element reports one,
so the selector narrows as far as the facts allow.

**Not yet implemented on macOS:** the second half of the Windows recorder's
check, counting how many other elements the candidate selector would match in
the captured tree and emitting an explicit `occurrence` when there is more than
one. A recorded selector here carries the default `occurrence: unique`, and the
ambiguity is discovered at `journey run`, where section 4.2 of the taxonomy turns
it into a failure that names the candidates. That is still better than a
hand-written guess, because the key itself was taken from the element rather
than from memory; what is missing is the early warning. It needs a tree walk at
each click on the main thread, which is affordable and is the next piece of this
work.

---

## 5. The assertions problem

This is the real gap, and it is not a small one: **a recording captures actions,
not intent.** A perfect capture of a human submitting an expense claim produces a
journey that verifies nothing at all. It proves the clicks landed. It does not
notice that the total was wrong.

A journey with no assertions is not a test, so the recorder's job is not finished
when it has captured the actions. Three mechanisms, which compose rather than
compete.

### 5.1 Mark an assertion while recording

The direct route, and the one that ships: point at what matters and press **F8**.

```
point at the control -> F8 -> an assertion appears on the step being recorded,
                              with the observed value already filled in
```

The recorder hit-tests under the **pointer** (the current cursor position, read
from a fresh `CGEvent`), not under focus. The author is pointing at the thing
they mean, which is rarely the focused control. It then proposes the assertion
the element can actually support:

| What the element exposes | Proposed assertion |
|---|---|
| a value with content | `element.value` `is` *what it currently reads* |
| a toggle state | `element.checked` `is_true` / `is_false`, as it currently is |
| a selection state | `element.selected` `is_true` / `is_false` |
| an identifier and a name | `element.name` `is` *the current name* |
| anything else | `element` `exists` |

The expected value is **what is on screen right now**, so the author confirms a
filled-in comparison rather than writing one: a different job, and a much
smaller one. The `element.name` case is worth noting: when the selector is an
identifier, asserting the name is a real check rather than a restatement of the
selector, and a renamed control is exactly the regression an id-targeted suite
would otherwise sail past.

The key is never recorded, so pressing it never appears as a step. A mark before
any action gets an `observe` step named "check the starting state" to hang from;
a mark mid-typing flushes the typed run first, so characters are never split or
dropped.

This is the model Selenium IDE and Playwright's codegen use, and it works for the
same reason: the moment you notice something matters is the moment you are looking
at it.

### 5.2 Propose from what changed: *not yet implemented*

The recorder can take a snapshot before and after each action and diff them. What
changed is a strong candidate for what the step was *for*:

| Change | Proposed assertion |
|---|---|
| the frontmost window's title changed | `window.title` `is` the new title |
| an element appeared | `element` `exists` |
| an element disappeared | `element` `does_not_exist` |
| a field's value changed | `element.value` `is` the new value |
| a control became enabled | `element.enabled` `is_true` |

Proposals are **ranked and offered**, never silently inserted. A step that changed
forty things does not need forty assertions; it needs the one the author cares
about, chosen from a list.

The cost is the snapshot: a bounded tree walk per action on the main thread,
which is real but affordable, and it must be **debounced**, taken once the UI
settles rather than once per keystroke, or typing a sentence costs a hundred tree
walks.

### 5.3 Freeze a golden run: *not yet implemented*

The most powerful of the three, and it costs almost nothing because the evidence
work already did it.

Run the recorded draft once against a known-good build. The
[run record](journey-evidence.md) already carries, for every assertion, what was
expected and **what was observed**, and the same evaluation machinery can read
every subject the taxonomy defines, whether or not an assertion asked for it. So a
run can produce a full observation baseline, and the author promotes the parts
that matter into assertions:

```
macos-mcp-server journey run draft.json --observe-all --out baseline.otlp.json
macos-mcp-server journey freeze draft.json --from baseline.otlp.json
```

The reviewer's question changes from "what should I assert?", which is hard, to
"which of these observed facts should be true every time?", which is easy.

The trap to write down now: a golden run **bakes in whatever was on screen**,
including a date, a session id, a generated reference number. Freezing must offer
`matches` with a pattern where a value looks generated, not just `is` with the
literal. A baseline that pins today's date is a suite that fails tomorrow.

---

## 6. Waits, not sleeps: *not yet implemented*

A human waiting for a slow screen produces an idle gap in the event stream. The
naive reading of that gap is a `pause` step, which is exactly the flakiness the
taxonomy tells authors to avoid: it records how long the machine took *once*.

The better reading: an idle gap is a **signal that a wait belongs here**. Pair it
with section 5.2's diff (the gap ended when something appeared) and the proposal
becomes a waited assertion on the thing that appeared, with a timeout derived
from the observed delay plus headroom:

```jsonc
// observed: the human waited 4.1s, and the confirmation panel appeared
{ "subject": "element", "target": { "automation_id": "pnlConfirm" },
  "operator": "exists", "wait": { "timeout": 15 } }
```

That is a step that passes in 0.2 seconds on a fast machine and still passes on a
slow one, from a recording that only ever saw 4.1.

---

## 7. Credentials

Before every typed character is accepted, the recorder asks the accessibility API
(on the main thread) for the focused application's focused element and checks
whether its role is `AXSecureTextField`. If it is, the character is marked
secure. If either lookup fails, the character is marked secure too: that is the
fail-closed rule, and it means a keystroke into an application whose focus the
API cannot see is dropped rather than kept.

A secure run of keystrokes is never written to the file. Instead the draft
carries an `enter_credential` step with the target already selected (the last
element clicked, which is where the typing went) and the credential name left
blank, under a step name that says the run was redacted and asks for the name of
the stored credential:

```jsonc
{ "name": "sign in (redacted ...)",
  "verb": "enter_credential",
  "credential": "",
  "target": { "automation_id": "txtPassword" },
  "submit": true }
```

The author fills in one field, and the resulting journey uses the
[credentials](credentials.md) path, where the agent can *use* a secret held in
the login keychain and never *read* one, rather than having a password typed back
into a document. The blank name is deliberate: the draft does not validate until
a human supplies it, which is better than a journey that runs and silently types
nothing into a sign-in form.

Two further rules of the emitter protect the redaction: a secure run and a
visible run never merge into one step, even when they are typed back to back,
and the test suite in `mcp-server-core` asserts that a recorded journey never
contains the secret.

---

## 8. The draft is a proposal

Whatever the inference achieves, the output of `record` is a **draft**, and the
review step is not optional. The recorder observes what was done; only the person
who did it knows why. Concretely, a reviewer is deciding:

- **Which actions were incidental.** A stray click, a scroll to find something, a
  correction. They were real, and they do not belong in the test.
- **Which assertions matter.** Of everything that changed, the two or three facts
  that constitute the journey having worked.
- **Where `type_text` should be `set_value`.** Typing is what happened; the
  settable `AXValue` is what should run.
- **Where a value is generated.** See section 5.3; this is the one a reviewer is
  uniquely able to spot.
- **Which lifecycle steps are missing.** The recorder does not yet see windows
  open and close, so the `open_app` the journey needs to start from a known state
  is the reviewer's to add.

A recorded journey that has been run once and never read is a journey that asserts
its own clicks landed. `record` says so in its own output when it writes the
file, not only here.

---

## 9. Deliberately not covered

- **Re-recording one step of an existing journey.** Genuinely useful and a
  significantly larger piece of work: it needs the recorder to align a new capture
  against an existing document, which is a diff over intent rather than over text.
  Re-record the journey.
- **A GUI editor.** The draft is JSON in a text editor, reviewed like code, in
  git. That is the point of journeys-as-code; an editor that hides the document
  hides the diff.
- **Recording an agent session.** An agent's tool calls are already on the audit
  chain and can be replayed from there. Watching the agent through the event tap
  would capture its synthetic input as if a person had typed it.
- **Cross-machine portability of `point` targets.** A coordinate is valid for one
  display arrangement and one window position. The ladder in section 4 exists so
  that almost nothing depends on one.
