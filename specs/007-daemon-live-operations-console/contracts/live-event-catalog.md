<!-- markdownlint-disable MD013 -->

# Contract: Live Event Catalog

## Purpose

The daemon event stream is the authoritative live-state feed for the WebUI and
TUI operations console. In 006 it was a refresh signal with a free-form payload.
In 007 it becomes a typed catalog that can update current panels after one seed
with no steady-state overview/audit re-fetch.

## Envelope

Every event must validate against `schemas/daemon-event.schema.json`.

Required envelope fields:

```json
{
  "version": "hideout.daemon-event/v1",
  "seq": 1,
  "kind": "environment",
  "entity": {"kind": "environment", "id": "env_...", "profile": "default"},
  "payload": {}
}
```

Rules:

- `seq` is monotonically increasing per daemon instance.
- A client that detects a gap must mark stale and request a new seed before
  claiming live state again.
- `entity` is required for row-level updates.
- Payloads must already have deterministic control-plane redaction applied.
- The stream is live fan-out only; no historical replay is promised.

## Event Kinds

### `environment`

Updates reusable environment rows.

Required payload fields:

- `id`

Display fields, when known:

- `name`
- `status`
- `profile`
- `backend`
- `workspace`
- `imageRef`
- `lastSessionId`
- `lastCommand`
- `lastStartedAt`
- `lastEndedAt`

Allowed phases: `start`, `progress`, `complete`, `failed`.

Rules:

- Environment events in 007 are emitted by the existing Manager environment
  stop/clean lifecycle.
- Environment create/recreate and run-start environment changes remain visible
  through seed/manual refresh until a later environment-emitter expansion.

### `session`

Updates session/run rows.

Required payload fields:

- `id`

Display fields, when known:

- `profile`
- `backend`
- `networkMode`
- `hasAudit`
- `hasEphemeralState`
- `status`

Allowed phases: `start`, `progress`, `complete`, `failed`.

Rules:

- Session events are emitted on the daemon-mediated Manager run path where
  `Core.Observer` is set.
- Standalone CLI runs with embedded `Core.Observer=nil` do not emit typed
  session rows; their activity still reaches the audit panel through audit-tail
  events.

### `background`

Updates daemon background operation rows.

Required payload fields:

- `id`
- `op`
- `status`

Allowed statuses: `queued`, `running`, `completed`, `failed`, `stale`.

### `audit`

Appends to recent audit/denied audit tails.

Required payload fields:

- `time`
- `session`
- `profile`
- `action`
- `decision`
- `details`

Rules:

- `details` must be the deterministic redacted local audit details.
- `decision=deny` also updates the denied audit tail.

### `export`

Records evidence export outcomes.

Required payload fields:

- `status`

Display fields, when known:

- `source`
- `artifactPath`
- `decision`

Rules:

- Export artifact content is not embedded in the event.
- Export events are emitted by the existing `evidence/export/apply` Manager
  operation; plan-only export reviews are not live outcomes.
- Local artifact paths are local user data and may appear in local UI.

### `cleanup`

Records cleanup outcomes.

Required payload fields:

- `status`

Display fields, when known:

- `sessions`
- `removed`
- `secretState`

Rules:

- Cleanup events are emitted by existing run session cleanup (`CloseRunSession`).
  007 does not add a new daemon session-cleanup operation class.
- `removed` contains redacted cleanup types, not raw local paths.

### `terminal`

Terminates or degrades a stream.

Required payload fields:

- `reason`

Known reasons:

- `stream closed`
- `credential invalidated`
- `daemon stopping`
- `backpressure`
- `schema mismatch`

## Unknown And Malformed Events

- Unknown future `kind`: ignore or record unsupported; do not crash; do not mark
  known state current from it.
- Known `kind` with missing required field: mark `schema-mismatch` and stop
  claiming live state.
- Duplicate or old `seq`: ignore deterministically.
- Out-of-order future `seq`: mark stale because a gap is possible.
- Unknown entity: mark the affected panel stale unless the event kind explicitly
  creates that entity.

## Redaction

Events must contain no:

- daemon/UI tokens;
- broker tokens;
- `HIDEOUT_SECRET_*` backing names or values;
- generated machine-id material;
- proxy credential values;
- hidden implementation paths classified as control-plane material.

Local user/application data may be present on this authenticated local surface.
Exporting or sharing event-derived artifacts remains governed by 005.

## Drift Guard

Gate 0 must fail if:

- schema required fields differ from Go catalog required fields;
- WebUI/TUI reducer fixtures omit a current panel event kind;
- a current panel can only update by calling overview/audit after seed;
- a required field is removed/renamed without updating schema, reducer, tests,
  and docs.
