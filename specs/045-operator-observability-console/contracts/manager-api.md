# Manager API Contract

## Authority and transport

All routes remain under the authenticated local daemon. Manager routes use
`/api/v1/`; daemon lifecycle and the SSE stream remain separate daemon-owned
routes. Requests require the current operator credential through the existing
authorization header. Browser origin/host checks and credential expiry remain
in force.

The response envelope remains backward-compatible:

```json
{
  "version": "hideout.manager-api/v1",
  "resource": "operator/snapshot",
  "data": {},
  "errors": [],
  "errorDetails": []
}
```

`errorDetails` is optional for old clients and contains stable machine-readable
codes:

```json
{
  "code": "stale-plan",
  "field": "baseRevision",
  "message": "profile changed after this review",
  "recovery": "refresh the profile, review the new diff, and confirm again"
}
```

Mutation requests use strict JSON decoding, bounded bodies, closed unions, and
reject unknown fields. No route logs a request body.

## Snapshot and projections

### `GET /api/v1/operator/snapshot`

Query:

- `profile` optional validated profile scope;
- `session` optional validated active or retained session;
- `activityLimit` optional `0..500`, default `100`.

Returns `hideout.operator-snapshot.v1` with daemon instance ID, event sequence,
stream credential generation, profile desired/effective/transition state,
sessions, recent activity, coverage, risks, operations, capabilities, and
catalog-backed next actions.

The snapshot is the only valid seed for applying subsequent SSE events. A
client must not merge a new snapshot with old local state.

### `GET /api/v1/profiles/{id}/projection`

Returns `ProfileProjection`. The existing `GET /api/v1/profiles` remains a
compact list and adds revision/transition summary fields.

### `GET /api/v1/operations/{id}`

Returns the durable `Operation`. It is safe to poll after disconnect or
response loss.

## Configuration transaction

Existing typed plan/apply routes remain compatible and become adapters over the
transaction service. New consoles use the generic closed-union routes.

### `POST /api/v1/profile/transaction/plan`

Request:

```json
{
  "schema": "hideout.configuration-draft.v1",
  "profile": "default",
  "baseRevision": 12,
  "clientNonce": "tui-4",
  "changes": [
    {
      "kind": "network.proxyRef",
      "value": {"ref": "local-proxy"}
    },
    {
      "kind": "network.dns",
      "value": {"mode": "doh", "serverIp": "1.1.1.1"}
    }
  ]
}
```

Response data is `ConfigurationPlan`. Planning has no side effect other than a
bounded operation reservation. A plan may contain blockers and still return
HTTP 200 for review.

Errors:

| HTTP | Code | Meaning |
| --- | --- | --- |
| 400 | `invalid-draft` | Unknown/invalid change or field |
| 404 | `profile-not-found` | No profile |
| 409 | `stale-draft` | Base revision already changed |
| 422 | `unsupported-capability` | Requested authority has no provider |

### `POST /api/v1/profile/transaction/apply`

Request:

```json
{
  "schema": "hideout.configuration-apply.v1",
  "operationId": "op_7adf...",
  "profile": "default",
  "baseRevision": 12,
  "planDigest": "sha256:...",
  "confirmed": true
}
```

Manager acquires the mutation key, replans from authoritative state, validates
the plan digest and revision, then executes typed effects. A successful response
contains the terminal or current `Operation` and the current
`ProfileProjection`.

Retry contract:

- same `operationId` and `planDigest`: return current/stored operation;
- same ID with different digest/profile: HTTP 409 `operation-mismatch`;
- changed revision/digest before ownership: HTTP 409 `stale-plan`, zero effect;
- response loss after commit: retry returns `succeeded`, no duplicate effect;
- nonterminal recovery: HTTP 202 with current phase and recovery, never a false
  success.

Status codes:

| HTTP | Meaning |
| --- | --- |
| 200 | Terminal success/rollback or terminal idempotent replay |
| 202 | Accepted and still transitioning |
| 400 | Invalid/expired/unconfirmed request |
| 409 | Stale plan, conflicting transition, mismatch, or blocker |
| 422 | Unsupported provider/capability |
| 500 | Failed operation with persisted recovery evidence |

## Secret lifecycle

