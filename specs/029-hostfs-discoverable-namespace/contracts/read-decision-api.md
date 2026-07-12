# Contract: HostFS Read Decision API

<!-- markdownlint-disable MD013 -->

## Decision Kind

`hostfs.read` joins the closed generic decision kind vocabulary. It uses the
existing decision record version and authenticated list/inspect/claim/approve/
deny routes.

```json
{
  "version": "hideout.decision/v1",
  "id": "dec_hfr_opaque",
  "kind": "hostfs.read",
  "source": {
    "profile": "default",
    "session": "ses_...",
    "backend": "lima"
  },
  "state": "pending",
  "revision": 1,
  "defaultOutcome": "deny",
  "timeoutAt": "2026-07-10T12:05:00Z",
  "proposedAction": {
    "operation": "read",
    "path": "/Users/operator/Documents/report.txt",
    "canonicalPath": "/Users/operator/Documents/report.txt",
    "visibilityRuleId": "hfs_...",
    "visibilitySource": "profile",
    "lifetime": "session"
  },
  "preview": {
    "summary": "Allow this running session to read one exact host file",
    "facts": {
      "scope": "exact-file",
      "contentPreview": false
    }
  },
  "allowedActions": ["approve", "deny"],
  "providerRef": {
    "provider": "hostfs.read",
    "decisionId": "dec_hfr_opaque",
    "sessionId": "ses_..."
  }
}
```

Paths are host-local user data. Public/exported artifacts remain subject to the
005 export boundary. File content and symlink target are never included.

## Proposal Contract

The broker submits a bounded internal proposal candidate to the Manager-owned
provider:

```text
sessionId, profile, backend
requestedPath, canonicalPath, operation=read
visibilityRuleId, visibilitySource
optional untrustedReason (<=512 UTF-8 bytes)
```

The provider revalidates every field and returns one of:

| Result | Broker mapping |
| --- | --- |
| New or existing pending/claimed decision | approval-required EACCES with public `decisionRef` |
| Explicit read deny | read-denied EACCES, no reference |
| Remembered denied/timed-out terminal | read-denied EACCES, no reference |
| Rate/pending capacity reached | request-limited EACCES, no reference |
| Session unprovable/provider failure | fail-closed EIO, no reference |

Equivalent proposals use `SHA-256(sessionId, canonicalPath, operation)` as the
opaque key input. Retries do not change timeout, claim lease, or revision.

## Limits And Timeout

- Decision timeout: five minutes, default deny.
- Pending limit: eight per session.
- Creation rate: eight new decisions per rolling 60 seconds per session.
- Rate refusal supplies `retryAfterMs` only when computed from the oldest
  retained creation timestamp.
- Pending-cap refusal has no false decision reference and may omit retry time.
- Untrusted reason is rendered as plain text with an explicit `untrusted`
  label after deterministic control-plane redaction.

## Existing Generic Routes

```text
GET  /api/v1/decisions
GET  /api/v1/decisions/{id}
POST /api/v1/decisions/{id}/claim
POST /api/v1/decisions/{id}/approve
POST /api/v1/decisions/{id}/deny
```

The query-style compatibility routes remain parity-locked. Claim tokens remain
private to authenticated operator surfaces.

### Approval

Approval requires a valid claim token and expected decision version. The
provider then:

1. holds the provider exclusive lock;
2. proves source-session liveness via owner-lock contention;
3. revalidates current profile/run policy, reserved roots, explicit deny,
   canonical path, regular-file type, and visibility rule;
4. prepares an exact-file read grant;
5. resolves and audits the generic decision;
6. atomically publishes the active grant before releasing the provider lock.

Any failure produces no usable grant. An activation failure is audited and the
decision becomes failed/stale rather than claiming successful authority.

### Denial And Timeout

Denial and timeout produce no grant. Their terminal provider state remains for
the source session so target retries cannot recreate or harass the operator.

## Reopen Routes

Reopen is an additive authenticated Manager operation and must be added to the
shared production route inventory:

```text
POST /api/v1/decision/reopen
POST /api/v1/decisions/{id}/reopen
```

Request:

```json
{
  "decisionId": "dec_hfr_opaque",
  "expectedVersion": "hideout.decision/v1",
  "reason": "operator wants to reconsider"
}
```

Response:

```json
{
  "version": "hideout.decision-result/v1",
  "decisionId": "dec_hfr_opaque",
  "kind": "hostfs.read",
  "status": "pending",
  "decision": "reopen",
  "revision": 2,
  "timeoutAt": "2026-07-10T12:15:00Z"
}
```

Rules:

- Only denied or timed-out `hostfs.read` decisions are reopenable in V1.
- The provider must prove the original session is live and owned by the current
  control plane.
- Ended, orphaned, missing, unreadable, or otherwise unprovable sessions fail
  closed and receive no grant or new deadline.
- Reopen clears stale claim state, increments revision, establishes a new
  five-minute timeout, and emits audit/live events.
- Target requests cannot call reopen.

CLI:

```text
hideout decision reopen --reason <text> <decision-id>
```

WebUI renders Reopen only for an eligible terminal `hostfs.read` row. TUI shows
the exact command as the terminal row's next action. The daemon carries the
authenticated Manager route and does not infer or supply approval.

## Audit And Event Contract

Detailed events are required for first create, claim, approve/activation,
deny, timeout, limit suppression, reopen, and activation failure. Repeated
deduplicated requests are aggregated rather than emitted per syscall.

Events include decision ID, revision, session, profile, backend, operation,
winning policy source/ID, outcome, and aggregate count. They omit file content,
symlink target, claim/capability token, and private read-grant path.
