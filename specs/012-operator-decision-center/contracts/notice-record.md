# Contract: Informational Notice Record

<!-- markdownlint-disable MD013 -->

## Version

`hideout.notice/v1`

## Shape

```json
{
  "version": "hideout.notice/v1",
  "id": "not_...",
  "kind": "privilege.status",
  "source": {
    "profile": "default",
    "session": "ses_...",
    "backend": "lima"
  },
  "severity": "warning",
  "status": "degraded",
  "payload": {
    "reason": "target user can run passwordless sudo",
    "guidance": "recreate with an enforced-capable base image"
  },
  "preview": {
    "summary": "Privilege separation is degraded"
  },
  "acknowledged": false,
  "auditRef": "audit:guest.privilege.status",
  "createdAt": "2026-07-08T00:00:00Z",
  "updatedAt": "2026-07-08T00:00:00Z"
}
```

## Required Invariants

- Notices never include `claim`, `allowedActions`, `defaultOutcome`,
  `timeoutAt`, `providerRef`, or claim tokens.
- Acknowledgement records operator awareness only; it never approves, denies,
  discards, or applies provider authority.
- Repeated status notices should update the stable notice where the kind
  represents current status.
- Notice output follows the same deterministic control-plane redaction rules as
  decisions.

## Current Kinds

| Kind | Source | Ack Semantics |
| --- | --- | --- |
| `privilege.status` | 009 privilege evidence | acknowledge warning/fact |
| `background.status` | 006 daemon background registry | acknowledge status/fact |

## Acknowledgement Result

```json
{
  "version": "hideout.notice-ack/v1",
  "noticeId": "not_...",
  "surface": "webui",
  "acknowledgedAt": "2026-07-08T00:00:30Z",
  "auditRef": "audit:decision.notice.ack"
}
```
