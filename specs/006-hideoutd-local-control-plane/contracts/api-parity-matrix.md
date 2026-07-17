<!-- markdownlint-disable MD013 -->

# Contract: Manager API Parity Matrix (v1)

The daemon serves the full existing typed Manager API by mounting
`manager.API.Handler()` (`internal/manager/api.go:126`) over its transport. Parity
is by construction: the same handler, the same `authorize`/`checkHost`/`checkOrigin`
guards, the same `Core` operations and validators. This matrix enumerates the v1
served routes so tasks can assert coverage and detect drift.

## Manager Route Inventory

The authoritative inventory is the production `manager.ManagerRoutes()` registry
in `internal/manager/routes.go`. Both dispatch and drift tests consume this registry;
this contract deliberately does not repeat a manually maintained route table.

At this revision the registry contains **26 GET + 34 POST = 60 routes**. That count
is descriptive, not authoritative. A route change is complete only when its
`RouteSpec` (method, resource pattern, owner, and description), dispatch handling,
and route tests change together. Dynamic member routes use the same `{id}` patterns
recognized by production dispatch.

## Daemon-Specific Endpoints (separate surface, not the Manager subrouter)

The daemon adds its own local lifecycle/observability/control endpoints, which are
NOT part of the parity-locked Manager subrouter and are inventoried here explicitly.
The control endpoints are under `/daemon/…`; the browser transport also serves its
authenticated UI at the loopback root:

- `GET /` — loopback-only operator console document. Manager and daemon API calls
  made by the document remain token authenticated.
- `GET /daemon/status` — status/inventory, backs `hideout daemon status`
  (`schemas/daemon-status.schema.json`).
- `POST /daemon/stop` — ordered shutdown, backs `hideout daemon stop`.
- `GET /daemon/events` — the live event subscription (SSE;
  `schemas/daemon-event.schema.json`).
- `POST /daemon/background` — submit an existing typed environment stop/clean apply
  as background work (FR-010). It runs the same `Core.ApplyEnvironmentStop`/
  `ApplyEnvironmentClean` and rejects any other op class; it adds no new Manager
  operation class.
- `POST /daemon/lifecycle/stop` — serialize an observed environment stop through
  the 036 lifecycle coordinator.
- `POST /daemon/lifecycle/mutate` — serialize an existing typed destructive
  environment mutation with lifecycle reconciliation and active-resource state.
- `POST /daemon/lifecycle/reconcile` — retry one blocked environment's typed
  reconciliation in the current daemon epoch; backs `hideout daemon reconcile`.

These endpoints add no raw profile write or host execution. The lifecycle
extensions coordinate existing typed Manager/backend actions; they do not expose
raw VM operations or treat journal state as authority. This list is the complete
daemon-specific surface; any addition MUST be inventoried here.

## Rules

- The daemon MUST mount the Manager API subrouter as the exact existing handler:
  under `/api/v1/…` it MUST NOT add, rename, or gate routes, add raw profile writes,
  or add host execution (FR-005). The daemon-specific endpoints above are a separate
  surface and MUST live outside `/api/v1/…`.
- A parity test MUST assert the daemon-served Manager route set equals the embedded
  handler's complete `ManagerRoutes()` set (drift guard), including special reads and
  dynamic member routes. Tests MUST NOT maintain a second hand-written expected list.
- `overview` is the client's initial-state seed for the event stream (one read, then
  events; no steady-state polling).
- Confirmation-required operations fail closed unless CLI/WebUI supplied confirmation
  (FR-015); the daemon adds no prompt route.
