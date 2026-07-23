# Implementation Plan: Disposable Orphan Recovery

<!-- markdownlint-disable MD013 -->

**Branch**: `042-disposable-orphan-recovery` | **Date**: 2026-07-23 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/042-disposable-orphan-recovery/spec.md`

## Summary

Finish the `--rm` contract with one daemon-owned, resumable disposal protocol
used by both ordinary run finalization and restart recovery. A validated
`Disposable=true` record is converted into a strict lifecycle-journal disposal
intent before destructive work. The protocol serializes with attach/stop/clean,
rejects live or unprovable owners, binds the exact backend instance and record
identity, invokes only Manager-owned backend cleanup, requires two consecutive
exact-instance absence observations, and clears environment-scoped
gateway/runtime authority.

Metadata converges in a deliberate order: mark the environment
cleanup-required, persist the intent, prove backend absence, clear owned runtime,
remove the lifecycle journal/coordinator identity, and remove the environment
record last. A crash therefore leaves either a record plus classifiable intent,
a disposable record without a journal, or complete absence; the implementation
does not create record-absent journal residue. The daemon exposes status before
running bounded recovery workers and keeps ordinary non-disposable orphan
handling report-only and fail-closed.

## Technical Context

**Language/Version**: Go 1.25.0, POSIX shell for gates, TLA+ for protocol model checking

**Primary Dependencies**: standard-library `context`/`sync`/`crypto/sha256`, existing `internal/environment`, `internal/lifecycle`, `internal/session`, `internal/manager`, `internal/daemon`, backend lifecycle observation/cleanup, product-evidence registry

**Storage**: strict filesystem environment records and lifecycle journals; existing environment runtime/owner trees; optional disposal intent in lifecycle journal v1

**Testing**: Go unit/integration tests, `go test -race`, failure injection at every durable transition, 100+ seeded schedules, TLC model checking, Gate 0, and clean exact-package macOS arm64 Lima Gate 2

**Target Platform**: macOS arm64 with Lima for promoted behavior; native remains a weak deterministic mechanics harness

**Project Type**: Go CLI/daemon/Manager monorepo

**Performance Goals**: daemon status available within 10 seconds; responsive-backend recovery reaches removed or cleanup-required within 60 seconds per candidate; unrelated environments continue concurrently

**Constraints**: automatic destruction only from validated disposable authorization; no generic orphan sweeping; exact instance and stable absence required; record removed last; recovery bounded/cancellable; no new CLI/config/manifest surface; deterministic redaction

**Scale/Scope**: one disposal state machine per disposable environment, up to four concurrent startup recovery workers, 100+ local crash schedules, 30 ordinary real `--rm` runs, and real crash checkpoints plus `--rm --ephemeral`

## Constitution Check

*GATE: Passed before Phase 0 research and re-checked after Phase 1 design.*

- **Privacy Boundary**: The feature touches destructive environment lifecycle,
  backend instance deletion, session ownership, gateway/runtime cleanup, and
  lifecycle metadata. Missing authorization, live/unprovable ownership,
  identity drift, unknown observation, unstable absence, persistence failure,
  or cancellation blocks final removal and retains classifiable recovery state.
- **Typed Authority**: Manager Core remains the only owner of backend cleanup,
  environment mutation, gateway closure, and record removal. The daemon-owned
  lifecycle coordinator validates and persists the disposal protocol and
  serializes it with existing lifecycle operations; it receives no backend
  handle and executes no authority itself. No JavaScript or configuration
  participates.
- **Workspace And Policy**: Workspace, HostFS, environment variables, profile
  state, proxy secrets, deny precedence, and grants are unchanged. Cleanup
  removes only runtime state already owned by the exact disposable environment.
- **Generality And Provider Scope**: The protocol is generic to an exact
  observable/cleanable backend. Lima supplies the promoted real path; Lima
  commands and test crash controls remain backend/gate details, not Core
  semantics.
- **Evidence And Redaction**: Lifecycle status/events and audit expose bounded
  disposal phases, outcome, attempt count, and reason code. Run results retain
  `removed`/`cleanup-required`; doctor and existing clean guidance remain the
  recovery surface. Paths, owner/session IDs, process details, command lines,
  credentials, target arguments, and workspace data are excluded.
- **Backend And Distribution**: No new helper, runtime image, bootstrap, or
  InitTask is introduced. Exact observation, typed cleanup, and stable absence
  are backend requirements. Native tests prove mechanics only.
- **Gates**: Gate 0 covers strict schema/digest validation, model invariants,
  every crash cut, seeded schedules, races, mutation proofs, negative evidence
  fixtures, docs truth, and the aggregate local suite. Real Gate 2 covers exact
  package/runtime identity, ordinary cleanup, forced daemon crash/restart,
  unknown/mismatch refusal, 30 residue-free runs, and `--rm --ephemeral`.
- **Status And Docs**: Update `docs/privacy-run-design.md`,
  `docs/threat-model.md`, `docs/privacy-run-test-plan.md`, `docs/STATUS.md`,
  `docs/claim-boundaries.md`, and narrow or retire the `--rm` phase-2 row in
  `docs/DEBT.md` only after real evidence passes.

### Post-Design Re-check

The design still passes all principles. Durable disposal intent is historical
coordination truth, not new cleanup authority: it can be created only while a
validated disposable record is locked and it binds that record digest and exact
backend instance. Flexible recovery judgment remains typed Go validation over a
closed proof set. Journal-first intent and record-last removal provide
crash-resumable convergence without a second ownership database. No Complexity
Tracking exception is required.

## Project Structure

### Documentation (this feature)

```text
specs/042-disposable-orphan-recovery/
├── spec.md
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── adversarial-report.md
├── checklists/
│   └── requirements.md
├── contracts/
│   ├── disposal-protocol.md
│   └── observability-and-evidence.md
└── tasks.md
```

### Source Code (repository root)

```text
formal/
├── DisposableRecovery.tla
└── DisposableRecovery.cfg

