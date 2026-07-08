<!-- markdownlint-disable MD013 -->

# Implementation Plan: Daemon Live Operations Console

**Branch**: `007-daemon-live-operations-console` | **Date**: 2026-07-08 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/007-daemon-live-operations-console/spec.md`

## Summary

007 finishes the UI slice that 006 explicitly deferred: WebUI and TUI must keep
their current operations panels live from daemon event payloads after one seed,
with no steady-state overview/audit re-fetch while the daemon stream is healthy.
The design adds a shared Go-owned live-state model and reducer in
`internal/liveconsole`, upgrades `schemas/daemon-event.schema.json` from an
untyped payload envelope to a panel-consumable event catalog, makes the daemon
publish typed payloads, and changes WebUI/TUI clients from event-triggered
re-fetch to seed-plus-event reduction. End-to-end tests must prove visible
browser HTML and terminal output changes from events, not hidden polling.

## Technical Context

**Language/Version**: Go 1.25.0 (`go.mod`); embedded browser UI is the existing
plain HTML/CSS/JavaScript string served by Go.

**Primary Dependencies**: Existing standard-library HTTP/SSE stack, existing
`github.com/dop251/goja` dependency for deterministic JavaScript evaluation in
tests, existing `github.com/santhosh-tekuri/jsonschema/v6` schema validator.
No Flutter, desktop shell, npm package, or Bubble Tea migration in 007.

**Storage**: No new durable storage. One initial seed is read from existing
Manager API resources; daemon events remain non-durable live fan-out. Existing
daemon audit and session audit storage remain unchanged.

**Testing**: `go test ./...`, focused package tests under `internal/liveconsole`,
`internal/daemon`, `internal/manager`, and `internal/app`; JSON schema validation;
`scripts/test-live-console-smoke.sh` wired into Gate 0; `scripts/test-gate0.sh`;
markdownlint for docs/specs.

**Target Platform**: Local macOS/Linux development host running Hideout. No real
backend or guest dependency. Native harness is sufficient because no isolation
claim changes.

**Project Type**: Go CLI/local daemon with embedded WebUI and terminal dashboard.

**Performance Goals**: Apply and render representative event payloads within 1s
in WebUI and TUI tests. Healthy idle streams must perform zero timer/overview
polls after seed. Reducer updates must be non-blocking for daemon publishers and
bounded per subscriber as in 006.

**Constraints**: One initial seed per client connection, then typed events only
while the stream is healthy. Any schema mismatch, missing required field, event
gap, credential expiry, or stream termination must mark stale/disconnected
before further state is claimed live. Event reducers are read-only and cannot
execute authority.

**Scale/Scope**: Existing WebUI/TUI operational panels only: environments,
runs/sessions, background operations, recent audit/denied audit,
`evidence/export/apply` outcomes, existing run session cleanup outcomes, and
stream health. Multiple local subscribers must be supported, but multi-user or
remote operation is out of scope.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

- **Privacy Boundary**: PASS. 007 touches UI/daemon/evidence read models only.
  It adds no target authority, raw host execution, raw profile writes, prompt
  channel, public network listener, backend capability, or Manager operation.
  Fail-closed behavior is stale/disconnected rendering when seed-plus-event
  proof is unavailable.
- **Typed Authority**: PASS. All authority-changing actions stay on existing
  Manager plan/apply routes. Event reducers are read-only Go/JS read-model
  logic over Go-validated event payloads. UI action handlers still call the
  existing API helpers.
- **Workspace And Policy**: PASS. No workspace mount, HostFS, passthrough, env
  policy, proxy secret, or profile state semantics change. Existing deny and
  redaction behavior remain Go-owned.
- **Generality And Provider Scope**: PASS. The feature is generic Hideout
  operations-console behavior over the daemon event contract. UI framework
  migration is explicitly out of scope.
- **Evidence And Redaction**: PASS. Evidence is the event catalog, schema drift
  tests, WebUI deterministic JavaScript reducer proof, TUI terminal output proof,
  production emit-source tests, and no-polling instrumentation. Event payloads
  reuse deterministic control-plane redaction.
- **Backend And Distribution**: PASS. No new backend helper, distribution
  artifact, InitTask, or repair path is introduced.
- **Gates**: PASS. Gate 0 is required: package tests, schema validation,
  markdownlint, live-console smoke, and existing daemon smoke. No Lima, network,
  HostFS, endpoint, browser-control, or dogfood gate is required.
- **Status And Docs**: PASS. Update `docs/STATUS.md`,
  `docs/tui-webui-experience.md`, `docs/manager-control-plane.md`, and
  `docs/privacy-run-test-plan.md` when implemented. `docs/threat-model.md` only
  needs an update if implementation changes claims or non-claims.

## Project Structure

### Documentation (this feature)

```text
specs/007-daemon-live-operations-console/
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── live-event-catalog.md
│   └── live-console-ui.md
└── tasks.md
```

### Source Code (repository root)

```text
internal/liveconsole/
├── model.go              # Seed, State, Event, panel view models, health states
├── seed.go               # Build seed from Manager overview/audit/status data
├── reducer.go            # Deterministic seed-plus-event reducer
├── validate.go           # Required-field, schema-version, ordering checks
├── fixtures_test.go      # Shared representative event sequences
└── reducer_test.go       # duplicate/out-of-order/stale/redaction tests

