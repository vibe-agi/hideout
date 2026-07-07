<!-- markdownlint-disable MD013 -->

# Contract: Daemon Transport, Auth, and Lifecycle

## Lifecycle

- `hideout daemon start` starts exactly one daemon for the effective store; a second
  start reports the existing instance (single-instance lock + socket liveness probe),
  never a second live daemon.
- `hideout daemon status` reports whether a daemon is serving, its transport, and its
  background-operation inventory (validates against `schemas/daemon-status.schema.json`).
- `hideout daemon stop` performs ordered shutdown: in-flight and background operations
  finish or fail closed with recorded terminal status, subscribers get a terminal
  event, and the socket/lock are removed.
- A stale socket/lock from a crash is detected on the next start and either safely
  reclaimed or the start fails closed with a diagnostic.

## Daemon-Specific Endpoints (separate surface, all under `/daemon/…`)

All are operator-token authenticated and add no Manager operation class:

- `GET /daemon/status` — status/inventory (`schemas/daemon-status.schema.json`).
- `POST /daemon/stop` — ordered shutdown.
- `GET /daemon/events` — live redacted event stream (SSE;
  `schemas/daemon-event.schema.json`).
- `POST /daemon/background` — submit an existing typed environment stop/clean apply
  as background work (`{"op":"environment-stop"|"environment-clean","ids":[...]}`);
  it runs the same Core apply and rejects any other op class.

## Primary Transport (guest-unreachable placement)

- A Unix socket at `<store-runtime-dir>/hideoutd.sock`. Placement MUST be validated by
  the existing store-reserved and workspace-safety guards; if an ancestor is not
  operator-private or the path is guest-visible (workspace, HostFS grant, passthrough
  mount), the daemon MUST refuse to start.
- Placement structurally excludes real backend guests (Lima). For a weak native target
  sharing the operator UID, placement is NOT the boundary — the operator token is.
- Loopback HTTP is permitted only as the short-lived tokened UI transport
  (`manager.StartLocalServer`); the daemon MUST NOT expose an unauthenticated API on
  loopback and MUST NOT bind a non-local address.

## Authentication

- The daemon mints one operator token per start, persisted `0600` at
  `<store-runtime-dir>/token`, and reuses `manager.API.authorize` (Bearer /
  `X-Hideout-UI-Token`, constant-time compare, TTL).
- Every request MUST carry a valid operator token; OS peer identity alone MUST NOT be
  sufficient.
- Unauthenticated or invalid-token requests MUST be refused and recorded in the
  daemon-local audit log with channel and reason, and MUST NOT change state or record
  any client-supplied token material.
- v1 ships the operator token only; the read-only token is out of v1.

## Confirmation

- Confirmation-required operations MUST fail closed unless the CLI/WebUI flow supplied
  the confirmation outside any daemon prompt channel. The daemon MUST NOT prompt and
  MUST NOT treat a missing prompt channel as approval (FR-015).

## Restart Fail-Closed

- After restart the daemon holds only in-memory ownership since its current start. Any
  live resource without a current-instance ownership record is unprovable and MUST be
  reported and audited as an orphan — never silently re-adopted, never destroyed.
