<!-- markdownlint-disable MD013 -->

# Tasks: Daemon Live Operations Console

**Input**: Design documents from `/specs/007-daemon-live-operations-console/`

**Prerequisites**: [plan.md](plan.md), [spec.md](spec.md), [research.md](research.md), [data-model.md](data-model.md), [contracts/](contracts/), [quickstart.md](quickstart.md)

**Tests**: Required. This feature touches UI/daemon evidence surfaces and must prove positive live updates, no hidden polling, fail-closed stale behavior, and redaction cleanliness.

**Organization**: Tasks are grouped by user story for independent delivery. Shared event catalog/reducer work is in Foundational because both WebUI and TUI depend on it.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependency on incomplete tasks)
- **[Story]**: User story label (`US1`, `US2`, `US3`) for story phases only
- Every task names exact file paths

## Phase 1: Setup

**Purpose**: Add the feature skeleton and schema placeholders.

- [X] T001 Create live console package skeleton in `internal/liveconsole/model.go`, `internal/liveconsole/seed.go`, `internal/liveconsole/reducer.go`, and `internal/liveconsole/validate.go`
- [X] T002 [P] Add seed schema placeholder in `schemas/live-console-seed.schema.json`
- [X] T003 [P] Add live console smoke placeholder in `scripts/test-live-console-smoke.sh`
- [X] T004 [P] Register `schemas/live-console-seed.schema.json` and `scripts/test-live-console-smoke.sh` in `scripts/test-gate0.sh`

---

## Phase 2: Foundational

**Purpose**: Blocking shared catalog, seed, reducer, schema, and daemon typed-event infrastructure.

**Critical**: No WebUI/TUI story work starts until this phase is complete.

- [X] T005 [P] Add catalog/schema tests for typed daemon events in `internal/liveconsole/catalog_test.go` covering FR-005, FR-006, FR-016, and SC-008
- [X] T006 [P] Add seed builder tests in `internal/liveconsole/seed_test.go` covering one-seed semantics, profile scope, and control-plane redaction
- [X] T007 [P] Add reducer tests in `internal/liveconsole/reducer_test.go` for duplicate, old, out-of-order, gap, unknown-kind, unknown-entity, terminal-state, and missing-required-field behavior
- [X] T008 Define live console types in `internal/liveconsole/model.go`: `Seed`, `State`, `Event`, `EntityRef`, panel row types, reducer result, and stream health states
- [X] T009 Implement seed construction in `internal/liveconsole/seed.go` from Manager overview, recent audit tails, daemon status, and optional profile scope
- [X] T010 Implement reducer and validation in `internal/liveconsole/reducer.go` and `internal/liveconsole/validate.go`, including stale/disconnected transitions and deterministic old-event handling
- [X] T011 Upgrade `schemas/daemon-event.schema.json` and `schemas/live-console-seed.schema.json` to match `internal/liveconsole` required fields
- [X] T012 Refactor `internal/daemon/events.go` so daemon events use `liveconsole.Event` instead of free-form daemon-local payloads
- [X] T013 Update daemon SSE publishing and tests in `internal/daemon/server.go`, `internal/daemon/events_test.go`, and `internal/daemon/uiweb_test.go` for typed event JSON and redaction
- [X] T014 Enrich production emitters in `internal/daemon/background.go`, `internal/daemon/audit.go`, `internal/manager/event_observer.go`, `internal/manager/run_environment.go`, `internal/manager/run_apply.go`, `internal/manager/run_session.go`, and `internal/manager/export.go` so environment, session/run, background, audit, export, cleanup, and terminal events carry catalog-required payload fields from real operation sources

**Checkpoint**: Shared typed event catalog, seed, reducer, and daemon stream are usable by both UI surfaces.

---

## Phase 3: User Story 1 - Watch WebUI update from live daemon payloads (Priority: P1) MVP

**Goal**: WebUI current panels update from daemon event payloads after one seed, with no steady-state overview/audit re-fetch while stream is healthy.

**Independent Test**: Run `go test ./internal/manager -run 'TestWebUILiveConsole'` and prove visible DOM changes from events with zero post-seed overview/audit reads.

### Tests for User Story 1

- [X] T015 [P] [US1] Add served WebUI JavaScript harness test in `internal/manager/server_liveconsole_test.go` proving one seed, representative event DOM update, and zero post-seed `overview`/`audit/events` fetches
- [X] T016 [P] [US1] Add WebUI stale/disconnected/redaction tests in `internal/manager/server_liveconsole_test.go` for terminal event, credential expiry, schema mismatch, event gap, and seeded control-plane material

### Implementation for User Story 1

