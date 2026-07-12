# Tasks: Operator Console MVP

<!-- markdownlint-disable MD013 -->

**Input**: Design documents from `specs/019-operator-console-mvp/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

**Tests**: Required. This feature touches UI control surfaces, decision/notice actions, event refresh, and redaction.

**Organization**: Tasks are grouped by user story to enable independent implementation and testing.

## Phase 1: Setup

**Purpose**: Establish the console contract without adding new authority.

- [X] T001 [P] Add console model contract references to docs/tui-webui-experience.md and docs/manager-control-plane.md
- [X] T002 [P] Add console smoke placeholder to scripts/test-live-console-smoke.sh without enabling assertions yet
- [X] T003 [P] Confirm no new schema is required for existing liveconsole state in internal/liveconsole/model.go

---

## Phase 2: Foundational

**Purpose**: Shared model helpers and redaction guards used by WebUI and TUI.

- [X] T004 Add console action-required grouping helpers for decisions/notices/HostFS writes in internal/liveconsole/model.go and internal/liveconsole/reducer.go
- [X] T005 Add package/support/doctor status rows to the liveconsole state or derived console view in internal/liveconsole/model.go
- [X] T006 Add shared redaction/canary test helpers for WebUI/TUI console output in internal/liveconsole/reducer_test.go and internal/app/app_liveconsole_test.go

**Checkpoint**: Console model exists and remains read-only except existing action references.

---

## Phase 3: User Story 1 - See What Needs Action (Priority: P1) 🎯 MVP

**Goal**: WebUI and TUI show coherent action/status panels for decisions, notices, doctor, package/support, environments, and background work.

**Independent Test**: Seed console state and verify WebUI/TUI render every required panel plus empty/warning/action-required states.

### Tests for User Story 1

- [X] T007 [P] [US1] Add WebUI console panel runtime test using the existing Goja harness in internal/manager/server_liveconsole_test.go
- [X] T008 [P] [US1] Add TUI compact console render test for action-required/doctor/package/support/environment/background summaries in internal/app/app_liveconsole_test.go
- [X] T009 [P] [US1] Add empty/loading/error state tests for required panels in internal/liveconsole/reducer_test.go or internal/manager/server_liveconsole_test.go

### Implementation for User Story 1

- [X] T010 [US1] Extend liveconsole seed/state with read-only doctor/package/support summaries in internal/liveconsole/model.go
- [X] T011 [US1] Populate console status summaries from Manager overview/support matrix/current cached doctor facts in internal/daemon or internal/manager seed construction
- [X] T012 [US1] Add WebUI Action Required, Doctor, Package/Support, Environments, Background, HostFS Writes, Decisions, Notices, and Stream panels in internal/manager/server.go
- [X] T013 [US1] Add compact TUI action/status sections in internal/app/app.go

**Checkpoint**: Operator can see what needs action without invoking new authority.

---

## Phase 4: User Story 2 - Resolve Existing Decisions From the Console (Priority: P2)

**Goal**: Console actions use existing Manager decision/notice routes with stale-token/timeout/denied visibility.

**Independent Test**: Seed decision/notice records and exercise WebUI JS/TUI command handlers against existing routes or action stubs that assert the route names.

### Tests for User Story 2

- [X] T014 [P] [US2] Add WebUI JS action test proving decision claim/resolve and notice ack use existing Manager routes in internal/manager/server_liveconsole_test.go
- [X] T015 [P] [US2] Add stale-token/expired/already-claimed visible-state tests in internal/manager/server_liveconsole_test.go
- [X] T016 [P] [US2] Add HostFS write preview redaction test proving staged content is not rendered in internal/liveconsole/reducer_test.go

### Implementation for User Story 2

- [X] T017 [US2] Wire WebUI decision claim/resolve controls to existing `/api/v1/decision/*` routes in internal/manager/server.go
- [X] T018 [US2] Wire WebUI notice acknowledge controls to the existing `/api/v1/notice/ack` route in internal/manager/server.go
- [X] T019 [US2] Render stale-token, denied, timeout, and already-claimed states without retrying or inventing authority in internal/manager/server.go
- [X] T020 [US2] Keep TUI action text as existing CLI/decision commands, not hidden local mutation, in internal/app/app.go

**Checkpoint**: Console can operate existing decisions/notices only through existing authority.

---

## Phase 5: User Story 3 - Refresh Without Hidden Polling (Priority: P3)

**Goal**: Healthy daemon streams update console panels without hidden steady-state polling; fallback is visible when streams close or are absent.

**Independent Test**: Run WebUI Goja and TUI watch tests that fail if interval polling runs while stream is healthy.

### Tests for User Story 3

- [X] T021 [P] [US3] Add WebUI no-hidden-polling runtime test for the console panels in internal/manager/server_liveconsole_test.go
- [X] T022 [P] [US3] Add TUI no-interval-polling regression test for console watch mode in internal/app/app_liveconsole_test.go
- [X] T023 [P] [US3] Add credential-expired/disconnected/fallback render tests in internal/liveconsole/reducer_test.go

### Implementation for User Story 3

- [X] T024 [US3] Ensure WebUI console panel updates reuse EventSource/liveconsole reducer payloads and explicit refresh only in internal/manager/server.go
- [X] T025 [US3] Ensure TUI watch mode continues to use event-driven render while stream is healthy in internal/app/app.go
- [X] T026 [US3] Add visible stream health and fallback copy to WebUI and TUI in internal/manager/server.go and internal/app/app.go

**Checkpoint**: Event honesty from 007 is preserved.

---

## Phase 6: Polish & Cross-Cutting

**Purpose**: Redaction, smoke, docs, and final verification.

- [X] T027 [P] Add console redaction canary tests for claim tokens/provider refs/proxy values/runtime paths/staged HostFS content in internal/manager/server_liveconsole_test.go and internal/app/app_liveconsole_test.go
- [X] T028 [P] Update README.md with operator console scope and non-authority note
- [X] T029 [P] Update docs/STATUS.md for implemented 019 console MVP
- [X] T030 [P] Update docs/tui-webui-experience.md with panels, action limits, explicit doctor behavior, and stream fallback
- [X] T031 [P] Update docs/manager-control-plane.md with console action routing and no-new-authority boundary
- [X] T032 [P] Update docs/privacy-run-test-plan.md with console smoke expectations
- [X] T033 Add console smoke assertions to scripts/test-live-console-smoke.sh and ensure scripts/test-gate0.sh runs them
- [X] T034 Mark all completed tasks in specs/019-operator-console-mvp/tasks.md
- [X] T035 Run gofmt -l internal cmd and ensure it prints nothing
- [X] T036 Run go build ./... and go vet ./...
- [X] T037 Run go test ./...
- [X] T038 Run scripts/test-gate0.sh
- [X] T039 Run npx --yes markdownlint-cli2 README.md docs/**/*.md specs/019-operator-console-mvp/**/*.md
- [X] T040 Run git diff --check

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies.
- **Foundational (Phase 2)**: Blocks user stories.
- **US1 (Phase 3)**: MVP after foundational.
- **US2 (Phase 4)**: Depends on console action-required model.
- **US3 (Phase 5)**: Can run after foundational but should verify US1 panel updates.
- **Polish (Phase 6)**: Depends on implemented stories.

### User Story Dependencies

- **US1**: No dependency on US2/US3.
- **US2**: Uses US1 action-required grouping.
- **US3**: Cross-cuts WebUI/TUI event refresh.

### Parallel Opportunities

- T001-T003 can run in parallel.
- T007-T009, T014-T016, and T021-T023 are independent test tasks.
- T028-T032 docs can run in parallel after implementation.

## Parallel Example: User Story 1

```text
Task: "Add WebUI console panel runtime test in internal/manager/server_liveconsole_test.go"
Task: "Add TUI compact console render test in internal/app/app_liveconsole_test.go"
Task: "Add empty/loading/error state tests in internal/liveconsole/reducer_test.go"
```

## Implementation Strategy

### MVP First

1. Complete setup and foundational model helpers.
2. Implement US1 panels and compact TUI summaries.
3. Validate WebUI/TUI render tests before adding action controls.
4. Add existing-route action controls and event-refresh regressions.

### Completion Bar

019 is complete only when all 40 tasks are checked, Gate 0 includes console smoke, and final build/test/lint/diff checks are green.
