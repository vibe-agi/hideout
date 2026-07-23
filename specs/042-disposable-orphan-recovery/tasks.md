# Tasks: Disposable Orphan Recovery

<!-- markdownlint-disable MD013 -->

**Input**: Design documents from `/specs/042-disposable-orphan-recovery/`

**Prerequisites**: `plan.md`, `spec.md`, `research.md`, `data-model.md`, `contracts/`, `quickstart.md`

**Tests**: Required. This feature changes destructive environment lifecycle authority and must include positive, fail-closed, mutation, model, race, and real-backend evidence.

**Organization**: Tasks are grouped by user story so each recovery behavior has an independently testable checkpoint.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel because it touches different files and has no dependency on an incomplete task
- **[Story]**: Maps the task to a user story from `spec.md`
- Every task names the concrete repository path it changes or validates

## Phase 1: Setup And Baseline

**Purpose**: Freeze the measured residue and exact authority boundary before changing cleanup.

- [X] T001 Record the current normal-return journal residue, report-only crash orphan behavior, and rejected authorization shortcuts in `specs/042-disposable-orphan-recovery/adversarial-report.md`
- [X] T002 Confirm the journal-first intent/record-last ordering and historical-residue non-claim across `specs/042-disposable-orphan-recovery/spec.md`, `research.md`, `data-model.md`, and `contracts/disposal-protocol.md`

---

## Phase 2: Foundational Identity, Journal, And Evidence Contracts

**Purpose**: Establish strict identity and evidence judges before any new destructive path.

**Checkpoint**: Intent cannot be forged from a name/status, unknown journal fields fail, and false-green 042 evidence is rejected.

- [X] T003 [P] Add failing canonical disposable-identity digest and mutation tests in `internal/environment/disposable_test.go`
- [X] T004 [P] Add failing strict disposal-intent journal/schema/state-transition tests in `internal/lifecycle/disposal_test.go`, `internal/lifecycle/journal_test.go`, and `schemas/lifecycle-journal.schema.json`
- [X] T005 [P] Add 042 proof registry coverage and false-green artifact fixtures for dirty identity, missing checks, unauthorized destruction, residue, undersampling, timeout, and unknown fields in `internal/productevidence/disposable_recovery_test.go`
- [X] T006 Implement the versioned canonical disposable identity projection/digest in `internal/environment/disposable.go`
- [X] T007 Implement strict optional disposal intent validation and JSON schema support in `internal/lifecycle/disposal.go`, `internal/lifecycle/journal.go`, and `schemas/lifecycle-journal.schema.json`
- [X] T008 Implement 042 proof IDs, strict real artifact validator, aggregate requirements, and registry entries in `internal/productevidence/disposable_recovery.go`, `aggregate.go`, and `registry.go`

---

## Phase 3: User Story 1 - Recover A Disposable Run After A Crash (Priority: P1) MVP

**Goal**: A stranded, authorized disposable environment is removed after restart only after exact proof.

**Independent Test**: Seed a validated disposable record/journal at each crash phase, start a daemon with a controlled backend, and observe exact removal or a durable retryable state.

### Tests For User Story 1

- [X] T009 [P] [US1] Add failing coordinator tests for intent creation, resume, serialization, cancellation, and every durable crash cut in `internal/lifecycle/disposal_test.go`
- [X] T010 [P] [US1] Add failing Manager tests for stale-owner cleanup, typed backend deletion, two-sample exact absence, gateway/runtime cleanup, and retained failure state in `internal/manager/disposable_recovery_test.go`
- [X] T011 [P] [US1] Add failing daemon restart tests for bounded worker recovery, early status availability, shutdown interruption, and multiple candidates in `internal/daemon/disposable_recovery_test.go`

### Implementation For User Story 1

- [X] T012 [US1] Implement the coordinator-owned disposal admission/resume protocol and mutation exclusion in `internal/lifecycle/disposal.go` and `internal/lifecycle/coordinator.go`
- [X] T013 [US1] Implement the Manager-owned recovery proof and cleanup transaction in `internal/manager/disposable_recovery.go`
- [X] T014 [US1] Implement bounded daemon startup recovery workers and lifecycle reconciliation handoff in `internal/daemon/disposable_recovery.go`, `internal/daemon/daemon.go`, and `internal/daemon/lifecycle.go`
- [X] T015 [US1] Record a mutation that disables durable intent or stable absence and prove the US1 tests fail, then restore it and document the result in `specs/042-disposable-orphan-recovery/adversarial-report.md`

