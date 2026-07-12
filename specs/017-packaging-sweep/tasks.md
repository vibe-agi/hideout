# Tasks: Packaging Sweep

<!-- markdownlint-disable MD013 -->

**Input**: Design documents from `/specs/017-packaging-sweep/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

**Tests**: Required. This feature touches package lifecycle, filesystem
mutation, durable-state preservation, audit/evidence, and Gate 0 smoke.

**Organization**: Tasks are grouped by user story to enable independent
implementation and testing.

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Prepare package lifecycle fixtures and command inventory.

- [X] T001 [P] Add reusable package fixture helpers for package A/B manifests, install prefixes, durable store fixtures, and checksum mutation in internal/packagekit/packagekit_test.go
- [X] T002 [P] Add CLI fixture helpers for package command stdout/stderr assertions in internal/app/app_test.go
- [X] T003 [P] Add package smoke fixture scaffolding for obsolete files, repair, migration rejection, and external prerequisite reporting in scripts/test-package-smoke.sh

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Shared data model and validation used by every story.

**CRITICAL**: No user story work can begin until this phase is complete.

- [X] T004 Add ObsoletePackageFile, RepairPlan, RepairResult, MigrationDecision, and ExternalPrerequisiteStatus types in internal/packagekit/manifest.go or internal/packagekit/lifecycle.go
- [X] T005 Implement migration compatibility checker for installed-state schema and previous package schema before mutation in internal/packagekit/migration.go
- [X] T006 Implement old/new installed file-set comparison and prefix containment validation helpers in internal/packagekit/repair.go
- [X] T007 Extend package lifecycle audit event shape for stale counts, repair counts, durable action, and survivor evidence path in internal/packagekit/audit.go
- [X] T008 Update package command usage to include `hideout package repair --prefix <dir> [--dry-run]` in internal/app/app.go

**Checkpoint**: Foundation ready; user stories can now be implemented.

---

## Phase 3: User Story 1 - Upgrade Without Silent Package Leftovers (Priority: P1) MVP

**Goal**: Upgrade reports obsolete package-owned files, does not delete them,
and verification fails until explicit repair removes them.

**Independent Test**: Install package A, upgrade to package B that omits an old
file, verify the stale path is reported and still present, then repair and
verify clean state.

### Tests for User Story 1

- [X] T009 [P] [US1] Add failing unit test for upgrade reporting obsolete package-owned files without deleting them in internal/packagekit/packagekit_test.go
- [X] T010 [P] [US1] Add failing unit test for VerifyInstalled rejecting proven obsolete leftovers with stale path and repair hint in internal/packagekit/packagekit_test.go
- [X] T011 [P] [US1] Add failing CLI test for `package repair --dry-run` and `package repair` output in internal/app/app_test.go

### Implementation for User Story 1

- [X] T012 [US1] Persist obsolete package-owned file metadata in installed state during upgrade in internal/packagekit/install.go and internal/packagekit/manifest.go
- [X] T013 [US1] Update VerifyInstalled to fail on present obsolete package-owned files and pass after repaired/missing stale files in internal/packagekit/verify.go
- [X] T014 [US1] Implement RepairObsoleteFiles dry-run/apply behavior with ownership and prefix revalidation in internal/packagekit/repair.go
- [X] T015 [US1] Add `hideout package repair` dispatch, flags, output, and error handling in internal/app/app.go
- [X] T016 [US1] Add repair lifecycle audit for dry-run/apply outcomes in internal/packagekit/audit.go

**Checkpoint**: US1 independently proves report-first stale-file behavior and
explicit repair.

---

## Phase 4: User Story 2 - Verify Helpers And External Prerequisites Honestly (Priority: P2)

**Goal**: Package verification names exact packaged artifact failures and
reports `tun2socks` as external prerequisite status, not package-owned checksum
coverage.

**Independent Test**: Corrupt or remove package-owned helpers/schemas, hide
`tun2socks`, and verify diagnostics classify each failure correctly.

### Tests for User Story 2

- [X] T017 [P] [US2] Add failing unit tests for missing, non-regular, mode-mismatched, and checksum-mismatched package artifacts naming expected/actual state in internal/packagekit/packagekit_test.go
- [X] T018 [P] [US2] Add failing unit tests for `tun2socks` external prerequisite status that never reports a package checksum failure in internal/packagekit/packagekit_test.go
- [X] T019 [P] [US2] Add failing doctor/package diagnostic test for packaged helper vs external prerequisite classification in internal/app/app_test.go

### Implementation for User Story 2

- [X] T020 [US2] Improve package artifact verification diagnostics with exact artifact, expected state, actual state, and reinstall/repair hint in internal/packagekit/verify.go
- [X] T021 [US2] Add external prerequisite discovery/reporting for `tun2socks` in internal/packagekit/prereq.go
- [X] T022 [US2] Surface packaged helper and external prerequisite status separately in `hideout package verify` output in internal/app/app.go
- [X] T023 [US2] Surface package prerequisite status in doctor feature diagnostics without claiming package checksum coverage in internal/app/app.go

**Checkpoint**: US2 independently proves helper/prerequisite diagnostics are
honest and artifact-specific.

---

## Phase 5: User Story 3 - Enforce Migration Range Before Mutation (Priority: P3)

**Goal**: Unsupported installed-state or previous package schema rejects
upgrade before any package-owned file is copied.

**Independent Test**: Prepare incompatible installed state and prove upgrade
fails with no file mutation and clear compatibility output.

### Tests for User Story 3

- [X] T024 [P] [US3] Add failing unit test for incompatible installed-state schema rejecting before file mutation in internal/packagekit/packagekit_test.go
- [X] T025 [P] [US3] Add failing unit test for incompatible previous package schema rejecting before file mutation in internal/packagekit/packagekit_test.go
- [X] T026 [P] [US3] Add failing CLI test for migration rejection diagnostic including current state, supported range, and guidance in internal/app/app_test.go

### Implementation for User Story 3

- [X] T027 [US3] Replace inline migration check in Install with MigrationDecision checker before copy loop in internal/packagekit/install.go
- [X] T028 [US3] Validate MinimumPackageSchema and MaximumPackageSchema fields during manifest load and upgrade compatibility in internal/packagekit/manifest.go and internal/packagekit/migration.go
- [X] T029 [US3] Ensure failed migration decisions emit no package mutation and no misleading passed audit in internal/packagekit/install.go

**Checkpoint**: US3 independently proves migration gates are effective before
mutation.

---

## Phase 6: User Story 4 - Preserve Clear Uninstall And Repair Evidence (Priority: P4)

**Goal**: Uninstall, purge, and repair leave clear lifecycle evidence while
preserving durable state unless purge is explicit.

**Independent Test**: Run uninstall preserve, uninstall purge, and repair
fixtures; inspect output and survivor audit evidence.

### Tests for User Story 4

- [X] T030 [P] [US4] Add failing unit tests for repair/uninstall lifecycle audit fields and redaction scan in internal/packagekit/packagekit_test.go
- [X] T031 [P] [US4] Add failing unit test proving purge survivor audit remains outside deleted store in internal/packagekit/packagekit_test.go
- [X] T032 [P] [US4] Add failing CLI test for uninstall/repair output listing removal set and durable-state action in internal/app/app_test.go

### Implementation for User Story 4

- [X] T033 [US4] Include package-owned removal lists, stale counts, repair counts, durable-state action, and survivor audit path in lifecycle output/evidence in internal/packagekit/audit.go
- [X] T034 [US4] Update package uninstall output to include durable-state action and survivor purge evidence path in internal/app/app.go
- [X] T035 [US4] Add lifecycle redaction guard for package outputs/evidence in internal/packagekit/packagekit_test.go

**Checkpoint**: US4 independently proves package cleanup evidence is clear and
redaction-safe.

---

## Phase 7: Polish & Cross-Cutting Concerns

**Purpose**: Smoke, docs, schemas, and final verification.

- [X] T036 [P] Update package manifest schema or fixture docs for effective migration fields in schemas/package-manifest.schema.json
- [X] T037 [P] Expand scripts/test-package-smoke.sh to cover obsolete reporting, explicit repair, incompatible migration, helper diagnostics, external prerequisite classification, uninstall preserve, and uninstall purge
- [X] T038 Update scripts/test-gate0.sh to ensure expanded package smoke is still part of Gate 0
- [X] T039 [P] Update docs/STATUS.md, docs/privacy-run-test-plan.md, README.md, and package docs for 017 package lifecycle behavior
- [X] T040 [P] Update specs/017-packaging-sweep/quickstart.md if implementation command names or smoke commands changed during implementation
- [X] T041 Run markdownlint over README/docs/specs/017 and fix documentation issues
- [X] T042 Run gofmt -l internal cmd and fix formatting issues
- [X] T043 Run go build ./... and go vet ./... and fix build/vet issues
- [X] T044 Run go test ./... and targeted package smoke, then fix test failures
- [X] T045 Run git diff --check and scripts/test-gate0.sh, then fix final gate failures

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies.
- **Foundational (Phase 2)**: Depends on Setup; blocks all user stories.
- **US1 (Phase 3)**: Depends on Foundational; MVP.
- **US2 (Phase 4)**: Depends on Foundational; can run after or alongside US1 once shared result structures are stable.
- **US3 (Phase 5)**: Depends on Foundational; should complete before final package smoke is trusted.
- **US4 (Phase 6)**: Depends on US1 repair/evidence structures and existing uninstall behavior.
- **Polish (Phase 7)**: Depends on selected user stories, then final gates.

### Parallel Opportunities

- T001-T003 can run in parallel.
- T009-T011, T017-T019, T024-T026, and T030-T032 are parallel test-writing tasks by story.
- T036, T037, and T039 can proceed in parallel after implementation stabilizes.

## Implementation Strategy

### MVP First

1. Complete Setup and Foundational tasks.
2. Complete US1.
3. Validate obsolete package-owned file report-first behavior and repair.
4. Continue with helper/prerequisite diagnostics, migration gates, and evidence.

### Verification Discipline

- Write failing tests before implementation for each story.
- Do not mark package smoke complete unless it exercises production package
  commands, not hand-mutated internal helpers only.
- Treat green tests as insufficient until output/evidence proves each FR/SC in
  quickstart mappings.
