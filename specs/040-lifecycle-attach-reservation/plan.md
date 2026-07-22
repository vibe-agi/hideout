# Implementation Plan: Lifecycle Attach Reservation

<!-- markdownlint-disable MD013 -->

**Branch**: `040-lifecycle-attach-reservation` | **Date**: 2026-07-22 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/040-lifecycle-attach-reservation/spec.md`

## Summary

Close the run-establishment/reconciliation race by adding a daemon-local,
environment-scoped establishment reservation to the lifecycle coordinator. A
reusable Lima run allocates only an opaque session identity before reserving;
after older reconciliation finishes, it holds the reservation while acquiring
the existing cross-process transition lock, reloading the environment record,
and independently observing the backend incarnation. Only then may Manager
publish the environment runtime, create the existing durable owner record, and
atomically promote the reservation into the normal lifecycle registration.

The implementation preserves the existing owner, journal, workspace,
provider, target, stop, cleanup, and UI authority models. Establishment state is
not durable and carries no backend handle or credential. Reconciliation and
destructive mutation fail closed while a reservation is held; cancellation
releases only the caller's reservation and session state; daemon restart loses
the reservation and relies solely on durable owner/backend facts.

## Technical Context

**Language/Version**: Go 1.25.0, POSIX shell for gate orchestration, TLA+ for protocol model checking

**Primary Dependencies**: standard-library `context`/`sync`, existing `internal/lifecycle`, `internal/manager`, `internal/environment`, `internal/runsession`, backend lifecycle observation, Spec Kit artifacts

**Storage**: existing filesystem environment records, session runtime children, run-session owner records, and lifecycle journals; reservations remain daemon-memory-only

**Testing**: Go unit/integration tests, `go test -race`, deterministic interleaving fixtures, at least 1,000 randomized schedules, TLC model checking, Gate 0 lifecycle smoke, and real macOS arm64 Lima Gate 2

**Target Platform**: macOS arm64 with Lima for product evidence; native backend remains a weak mechanics harness

**Project Type**: Go CLI/daemon/Manager monorepo

**Performance Goals**: preserve the existing 2.0-second warm first-output contract for at least 95% of 30 real samples; serialize only the establishment boundary

**Constraints**: no wait for reconciliation while holding the environment transition lock; no durable provisional authority; no silent re-adoption after restart; no new CLI/config/manifest surface; all waits honor caller cancellation/timeouts; public evidence is deterministically redacted

**Scale/Scope**: one coordinator state machine per environment, multiple concurrent run reservations per compatible environment, 1,000+ randomized local schedules, four required real-backend order/recovery cases

## Constitution Check

*GATE: Passed before Phase 0 research and re-checked after Phase 1 design.*

- **Privacy Boundary**: The change touches daemon lifecycle coordination,
  reusable-environment transition ordering, environment runtime publication,
  durable run ownership, reconciliation, stop/clean refusal, status, and
  evidence. Missing reservation support, unknown backend observation,
  incarnation drift, mutation, reconciliation blockage, owner-write failure,
  promotion failure, cancellation, and cleanup uncertainty all fail before
  target launch or retain an explicit blocked state.
- **Typed Authority**: Manager `ApplyRun` remains the only run apply path. It
  allocates and materializes session state, writes the existing Go-owned owner
  record, and calls a narrow lifecycle reservation interface implemented by the
  daemon-owned Go coordinator. Backend observation remains independently typed
  by `backend.LifecycleObservation`; no JavaScript or configuration participates.
- **Workspace And Policy**: No workspace mount, HostFS rule, environment
  policy, proxy secret, profile, or grant semantics change. Shared workspace
  planning consumes the reservation-proved incarnation, but its exact-root and
  attachment validation remains unchanged. Denies and reserved roots are not
  bypassed.
- **Generality And Provider Scope**: The state machine is backend-neutral
  lifecycle coordination. The production path activates it for the existing
  lifecycle-managed reusable Lima path, while Lima-specific commands remain
  confined to Gate 2 fixtures and backend adapters.
- **Evidence And Redaction**: Derived lifecycle status exposes only an
  establishment count/activity and bounded reason codes. Events name waiting,
  reserved, prepared, promoted, and aborted transitions without session IDs,
  paths, lock names, PIDs, credentials, or raw command arguments. Existing
  Manager/TUI/WebUI/doctor consumers continue to use daemon status/events.
- **Backend And Distribution**: No helper, bootstrap, package, schema repair,
  or InitTask changes are required. Native tests prove mechanics only; real
  macOS arm64 Lima evidence proves backend incarnation and restart behavior.
- **Gates**: Gate 0 includes TLC, targeted lifecycle/manager tests, race tests,
  randomized replay, schema/status/event/redaction negative fixtures, docs
  truth, and the full existing Gate 0. Gate 2 must prove reconciliation-first,
  reservation-first, cancellation, restart before/after owner durability,
  identity/provenance, and 30-sample warm first-output performance.
- **Status And Docs**: Update `docs/privacy-run-design.md`,
  `docs/privacy-run-test-plan.md`, `docs/STATUS.md`, `docs/claim-boundaries.md`,
  and retire the resolved entry in `docs/DEBT.md`. `docs/threat-model.md` is
  updated only to clarify lifecycle recovery/non-claims; no broader privacy
  claim is introduced.

### Post-Design Re-check

The design still passes all principles. The new flexible judgment is a typed Go
coordinator primitive rather than compiled policy choice: it orders existing
authority and does not grant any. In-memory reservation state is intentionally
not a journal schema because durable provisional authority would create the
restart re-adoption ambiguity the feature must avoid. The existing owner record
remains the first durable session authority. No Complexity Tracking exception
is required.

## Project Structure

### Documentation (this feature)

```text
specs/040-lifecycle-attach-reservation/
├── spec.md
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── adversarial-report.md
├── checklists/
│   └── requirements.md
├── contracts/
│   ├── establishment-protocol.md
│   └── observability-and-evidence.md
└── tasks.md
```

### Source Code (repository root)

```text
formal/
├── AttachReservation.tla
└── AttachReservation.cfg

