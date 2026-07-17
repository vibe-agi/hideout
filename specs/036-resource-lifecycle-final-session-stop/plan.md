# Implementation Plan: Resource Lifecycle And Final-Session Stop

<!-- markdownlint-disable MD013 MD060 -->

**Branch**: `036-resource-lifecycle-final-session-stop` | **Date**: 2026-07-17 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/036-resource-lifecycle-final-session-stop/spec.md`

## Summary

Add one daemon-owned, non-authoritative lifecycle coordinator over the Manager
run path introduced by 034. Manager remains the capability authority and
registers typed effects through a daemon-supplied registrar. A closed live
resource catalog and pure reducer distinguish VM pins, pre-stop drains, and
unproved orphans; a separate bounded fact catalog classifies handoffs and
retained product state without entering the stop predicate. Automatic stop is
allowed only after the final VM dependency is released, a 15-second grace
expires, the graph is reconciled, and the same Lima incarnation is observed
running under the environment transition lock.

Persist bounded discovery metadata in a store-rooted lifecycle journal. Bind
each stop attempt to a host-issued start generation plus the Lima instance name
and guest kernel boot ID. Backend command completion is not success: one typed
lifecycle observer must subsequently report the expected instance stopped or
absent. Ambiguity remains `stopping-unknown`, blocks attachment, and never
triggers clean, delete, overlay discard, or independent host-app termination.

Manager plans each registration-owned provider subgraph in memory, then
commits that complete subgraph through one atomic journal write before that
provider effect becomes usable. Provider phases discovered later in startup
repeat the same barrier; the implementation does not claim one transaction
contains every future run effect. Routine `active`, `draining`, and successful-release
observations are coalesced into a bounded 500 ms checkpoint; until that
checkpoint lands, the already-durable planned graph is the conservative restart
envelope. New boot binding, orphan/cleanup failure, reconciliation, idle
deadline, stop attempt, and coordinator close remain synchronously durable.

Daemon socket/authenticated status readiness is independent of backend probe
latency. Startup first persists every eligible environment as current-epoch
`pending`, begins serving status, then reconciles environments with bounded
parallelism. Attach waits only for its environment's in-flight reconciliation;
stop and destructive mutation refuse that environment while reconciliation is
in flight. An authenticated environment-scoped retry reuses the same path.

## Technical Context

**Language/Version**: Go 1.25; strict JSON schemas for journal and public status

**Primary Dependencies**: Existing daemon, Manager, environment, session, backend/Lima, network, HostFS, portbridge, host-capability, audit, doctor, and live-console packages; standard library plus the existing `golang.org/x/sys/unix` locking primitives; no new module dependency

**Storage**: Existing private store and environment records plus an atomic, bounded, store-rooted lifecycle journal with a pre-authority commit per provider subgraph and bounded routine checkpoints; append-only audit remains separate from mutable discovery metadata

**Testing**: Go unit, reducer/model, race, contract, schema, migration, redaction, Manager/daemon parity, Gate 0 smoke, and real macOS arm64 Lima lifecycle evidence

**Target Platform**: Product behavior on macOS arm64 with preserved Lima environments; native has no VM root and is a mechanics harness only

**Project Type**: Go CLI/thin client, local single-user daemon, Manager Core, and VM backend

**Performance Goals**: Lifecycle registration adds no more than 5% or 10 ms, whichever is larger, to median warm bounded-command overhead; authenticated daemon status is ready within 3 seconds regardless of serial backend-probe duration; a proved-idle environment begins stop after 15 seconds and the Lima stop-and-observation transaction is bounded to 35 seconds

**Constraints**: No new capability action; no command-name lifecycle cases; no mutable count as authority; no automatic clean/delete/recreate; no host GUI process ownership claim; unknown state blocks automatic stop; daemon shutdown remains bounded; no new Lima config version or environment record version solely for lifecycle metadata

**Scale/Scope**: One operator and store, one daemon, up to 16 concurrent sessions, multiple preserved environments, at most one current stop attempt per environment incarnation, bounded journal and status output, and one production stop-capable backend in 036

## Constitution Check

*GATE: Passed before research and re-checked after Phase 1 design.*

- **Privacy Boundary - PASS**: Lifecycle touches daemon, Manager, session,
  backend, network, HostFS, endpoint, and host-app effect lifetime. Unknown
  kinds, missing dependencies, invalid generations, cycles, owner ambiguity,
  failed drain, backend identity drift, and ambiguous stop observations deny
  automatic stop. No ambient fallback is introduced.
- **Typed Authority - PASS**: Manager Core and existing Go providers retain all
  operational authority. The lifecycle registrar records ownership and
  dependencies but cannot grant HostFS, network, endpoint, host-app, or backend
  operations. No JavaScript or config participates in stop authority.
- **Workspace And Policy - PASS**: Workspace transport, HostFS policy,
  reserved roots, deny precedence, staged-write apply/discard, and profile
  authority are unchanged. Retained overlays are explicitly not VM pins.
- **Generality And Provider Scope - PASS**: The resource catalog is generic and
  producer-backed. Lima is the first lifecycle observer/provider, while Code,
  browsers, shells, agents, and ADB appear only as examples or future fixtures.
- **Evidence And Redaction - PASS**: One lifecycle classification feeds status,
  events, doctor, TUI/WebUI, and audit. Lifecycle records exclude credentials, raw
  authority paths, descriptors, PIDs, proxy values, and argv. Real stop claims
  require Lima evidence rather than native or persisted status.
- **Backend And Distribution - PASS**: The feature reuses installed Lima and
  the existing backend invocation path. It adds no helper, image, bootstrap
  script, package dependency, or hidden repair step.
- **Gates - PASS**: Gate 0 covers catalog/reducer/journal/schema/race/redaction
  and shadow parity. A new real Lima lifecycle lane proves boot identity,
  attach-versus-stop serialization, retained-state survival, restart recovery,
  and observed stop.
- **Status And Docs - PASS**: `docs/STATUS.md`, `docs/privacy-run-design.md`,
  `docs/privacy-run-test-plan.md`, `docs/threat-model.md`,
  `docs/architecture-principles.md`, `docs/claim-boundaries.md`, and daemon/UI
  lifecycle documentation are updated from the same catalog and evidence.

Post-design re-check: all items remain PASS. The journal is discovery metadata,
not authority; the coordinator serializes lifecycle decisions but does not
execute capabilities. No constitutional waiver is required.

## Project Structure

### Documentation (this feature)

```text
specs/036-resource-lifecycle-final-session-stop/
|-- spec.md
|-- plan.md
|-- research.md
|-- data-model.md
|-- quickstart.md
|-- contracts/
|   |-- backend-lifecycle-observation.md
|   |-- lifecycle-journal.md
|   |-- resource-catalog.md
|   `-- status-and-events.md
|-- checklists/
|   `-- requirements.md
`-- tasks.md
```

