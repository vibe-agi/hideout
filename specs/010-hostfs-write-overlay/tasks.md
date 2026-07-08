# Tasks: HostFS Write Overlay

<!-- markdownlint-disable MD013 -->

**Input**: Design documents from `/specs/010-hostfs-write-overlay/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

**Tests**: Required. This feature touches HostFS, host filesystem mutation, Manager API, daemon/UI control, lifecycle cleanup, audit/export evidence, and release gates.

**Organization**: Tasks are grouped by user story to enable independent implementation and testing of each story.

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Add shared package/schemas/smoke placeholders without changing behavior.

- [X] T001 Create `internal/hostfs/overlay/` package skeleton with doc.go and empty test file in `internal/hostfs/overlay/doc.go` and `internal/hostfs/overlay/overlay_test.go`
- [X] T002 [P] Add HostFS write decision schema placeholder in `schemas/hostfs-write-decision.schema.json`
- [X] T003 [P] Add HostFS write event schema placeholder in `schemas/hostfs-write-event.schema.json`
- [X] T004 [P] Add smoke script placeholder in `scripts/test-hostfs-write-overlay-smoke.sh`
- [X] T005 Register new schema and smoke placeholders in `scripts/test-gate0.sh`

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Core data types, validation seams, and event/audit vocabulary that all stories depend on.

**CRITICAL**: No user story work can begin until this phase is complete.

- [X] T006 Extend HostFS op constants and validation for append, truncate, mkdir, chmod, and chown in `internal/hostfs/hostfs.go`
- [X] T007 Add overlay write grant parsing/summary support without granting read authority in `internal/app/app.go` and `internal/manager/profile_hostfs.go`
- [X] T008 [P] Define overlay operation, snapshot, decision, claim, and result types in `internal/hostfs/overlay/types.go`
- [X] T009 [P] Define HostFS write audit action constants and redaction-safe detail builders in `internal/hostfs/overlay/audit.go`
- [X] T010 [P] Add HostFS write event kind/payload fields to `internal/liveconsole/events.go` and `schemas/hostfs-write-event.schema.json`
- [X] T011 Define Manager HostFS write plan version constants and request/response structs in `internal/manager/hostfs_write.go`
- [X] T012 Add broker request argument structs for write-class actions without enabling execution in `internal/broker/hostfs.go`
- [X] T013 Add hostfsd write operation stubs returning unsupported until US1 in `cmd/hideout-hostfsd/main_linux.go`
- [X] T014 Add 010 schema references to `schemas/manager-api.schema.json` for HostFS write resources

**Checkpoint**: Foundation ready - user story implementation can now begin.

---

## Phase 3: User Story 1 - Stage HostFS Writes Without Host Mutation (Priority: P1) MVP

**Goal**: Guest write-class operations covered by explicit overlay grants produce durable staged state, guest reads reflect the overlay, and host lower files remain unchanged before apply.

**Independent Test**: Run targeted hostfs/broker/hostfsd tests for all supported operations and denial paths; verify guest-visible success only follows durable staging.

### Tests for User Story 1

- [X] T015 [P] [US1] Add overlay grant separation tests proving read grants do not permit writes in `internal/hostfs/hostfs_test.go` (FR-001, FR-002, SC-009)
- [X] T016 [P] [US1] Add durable staging tests for create/replace/append/truncate in `internal/hostfs/overlay/overlay_test.go` (FR-003, FR-006, FR-007, FR-008, FR-009, FR-025, SC-001, SC-012)
- [X] T017 [P] [US1] Add durable staging tests for mkdir/delete/rename/chmod/chown in `internal/hostfs/overlay/overlay_test.go` (FR-003, FR-006, FR-007, FR-008, FR-009, FR-025, SC-001, SC-012)
- [X] T018 [P] [US1] Add denied staging tests for ungranted, deny-rule, reserved-root, unsupported special-file, unsafe symlink, and unsafe overlay store cases in `internal/hostfs/overlay/overlay_test.go` (FR-004, FR-005, FR-023, SC-003)
- [X] T019 [P] [US1] Add broker contract tests for write-class request envelopes, unknown args, action-specific args, and redacted audit details in `internal/broker/broker_test.go` (FR-006, FR-008, FR-020, FR-025, SC-001, SC-003, SC-012)
- [X] T020 [P] [US1] Add hostfsd/FUSE unit tests for translating write/create/delete/rename/chmod/chown calls into HostFS write RPC in `cmd/hideout-hostfsd/main_linux_test.go` (FR-003, FR-006, FR-025, SC-001, SC-012)

### Implementation for User Story 1

- [X] T021 [US1] Implement overlay store creation, locking, object writes, operation records, and durability boundaries in `internal/hostfs/overlay/store.go`
- [X] T022 [US1] Implement base snapshot capture, content hashing, and safe path/symlink facts in `internal/hostfs/overlay/snapshot.go`
- [X] T023 [US1] Implement staging for create/replace/append/truncate content operations in `internal/hostfs/overlay/stage_content.go`
- [X] T024 [US1] Implement staging for mkdir/delete/rename/chmod/chown metadata/path operations in `internal/hostfs/overlay/stage_metadata.go`
- [X] T025 [US1] Extend `internal/hostfs/service.go` to route write-class operations to the overlay store and keep host lower layer unchanged
- [X] T026 [US1] Extend overlay-aware Stat/Read/List behavior for same-session staged state in `internal/hostfs/service.go`
- [X] T027 [US1] Extend broker HostFS dispatch for write-class actions and staged-success responses in `internal/broker/hostfs.go`
- [X] T028 [US1] Implement Linux hostfsd FUSE write/create/delete/rename/chmod/chown translation in `cmd/hideout-hostfsd/main_linux.go`
- [X] T029 [US1] Emit stage/deny/pending audit records without overlay object paths in `internal/hostfs/overlay/audit.go`

**Checkpoint**: US1 MVP stages all supported operations, guest sees overlay state, host is unchanged before apply.

---

## Phase 4: User Story 2 - Review And Resolve Pending Writes From Local Surfaces (Priority: P2)

**Goal**: Pending write decisions are reviewable from authenticated local surfaces, only one claimant can resolve each decision, and timeout defaults to deny/discard.

**Independent Test**: Create staged decisions, observe from multiple clients, claim from one client, verify claim token enforcement, and verify timeout deny/discard.

### Tests for User Story 2

- [X] T030 [P] [US2] Add Manager API contract tests for `hostfs/write/plan`, `claim`, `apply`, `discard`, and `status` response shapes in `internal/manager/api_test.go` (FR-009, FR-010, FR-012, SC-006)
- [X] T031 [P] [US2] Add claim/lease race tests proving one claimant wins and stale claim tokens fail closed in `internal/manager/hostfs_write_test.go` (FR-011, FR-012, SC-002, SC-006)
- [X] T032 [P] [US2] Add approval timeout tests proving deny/discard and `approval-timeout` audit in `internal/manager/hostfs_write_test.go` (FR-013, FR-022, SC-007)
- [X] T033 [P] [US2] Add daemon/live event tests for pending, claimed, resolved, timeout, and no claim-token leakage in `internal/daemon/server_test.go` (FR-010, FR-013, FR-020, SC-006, SC-007)
- [X] T034 [P] [US2] Add WebUI/TUI rendering tests for pending HostFS write decisions without direct filesystem authority in `internal/manager/server_test.go` and `internal/app/app_test.go` (FR-009, FR-010, SC-006)

### Implementation for User Story 2

- [X] T035 [US2] Implement Manager HostFS write plan/status using staged operation records in `internal/manager/hostfs_write.go`
- [X] T036 [US2] Implement Manager claim/lease creation, expiry, and token validation in `internal/manager/hostfs_write.go`
- [X] T037 [US2] Add Manager API routes for HostFS write plan/claim/apply/discard/status and keep JavaScript adapter proposals proposal-only in `internal/manager/api.go` (FR-019)
- [X] T038 [US2] Add CLI commands for listing, reviewing, claiming, applying, and discarding write decisions in `internal/app/app.go`
- [X] T039 [US2] Add WebUI controls for pending HostFS write decisions through Manager routes in `internal/manager/server.go`
- [X] T040 [US2] Add TUI display and actions for pending HostFS write decisions in `internal/app/app.go`
- [X] T041 [US2] Emit daemon/liveconsole HostFS write events and reduce them into local state in `internal/daemon/events.go` and `internal/liveconsole/reducer.go`
- [X] T042 [US2] Implement timeout worker that denies, discards staged artifacts, and audits `approval-timeout` in `internal/manager/hostfs_write.go`

**Checkpoint**: US2 review/claim/timeout flow works from local authenticated surfaces without adding raw host authority.

---

## Phase 5: User Story 3 - Apply Or Discard With Revalidation And Evidence (Priority: P3)

**Goal**: Claimed decisions apply or discard with full revalidation, conflict detection, no partial host mutation, privilege-status evidence, and exportable audit.

**Independent Test**: Apply clean operations, force conflicts and symlink swaps, test chown constraints, verify no partial mutations, and export evidence.

### Tests for User Story 3

- [X] T043 [P] [US3] Add clean apply tests for create/replace/append/truncate/mkdir/delete/rename/chmod/chown in `internal/hostfs/overlay/apply_test.go` (FR-014, FR-016, FR-017, SC-004, SC-005)
- [X] T044 [P] [US3] Add conflict tests for same-second content change, inode/type change, destination appearance, delete target change, and symlink swap in `internal/hostfs/overlay/apply_test.go` (FR-014, FR-015, SC-004)
- [X] T045 [P] [US3] Add no-partial failure tests for content apply, rename, delete, chmod, and chown in `internal/hostfs/overlay/apply_test.go` (FR-016, FR-017, SC-005)
- [X] T046 [P] [US3] Add chown constraint tests for plan-captured resolvable IDs versus unknown, changed, or privilege-requiring targets in `internal/hostfs/overlay/apply_test.go` (FR-026, SC-013)
- [X] T047 [P] [US3] Add degraded/unknown privilege-status plan/apply non-claim tests in `internal/manager/hostfs_write_test.go` (FR-018, SC-008)
- [X] T048 [P] [US3] Add export/redaction tests for HostFS write evidence, claim-token stripping, and overlay object path stripping in `internal/export/export_test.go` (FR-020, FR-021, SC-011)

### Implementation for User Story 3

- [X] T049 [US3] Implement apply preflight revalidation for path, grant, deny, reserved roots, symlink, snapshot, and privilege status in `internal/hostfs/overlay/store.go`
- [X] T050 [US3] Implement no-partial content apply with temp file, fsync, and rename in `internal/hostfs/overlay/store.go`
- [X] T051 [US3] Implement no-partial metadata/path apply for mkdir/delete/rename/chmod/chown in `internal/hostfs/overlay/store.go`
- [X] T052 [US3] Implement chown host resolution and extra-privilege denial in `internal/hostfs/overlay/store.go`
- [X] T053 [US3] Wire Manager apply/discard to overlay apply/discard and emit result events in `internal/manager/hostfs_write.go`
- [X] T054 [US3] Implement terminal cleanup of staged content objects on timeout, discard, conflict, failed apply, and successful apply while preserving pending review material across session termination and daemon restart in `internal/hostfs/overlay/store.go`
- [X] T055 [US3] Include HostFS write overlay evidence in audit export without leaking claim tokens or overlay object paths in `internal/export/source.go`

**Checkpoint**: All user stories are independently functional and 010 host mutation occurs only through claimed Manager apply.

---

## Phase 6: Polish & Cross-Cutting Concerns

**Purpose**: Documentation, schemas, gates, and final verification.

- [X] T056 [P] Update `docs/STATUS.md` to move HostFS write overlay from Later to Implemented after verification (FR-024)
- [X] T057 [P] Update `docs/privacy-run-design.md` and `docs/hostfs-overlay-design.md` with staged write/apply behavior and unsupported special-file scope (FR-024)
- [X] T058 [P] Update `docs/privacy-run-test-plan.md` with Gate 2 HostFS write overlay requirements and smoke command (FR-024, SC-010)
- [X] T059 [P] Update `docs/manager-control-plane.md`, `docs/tui-webui-experience.md`, and `docs/script-extension-architecture.md` for HostFS write decisions and JS proposal-only behavior (FR-019, FR-024)
- [X] T060 [P] Update `docs/threat-model.md` and README/user-facing docs so 010 does not claim workspace blocking, DLP, guest-root containment, or exclusive host mutation under 009 degraded/unknown (FR-018, FR-024, SC-008)
- [X] T061 Finalize `schemas/hostfs-write-decision.schema.json`, `schemas/hostfs-write-event.schema.json`, and `schemas/manager-api.schema.json` validation
- [X] T062 Implement `scripts/test-hostfs-write-overlay-smoke.sh` and wire it into `scripts/test-gate2-lima.sh` and `scripts/test-gate0.sh` where appropriate (SC-010)
- [X] T063 Run quickstart scenarios from `specs/010-hostfs-write-overlay/quickstart.md` (FR-001 through FR-026, SC-001 through SC-013)
- [X] T064 Run final battery: `go build ./...`, `go vet ./...`, `gofmt -l internal cmd`, `git diff --check`, `npx --yes markdownlint-cli2 README.md docs/**/*.md specs/010-hostfs-write-overlay/**/*.md`, `go test ./...`, `scripts/test-gate0.sh`, and real Lima HostFS write smoke

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies.
- **Foundational (Phase 2)**: Depends on Setup completion and blocks all user stories.
- **US1 (Phase 3)**: Depends on Foundational; delivers MVP staged writes.
- **US2 (Phase 4)**: Depends on Foundational and uses staged decisions from US1 for full end-to-end behavior, but Manager claim/timeout logic can be unit-tested with fixture staged operations.
- **US3 (Phase 5)**: Depends on US1 staged operations and US2 claim state.
- **Polish (Phase 6)**: Depends on all implemented stories.

### User Story Dependencies

- **User Story 1 (P1)**: Can start after Foundational; no dependency on US2/US3.
- **User Story 2 (P2)**: Can start after Foundational with fixtures, then integrate with US1 staged operations.
- **User Story 3 (P3)**: Requires US1 staging and US2 claim/decision state for product apply.

### Within Each User Story

- Write tests first and verify they fail.
- Data model and validators before broker/API routes.
- Broker/service behavior before hostfsd integration.
- Manager decision state before UI/daemon rendering.
- Apply preflight before mutation implementation.

## Parallel Opportunities

- T002-T004 can run in parallel.
- T008-T010 can run in parallel after T006-T007.
- T015-T020 can run in parallel.
- T030-T034 can run in parallel.
- T043-T048 can run in parallel.
- T056-T060 can run in parallel after implementation behavior is stable.

## Parallel Example: User Story 1

```text
Task: "T015 Add overlay grant separation tests in internal/hostfs/hostfs_test.go"
Task: "T016 Add durable content staging tests in internal/hostfs/overlay/overlay_test.go"
Task: "T017 Add metadata/path staging tests in internal/hostfs/overlay/overlay_test.go"
Task: "T019 Add broker contract tests in internal/broker/broker_test.go"
Task: "T020 Add hostfsd/FUSE unit tests in cmd/hideout-hostfsd/main_linux_test.go"
```

## Implementation Strategy

### MVP First (User Story 1 Only)

1. Complete Phase 1 and Phase 2.
2. Implement US1 staging for all supported operations.
3. Verify host lower files remain unchanged and guest reads see overlay state.
4. Stop and validate MVP before adding approval/apply.

### Incremental Delivery

1. US1: staged write overlay, no host apply.
2. US2: review/claim/timeout from local surfaces.
3. US3: apply/discard with revalidation and evidence.
4. Polish: docs, schemas, Gate 2 smoke, final battery.

### Notes

- Every task uses exact repository paths.
- `[P]` tasks touch different files or can be implemented from fixtures.
- Story labels map directly to spec user stories.
- Host mutation tasks must not run before their fail-closed tests exist.
