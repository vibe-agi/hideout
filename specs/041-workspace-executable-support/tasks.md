# Tasks: Workspace Executable Support

<!-- markdownlint-disable MD013 -->

**Input**: Design documents from `/specs/041-workspace-executable-support/`

**Prerequisites**: `plan.md`, `spec.md`, `research.md`, `data-model.md`, `contracts/`, `quickstart.md`

**Tests**: Required because this feature changes filesystem transport behavior and promotes a real-backend product claim.

**Organization**: Tasks are grouped by user story and retain a separate evidence/closure phase so every claim is independently judged.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel because it uses different files and has no unmet dependency.
- **[Story]**: Maps the task to a user story in `spec.md`.
- Every implementation task names its concrete file or gate.

## Phase 1: Setup And Diagnosis

**Purpose**: Freeze the observed failure, scope, and current probe prerequisites.

- [X] T001 Confirm the shared-Portal promotion boundary and static/dedicated virtiofs non-claim across `specs/041-workspace-executable-support/spec.md`, `plan.md`, and `research.md`
- [X] T002 Record the pre-fix `OPEN {EXEC,0x20000}` / `EOPNOTSUPP` trace and the post-fix hypothesis in `specs/041-workspace-executable-support/adversarial-report.md`
- [X] T003 [P] Repair current environment/provider/admission construction in `cmd/hideout-workspace-probe/portal.go` and cover it with `go test ./cmd/hideout-workspace-probe ./internal/workspaceattach`

---

## Phase 2: Foundational Contracts And Evidence Judges

**Purpose**: Establish the fail-closed local flag contract and proof registry before promoting behavior.

**Checkpoint**: Unknown flags still fail closed, the execution hint grants no wire authority, and false-green evidence cannot satisfy 041.

- [X] T004 Add an OS-neutral encoder seam and tests that prove an allowed local-only hint leaves read-only wire semantics unchanged and an unallowed bit returns `ENOTSUP` in `internal/workspaceattach/portal_openflags.go` and `portal_openflags_test.go`
- [X] T005 [P] Bind the Linux allowlist to go-fuse `FMODE_EXEC` and add Linux/arm64 compile-contract coverage in `internal/workspaceattach/portal_openflags_linux.go` and `portal_openflags_linux_test.go`
- [X] T006 Define 041 proof IDs and required proof aggregation in `internal/productevidence/workspace_executable.go` and `internal/productevidence/aggregate.go`
- [X] T007 Register Gate 0, strict real Gate 2, supporting not-run, and docs claim-boundary requirements in `internal/productevidence/registry.go`
- [X] T008 Implement the strict workspace-execution artifact validator and dispatch in `internal/productevidence/workspace_executable.go` and `internal/productevidence/concurrent_sessions.go`
- [X] T009 Add registry coverage and false-green evidence mutations for dirty identity, mechanism drift, missing/false checks, undersampling, slow p95, overclaim, and unknown fields in `internal/productevidence/workspace_executable_test.go`

---

## Phase 3: User Story 1 - Run Project-Local Tools (Priority: P1) MVP

**Goal**: Directly execute a script, Linux arm64 binary, and workspace-local launcher through the promoted shared Workspace Portal.

**Independent Test**: Run `scripts/test-workspace-portal-lima.sh <new-absolute-dir>` and confirm `exec-script` and `exec-binary`; then run the feature Gate 2 and confirm 30/30 launcher samples.

### Tests For User Story 1

- [X] T010 [US1] Extend `scripts/test-workspace-portal-lima.sh` with executable script/binary fixtures and assertions that fail before the `FMODE_EXEC` allowlist change
- [X] T011 [US1] Add a workspace-local relative launcher fixture and direct execution assertions to `scripts/test-workspace-executable-lima-e2e.sh`

### Implementation For User Story 1

- [X] T012 [US1] Accept and strip `FMODE_EXEC` in `internal/workspaceattach/portal_openflags_linux.go` without adding a Portal protocol flag
- [X] T013 [US1] Produce redacted focused Portal correctness evidence containing direct script and binary operations in `scripts/test-workspace-portal-lima.sh`
- [X] T014 [US1] Implement the clean-candidate 041 product run and 30-sample launcher lane in `scripts/test-workspace-executable-lima-e2e.sh`

**Checkpoint**: The shared Portal path runs all three executable forms without guest-local copies.

---

## Phase 4: User Story 2 - Preserve Workspace And Session Boundaries (Priority: P1)

**Goal**: Execution retains exact workspace, attachment, session, and no-host-fallback boundaries.

**Independent Test**: The feature-specific shared-Portal Gate 2 directly executes each workspace's helper while preserving exact-root negatives; the aggregate static-virtiofs Gate 2 remains green without contributing to the 041 proof.

### Tests For User Story 2

- [X] T015 [US2] Add exact-root, escaping-link, and no-host-fallback checks to `scripts/test-workspace-executable-lima-e2e.sh`
- [X] T016 [US2] Add 100 repeated executions across two disjoint workspaces and reject any marker substitution in `scripts/test-workspace-executable-lima-e2e.sh`

### Implementation For User Story 2

- [X] T017 [US2] Keep the `/tmp/hideout-gate-fsread` copy in the legacy static-virtiofs HostFS lane and document why it cannot satisfy 041 in `scripts/test-gate2-lima.sh`
- [X] T018 [US2] Keep the Python live-lane static-virtiofs helper copy explicit while requiring direct execution only in the feature-specific shared-Portal gate in `scripts/test-gate2-lima.sh`
- [X] T019 [US2] Bind real evidence to `lima`, `darwin/arm64`, Linux `aarch64`, `workspace-portal`, clean commit, package, runtime, and no-copy/no-fallback checks in `scripts/test-workspace-executable-lima-e2e.sh`

