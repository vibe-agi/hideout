# Research: Route And Event Drift Guards

<!-- markdownlint-disable MD013 -->

## Decision 1: Use A Shared Route Recognizer/Table, Not A Test-Only List

**Decision**: Introduce small production-owned route metadata for Manager
`/api/v1` and daemon-specific endpoints. Production dispatch should consume it
where the change is small; otherwise production exposes a recognizer derived
from the same table that tests consume.

**Rationale**: `internal/manager/api.go:160-255` currently dispatches GET
routes through special cases plus `overviewResource`, and
`internal/manager/api.go:258-323` dispatches POST routes through a switch.
`internal/daemon/server_test.go:164-190` currently carries a hand-written route
list that proves listed routes are accepted but cannot prove every production
route is listed. 027 must close that gap without rewriting the router.

**Rejected Alternatives**:

- Full OpenAPI generation: too large for this internal-hardening slice.
- Test-only route list: repeats the failure mode that motivated 027.

## Decision 2: Keep Daemon Endpoints Separate From Manager Routes

**Decision**: Daemon endpoints get their own endpoint class and inventory.
Manager `/api/v1` route counts must not include `/daemon/status`,
`/daemon/events`, `/daemon/stop`, or `/daemon/background`.

**Rationale**: Daemon endpoints authenticate through the same operator token
but are not Manager resources. `internal/daemon/server.go:62-90` serves
background submission, `internal/daemon/server.go:92-190` serves the event
stream, and `internal/daemon/server.go:193-217` serves status/stop. Mixing them
with `/api/v1` would recreate the 006 route-count ambiguity.

## Decision 3: Manual Event Catalog With Accountable Producer Source

**Decision**: The live-event catalog is manual but validated. Each row names
producer kind, optional remap/default behavior, required fields, redaction
expectation, production source, and reducer coverage.

**Rationale**: Static discovery of Go producer calls is fragile. The current
bus maps producer kinds in `internal/daemon/events.go:41-118`, while reducer
branches live in `internal/liveconsole/reducer.go:28-61` and validation in
`internal/liveconsole/validate.go:15-65`. A validated catalog is small and
directly targets the drift class.

**Rejected Alternatives**:

- AST discovery of producer calls: brittle and high-maintenance.
- Representative events alone: they validate schema but do not prove production
  source, remap semantics, or reducer coverage.

## Decision 4: Unknown Event Behavior Is A Contract

**Decision**: Unknown event kinds remain forward-compatible and ignored by the
reducer while preserving stream sequencing. Producer-side default remaps must be
declared separately from reducer unknown-kind behavior.

**Rationale**: `internal/liveconsole/reducer.go:59-60` ignores unknown reducer
kinds. `internal/daemon/events.go:109-117` currently remaps unknown producer
kinds to session events. Both behaviors can be valid, but only if cataloged and
tested as different contracts.

## Decision 5: Runtime WebUI Action Proof, Not Source Grep

**Decision**: WebUI action-route tests must execute the served JavaScript in a
runtime harness and record actual `fetch` calls or use a shared action
descriptor consumed by the runtime. Searching HTML text is not sufficient.

**Rationale**: `internal/manager/server.go:569` and `:578` build Manager
requests dynamically, while `internal/manager/server.go:1482` builds daemon
requests. Existing checks around `server_liveconsole_test.go:116-144` validate
helper strings, not a clicked/triggered runtime request.

## Decision 6: No New UI Capability

**Decision**: 027 may test and catalog current action routes but must not add
new WebUI/TUI actions, panels, or authority.

**Rationale**: The batch goal is internal hardening. New user-visible behavior
belongs in a separate product spec.
