# Session recording

Record the whole session to a single video file, automatically, for every
persona, so what the agent did can be reviewed after the fact.

Recording starts when the desktop engine starts and finalizes when it closes,
including on a kill-switch trip. The agent cannot turn it off: the `Recording`
tool exposes only `status` and `mark`.

- [Enabling it](#enabling-it)
- [How it captures](#how-it-captures)
- [Timeline markers](#timeline-markers)
- [Output and housekeeping](#output-and-housekeeping)

---

## Enabling it

Recording is a policy setting, not a flag. It is part of the transparency
guarantee, so it lives with the rest of it:

```jsonc
"transparency": {
  "recording_dir": "/Library/Application Support/MacOSMCP/recordings"
}
```

The server creates the directory `0700` if it is missing. It needs the **Screen
Recording** grant, keyed on the binary's signing identity like every other TCC
permission; `macos-mcp-server permissions check` reports whether it is held, and
a server without it starts but logs that the recorder could not be started.

Frame rate and codec flags are accepted so the command line matches
windows-mcp-server's:

```sh
macos-mcp-server stdio --policy-config policy.json --record-codec hevc --record-fps 4
```

| Flag | Default | What it does here |
|---|---|---|
| `--record-fps` | `4` | **Nothing yet.** Accepted for parity; the capture process picks its own frame rate. |
| `--record-codec` | `h264` | `h264` or `hevc`. **Reported by `status`, not applied.** The capture process encodes H.264 regardless. |

That is worth stating plainly: on this platform the two flags are a label and a
placeholder, not tuning. The section below says why.

---

## How it captures

The recorder is **`screencapture -v -x -k`** run as a child process of the
server, writing a QuickTime `.mov` straight to `recording_dir`: `-v` records
video, `-x` keeps it silent, `-k` draws the mouse clicks into the frames. The
system's own encoder decides the resolution, frame rate and bitrate; the server
neither downscales nor re-encodes, and there is no ffmpeg anywhere in the path.

ScreenCaptureKit's recording output is the in-process alternative, and it is
where the frame-rate and codec flags will eventually land. The SDK's idiomatic
layer does not yet expose the stream initialiser it needs, so the CLI is the
honest path for now, and `docs/README.md`'s one-line description of this page is
ahead of the code.

Two consequences follow:

- **Finalization is a signal.** On shutdown the server sends the child `SIGINT`,
  which is how `screencapture` finishes writing its file, and waits up to 15
  seconds for it to exit before killing it. A `.mov` that was killed rather than
  interrupted may not play; a clean shutdown of the server is what produces a
  clean file.
- **The whole session is one file.** There is no segmenting; a six-hour session
  is a six-hour `.mov`.

The child is started through the server's argv-only runner with the rebuilt
`PATH`, so it works under an MCP host that stripped the environment.

### Confirming it is on

```jsonc
{"mode": "status"}
```

```
Session recording: ON
File: /Library/Application Support/MacOSMCP/recordings/session-20261007-142230.mov
Markers: /Library/Application Support/MacOSMCP/recordings/session-20261007-142230.markers.jsonl
Codec: h264
Duration: 321.0s
```

When `recording_dir` is not set the same call reports `OFF` and names the policy
setting that enables it.

---

## Timeline markers

The `Recording` tool is in the `screen` toolset, so every persona has it. Marking
each step aligns the video with what the agent was doing:

```jsonc
{"mode": "mark", "label": "signed in"}
```

Markers land in a sidecar `.markers.jsonl` next to the video, one JSON object per
line with the seconds since the recording started, the wall-clock time, a kind
and the label:

```json
{"t":0,"time":"2026-10-07T13:22:30.104Z","kind":"recording.started","label":""}
{"t":41.8,"time":"2026-10-07T13:23:11.904Z","kind":"mark","label":"signed in"}
{"t":321.0,"time":"2026-10-07T13:27:51.113Z","kind":"recording.stopped","label":""}
```

Clicks are not written to the sidecar; `-k` paints them into the video itself.

For journey testing, mark at each step. It turns a 5-minute video into something
you can navigate:

```
Snapshot
Invoke  {name:"Sign in"}
WaitFor {condition:active_window, window_name:"Inbox"}
Recording {mode:mark, label:"signed in"}
Assert  {subject:screen.text, operator:contains, expected:"Welcome"}
```

`mark` on a session that is not being recorded is a no-op that says so, rather
than an error: a journey script works the same whether recording is on or not.

`mark` is annotated **destructive** (it writes model-authored text into an
evidence artefact), so a policy rule matching `annotation: destructive` governs
it like any other write; it is never served under `--read-only`.

---

## Output and housekeeping

Each session produces two files in `recording_dir`:

```
session-<stamp>.mov              the video
session-<stamp>.markers.jsonl    the markers
```

The stamp is the session stamp that also names the audit file and the journey
run record, which is what lets an evidence bundle gather all three:
`evidence bundle --recording-dir` picks up every `session-<stamp>.*` in the
directory.

### Disk

No sizes are quoted here because the encoder's settings are the system's and
vary with the display: a Retina display records at its full resolution. H.264
compresses a static form to very little and a scrolling document to much more.
Measure one typical session on the hardware you deploy to before sizing the
volume.

**The server never rotates or prunes.** A long-lived deployment needs a scheduled
cleanup, for example a root launchd job running:

```sh
find "/Library/Application Support/MacOSMCP/recordings" -type f -mtime +30 -delete
```

### Access

Recordings show whatever was on screen, which may include data the credentials
system was careful to keep out of the agent's context. Treat `recording_dir` as
sensitive: the server creates it owner-only, and if you create it yourself keep
it that way.

```sh
sudo mkdir -p "/Library/Application Support/MacOSMCP/recordings"
sudo chown root:admin "/Library/Application Support/MacOSMCP/recordings"
sudo chmod 700 "/Library/Application Support/MacOSMCP/recordings"
```

(Use the user the server runs as in place of `root` when it is a LaunchAgent.)

### On a kill

The desktop engine is closed as part of the server's teardown on every exit
path, including a kill-switch trip, and closing the engine is what finalizes the
recording; the frames leading up to a security event are the ones worth having.
The on-screen security banner is an ordinary window, so it is captured like
anything else on screen.

---

## Related

- [Policy configuration](policy-config.md): the `transparency` block and the
  default paths under `/Library/Application Support/MacOSMCP/`
- [Monitoring](monitoring.md): the audit log that pairs with the video
- [Journey evidence](journey-evidence.md): the run record and the bundle the
  video is sealed into
- [Security architecture](security-architecture.md): why this is always-on
- [Credentials](credentials.md): why the recording directory is sensitive
