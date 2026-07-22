# Tasks: Lifecycle Attach Reservation

<!-- markdownlint-disable MD013 -->

**Input**: Design documents from `/specs/040-lifecycle-attach-reservation/`

**Prerequisites**: `plan.md`, `spec.md`, `research.md`, `data-model.md`, `contracts/`

**Tests**: Required. This feature changes daemon lifecycle, environment-runtime publication, durable ownership, reconciliation, cleanup, status/events, and release evidence. Tests must be observed red before implementation and mutation/negative evidence must be retained.

**Organization**: Tasks are grouped by user story and ordered test-first. Each task names its primary requirement/acceptance coverage and exact repository paths.

## Phase 1: Setup And Evidence Skeleton

**Purpose**: Establish traceable proof surfaces before changing lifecycle authority.

- [X] T001 Create the 040 adversarial-report structure with fresh-eyes, mutation-proof, negative-fixture, randomized-schedule, and real-gate sections in `specs/040-lifecycle-attach-reservation/adversarial-report.md` (Constitution IV; FR-015)
- [X] T002 [P] Register 040 local-model, local-mechanics, real-lifecycle, and real-performance proof requirements and refusal semantics in `internal/productevidence/registry.go` and its registry tests (SC-001, SC-002, SC-005, SC-006)
- [X] T003 [P] Add 040 Gate 0 and Gate 2 evidence acceptance/negative fixture scaffolding to `scripts/test-lifecycle-smoke.sh` and `scripts/test-lifecycle-lima-e2e.sh` without claiming a passing implementation (FR-015; SC-006)

---

## Phase 2: Foundational Session And Protocol Boundaries

**Purpose**: Create the side-effect-free identity and typed reservation seams required by every story.

**CRITICAL**: No user-story implementation begins until this phase passes its red-to-green tests.

- [X] T004 [P] Add failing tests proving session layout allocation creates no filesystem state and explicit preparation preserves current layout/cleanup behavior in `internal/session/session_test.go` (FR-001, FR-007)
- [X] T005 Implement side-effect-free `Allocate` plus explicit idempotence/fail-closed layout preparation while retaining `New` compatibility in `internal/session/session.go` (FR-001, FR-007, FR-014)
- [X] T006 [P] Add compile-time/failing contract tests for reservation identity validation, single-use prepare/promote/abort, and absence of backend handles/credentials in `internal/lifecycle/establishment_test.go` (FR-001, FR-005, FR-006, FR-014)
- [X] T007 Define `EstablishmentRequest`, `EstablishmentReservation`, and the extended narrow `Registrar` contract in `internal/lifecycle/registry.go`, and create the reservation object/state skeleton in `internal/lifecycle/establishment.go` (FR-001, FR-005, FR-006)
- [X] T008 Extend `registryEnvironment` initialization and lifecycle status derivation for in-memory reservations without changing the durable journal schema in `internal/lifecycle/registry.go`, `internal/lifecycle/status.go`, and status schema tests (FR-009, FR-013, FR-014)

**Checkpoint**: An opaque session ID can exist without runtime state, and the coordinator exposes a typed but not-yet-authoritative reservation boundary.

---

## Phase 3: User Story 1 - Start Reliably During Reconciliation (Priority: P1) MVP

**Goal**: A run waits for older reconciliation without holding the transition lock, then publishes runtime only while protected and promotes only after durable ownership.

**Independent Test**: Force reconciliation-first and reservation-first schedules and observe either a durable normal registration or failure before runtime/target publication, with no scrubbed live runtime and no indefinite wait.

### Tests For User Story 1

- [X] T009 [US1] Add deterministic failing barrier tests for reconciliation-first wait, reservation-first exclusion, cancelled wait, unknown observation, and atomic reservation-to-registration visibility in `internal/lifecycle/establishment_test.go` (US1/AC1-3; FR-002, FR-003, FR-006, FR-008, FR-010; SC-001)
- [X] T010 [US1] Add failing Manager ordering tests proving no reconciliation wait occurs under the transition lock, no environment runtime precedes reservation/preparation, owner durability precedes promotion, and early failure launches no target in `internal/manager/run_attach_establishment_test.go` (US1/AC1-3; FR-001, FR-004, FR-005; SC-001)
- [X] T011 [P] [US1] Add failing shared-workspace tests proving planning consumes the reservation-proved exact incarnation without an early lifecycle registration in `internal/manager/run_workspace_lifecycle_test.go` (FR-004, FR-005, FR-012)

