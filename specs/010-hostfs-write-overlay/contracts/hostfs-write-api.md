# Contract: HostFS Write Manager API

<!-- markdownlint-disable MD013 -->

## Scope

The Manager API is the only product surface that can resolve staged HostFS write decisions. CLI, TUI, WebUI, and daemon clients call these routes; none receive raw host filesystem authority.

All routes require the existing Manager API token and host/origin checks.

## Resources

```text
POST /api/v1/hostfs/write/plan
POST /api/v1/hostfs/write/claim
POST /api/v1/hostfs/write/apply
POST /api/v1/hostfs/write/discard
GET  /api/v1/hostfs/write/status
```

## Plan

`POST /api/v1/hostfs/write/plan`

Purpose: return a reviewable decision for a staged operation.

Request:

```json
{
  "operationId": "hfwop_123",
  "includePreview": true
}
```

Response data:

```json
{
  "version": "hideout.hostfs-write-plan/v1",
  "decisionId": "hfwdec_123",
  "operationId": "hfwop_123",
  "state": "pending",
  "operation": "replace",
  "path": "/Users/alice/project-notes.txt",
  "destinationPath": "",
  "preview": {
    "kind": "text-diff",
    "truncated": false,
    "summary": "3 lines changed"
  },
  "policy": {
    "grantId": "fs_abc",
    "source": "profile",
    "denyMatched": false
  },
  "privilege": {
    "status": "enforced",
    "reason": "target-no-sudo"
  },
  "timeoutAt": "2026-07-08T12:00:00Z",
  "claim": null,
  "warnings": []
}
```

Fail closed when:

- operation id is unknown;
- staged record is not durable or readable;
- operation is already resolved;
- preview generation would expose control-plane paths or exceed limits.

## Claim

`POST /api/v1/hostfs/write/claim`

Request:

```json
{
  "decisionId": "hfwdec_123",
  "expectedVersion": "hideout.hostfs-write-plan/v1",
  "surface": "webui"
}
```

Response data:

```json
{
  "decisionId": "hfwdec_123",
  "state": "claimed",
  "claimToken": "claim_opaque_secret",
  "claimExpiresAt": "2026-07-08T12:01:00Z"
}
```

Rules:

- only one active claim exists at a time;
- claim token is returned only to the claimant;
- claim token is never written to audit, daemon events, or exports;
- stale/incorrect expected version fails closed.

## Apply

`POST /api/v1/hostfs/write/apply`

Request:

```json
{
  "decisionId": "hfwdec_123",
  "claimToken": "claim_opaque_secret",
  "expectedVersion": "hideout.hostfs-write-plan/v1"
}
```

Response data:

```json
{
  "version": "hideout.hostfs-write-result/v1",
  "decisionId": "hfwdec_123",
  "operationId": "hfwop_123",
  "decision": "allow",
  "status": "applied",
  "changedPaths": ["/Users/alice/project-notes.txt"],
  "privilege": {
    "status": "enforced",
    "reason": "target-no-sudo"
  },
  "auditRef": "audit:session:line"
}
```

Fail closed when:

- claim token is missing, wrong, expired, or not the active claim;
- current plan version differs from expected version;
- policy, grant, path, symlink, reserved-root, conflict, or metadata revalidation fails;
- current privilege status cannot be surfaced;
- operation-specific no-partial apply cannot be guaranteed.

## Discard

`POST /api/v1/hostfs/write/discard`

Request:

```json
{
  "decisionId": "hfwdec_123",
  "claimToken": "claim_opaque_secret",
  "expectedVersion": "hideout.hostfs-write-plan/v1",
  "reason": "operator-denied"
}
```

Response data:

```json
{
  "version": "hideout.hostfs-write-result/v1",
  "decisionId": "hfwdec_123",
  "operationId": "hfwop_123",
  "decision": "deny",
  "status": "discarded",
  "auditRef": "audit:session:line"
}
```

Rules:

- discard deletes authority-bearing staged objects;
- discard is audited;
- timeout uses the same deny/discard state with reason `approval-timeout`.

## Status

`GET /api/v1/hostfs/write/status`

Query parameters:

- `session`: optional session id filter.
- `profile`: optional profile filter.
- `state`: optional decision state filter.

Response data:

```json
{
  "version": "hideout.hostfs-write-status/v1",
  "pending": [
    {
      "decisionId": "hfwdec_123",
      "operationId": "hfwop_123",
      "profile": "default",
      "state": "pending",
      "operation": "replace",
      "path": "/Users/alice/project-notes.txt",
      "timeoutAt": "2026-07-08T12:00:00Z",
      "privilegeStatus": "enforced"
    }
  ]
}
```

Status is read-only. It never returns claim tokens or overlay object paths.