internal/
├── environment/
│   └── environment.go                 # existing transition lock/runtime paths
├── lifecycle/
│   ├── coordinator.go                 # reservation blockers/status integration
│   ├── establishment.go               # reservation state machine and promotion
│   ├── reconciliation_fence.go        # reservation-aware reconcile admission
│   ├── reconcile.go                   # direct reconcile exclusion
│   ├── registry.go                    # Registrar and shared promotion helper
│   ├── status.go                      # redacted establishing activity/count
│   └── *_test.go                      # forced schedules, random replay, redaction
├── manager/
│   ├── run_apply.go                   # corrected lock/owner/promotion ordering
│   ├── run_session.go                 # allocate identity separately from materialize
│   ├── run_workspace.go               # plan from reservation-proved incarnation
│   └── *_test.go                      # integration, cancellation, sibling safety
├── productevidence/
│   └── registry.go                    # registered 040 proof requirements
└── session/
    ├── session.go                     # side-effect-free layout allocation
    └── session_test.go

scripts/
├── test-lifecycle-smoke.sh             # 040 Gate 0 proofs/negative fixtures
├── test-lifecycle-lima-e2e.sh          # real ordering/restart/performance proof
└── test-gate0.sh                       # existing aggregate gate

docs/
├── STATUS.md
├── DEBT.md
├── claim-boundaries.md
├── privacy-run-design.md
├── privacy-run-test-plan.md
└── threat-model.md
```

**Structure Decision**: Extend the existing Go Manager/lifecycle/session
packages and lifecycle gates. The reservation receives its own lifecycle source
file so lock-order and state transitions remain reviewable. No new command,
service, schema package, or provider is introduced.

## Implementation Phases

### Phase 0: Protocol Research

1. Record the current race and rejected transition-lock/reconciliation cycle.
2. Define reservation admission, preparation, promotion, abort, crash, and
   concurrent-run semantics.
3. Bind every state transition to existing durable owner and lifecycle facts.
4. Define public evidence, failure reasons, redaction, mutation proofs, and gate
   acceptance.

### Phase 1: Contracts And Data Model

1. Define `EstablishmentRequest`, `EstablishmentReservation`, and promotion
   contracts without backend handles or credentials.
2. Split session layout allocation from filesystem materialization.
3. Define shared-workspace planning from a proved `EnvironmentRef` so lifecycle
   registration is not required before durable ownership.
4. Add derived status/event semantics while keeping reservation data out of the
   journal.

### Phase 2: Test-First Implementation

1. Add red tests for reconciliation-first and reservation-first orderings,
   mutation refusal, concurrent reservations, cancellation at each boundary,
   crash/restart classification, and side-effect-free allocation.
2. Implement coordinator reservation state and all admission blockers.
3. Reorder Manager establishment through allocation, reservation, transition
   lock, revalidation/observation, runtime, owner, and promotion.
4. Preserve all downstream provider/workspace/target/cleanup behavior and add
   status/event/redaction coverage.

### Phase 3: Evidence And Closure

1. Run 1,000+ randomized schedules, race tests, TLC, targeted and full Gate 0.
2. Temporarily break every new assertion/judge, record the expected red result,
   restore it, and retain negative fixtures in `adversarial-report.md`.
3. Run the real Lima topology and 30-sample warm performance lane, retaining
   exact source/runtime identity and artifact digests.
4. Update design/status/test-plan/claim/debt documents and converge the
   implementation against every FR, SC, and acceptance scenario.

## Complexity Tracking

No constitution violations or justified exceptions.