### Implementation For User Story 1

- [X] T012 [US1] Implement cancellable reservation admission that waits for an existing reconciliation with no coordinator/transition lock held, cancels idle grace safely, and inserts only caller-owned in-memory state in `internal/lifecycle/establishment.go` (FR-001, FR-002, FR-008, FR-010)
- [X] T013 [US1] Implement reservation preparation from a fresh validated `AttachRequest` and refactor registration creation so promotion atomically replaces the reservation with the existing planned session graph in `internal/lifecycle/establishment.go` and `internal/lifecycle/registry.go` (FR-004, FR-005, FR-006, FR-010)
- [X] T014 [US1] Add an incarnation-based shared-workspace planning seam while preserving existing registration-based callers and exact-root validation in `internal/manager/run_workspace.go` (FR-004, FR-012, FR-014)
- [X] T015 [US1] Split Manager run-session allocation from materialization and preserve explain/inactive compatibility in `internal/manager/run_session.go` and its tests (FR-001, FR-007, FR-012, FR-014)
- [X] T016 [US1] Reorder lifecycle-managed reusable Lima establishment in `internal/manager/run_apply.go` to allocate, reserve, lock, reload/revalidate, observe/prepare, materialize, plan workspace, acquire owner, install cleanup, promote, and continue existing provider/target setup (FR-001 through FR-008, FR-012, FR-014)
- [X] T017 [US1] Update Manager lifecycle test registrars and daemon wiring to require the reservation contract with no executable fallback in `internal/manager/*_test.go`, `internal/daemon/lifecycle.go`, and `internal/daemon/*_test.go` (FR-010, FR-014)

**Checkpoint**: User Story 1 independently passes forced reconciliation-first/run-first tests and Manager never exposes an ownerless runtime outside a reservation.

---

## Phase 4: User Story 2 - Order Runs And Destructive Mutation Safely (Priority: P2)

**Goal**: Reservations exclude reconcile/stop/clean/forget while allowing distinct compatible run establishments with bounded outcomes.

**Independent Test**: Force mutation-first, reservation-first, idle-expiry, explicit-stop, forget, and two-run schedules; destructive work never overlaps establishment and siblings retain distinct authority.

### Tests For User Story 2

- [X] T018 [US2] Add failing coordinator tests for reservation blockers across `BeginReconciliation`, direct `Reconcile`, idle expiry, `StopExplicit`, `ForgetEnvironment`, and `RunDestructiveMutation` in `internal/lifecycle/establishment_test.go` and `internal/lifecycle/reconciliation_fence_test.go` (US2/AC1-2; FR-003, FR-008, FR-010)
- [X] T019 [US2] Add failing concurrent-reservation and sibling-isolation tests proving distinct sessions can prepare/promote/abort independently in `internal/lifecycle/establishment_test.go` and `internal/manager/concurrent_run_test.go` (US2/AC3; FR-007, FR-011, FR-012)
- [X] T020 [US2] Add a deterministic seeded replay of at least 1,000 reconciliation/reservation/mutation/cancellation schedules with deadlock timeout and invariant checks in `internal/lifecycle/randomized_replay_test.go` (FR-003, FR-006, FR-007, FR-008, FR-011; SC-002)

### Implementation For User Story 2

- [X] T021 [US2] Make reconciliation admission/direct reconciliation, stop, forget, idle expiry, and destructive mutation fail closed before side effects whenever reservations exist in `internal/lifecycle/reconciliation_fence.go`, `internal/lifecycle/reconcile.go`, and `internal/lifecycle/coordinator.go` (FR-003, FR-008, FR-010)
- [X] T022 [US2] Complete multi-reservation bookkeeping and session-scoped idempotent abort/promotion so compatible runs overlap only outside the existing transition-lock boundary in `internal/lifecycle/establishment.go` and `internal/lifecycle/registry.go` (FR-006, FR-007, FR-011)
- [X] T023 [US2] Preserve all existing final-session stop, grace, workspace, network, HostFS, projection, audit, and cleanup paths and add regression assertions where ordering changed in `internal/lifecycle/*_test.go` and `internal/manager/*_test.go` (FR-012)

