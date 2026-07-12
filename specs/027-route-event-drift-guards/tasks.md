# Tasks: Route And Event Drift Guards

<!-- markdownlint-disable MD013 -->

**Input**: Design documents from `/specs/027-route-event-drift-guards/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

**Tests**: Required. This feature exists to prevent green-but-false route/event
coverage.

**Organization**: Tasks are grouped by user story so each story remains
independently testable.

## Phase 1: Setup

**Purpose**: Add placeholders for route and event metadata in owning packages.

- [X] T001 Create Manager route metadata placeholder in `internal/manager/routes.go`
- [X] T002 Create daemon endpoint metadata placeholder in `internal/daemon/routes.go`
- [X] T003 Create live-event catalog placeholder in `internal/liveconsole/catalog.go`
- [X] T004 [P] Add 027 docs/status placeholders in `docs/privacy-run-test-plan.md` and `docs/STATUS.md`

---

## Phase 2: Foundational

**Purpose**: Define common route/event record shapes before story work.

- [X] T005 Define `RouteClass`, `RouteSpec`, and Manager route recognizer in `internal/manager/routes.go`
- [X] T006 Define daemon endpoint spec and recognizer in `internal/daemon/routes.go`
- [X] T007 Define structured live-event catalog entry fields in `internal/liveconsole/catalog.go`

**Checkpoint**: Shared metadata types exist; story work can begin.

---

## Phase 3: User Story 1 - Keep Route Inventories Tied To Production Dispatch (Priority: P1) 🎯 MVP

**Goal**: Manager and daemon route inventories are tied to production route
recognition rather than test-only hand-written lists.

**Independent Test**: `go test ./internal/manager ./internal/daemon -run 'Route|Inventory|Recognizer|Parity'`

### Tests for User Story 1

- [X] T008 [P] [US1] Add Manager route inventory/recognizer drift tests in `internal/manager/api_routes_test.go`
- [X] T009 [P] [US1] Replace daemon 32-route hand list test with Manager route metadata in `internal/daemon/server_test.go`
- [X] T010 [P] [US1] Add daemon endpoint separation tests in `internal/daemon/routes_test.go`

### Implementation for User Story 1

- [X] T011 [US1] Implement Manager GET/POST route specs and recognizer in `internal/manager/routes.go`
- [X] T012 [US1] Update `internal/manager/api.go` route dispatch to consume route specs/recognizer where scoped
- [X] T013 [US1] Implement daemon endpoint specs in `internal/daemon/routes.go`
- [X] T014 [US1] Update daemon parity tests to consume `manager.ManagerRoutes()` instead of local lists
- [X] T015 [US1] Run `go test ./internal/manager ./internal/daemon -run 'Route|Inventory|Recognizer|Parity'`

**Checkpoint**: Adding a production route or daemon endpoint without inventory
coverage fails locally.

---

## Phase 4: User Story 2 - Prove Live Events Have Honest Producers And Reducers (Priority: P2)

**Goal**: Live-event catalog ties production producers, remaps, required fields,
redaction expectations, and reducer coverage together.

**Independent Test**: `go test ./internal/liveconsole ./internal/daemon -run 'Catalog|Producer|Reducer|Representative'`

### Tests for User Story 2

- [X] T016 [P] [US2] Add catalog row validation tests in `internal/liveconsole/catalog_test.go`
- [X] T017 [P] [US2] Add reducer-branch coverage tests in `internal/liveconsole/catalog_test.go`
- [X] T018 [P] [US2] Add daemon producer/remap coverage tests in `internal/daemon/events_test.go`
- [X] T019 [P] [US2] Add unknown-kind compatibility tests for Go and JavaScript reducers in `internal/liveconsole/reducer_test.go` and `internal/manager/server_liveconsole_test.go`

### Implementation for User Story 2

- [X] T020 [US2] Implement structured event catalog rows in `internal/liveconsole/catalog.go`
- [X] T021 [US2] Update `ValidateEvent` and representative-event tests to use catalog required fields where scoped
- [X] T022 [US2] Expose daemon producer kind/remap metadata for tests in `internal/daemon/events.go`
- [X] T023 [US2] Update panel coverage tests to distinguish production, seed-only, and test-only rows
- [X] T024 [US2] Run `go test ./internal/liveconsole ./internal/daemon -run 'Catalog|Producer|Reducer|Representative'`

**Checkpoint**: Reducer branches without production/seed/test catalog status
and producer remaps without catalog coverage fail locally.

---

## Phase 5: User Story 3 - Verify UI Action Routes At Runtime (Priority: P3)

**Goal**: WebUI/TUI action route proof observes runtime action requests rather
than static source strings.

**Independent Test**: `go test ./internal/manager ./internal/app -run 'WebUI.*Action|TUI.*Action|LiveConsole'`

### Tests for User Story 3

- [X] T025 [P] [US3] Add WebUI runtime fetch interception test in `internal/manager/server_liveconsole_test.go`
- [X] T026 [P] [US3] Add TUI action/route guard test where action route surfaces exist in `internal/app/app_liveconsole_test.go`
- [X] T027 [P] [US3] Add no-source-grep regression assertion for WebUI route proof in `internal/manager/server_liveconsole_test.go`

### Implementation for User Story 3

- [X] T028 [US3] Refactor WebUI action route helpers to expose runtime-observable action descriptors where useful in `internal/manager/server.go`
- [X] T029 [US3] Wire runtime action tests to Manager/daemon route recognizers
- [X] T030 [US3] Ensure healthy stream no-polling tests still cover WebUI and TUI paths
- [X] T031 [US3] Run `go test ./internal/manager ./internal/app -run 'WebUI.*Action|TUI.*Action|LiveConsole'`

**Checkpoint**: Covered UI actions prove actual recognized runtime routes.

---

## Phase 6: Polish & Cross-Cutting Concerns

**Purpose**: Align docs, smoke gates, and final cleanliness.

- [X] T032 Update `scripts/test-live-console-smoke.sh` or Gate 0 comments to name 027 drift guards
- [X] T033 Update `docs/privacy-run-test-plan.md` and `docs/STATUS.md`
- [X] T034 [P] Run `npx --yes markdownlint-cli2 'specs/027-route-event-drift-guards/**/*.md' docs/privacy-run-test-plan.md docs/STATUS.md`
- [X] T035 Run `gofmt -l internal/manager internal/daemon internal/liveconsole internal/app`
- [X] T036 Run `go build ./...`
- [X] T037 Run `go vet ./...`
- [X] T038 Run `git diff --check`
- [X] T039 Run `go test ./...`
- [X] T040 Run `scripts/test-gate0.sh`
- [X] T041 Mark all tasks complete in `specs/027-route-event-drift-guards/tasks.md`

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies.
- **Foundational (Phase 2)**: Depends on Setup and blocks all stories.
- **US1 (Phase 3)**: MVP; route metadata feeds UI action guards.
- **US2 (Phase 4)**: Independent after Foundational.
- **US3 (Phase 5)**: Depends on US1 route recognizers and existing UI action surfaces.
- **Polish (Phase 6)**: Depends on selected stories complete.

### User Story Dependencies

- **US1**: Independent MVP.
- **US2**: Independent from route work after catalog types exist.
- **US3**: Depends on US1 recognizers for runtime request classification.

### Parallel Opportunities

- T004 can run in parallel with source placeholders.
- T008-T010 can run in parallel.
- T016-T019 can run in parallel.
- T025-T027 can run in parallel.
- T034 can run while code checks are prepared after implementation.

## Implementation Strategy

1. Add production-owned route recognizers first.
2. Move route parity tests off hand-written local lists.
3. Upgrade live-event catalog from representative events to structured rows.
4. Replace source-grep action proof with runtime request observation.
5. Validate with targeted tests, full Go tests, and Gate 0.
