<!-- markdownlint-disable MD013 -->

# Data Model: Daemon Live Operations Console

## Live State Seed

Represents the single current-state snapshot a client reads before consuming
events.

Fields:

- `version`: constant schema version for the seed.
- `generatedAt`: host-local timestamp for diagnostics only.
- `overview`: redacted Manager overview data needed by current panels.
- `auditTail`: recent redacted audit events for the audit panel.
- `deniedAuditTail`: recent redacted denied audit events.
- `daemonStatus`: daemon status inventory when available.
- `streamHealth`: initial `seeding` or `daemon-less` state.
- `profileScope`: optional local profile filter requested by the surface.

Validation rules:

- Must contain no Hideout-minted control-plane secret.
- Must be read at most once per healthy-stream connection before event
  reduction begins.
- Must be replaced by a new seed after reconnect/restart; historical replay is
  not assumed.

## Live Event Envelope

Shared event wrapper emitted by daemon and consumed by WebUI/TUI reducers.

Fields:

- `version`: constant `hideout.daemon-event/v1` until a catalog-breaking change.
- `seq`: monotonically increasing integer per daemon instance.
- `kind`: one of the catalog event kinds.
- `phase`: optional lifecycle phase for operation-like events.
- `entity`: stable reference to the panel entity affected by the event.
- `payload`: typed payload object for the event kind.
- `redaction`: optional marker that control-plane strip has been applied.

Validation rules:

- `version`, `seq`, `kind`, and required kind-specific fields are mandatory.
- `seq` must increase by one for a continuous stream. A gap marks the affected
  view stale and requires a new seed.
- Known event kinds with missing required fields are invalid and must not be
  applied as live state.
- Unknown future event kinds are ignored or recorded as unsupported without
  crashing the reducer.

## Entity Reference

Identifies the state object a payload updates.

Fields:

- `kind`: `environment`, `session`, `run`, `background`, `audit`, `export`,
  `cleanup`, or `stream`.
- `id`: stable local identifier when the entity has one.
- `profile`: optional profile owner.
- `session`: optional session owner.

Validation rules:

- Required for events that mutate existing panel rows.
- Unknown entity references must not silently create a live row unless the event
  kind is explicitly allowed to create that entity.

## Operations Panel State

The shared read model rendered by WebUI and TUI.

Fields:

- `profiles`: profile summary rows used by existing panels.
- `environments`: reusable environment rows and lifecycle status.
- `sessions`: session/run rows and audit/runtime availability.
- `background`: background operation rows and terminal status.
- `auditTail`: recent audit rows.
- `deniedAuditTail`: recent denied audit rows.
- `exportOutcomes`: recent evidence export outcomes from `evidence/export/apply`
  events.
- `cleanupOutcomes`: recent run session cleanup outcomes from existing
  `CloseRunSession` cleanup events.
- `streamHealth`: current stream health state.
- `lastSeq`: latest applied daemon sequence.

Validation rules:

- Reducer updates are deterministic for the same seed and event sequence.
- Terminal operation states (`completed`, `failed`, `stale`) must not be revived
  by duplicate or older events.
- State may contain local user/application data, but not Hideout-minted
  control-plane material.

## Stream Health State

Client-visible proof state for live mode.

States:

- `seeding`: initial seed in progress.
- `live`: stream is healthy and events are being applied.
- `idle-live`: stream is healthy but no recent events arrived.
- `stale`: current panel state can no longer be proven from seed plus events.
- `disconnected`: stream ended or daemon stopped.
- `credential-expired`: stream terminated because credential became invalid.
- `schema-mismatch`: supported catalog cannot validate a known event.
- `daemon-less`: fallback mode with no daemon stream.

Validation rules:

- Any unsupported schema version, required-field failure, event gap, stream
  termination, or credential expiry must leave `live` before rendering further
  state as live.
- Recovery from stale/disconnected requires a new seed plus authenticated stream.

## Live State Reducer

Go-owned reducer that applies events to panel state.

Inputs:

- `Live State Seed`
- ordered `Live Event Envelope` values
- optional profile scope

Outputs:

- updated `Operations Panel State`
- reducer decision: `applied`, `ignored`, `stale`, or `error`
- diagnostic reason for stale/error cases

Rules:

- Read-only; cannot execute authority or mutate profile/run/backend state.
- Applies redacted payloads only.
- Rejects or marks stale for invalid known events.
- Ignores duplicate or old events deterministically.
- Maintains capped recent audit/export/run-session-cleanup tails for UI
  ergonomics.

## WebUI Live Binding

Browser-side binding over the same catalog.

Responsibilities:

- Fetch one seed on startup or explicit reconnect.
- Subscribe to daemon events.
- Apply event payloads to the browser state model.
- Render the existing panels from the state model.
- Show stream health and stale/disconnected states.

Rules:

- No `overview` or `audit/events` fetch after seed while stream is healthy.
- Manual refresh is explicit and starts a new seed.
- Event handlers must not call Manager plan/apply except through existing UI
  action flows.

## TUI Live Binding

Terminal-side binding over the same catalog.

Responsibilities:

- Fetch one seed when daemon stream is available.
- Subscribe to typed daemon events.
- Render terminal output from `Operations Panel State`.
- Fall back to daemon-less interval behavior only after stream absence or
  explicit stale/disconnected transition.

Rules:

- No interval overview/audit polling while stream is healthy.
- Terminal proof captures visible output before and after representative events.
- Existing `--once` behavior remains daemon-less snapshot mode.

## User-Visible Live Proof

Verification artifacts for product completion.

Fields:

- `surface`: `webui` or `tui`.
- `seedReads`: overview/audit seed read counts.
- `postSeedReads`: overview/audit reads after seed during healthy-stream window.
- `eventSequence`: representative typed events applied.
- `before`: visible output or DOM text before events.
- `after`: visible output or DOM text after events.
- `redactionScan`: result proving no control-plane material.

Rules:

- `postSeedReads` must be zero for healthy-stream windows.
- `after` must differ from `before` due to event payloads.
- Artifacts must contain no Hideout-minted control-plane material.