**Checkpoint**: Authorized crash residue recovers; interrupted work remains resumable and daemon control surfaces stay available.

---

## Phase 4: User Story 2 - Refuse Ambiguous Or Unauthorized Cleanup (Priority: P1)

**Goal**: Automatic recovery makes zero destructive calls for every unauthorized or unproved candidate.

**Independent Test**: Independently remove or corrupt each proof and verify the exact stable blocker, retained state, redacted evidence, and zero backend cleanup calls.

### Tests For User Story 2

- [X] T016 [P] [US2] Add the non-disposable/name-only/status-only/live-owner/unprovable-owner/unknown/mismatch negative matrix in `internal/manager/disposable_recovery_test.go`
- [X] T017 [P] [US2] Add missing-record valid-intent and historical journal-only refusal tests in `internal/daemon/disposable_recovery_test.go`
- [X] T018 [P] [US2] Add lifecycle status/event/audit redaction and closed reason-code tests in `internal/lifecycle/status_schema_test.go` and `internal/daemon/disposable_recovery_test.go`

### Implementation For User Story 2

- [X] T019 [US2] Enforce closed authorization, owner, identity, observation, and historical-residue judgments in `internal/manager/disposable_recovery.go` and `internal/daemon/disposable_recovery.go`
- [X] T020 [US2] Expose bounded disposal phase/outcome through lifecycle status and daemon audit/events in `internal/lifecycle/status.go`, `internal/daemon/lifecycle.go`, and `internal/daemon/audit.go`
- [X] T021 [US2] Add evidence-judge negative mutation results and zero-destructive-call identities to `specs/042-disposable-orphan-recovery/adversarial-report.md`

**Checkpoint**: The feature remains disposable-only; ordinary or ambiguous resources retain report/block/explicit-recovery semantics.

---

## Phase 5: User Story 3 - Keep Record And Journal State Convergent (Priority: P2)

**Goal**: Ordinary and recovered disposal use record-last ordering and never create unclassifiable journal-only residue.

**Independent Test**: Fail each metadata step, restart, and verify complete removal or a classifiable retained record/intent.

### Tests For User Story 3

- [X] T022 [P] [US3] Add failing ordinary-finalizer tests proving journal removal, record-last ordering, and cleanup-required recovery after each metadata failure in `internal/manager/run_disposable_test.go`
- [X] T023 [P] [US3] Add coordinator restart/randomized replay tests covering record+intent, record-only, valid intent-only, and legacy journal-only shapes in `internal/lifecycle/randomized_replay_test.go` and `internal/lifecycle/disposal_test.go`

### Implementation For User Story 3

- [X] T024 [US3] Route `finishConcurrentRunEnvironment` and `disposeFinishedEnvironment` through the shared lifecycle disposal protocol in `internal/manager/run_environment.go` and `internal/manager/disposable_recovery.go`
- [X] T025 [US3] Implement record-last journal/coordinator removal and classifiable retry behavior in `internal/lifecycle/disposal.go` and `internal/manager/disposable_recovery.go`
- [X] T026 [US3] Run at least 100 seeded crash/interleaving schedules and retain invariant counts in `internal/lifecycle/randomized_replay_test.go` and `specs/042-disposable-orphan-recovery/adversarial-report.md`

**Checkpoint**: Successful disposal leaves no environment or lifecycle identity; every interrupted shape is safe to retry or explicitly blocked.

---

## Phase 6: User Story 4 - Preserve `--rm` Run Semantics (Priority: P3)

**Goal**: Target results, cleanup disposition, and ephemeral identity remain orthogonal under the new protocol.

**Independent Test**: Run success/failure and `--rm --ephemeral` cases and verify exact target result plus full environment/identity cleanup.

### Tests And Implementation For User Story 4

- [X] T027 [P] [US4] Add local product-path tests for successful target, failed target, cleanup-required, and `--rm --ephemeral` in `internal/manager/run_disposable_test.go` and `internal/manager/run_service_test.go`
- [X] T028 [US4] Preserve result/disposition and session-local identity cleanup while using the shared protocol in `internal/manager/run_apply.go`, `run_environment.go`, and `run_session.go`
- [X] T029 [US4] Add the real `--rm --ephemeral` and target-failure lanes without weakening existing inventory assertions in `scripts/test-gate2-lima.sh`