internal/
├── environment/
│   ├── disposable.go                 # canonical disposable identity digest
│   └── disposable_test.go
├── lifecycle/
│   ├── disposal.go                   # intent/request/protocol state machine
│   ├── coordinator.go                # serialization and journal/record ordering
│   ├── journal.go                    # strict optional disposal intent
│   ├── status.go                     # redacted disposal status
│   └── *_test.go                     # schema, crash, mutation, randomized tests
├── manager/
│   ├── disposable_recovery.go        # Manager-owned cleanup/proof transaction
│   ├── run_environment.go            # normal finalizer uses shared protocol
│   └── *_test.go                     # proof matrix and record/journal convergence
├── daemon/
│   ├── disposable_recovery.go        # bounded startup recovery workers
│   ├── daemon.go                     # recovery startup/shutdown integration
│   ├── lifecycle.go                  # lifecycle inventory/reconciliation handoff
│   └── *_test.go                     # restart, availability, non-disposable refusal
└── productevidence/
    ├── disposable_recovery.go        # strict 042 real evidence judge
    ├── aggregate.go
    ├── registry.go
    └── disposable_recovery_test.go

schemas/
└── lifecycle-journal.schema.json     # optional strict disposal intent

scripts/
├── test-disposable-recovery-smoke.sh
├── test-disposable-recovery-lima-e2e.sh
├── test-gate0.sh
└── test-gate2-lima.sh

docs/
├── STATUS.md
├── DEBT.md
├── claim-boundaries.md
├── privacy-run-design.md
├── privacy-run-test-plan.md
└── threat-model.md
```

**Structure Decision**: Extend the existing environment/lifecycle/Manager/
daemon authority chain. Disposal receives dedicated lifecycle and Manager files
so proof ordering is reviewable. The existing journal remains the single
durable lifecycle database, the existing environment transition lock remains
the cross-process mutation boundary, and the existing CLI is unchanged.

## Implementation Phases

### Phase 0: Protocol Research

1. Freeze disposable authorization and identity-digest inputs.
2. Define the durable intent states and record-last crash matrix.
3. Map owner/backend/runtime proofs to typed failure reasons.
4. Define startup scheduling, status availability, retry, and shutdown behavior.
5. Define product evidence, mutations, negative fixtures, and real crash gates.

### Phase 1: Contracts And Data Model

1. Add the optional strict journal intent and validation contract.
2. Define Manager/coordinator request, proof, and outcome types without backend
   handles or credentials in durable state.
3. Define normal-finalizer and restart-recovery entry contracts over the same
   protocol.
4. Define redacted lifecycle status/events and strict product evidence.
5. Model all durable cut points and unauthorized-destruction invariants.

### Phase 2: Test-First Implementation

1. Add red tests for authorization, digest/instance mismatch, owner
   live/unprovable, unknown/stable-absence failure, every durable cut, and
   metadata cleanup failures.
2. Implement environment identity digest and journal intent validation.
3. Implement coordinator disposal serialization and crash-resumable metadata
   ordering.
4. Implement Manager-owned cleanup/proof transaction and route ordinary
   finalization through it.
5. Implement bounded daemon startup recovery and redacted status/audit.
6. Register strict product evidence and integrate Gate 0.

### Phase 3: Evidence And Closure

1. Run races, 100+ seeded crash schedules, TLC, targeted tests, and full Gate 0.
2. Record mutation-red outcomes and evidence-judge negative fixtures in the
   adversarial report.
3. Run clean exact-package real Lima ordinary/crash/ephemeral gates and retain
   source/runtime/artifact hashes.
4. Update design/status/test-plan/claim/debt documents, then converge and
   analyze every FR, SC, and task.

## Complexity Tracking

No constitution violations or justified exceptions.
