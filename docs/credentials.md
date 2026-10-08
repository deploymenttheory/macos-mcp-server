# Credentials

The server can hold credentials for an agent to *use* without ever letting it
*read* them. Secrets are supplied once, at startup, and the only thing the
agent can do with one is type it into a password field.

## Supplying credentials

Write a JSON document, make it readable by you alone, and pass it at startup:

```sh
umask 077
cat > ~/.config/macos-mcp/creds.json <<'JSON'
{
  "credentials": [
    {"name": "corp-sso", "target": "login.example.com", "username": "svc@example.com", "secret": "..."},
    {"name": "api",      "target": "api.internal",      "secret": "..."}
  ]
}
JSON
macos-mcp-server stdio --credentials-file ~/.config/macos-mcp/creds.json
```

| Field | Required | Meaning |
|---|---|---|
| `name` | yes | The handle the agent uses. Unique, never secret. |
| `target` | yes | Host, URL or app identifier. Unique. Becomes the keychain service name, namespaced under `com.deploymenttheory.macos-mcp-server:`. |
| `username` | no | Stored as the keychain account. |
| `secret` | yes | The plaintext. Decoded into wipeable bytes, never kept as a Go string where avoidable. |
| `type` | no | `generic` (the only class supported). |
| `persist` | no | `session` (the only value supported): everything installed is removed again on every exit path. |
| `allow_unmasked_target` | no | Permit injection into a control that does not report itself as a secure text field. Default `false`. |

The file must be mode `0600` or stricter and owned by the user running the
server. A group- or world-readable file is refused at startup with the
`chmod 600` remedy in the message. Secrets are never accepted on the command
line, because `ps` shows argv to every user on the machine.

Supplying a credentials file enables the `credentials` toolset
automatically; it is never on by default otherwise.

## The never-read invariant

The `Credentials` tool has exactly three modes:

- `list` reports names, targets, usernames and whether each item is present in
  the keychain.
- `verify` checks that one credential is still present.
- `inject` clicks an optional target (by Snapshot label, accessibility name or
  identifier, or `loc`) and types the secret, then optionally presses Return.

No mode returns a secret. Injection reads the item inside the desktop engine,
converts it to UTF-16 code units, delivers them as keyboard events, and zeroes
the buffers. The result says only that "a short", "medium-length" or "long"
secret was typed, so a precise length never reaches the transcript.

Immediately before typing, on the main thread, the engine checks that the
focused element is an `AXSecureTextField`. If it is not, injection is refused
and the message names the per-credential `allow_unmasked_target` opt-out for
destinations that genuinely cannot report themselves as secure (a terminal,
some Electron and Java applications).

## Exposure rules

Installed credentials live in the login keychain. Some toolsets can read that
back, so serving them together is refused at startup unless the policy
document acknowledges it:

| Toolset | Why it is risky |
|---|---|
| `shell` | `security find-generic-password -w` returns the secret. |
| `filesystem` | The login keychain is a file that can be copied for offline attack. |
| `screen`, `interaction`, `system` | Only when a credential sets `allow_unmasked_target`: a secret typed somewhere unmasked can be read back by Screenshot, GetText or Clipboard. |

To accept the exposure deliberately:

```json
{ "credentials": { "acknowledge_toolset_exposure": ["shell"] } }
```

The acknowledgement is logged and, once the audit chain lands, audited.

## Lifecycle

Credentials are installed after the tool surface is admitted and the exposure
check passes, and removed on normal exit and on the kill-switch path alike
(exactly once, whichever comes first). A partial install is rolled back. The
audit log records names, targets and usernames only.
