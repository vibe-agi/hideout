# Contract: Broker Action `host.app.open-resource`

<!-- markdownlint-disable MD013 MD060 -->

New broker action alongside `host.open` in `internal/broker`. Routed by `Action` from the command-proxy `Registration` for `code`.

## Request (guest → broker)

```json
{
  "subject": "<broker subject>",
  "action": "host.app.open-resource",
  "command": "code",
  "intent": {
    "appRef": "vscode",
    "resources": [{ "kind": "workspace", "guestPath": "/workspace/src/app.ts", "relativePath": "src/app.ts" }],
    "location": { "line": 12, "column": 3 },
    "windowMode": "reuse"
  }
}
```

- The guest sends the structured intent (produced by the grammar), never raw argv and never a host path.
- Args allowlist: `action`, `command`, `intent` only. Any other key → rejected (mirrors the strict `host.fs` envelope allowlist).

## Handling (broker, Core)

1. Strict-decode `intent`; reject unknown fields.
2. Delegate to the `hostcap` `host.app.open-resource` provider with the session context (workspace `HostRoot`, profile, session id, active IdeMode).
3. The provider maps `ResourceRef` → host path under `HostRoot` and re-checks symlink escape using the same helper `host.open` uses; resolves `appRef` via the Core app-identity registry; enforces mode; launches; emits `ide.open` audit.

## Response (broker → guest)

Success:

```json
{ "status": "ok", "data": { "outcome": "launched" } }
```

Refusal (fail-closed), typed:

```json
{ "status": "denied", "error": { "code": "projection.path.no-host-mapping", "outcome": "refused" } }
```

- No host path, host username, or token in any response field.
- Recovery `code` values come from the `internal/recovery` registry (`projection.*`).

## errno / exit mapping (guest shim)

| Outcome | Guest exit |
|---------|------------|
| launched | 0 |
| refused (no-host-mapping / unbound) | non-zero, typed message pointing at the recovery code |
| refused (trusted-denied) | non-zero, message points at the grant flow |
| provider unavailable | non-zero (fail-closed), never delegates to a shadowed binary |

## Test obligations

- Args outside the allowlist rejected.
- A resource escaping the workspace refused; host path never resolved into the response.
- Provider-unavailable refuses, does not fall back.
- Guest response carries no host absolute path / username / token.