**Checkpoint**: User Stories 1 and 2 pass independently, including 1,000 seeded schedules and race-detector coverage with no cross-session cleanup or lock cycle.

---

## Phase 5: User Story 3 - Recover From Cancellation Or Restart (Priority: P3)

**Goal**: Cancellation removes only caller-owned provisional state, while daemon restart discards reservations and classifies residue from durable owner/backend facts without silent adoption.

**Independent Test**: Cancel at every establishment boundary and restart before/after owner durability; only the interrupted session is cleaned, siblings survive, and each restart yields proved cleanup, preserved owner, or a stable blocker.

### Tests For User Story 3

- [X] T024 [US3] Add failing cancellation tests at allocation, reconcile wait, reservation, preparation, runtime publication, owner durability, and promotion boundaries with bounded cleanup and sibling snapshots in `internal/manager/run_attach_establishment_test.go` (US3/AC1; FR-007, FR-008; SC-003)
- [X] T025 [P] [US3] Add failing replacement-coordinator tests for crash before owner, crash after owner, ambiguous cleanup, and prohibition on reservation re-adoption in `internal/lifecycle/reconcile_test.go` and `internal/daemon/lifecycle_sessions_test.go` (US3/AC2-4; FR-009, FR-010; SC-004)
- [X] T026 [P] [US3] Add failing status/event/doctor redaction tests that inject paths, lock names, credentials, PIDs, and raw arguments in `internal/lifecycle/status_schema_test.go`, `internal/daemon/lifecycle_test.go`, and doctor smoke fixtures (US3/AC4; FR-013; SC-007)

### Implementation For User Story 3

- [X] T027 [US3] Complete Manager error defers so every pre/post-owner failure closes only its owner/runtime/global session state and aborts only its reservation within bounded cleanup contexts in `internal/manager/run_apply.go` (FR-007, FR-008; SC-003)
- [X] T028 [US3] Ensure coordinator close/restart drops all in-memory reservations and replacement reconciliation consumes only owner/backend/provider facts in `internal/lifecycle/coordinator.go`, `internal/lifecycle/establishment.go`, and `internal/daemon/lifecycle.go` (FR-009, FR-010; SC-004)
- [X] T029 [US3] Implement redacted establishing status/activity, stable event kinds/reason codes, and existing UI/doctor propagation without new routes or actions in `internal/lifecycle/status.go`, `internal/lifecycle/coordinator.go`, `internal/liveconsole/`, and affected tests (FR-010, FR-013, FR-014; SC-007)

**Checkpoint**: All three stories pass cancellation and replacement-daemon recovery tests with zero leaked control material.

---

## Phase 6: Gates, Mutation Proofs, Documentation, And Completion

**Purpose**: Prove the implementation, record adversarial evidence, and align authoritative docs.