**Checkpoint**: Recovery completes the existing `--rm` contract without redefining target, workspace, network, HostFS, or identity authority.

---

## Phase 7: Model, Product Gate, Documentation, And Closure

**Purpose**: Prove the destructive protocol, promote only exact real evidence, and close every governing artifact.

- [X] T030 [P] Model authorization, crash cuts, stable absence, record-last convergence, and blocked outcomes in `formal/DisposableRecovery.tla` and `formal/DisposableRecovery.cfg`
- [X] T031 [P] Add local protocol/model/evidence mechanics to `scripts/test-disposable-recovery-smoke.sh`, `scripts/test-formal-models.sh`, and `scripts/test-gate0.sh`
- [X] T032 Implement the strict clean exact-package real recovery producer with 30 ordinary runs and forced restart checkpoints in `scripts/test-disposable-recovery-lima-e2e.sh`
- [X] T033 [P] Document protocol authority, failure behavior, and non-claims in `docs/privacy-run-design.md` and `docs/threat-model.md`
- [X] T034 [P] Document Gate 0, mutations, real Gate 2, aggregate regression, and artifact requirements in `docs/privacy-run-test-plan.md`
- [ ] T035 [P] Update `docs/STATUS.md`, `docs/claim-boundaries.md`, and narrow the resolved `--rm` phase-2 entry in `docs/DEBT.md` only after clean real evidence passes
- [ ] T036 Record exact commands, mutation-red outputs, false-green fixtures, model results, real artifacts, hashes, and remaining non-claims in `specs/042-disposable-orphan-recovery/adversarial-report.md`
- [ ] T037 Run targeted tests, race tests, 100+ schedules, model checking, markdown/doc truth, full Gate 0, clean feature Gate 2, and aggregate Lima Gate 2; retain exact-commit artifacts and hashes
- [ ] T038 Review every FR/SC/acceptance scenario against code and evidence, run convergence/consistency analysis, append any missing work, and mark completed tasks in `specs/042-disposable-orphan-recovery/tasks.md`

---

## Dependencies & Execution Order

### Phase Dependencies

- Phase 1 freezes the measured defect and authorization boundary.
- Phase 2 defines strict identity/journal/evidence contracts and blocks all destructive implementation.
- User Story 1 depends on Phase 2 and is the MVP recovery path.
- User Story 2 hardens US1 judgments and must complete before product promotion.
- User Story 3 depends on the shared US1 protocol and integrates ordinary finalization.
- User Story 4 depends on US3 finalizer integration.
- Phase 7 depends on all selected stories.

### User Story Dependencies

```text
Setup -> Foundation -> US1 -> US2
                           `-> US3 -> US4
US1 + US2 + US3 + US4 -> Model/Evidence/Docs Closure
```

### Parallel Opportunities

- T003-T005 touch independent identity, lifecycle, and evidence test files.
- T009-T011 cover coordinator, Manager, and daemon layers independently before integration.
- T016-T018 cover authority, historical residue, and observability independently.
- T030-T031 and T033-T034 use separate model/script/doc files after protocol behavior stabilizes.
- Real evidence production is sequential with final clean-candidate freezing.

## Parallel Example: User Story 1

```text
Task: add coordinator crash-cut tests in internal/lifecycle/disposal_test.go
Task: add Manager proof/cleanup tests in internal/manager/disposable_recovery_test.go
Task: add daemon restart worker tests in internal/daemon/disposable_recovery_test.go
```

## Implementation Strategy

### MVP First

1. Freeze authorization and residue shapes.
2. Implement strict digest and journal intent.
3. Recover one authorized disposable environment after restart.
4. Verify every unknown or live-owner input blocks before backend cleanup.

### Incremental Closure

1. Route ordinary finalization through record-last convergence.
2. Preserve target failure and ephemeral identity semantics.
3. Add model, strict evidence, mutation proofs, and local aggregate gates.
4. Freeze a clean candidate, run real crash recovery and aggregate Lima gates,
   then update status/debt only for the proved scope.

## Notes

- Tests for destructive behavior are written and observed red before implementation.
- Lifecycle metadata coordinates but never executes backend authority.
- Historical untrusted journal-only residue remains an explicit non-claim.
- Commit after coherent protocol, integration, and promotion batches.