- [X] T017 [US1] Refactor WebUI startup in `internal/manager/server.go` from `load()` global overview state to explicit live seed state and render state
- [X] T018 [US1] Add WebUI reducer functions in `internal/manager/server.go` that apply typed daemon events to browser state without calling `load()` during healthy-stream event handling
- [X] T019 [US1] Update WebUI panel renderers in `internal/manager/server.go` to render environments, sessions/runs, background, audit/denied audit, export/cleanup outcomes, and stream health from reducer state
- [X] T020 [US1] Replace WebUI `EventSource.onmessage -> load()` logic in `internal/manager/server.go` with `onmessage -> validate/apply/render` and explicit stale/disconnected states
- [X] T021 [US1] Preserve explicit manual refresh in `internal/manager/server.go` as a new seed action, distinct from hidden polling
- [X] T022 [US1] Update WebUI loopback test in `internal/daemon/uiweb_test.go` to assert typed event consumption and absence of post-seed re-fetch hooks, not only `EventSource` presence
- [X] T023 [US1] Add redaction scan helpers for WebUI live proof in `internal/manager/server_liveconsole_test.go`
- [X] T024 [US1] Add WebUI authority-readonly test in `internal/manager/server_liveconsole_test.go` proving reducers do not call plan/apply/write paths and existing action buttons still call Manager API endpoints

**Checkpoint**: WebUI P1 is independently demonstrable as payload-driven live state for current panels.

---

## Phase 4: User Story 2 - Watch TUI update from live daemon payloads (Priority: P2)

**Goal**: TUI/watch dashboard updates terminal output from typed daemon events after one seed, with no interval polling while stream is healthy.

**Independent Test**: Run `go test ./internal/app -run 'TestTUILiveConsole'` and prove terminal output changes from events with zero post-seed overview/audit reads.

### Tests for User Story 2

- [X] T025 [P] [US2] Add TUI visible live-output tests in `internal/app/app_liveconsole_test.go` for environment, session/run, background, audit, denied audit, and terminal events
- [X] T026 [P] [US2] Add TUI no-polling and fallback tests in `internal/app/app_liveconsole_test.go` proving healthy streams do not use interval reads and closed streams mark degraded before daemon-less fallback

### Implementation for User Story 2

- [X] T027 [US2] Change `daemon.SubscribeEvents` in `internal/daemon/client.go` to return typed `liveconsole.Event` values instead of refresh signals
- [X] T028 [US2] Update daemon client tests in `internal/daemon/client_test.go` for typed event delivery and channel close semantics
- [X] T029 [US2] Refactor `app.tui` in `internal/app/app.go` to read one seed when daemon subscription succeeds, then call a typed live dashboard loop
- [X] T030 [US2] Implement TUI live dashboard loop in `internal/app/app.go` that applies events to `liveconsole.State` and renders without interval polling while stream is healthy
- [X] T031 [US2] Add terminal rendering from live console state in `internal/app/app.go`, preserving existing `--once` snapshot behavior and profile filtering
- [X] T032 [US2] Update existing `watchDashboard` tests in `internal/app/app_test.go` to cover daemon-less fallback only; typed daemon-stream behavior lives in `internal/app/app_liveconsole_test.go`

**Checkpoint**: TUI P2 is independently demonstrable as payload-driven terminal live state.

---

## Phase 5: User Story 3 - Trust the live view and diagnose stream health (Priority: P3)

**Goal**: Event catalog coverage, stream health, stale recovery, drift guards, multi-subscriber behavior, and evidence artifacts make the live view trustworthy.

**Independent Test**: Run `go test ./internal/liveconsole ./internal/daemon ./internal/manager ./internal/app -run 'Catalog|StreamHealth|Drift|Subscriber|Redact'`.

### Tests for User Story 3

- [X] T033 [P] [US3] Add catalog coverage tests in `internal/liveconsole/catalog_test.go` proving every current WebUI/TUI panel has at least one representative event kind and required fields
- [X] T034 [P] [US3] Add stream health tests in `internal/liveconsole/reducer_test.go` for schema mismatch, credential expiry terminal event, daemon restart/disconnect, event gap, and explicit re-seed recovery
- [X] T035 [P] [US3] Add multi-subscriber and backpressure tests in `internal/daemon/events_test.go` proving slow subscribers do not block daemon publishing or other subscribers
- [X] T036 [P] [US3] Add user-visible proof artifact tests in `internal/manager/server_liveconsole_test.go` and `internal/app/app_liveconsole_test.go` that record before/after output, read counts, and redaction scan results

### Implementation for User Story 3

- [X] T037 [US3] Implement catalog drift guard helpers in `internal/liveconsole/catalog.go` and `internal/liveconsole/catalog_test.go`
- [X] T038 [US3] Implement explicit stream health rendering/state propagation for WebUI and TUI in `internal/manager/server.go`, `internal/app/app.go`, and `internal/liveconsole/reducer.go`
- [X] T039 [US3] Ensure `scripts/test-live-console-smoke.sh` captures WebUI DOM proof, TUI terminal proof, schema validation, no-polling counters, and redaction scans