- [X] T030 Run TLC for `formal/AttachReservation.tla`, add/retain a negative configuration or documented temporary mutation that reproduces the scrub race, and record results in `specs/040-lifecycle-attach-reservation/adversarial-report.md` (FR-015; SC-001, SC-004)
- [X] T031 Run targeted Go tests and `go test -race` while temporarily breaking each new concurrency assertion/judge, restore the implementation, and record exact red/green commands and negative fixtures in `specs/040-lifecycle-attach-reservation/adversarial-report.md` (Constitution IV; FR-015)
- [X] T032 Complete the 040 Gate 0 proof entries, artifact digests, randomized-schedule count, refusal fixtures, and aggregate wiring in `scripts/test-lifecycle-smoke.sh`, `internal/productevidence/registry.go`, and `scripts/test-gate0.sh` (SC-001, SC-002, SC-003, SC-004, SC-007)
- [X] T033 Extend and run the real macOS arm64 Lima lifecycle topology for reconciliation-first, reservation-first, cancellation, restart before/after owner, exact source/runtime provenance, and 30 warm samples in `scripts/test-lifecycle-lima-e2e.sh`, retaining evidence under `.hideout-release-evidence/040-attach-reservation-real-gate2` (SC-005, SC-006)
- [X] T034 Update lifecycle ordering, recovery/non-claims, gate commands/evidence refusal, proof ownership, implementation status, and resolved debt in `docs/privacy-run-design.md`, `docs/threat-model.md`, `docs/privacy-run-test-plan.md`, `docs/claim-boundaries.md`, `docs/STATUS.md`, and `docs/DEBT.md` (FR-012, FR-013, FR-015)
- [X] T035 Validate every command and acceptance statement in `specs/040-lifecycle-attach-reservation/quickstart.md`, then run `scripts/test-gate0.sh`, `scripts/test-doc-truth-smoke.sh`, `git diff --check`, and all feature Markdown lint checks (all FRs and SCs)
- [X] T036 Mark the spec implemented only after every task/evidence requirement passes and finalize requirement-by-requirement findings plus deferred-work audit in `specs/040-lifecycle-attach-reservation/spec.md` and `specs/040-lifecycle-attach-reservation/adversarial-report.md` (all FRs and SCs; Constitution Development Workflow)

---

## Dependencies And Execution Order

### Phase Dependencies

- **Phase 1**: Starts immediately and creates traceable evidence surfaces.
- **Phase 2**: Depends on Phase 1; blocks all user stories.
- **Phase 3 / US1**: Depends on Phase 2 and is the MVP safety fix.
- **Phase 4 / US2**: Depends on the reservation/promotion mechanics from US1.
- **Phase 5 / US3**: Depends on US1 cleanup ordering and US2 multi-reservation isolation.
- **Phase 6**: Depends on all stories; completion requires both local and real evidence or an explicit honest non-promotion result.

### User Story Dependencies

- **US1**: Independently proves the original reconciliation/run race is closed.
- **US2**: Reuses US1 reservation state to order destructive operations and concurrent runs.
- **US3**: Reuses US1/US2 ownership boundaries to prove cancellation and restart outcomes.

### Within Each User Story

1. Add deterministic positive and fail-closed tests and observe them fail.
2. Implement the smallest protocol/state change that makes them pass.
3. Run the story's targeted tests before moving to the next story.
4. Preserve the existing lifecycle and Manager regression suite at every checkpoint.

### Parallel Opportunities

- T002 and T003 touch independent evidence layers.
- T004 and T006 are independent red-test additions before their sequential implementations.
- T011 can be written alongside T009/T010 because it targets workspace planning.
- T025 and T026 target separate recovery and evidence surfaces.
- Documentation edits in T034 may be prepared by file after code/evidence semantics are final, but authoritative status/debt wording is finalized last.

## Parallel Example: User Story 1

```text
Task T009: lifecycle forced-interleaving tests
Task T010: Manager ordering tests
Task T011: shared-workspace incarnation-planning tests
```

The implementation tasks remain sequential because T012/T013 define the
coordinator boundary consumed by T014-T017.

## Implementation Strategy

### MVP First

1. Complete setup and foundational phases.
2. Complete US1 through T017.
3. Run the forced reconciliation-first/run-first tests and targeted race tests.
4. Do not claim full 040 completion until US2, US3, evidence, mutation proofs,
   real backend checks, and documentation are also complete.

### Full Delivery

1. Land the coordinator/session boundary with red-to-green tests.
2. Reorder Manager and validate the P1 race closure.
3. Add all conflict, concurrency, cancellation, and restart coverage.
4. Produce local model/mechanics evidence and mutation proofs.
5. Produce real Lima lifecycle/performance evidence.
6. Converge against every requirement, run cross-artifact analysis, and update
   status/debt only from observed results.

## Notes

- `[P]` means different primary files and no unmet dependency; it does not authorize uncoordinated edits to shared state-machine files.
- No task authorizes a new CLI/config/manifest field or a native-to-real evidence substitution.
- The ignored evidence directory is not product state; retained artifact identity and digests are documented without committing private/local evidence.
- Any intentionally deferred work must remain in `docs/DEBT.md` with a trigger before T036 can complete.