internal/daemon/
├── events.go             # publish liveconsole.Event instead of untyped payloads
├── server.go             # SSE writes typed event catalog
├── client.go             # SubscribeEvents returns typed events for TUI
└── *_test.go             # stream, subscriber, redaction, backpressure tests

internal/manager/
├── server.go             # WebUI reducer/render wiring over existing HTML
├── server_liveconsole_test.go # served HTML + JS harness proves visible DOM updates
└── api.go                # unchanged authority surface; no new Manager routes

internal/app/
├── app.go                # TUI seed, typed event subscription, live render
├── app_liveconsole_test.go # terminal proof and no-polling regression tests
└── app_test.go           # daemon-less fallback and existing dashboard tests

schemas/
├── daemon-event.schema.json
└── live-console-seed.schema.json

scripts/
├── test-live-console-smoke.sh
└── test-gate0.sh         # wires live-console smoke

docs/
├── STATUS.md
├── tui-webui-experience.md
├── manager-control-plane.md
└── privacy-run-test-plan.md
```

**Structure Decision**: Add one shared `internal/liveconsole` package so the
event catalog and reducer invariants are Go-owned and reusable by daemon, TUI,
tests, and WebUI fixtures. WebUI keeps the existing embedded HTML/JS surface;
TUI keeps the existing terminal dashboard shape. Framework replacement is not
part of 007.

## Phase 0: Research Summary

See [research.md](research.md). Key decisions:

- Keep the existing embedded WebUI and terminal dashboard; do not migrate UI
  frameworks in 007.
- Introduce `internal/liveconsole` as the shared event catalog and reducer.
- Upgrade daemon events to typed payloads sufficient for current panels.
- Prove WebUI visible state with a deterministic served-HTML JavaScript harness
  rather than a browser binary dependency.
- Prove TUI visible state by capturing terminal output from a typed event
  stream and instrumenting overview/audit reads.

## Phase 1: Design And Contracts

See:

- [data-model.md](data-model.md)
- [contracts/live-event-catalog.md](contracts/live-event-catalog.md)
- [contracts/live-console-ui.md](contracts/live-console-ui.md)
- [quickstart.md](quickstart.md)

## Post-Design Constitution Check

- **Privacy Boundary**: PASS. The design adds read-model state, typed event
  schema, and tests only. Unsupported or unverifiable stream state fails closed
  to stale/disconnected UI.
- **Typed Authority**: PASS. `internal/liveconsole` reduces events only. UI
  actions still use existing Manager plan/apply/read routes.
- **Workspace And Policy**: PASS. No workspace, HostFS, env policy, proxy, or
  profile authority changes.
- **Generality And Provider Scope**: PASS. No provider or framework is promoted
  to Core semantics.
- **Evidence And Redaction**: PASS. Contracts require seeded control-plane
  material to be absent from payloads, WebUI output, terminal captures, logs,
  screenshots, and test artifacts. Drift guards fail tests on required-field
  changes.
- **Backend And Distribution**: PASS. No helper binary, packaging, backend, or
  InitTask changes.
- **Gates**: PASS. Gate 0 plus focused package tests and live-console smoke are
  sufficient.
- **Status And Docs**: PASS. Docs listed above are part of implementation tasks.

## Complexity Tracking

No constitution violations. The new package is justified by shared reducer
ownership: without it, WebUI JavaScript, TUI rendering, daemon schemas, and tests
would each define live-state semantics independently, which would recreate the
drift this feature is meant to remove.