### Source Code (repository root)

```text
internal/
|-- lifecycle/
|   |-- catalog.go            # closed producer-backed resource descriptors
|   |-- model.go              # identities, edges, states, close policies
|   |-- reducer.go            # pure transition and stop predicate
|   |-- journal.go            # atomic bounded discovery metadata
|   |-- registry.go           # daemon-owned single-writer coordination
|   |-- reconcile.go          # provider probes and restart classification
|   |-- modelcheck.go         # production-transition explorer and replay proof
|   `-- status.go             # redacted derived public view
|-- backend/
|   |-- lifecycle.go          # backend-neutral observation contract
|   `-- lima/
|       |-- lifecycle.go      # inventory plus boot-id observation
|       `-- ssh_pool.go       # daemon-scoped transport, per-operation channels
|-- daemon/
|   |-- lifecycle.go          # bounded per-environment reconciliation and retry
|   |-- daemon.go             # composition and bounded shutdown
|   |-- sessions.go           # worker state bridged to production registrar
|   `-- status.go             # lifecycle inventory and events
|-- manager/
|   |-- run_service.go        # daemon registrar dependency
|   |-- run_environment.go    # registration under environment lock
|   |-- run_dataplane.go      # provider registrations and releases
|   `-- environment_lifecycle.go # explicit and automatic stop transaction
|-- session/
|   `-- ownership.go          # existing kernel proof mapped semantically
|-- environment/
|   `-- environment.go        # existing transition lock; no lifecycle version churn
|-- doctor/
|   `-- report.go             # blocked/orphaned recovery findings
`-- liveconsole/
    `-- reducer.go            # lifecycle status/event projection

schemas/
|-- lifecycle-journal.schema.json
|-- lifecycle-status.schema.json
`-- daemon-status.schema.json

scripts/
|-- test-lifecycle-smoke.sh
|-- test-lifecycle-lima-e2e.sh
|-- lib/
|   |-- gate2-resource-lifecycle.sh
|   `-- gate2-resource-lifecycle-performance.sh
`-- test-gate0.sh

cmd/
|-- hideout-lifecycle-model/ # strict model evidence producer
`-- hideout-session-supervisor/ # target/EOF close-race ownership

docs/
|-- architecture-principles.md
|-- privacy-run-design.md
|-- privacy-run-test-plan.md
|-- threat-model.md
|-- claim-boundaries.md
|-- STATUS.md
`-- tui-webui-experience.md
```

**Structure Decision**: Add one internal lifecycle domain package because the
same pure model must be shared by daemon coordination, Manager registration,
backend observation, status, tests, and docs-truth. Keep stop execution in the
existing Manager/backend path, use the existing environment transition lock,
and keep CLI/TUI/WebUI as clients of derived state. Do not place capability
providers or raw backend handles in the lifecycle package.

## Complexity Tracking

No constitution violations or waivers are required.
