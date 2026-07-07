<!-- markdownlint-disable MD013 -->

# Contract: Manager API Parity Matrix (v1)

The daemon serves the full existing typed Manager API by mounting
`manager.API.Handler()` (`internal/manager/api.go:126`) over its transport. Parity
is by construction: the same handler, the same `authorize`/`checkHost`/`checkOrigin`
guards, the same `Core` operations and validators. This matrix enumerates the v1
served routes so tasks can assert coverage and detect drift.

## POST (typed plan/apply) — `servePostResource` (`api.go:184-217`)

| Route | Op |
| ----- | -- |
| `/api/v1/init/plan` | init plan |
| `/api/v1/init/apply` | init apply |
| `/api/v1/run/plan` | run plan |
| `/api/v1/run/apply` | run apply |
| `/api/v1/environment/stop/plan` | env stop plan |
| `/api/v1/environment/stop/apply` | env stop apply |
| `/api/v1/environment/clean/plan` | env clean plan |
| `/api/v1/environment/clean/apply` | env clean apply |
| `/api/v1/profile/command-proxy/plan` | command-proxy plan |
| `/api/v1/profile/command-proxy/apply` | command-proxy apply |
| `/api/v1/profile/hostfs/plan` | HostFS rule plan |
| `/api/v1/profile/hostfs/apply` | HostFS rule apply |
| `/api/v1/profile/env/plan` | profile env plan |
| `/api/v1/profile/env/apply` | profile env apply |
| `/api/v1/evidence/export/plan` | export plan (005) |
| `/api/v1/evidence/export/apply` | export apply (005) |

## GET (read)

Two GET resources are special-cased in `ServeHTTP` before the `overviewResource`
switch — `audit/events` (`api.go:154`) and `run/status` (`api.go:163`); the rest
resolve through `overviewResource` (`api.go:969-998`). All 16 are served.

| Route | Resource |
| ----- | -------- |
| `/api/v1/audit/events` | audit events (special-cased, `api.go:154`) |
| `/api/v1/run/status` | run status (special-cased, `api.go:163`) |
| `/api/v1/overview` | overview (stream seed source) |
| `/api/v1/profiles` | profiles |
| `/api/v1/sessions` | sessions |
| `/api/v1/environments` | environments |
| `/api/v1/backends` | backends |
| `/api/v1/capabilities` | capabilities |
| `/api/v1/broker` | broker |
| `/api/v1/network` | network |
| `/api/v1/secrets` | secrets (names only) |
| `/api/v1/audit` | audit events |
| `/api/v1/settings` | settings |
| `/api/v1/init` | init resource |
| `/api/v1/bundles` | bundles |
| `/api/v1/projects` | projects |

The current Manager API surface is **16 POST + 16 GET = 32 routes**.

## Daemon-Specific Endpoints (separate surface, not the Manager subrouter)

The daemon adds its own local lifecycle/observability endpoints, which are NOT part
of the parity-locked Manager subrouter and are inventoried here explicitly:

- daemon status/inventory (backs `hideout daemon status`; `schemas/daemon-status.schema.json`).
- event subscription (the live stream; `schemas/daemon-event.schema.json`).

These add no Manager operation class, no raw profile write, and no host execution.

## Rules

- The daemon MUST mount the Manager API subrouter as the exact existing handler:
  under `/api/v1/…` it MUST NOT add, rename, or gate routes, add raw profile writes,
  or add host execution (FR-005). The daemon-specific endpoints above are a separate
  surface and MUST live outside `/api/v1/…`.
- A parity test MUST assert the daemon-served Manager route set equals the embedded
  handler's route set (drift guard). The expected set is exactly the 32 routes above;
  the two special-cased GET routes (`audit/events`, `run/status`) MUST be included.
  If a future route is added to `ServeHTTP`/`servePostResource`/`serveGetResource`,
  the daemon serves it automatically and the test's expected set is the single source
  updated intentionally.
- `overview` is the client's initial-state seed for the event stream (one read, then
  events; no steady-state polling).
- Confirmation-required operations fail closed unless CLI/WebUI supplied confirmation
  (FR-015); the daemon adds no prompt route.
