# Contract: Actionable Decision Record

<!-- markdownlint-disable MD013 -->

## Version

`hideout.decision/v1`

## Shape

```json
{
  "version": "hideout.decision/v1",
  "id": "dec_...",
  "kind": "hostfs.write",
  "source": {
    "profile": "default",
    "session": "ses_...",
    "backend": "lima",
    "surface": "hostfs"
  },
  "state": "pending",
  "risk": {
    "summary": "replace one host file",
    "severity": "warning"
  },
  "proposedAction": {
    "operation": "replace",
    "resource": "/Users/alice/project/config.json"
  },
  "preview": {
    "summary": "replace 42 bytes",
    "facts": {
      "path": "/Users/alice/project/config.json"
    },
    "userDataPresent": true
  },
  "allowedActions": ["claim", "deny"],
  "defaultOutcome": "discard",
  "timeoutAt": "2026-07-08T00:01:00Z",
  "claim": {
    "surface": "cli",
    "claimedAt": "2026-07-08T00:00:30Z",
    "expiresAt": "2026-07-08T00:01:30Z"
  },
  "auditRef": "audit:hostfs-overlay:decision",
  "createdAt": "2026-07-08T00:00:00Z",
  "updatedAt": "2026-07-08T00:00:30Z"
}
```

## Required Invariants

- `claim.token` and token hashes never appear in list, inspect, watch, audit,
  UI, or export output.
- `providerRef` is not part of public output. Manager/Core may hold it in
  private state.
- `kind` determines valid `allowedActions` and provider apply behavior.
- `timeoutAt` is required for pending authority and resolves to
  `defaultOutcome`.
- Terminal decisions cannot move back to pending or claimed.
- Redacted preview is required for operator review.

## Current Kinds

| Kind | Provider | Terminal Actions |
| --- | --- | --- |
| `hostfs.write` | HostFS write overlay provider | apply, discard, timeout-discard |
| `adapter.proposal` | promoted capability provider only | approve, deny, timeout-deny |
| `evidence.share` | export/share boundary | approve-release, deny, timeout-deny |

## Redaction Requirements

Records must not expose:

- broker tokens;
- UI tokens;
- claim tokens or token hashes;
- `HIDEOUT_SECRET_*` backing values;
- generated machine IDs;
- proxy values;
- overlay object paths;
- hidden store/runtime paths;
- backend handles.