**Checkpoint**: Direct execution changes no workspace, HostFS, lifecycle, or environment authority.

---

## Phase 5: User Story 3 - Receive Actionable Compatibility Failures (Priority: P2)

**Goal**: Preserve ordinary Linux execution failures and clearly bound unsupported mechanisms.

**Independent Test**: Non-executable, missing-interpreter, and incompatible-format fixtures fail without `EOPNOTSUPP` or fallback; docs and evidence mark static/dedicated virtiofs `not-claimed`.

### Tests And Implementation For User Story 3

- [X] T020 [US3] Add permission, missing-interpreter, and incompatible-format negative fixtures to `scripts/test-workspace-executable-lima-e2e.sh`
- [X] T021 [US3] Assert the negative fixtures do not report Portal `EOPNOTSUPP`, do not execute a host fallback, and leave no guest-local copy in `scripts/test-workspace-executable-lima-e2e.sh`
- [X] T022 [US3] Encode and validate the `staticVirtiofs: not-claimed` evidence boundary in `internal/productevidence/workspace_executable.go` and its tests
- [X] T023 [US3] Narrow the existing debt entry to static/dedicated virtiofs and add operator recovery scope in `docs/DEBT.md` and `docs/claim-boundaries.md`

**Checkpoint**: Promoted failures are truthful guest failures and unsupported modes are not overclaimed.

---

## Phase 6: User Story 4 - Keep Normal Workspace Semantics (Priority: P2)

**Goal**: Executed tools operate on the selected host checkout, including later-session visibility, without a divergent copy.

**Independent Test**: A workspace script creates/updates a host-observed file; a later run reads the updated value; focused Portal cache-invalidation checks remain green.

### Tests And Implementation For User Story 4

- [X] T024 [US4] Add executed-tool checkout write and later-session visibility checks to `scripts/test-workspace-executable-lima-e2e.sh`
- [X] T025 [US4] Preserve host-to-guest invalidation and ordinary write/rename/mode/truncate checks alongside executable operations in `scripts/test-workspace-portal-lima.sh`
- [ ] T026 [US4] Capture 30 warm first-output samples, enforce p95 at most 2 seconds and median regression at most 10%, and retain raw samples in `scripts/test-workspace-executable-lima-e2e.sh`

**Checkpoint**: The host checkout remains the single source of truth and performance stays within the existing warm-run objective.

---

## Phase 7: Documentation, Adversarial Proof, And Closure

**Purpose**: Promote only evidence-backed support and close every governing artifact.

- [X] T027 [P] Document the shared-Portal execution design and unchanged authority in `docs/privacy-run-design.md` and `docs/threat-model.md`
- [X] T028 [P] Add Gate 0, focused Portal, feature Gate 2, mutation, negative-fixture, and integrated regression requirements to `docs/privacy-run-test-plan.md`
- [X] T029 [P] Add the 041 support row and exact non-claims to `docs/STATUS.md` and `docs/claim-boundaries.md`
- [ ] T030 Record the flag-removal mutation, evidence false-green fixture, exact commands, and retained artifact identities in `specs/041-workspace-executable-support/adversarial-report.md`
- [X] T031 Add the 041 local mechanics lane and proof assertions to `scripts/test-gate0.sh`, then run targeted tests, Linux arm64 cross-build, and full Gate 0
- [ ] T032 Run the focused real Portal probe, feature-specific clean 041 Gate 2, and integrated Lima Gate 2; retain exact-commit artifacts and hashes
- [ ] T033 Run markdown/doc truth checks, review every FR/SC/acceptance scenario against code and evidence, and mark all completed tasks in `specs/041-workspace-executable-support/tasks.md`

---

## Dependencies & Execution Order

### Phase Dependencies

- Phase 1 establishes the observed defect and scope.
- Phase 2 depends on Phase 1 and blocks product promotion.
- User Story 1 depends on Phase 2 and is the MVP.
- User Story 2 depends on User Story 1's direct-execution behavior.
- User Stories 3 and 4 depend on User Story 1 and can otherwise proceed independently.
- Phase 7 depends on all selected stories and evidence producers.

### User Story Dependencies

```text
Setup/Diagnosis -> Foundational -> US1 -> US2
                                  |----> US3
                                  `----> US4
US2 + US3 + US4 -> Documentation/Evidence Closure
```

### Parallel Opportunities

- T003 and T002 use different files after T001.
- T005 can proceed beside T006-T009 after T004 defines the encoder seam.
- T015-T016 and T020-T021 can be authored independently after the product-gate shell exists.
- T027-T029 touch separate documentation files after the final claim boundary is stable.

## Parallel Example: User Story 3 And User Story 4

```text
Task: add permission/interpreter/format negative fixtures and no-fallback assertions
Task: add checkout-effect/later-session checks and performance sampling
```

## Implementation Strategy

### MVP First

1. Freeze the shared-Portal-only claim.
2. Prove the local-only execution hint contract.
3. Make direct script, binary, and launcher execution pass in a real Portal.
4. Stop and validate that no wire authority or fallback was added.

### Incremental Closure

1. Keep aggregate static-virtiofs copy controls explicit and outside the claim.
2. Add isolation, negative compatibility, checkout, and performance lanes.
3. Register strict product evidence and false-green rejection.
4. Run clean exact-commit gates, update docs, and promote only the proved scope.