No public API returns secret bytes or a secret-derived hash.

### `GET /api/v1/secrets`

Returns `SecretReference` metadata. Optional `ref` filters one item. Listing is
local-operator-only and returns name, provider, availability, generation,
updated time, and stable reason.

### `POST /api/v1/secret/plan`

Request:

```json
{
  "schema": "hideout.secret-draft.v1",
  "ref": "local-proxy",
  "action": "set"
}
```

`action` is `set`, `rotate`, or `delete`. Planning does not include the value.
The returned canonical plan shows current/next availability and generation,
affected profiles/environments, eligible live network transitions, blockers,
and recovery. It reserves an operation ID.

### `POST /api/v1/secret/apply`

Set/rotate request:

```json
{
  "schema": "hideout.secret-apply.v1",
  "operationId": "op_...",
  "planDigest": "sha256:...",
  "ref": "local-proxy",
  "action": "rotate",
  "value": "socks5://127.0.0.1:7890",
  "confirmed": true
}
```

Delete omits `value`. The route has `Cache-Control: no-store`, a 16 KiB body
limit, no body/audit logging, and zeroes temporary byte buffers on all paths.
The value is written only to the secret provider. Operation/audit/event output
uses reference/generation/availability.

After a successful set/rotation, the same operation coordinates all eligible
live network stage/activate/prove effects named in the review. A profile-only
future-attach effect can succeed without an active VM. A blocked posture change
does not stop the daemon or VM and returns exact blocking sessions.

## Activity queries

Every query requires exactly one resolved `ActivityOwner`; display names are
resolved to an exact owner by Manager before the store is opened. The owner
selector is either `session` for a disposable owner or
`environment` + `incarnation` for a reusable owner. An optional `run` session
ID narrows a reusable owner to one run. Manager binds `run` into every query
and events cursor; it rejects cross-session provider responses.

### `GET /api/v1/activity/summary`

Query: exact owner selector, optional `run`, and optional time range.
Returns counts by kind, current coverage, highest risks, retained range, quota,
pruning/corruption state, and latest cursor.

### `GET /api/v1/activity/events`

Query:

- exact owner selector;
- optional `run` session scope;
- `from`, `to`;
- `cursor`;
- `limit` (`1..500`, default `100`);
- repeated `kind`, `operation`, `execution`, `risk`;
- optional bounded `path`, `domain`, `ip` search.

Returns:

```json
{
  "records": [],
  "nextCursor": "cur_...",
  "coverage": [],
  "queryTruncated": false
}
```

The cursor binds the exact owner, optional run session, and normalized filters.
It cannot be used to cross owners or runs. A missing/pruned/corrupt interval is
returned in `coverage`, not silently omitted.

### `GET /api/v1/activity/executions`

Returns execution trees and aggregate activity counts. Optional `run` selects
one reusable-owner session; `id` selects one execution and `root=true` selects
top-level session commands.

### `GET /api/v1/activity/coverage`

Returns process/file/network/DNS coverage intervals with reason and evidence.
Optional `run` restricts intervals and current state to one session.

### `GET /api/v1/activity/risks`

Returns deterministic findings. Filters: optional `run`, severity, rule ID,
execution, and active time range. Findings link to activity IDs, not embedded
raw records.

### `POST /api/v1/activity/export/plan` and `/apply`

Adapters over the existing evidence export authority. The plan shows record
count, time/owner scope, redaction selectors, path policy, destination, and
review decision. Apply never treats authenticated local visibility as implicit
permission to publish/share.

## Lifecycle integration

Environment clean/delete/recreate and disposable cleanup plans include an
`activity.cleanup` typed effect for the exact owner. The terminal lifecycle
operation includes `CleanupProof`. A lifecycle route cannot return success when
activity absence is unproved.

## Compatibility

- Existing `/api/v1/overview`, profile-specific plan/apply, run, audit,
  decision, lifecycle, and daemon routes continue to work.
- Existing clients may ignore new optional projection fields.
- New clients require `operator-snapshot.v1` and daemon event v2 for mutation.
  If only the legacy event contract is available, they render read-only
  compatibility mode and show the missing capability.
- Secret environment fallback is reported as deprecated and cannot be created
  through the API.