**Checkpoint**: All user stories are independently functional and the live console has trust/evidence coverage.

---

## Phase 6: Polish And Cross-Cutting

**Purpose**: Documentation, gates, cleanup, and final verification.

- [X] T040 [P] Update `docs/tui-webui-experience.md` so payload-driven panel state and end-to-end proof are implemented rather than deferred
- [X] T041 [P] Update `docs/STATUS.md` for the WebUI/TUI smoke surfaces and `hideoutd` row to reflect 007 live console completion
- [X] T042 [P] Update `docs/manager-control-plane.md` with the typed event catalog, seed-plus-event reducer model, and no-polling UI contract
- [X] T043 [P] Update `docs/privacy-run-test-plan.md` Gate 0 section to include live-console smoke, schema drift, WebUI/TUI visible proof, and redaction checks
- [X] T044 [P] Update `specs/007-daemon-live-operations-console/quickstart.md` if implementation test command names differ from plan
- [X] T045 Run `go test ./internal/liveconsole ./internal/daemon ./internal/manager ./internal/app` and fix failures in touched packages
- [X] T046 Run `scripts/test-live-console-smoke.sh` and `scripts/test-gate0.sh`, then fix any gate or smoke failures
- [X] T047 Run final battery: `go build ./...`, `go vet ./...`, `test -z "$(gofmt -l internal cmd)"`, `git diff --check`, `go test ./...`, `scripts/test-gate0.sh`, and `npx markdownlint-cli2 README.md "docs/**/*.md" "specs/007-daemon-live-operations-console/**/*.md"`

---

## Dependencies And Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: no dependencies.
- **Foundational (Phase 2)**: depends on Setup; blocks all user stories.
- **US1 WebUI (Phase 3)**: depends on Foundational; MVP.
- **US2 TUI (Phase 4)**: depends on Foundational; can run in parallel with US1 after `liveconsole.Event` and seed/reducer APIs settle.
- **US3 Trust/Health (Phase 5)**: depends on Foundational and integrates evidence from US1/US2, but most tests can be authored once reducer contracts exist.
- **Polish (Phase 6)**: depends on completed story slices.

### User Story Dependencies

- **US1 (P1)**: requires typed daemon events and shared reducer; no dependency on US2.
- **US2 (P2)**: requires typed daemon client and shared reducer; no dependency on WebUI implementation except shared fixtures.
- **US3 (P3)**: strengthens catalog and health behavior across both surfaces; depends on the shared reducer and should validate US1/US2 artifacts.

### Parallel Opportunities

- T002-T004 can run in parallel after T001 starts.
- T005-T007 can run in parallel before T008-T010 implementation.
- T015-T016 can run in parallel with T024-T025 after Foundational.
- T033-T036 can run in parallel after reducer and typed daemon stream exist.
- T040-T044 can run in parallel after implementation behavior stabilizes.

## Parallel Examples

### US1 WebUI

```text
Task: "T015 WebUI JavaScript harness test in internal/manager/server_liveconsole_test.go"
Task: "T016 WebUI stale/disconnected/redaction tests in internal/manager/server_liveconsole_test.go"
```

### US2 TUI

```text
Task: "T025 TUI visible live-output tests in internal/app/app_liveconsole_test.go"
Task: "T026 TUI no-polling and fallback tests in internal/app/app_liveconsole_test.go"
```

### US3 Trust/Health

```text
Task: "T033 catalog coverage tests in internal/liveconsole/catalog_test.go"
Task: "T034 stream health tests in internal/liveconsole/reducer_test.go"
Task: "T035 multi-subscriber tests in internal/daemon/events_test.go"
Task: "T036 user-visible proof artifact tests in internal/manager/server_liveconsole_test.go and internal/app/app_liveconsole_test.go"
```

## Implementation Strategy

### MVP First

1. Complete Setup and Foundational.
2. Complete US1 WebUI.
3. Validate with `go test ./internal/manager -run 'TestWebUILiveConsole'`.
4. Stop for review if needed; US1 alone proves the main 006 deferred browser slice.

### Incremental Delivery

1. Add shared catalog/reducer and typed daemon events.
2. Add WebUI payload-driven live state.
3. Add TUI payload-driven live state.
4. Add stream health, drift guards, multi-subscriber proof, docs, and Gate 0.

### Review Focus

- Hidden re-fetch or timer polling after seed is a blocking regression.
- Any event payload or proof artifact leaking control-plane material is blocking.
- Any reducer path executing authority or writing profile/run/backend state is blocking.
- Docs must not continue to describe payload-driven live state as deferred once implementation is complete.
