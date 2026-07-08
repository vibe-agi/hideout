# Tasks: Operator Decision Center

<!-- markdownlint-disable MD013 -->

**Input**: Design documents from `specs/012-operator-decision-center/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

**Tests**: Required. This feature touches HostFS write authority, adapter proposals, export/share, Manager API, daemon events, UI/TUI, audit, and redaction.

**Organization**: Tasks are grouped by user story to enable independent implementation and testing of each story.

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Shared package, schemas, and smoke entrypoints

- [X] T001 Create `internal/decision/doc.go`, `internal/decision/types.go`, `internal/decision/store.go`, `internal/decision/redaction.go`, and `internal/decision/evidence.go`
- [X] T002 [P] Add `schemas/decision-record.schema.json` and `schemas/notice-record.schema.json`
- [X] T003 [P] Add initial `scripts/test-decision-center-smoke.sh`
- [X] T004 Register decision/notice schemas and decision-center smoke in `scripts/test-gate0.sh`

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Generic records and store that all stories depend on

**⚠️ CRITICAL**: No user story work can begin until this phase is complete

- [X] T005 [P] Add decision record validation/redaction tests in `internal/decision/types_test.go` for required fields, states, terminal transitions, forbidden public fields, and malformed kinds
- [X] T006 [P] Add notice record validation/redaction tests in `internal/decision/types_test.go` proving notices reject claim/approve/deny/timeout/provider fields
- [X] T007 [P] Add decision store atomic-write, write-failure, no-partial-file, list/filter, timeout ordering, and 100 decision/notice scale tests in `internal/decision/store_test.go`
- [X] T008 [P] Add decision evidence redaction tests in `internal/decision/evidence_test.go` for create, claim, apply, deny, timeout, stale-claim, notice create, and notice ack events
- [X] T009 Implement `Decision`, `Notice`, `Claim`, `Resolution`, `Preview`, source metadata, kind constants, and validation in `internal/decision/types.go`
- [X] T010 Implement deterministic preview/control-plane redaction helpers in `internal/decision/redaction.go`
- [X] T011 Implement file-backed decision/notice store with atomic writes, filters, state transitions, timeout scanning, and ack persistence in `internal/decision/store.go`
- [X] T012 Implement evidence event builders in `internal/decision/evidence.go`
- [X] T013 Add Manager Core decision center scaffolding in `internal/manager/decisions.go` with list/inspect/create/claim/resolve/ack methods and no provider side effects yet
- [X] T014 Update `schemas/manager-api.schema.json` with decision/notice record and response shapes

**Checkpoint**: Foundation ready - user story implementation can now begin.

---

## Phase 3: User Story 1 - Resolve Local Actionable Decisions (Priority: P1) 🎯 MVP

**Goal**: HostFS write decisions and adapter capability proposals appear in one actionable decision queue with claim/resolve semantics.

**Independent Test**: Stage HostFS write and adapter proposal fixtures, list generic decisions, claim one, resolve it, verify provider validation and stale-claim behavior.

### Tests for User Story 1

- [X] T015 [P] [US1] Add Manager decision list/inspect/claim/resolve tests in `internal/manager/decisions_test.go` for HostFS write decision positive path and stale duplicate claim
- [X] T016 [P] [US1] Add HostFS compatibility parity tests in `internal/manager/hostfs_write_test.go` proving generic decision route and `hostfs/write/*` route share one source of truth
- [X] T017 [P] [US1] Add adapter proposal admission tests in `internal/broker/broker_adapter_test.go` and `internal/manager/decisions_test.go` for undeclared, unpromoted, and promoted-provider proposal cases
- [X] T018 [P] [US1] Add Manager API contract tests in `internal/manager/api_test.go` for `GET /decisions`, inspect, claim, approve, deny, and stale-token failures
- [X] T019 [P] [US1] Add CLI tests in `internal/app/app_test.go` for `hideout decision list|inspect|claim|approve|deny`

### Implementation for User Story 1

- [X] T020 [US1] Adapt HostFS write staging to create/update `hostfs.write` decision records in `internal/manager/hostfs_write.go` and `internal/decision/store.go`
- [X] T021 [US1] Rework `PlanHostFSWrite`, `ClaimHostFSWrite`, `ApplyHostFSWrite`, `DiscardHostFSWrite`, `HostFSWriteStatus`, and timeout handling in `internal/manager/hostfs_write.go` as compatibility shims backed by `internal/decision`
- [X] T022 [US1] Add generic Manager decision claim/approve/deny provider dispatch for HostFS write in `internal/manager/decisions.go`
- [X] T023 [US1] Add adapter proposal decision creation path in `internal/broker/broker.go`, `internal/manager/run_session.go`, and `internal/manager/decisions.go` while preserving non-applied fail-closed behavior for unsupported providers
- [X] T024 [US1] Add Manager API decision routes in `internal/manager/api.go`
- [X] T025 [US1] Add CLI `hideout decision list|inspect|claim|approve|deny|watch` in `internal/app/app.go`
- [X] T026 [US1] Emit decision lifecycle audit and live operation events from `internal/manager/decisions.go`
- [X] T027 [US1] Add decision summaries and machine-readable decision/notice diagnostic status for doctor/future diagnostics in `internal/manager/manager.go`, `internal/manager/decisions.go`, and `internal/app/app.go`

**Checkpoint**: US1 delivers a usable MVP for actionable decisions.

---

## Phase 4: User Story 2 - Notices Are Not Fake Approvals (Priority: P2)

**Goal**: Privilege/background status appears as informational notices with acknowledgement only.

**Independent Test**: Emit privilege degraded and background status notices, list/ack them, and prove no claim/apply/deny semantics exist.

### Tests for User Story 2

- [X] T028 [P] [US2] Add notice validation tests in `internal/manager/decisions_test.go` proving privilege/background notices list, inspect, ack, and reject claim/apply/deny fields
- [X] T029 [P] [US2] Add privilege notice creation tests in `internal/manager/run_privilege_test.go` or `internal/privilege/evidence_test.go`
- [X] T030 [P] [US2] Add daemon background notice tests in `internal/daemon/background_test.go`
- [X] T031 [P] [US2] Add Manager API notice route tests in `internal/manager/api_test.go`
- [X] T032 [P] [US2] Add CLI notice tests in `internal/app/app_test.go`

### Implementation for User Story 2

- [X] T033 [US2] Add Manager notice create/list/inspect/ack methods in `internal/manager/decisions.go`
- [X] T034 [US2] Convert 009 privilege status observations into `privilege.status` notices in `internal/manager/run_apply.go` or the existing privilege evidence path
- [X] T035 [US2] Convert daemon background status changes into `background.status` notices in `internal/daemon/background.go` and Manager bridge code
- [X] T036 [US2] Add Manager API notice routes in `internal/manager/api.go`
- [X] T037 [US2] Add CLI `hideout notice list|inspect|ack` in `internal/app/app.go`
- [X] T038 [US2] Emit notice lifecycle audit/live events in `internal/manager/decisions.go`

**Checkpoint**: US2 makes warnings/status visible without approval semantics.

---

## Phase 5: User Story 3 - Share Evidence Only After Explicit Local Decision (Priority: P3)

**Goal**: Share/leaving-machine export uses the decision center; pure local export remains synchronous.

**Independent Test**: Pure local export creates no decision, share export creates `evidence.share`, denial/timeout releases no artifact, approval releases after redaction/provider validation.

### Tests for User Story 3

- [X] T039 [P] [US3] Add export decision tests in `internal/export/export_test.go` proving local export stays synchronous and share export requires decision approval
- [X] T040 [P] [US3] Add Manager export/share decision tests in `internal/manager/export_test.go`
- [X] T041 [P] [US3] Add API tests in `internal/manager/api_test.go` proving share export decision cannot bypass redaction or claim/audit path
- [X] T042 [P] [US3] Add CLI tests in `internal/app/app_test.go` for share export create, claim, approve, deny, and timeout

### Implementation for User Story 3

- [X] T043 [US3] Extend export options/contracts for share/leaving-machine intent in `internal/export/artifact.go`, `internal/export/export.go`, and `internal/manager/export.go`
- [X] T044 [US3] Create `evidence.share` decisions during share export planning in `internal/manager/export.go`
- [X] T045 [US3] Add decision-approved share release path in `internal/manager/decisions.go` and `internal/export/export.go`
- [X] T046 [US3] Preserve pure local export behavior in `internal/app/app.go` and `internal/manager/api.go`
- [X] T047 [US3] Emit share export decision audit/live events in `internal/manager/export.go`

**Checkpoint**: US3 integrates export/share without regressing 005 local export.

---

## Phase 6: User Story 4 - Watch Decisions From CLI, TUI, And WebUI (Priority: P4)

**Goal**: CLI, TUI, and WebUI observe the same decision/notice state over Manager/daemon events without owning authority.

**Independent Test**: Two surfaces watch updates, one claims/resolves, all surfaces converge without leaking tokens or hidden paths.

### Tests for User Story 4

- [X] T048 [P] [US4] Add daemon event fan-out tests in `internal/daemon/events_test.go` for decision/notice create/update/terminal/ack and credential expiry
- [X] T049 [P] [US4] Add live console reducer tests in `internal/liveconsole/reducer_test.go` for decision and notice panels, stale claim, timeout, and ack states
- [X] T050 [P] [US4] Add WebUI JavaScript reducer runtime test in `internal/manager/server_liveconsole_test.go` proving no claim token or control-plane secret renders
- [X] T051 [P] [US4] Add TUI render/watch tests in `internal/app/app_liveconsole_test.go`
- [X] T052 [P] [US4] Add decision-center smoke coverage in `scripts/test-decision-center-smoke.sh`

### Implementation for User Story 4

- [X] T053 [US4] Add decision/notice event kinds and payloads in `internal/daemon/events.go`
- [X] T054 [US4] Add live console decision/notice reducer state in `internal/liveconsole/`
- [X] T055 [US4] Add WebUI decision/notice panels and EventSource update handling in `internal/manager/server.go`
- [X] T056 [US4] Add TUI decision/notice panels and watch integration in `internal/app/app.go`
- [X] T057 [US4] Add Manager overview decision/notice summaries consumed by UI surfaces in `internal/manager/manager.go`
- [X] T058 [US4] Ensure watch/event output uses redacted records and never emits claim tokens in `internal/decision/redaction.go` and `internal/daemon/events.go`

**Checkpoint**: US4 exposes the center across local operator surfaces.

---

## Phase 7: Polish & Cross-Cutting Concerns

**Purpose**: Docs, schemas, smoke, and final verification

- [X] T059 [P] Update `docs/STATUS.md` to mark operator decision center implemented and distinguish decisions from notices
- [X] T060 [P] Update `docs/privacy-run-design.md` and `docs/manager-control-plane.md` with decision/notice contracts and Manager route semantics
- [X] T061 [P] Update `docs/threat-model.md` with non-claims: no remote approvals, no org roles, no daemon implicit approval, notices are not grants
- [X] T062 [P] Update `docs/privacy-run-test-plan.md` with decision-center Gate 0 smoke and no-real-Lima requirement
- [X] T063 [P] Update `docs/tui-webui-experience.md`, `docs/README.md`, and `README.md` with decision/notice surfaces
- [X] T064 [P] Add schema validation coverage for `schemas/decision-record.schema.json`, `schemas/notice-record.schema.json`, and `schemas/manager-api.schema.json`
- [X] T065 Run `gofmt -l internal cmd` and fix formatting issues
- [X] T066 Run `go build ./...` and `go vet ./...`
- [X] T067 Run `go test ./...`
- [X] T068 Run `scripts/test-decision-center-smoke.sh`
- [X] T069 Run `scripts/test-gate0.sh`
- [X] T070 Run `npx --yes markdownlint-cli2 README.md 'docs/**/*.md' 'specs/012-operator-decision-center/**/*.md'`
- [X] T071 Run `git diff --check`
- [X] T072 Confirm 012 task completion count and commit only 012 changes

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies.
- **Foundational (Phase 2)**: Depends on Setup and blocks all stories.
- **US1 (Phase 3)**: Depends on Foundational; MVP.
- **US2 (Phase 4)**: Depends on Foundational; can proceed after basic notice store exists, but docs should preserve decision/notice split.
- **US3 (Phase 5)**: Depends on US1 decision provider dispatch.
- **US4 (Phase 6)**: Depends on US1/US2 record APIs and events.
- **Polish**: Depends on desired stories complete.

### User Story Dependencies

- **US1**: Independent MVP after Foundational.
- **US2**: Independent from HostFS apply; needs foundational notice store.
- **US3**: Uses US1 decision lifecycle for share/export.
- **US4**: Observes US1/US2/US3 state through existing daemon/live-console substrate.

### Parallel Opportunities

- Setup schema/smoke tasks T002-T003 can run in parallel.
- Foundational tests T005-T008 can run in parallel.
- US1 tests T015-T019 can run in parallel.
- US2 tests T028-T032 can run in parallel.
- US3 tests T039-T042 can run in parallel.
- US4 tests T048-T052 can run in parallel.
- Docs tasks T059-T063 can run in parallel after behavior stabilizes.

## Parallel Example: User Story 1

```text
Task: "T015 [US1] Add Manager decision list/inspect/claim/resolve tests in internal/manager/decisions_test.go"
Task: "T016 [US1] Add HostFS compatibility parity tests in internal/manager/hostfs_write_test.go"
Task: "T017 [US1] Add adapter proposal admission tests in internal/broker/broker_adapter_test.go and internal/manager/decisions_test.go"
Task: "T018 [US1] Add Manager API contract tests in internal/manager/api_test.go"
Task: "T019 [US1] Add CLI tests in internal/app/app_test.go"
```

## Implementation Strategy

### MVP First

1. Complete Setup and Foundational.
2. Complete US1 HostFS/generic decision lifecycle.
3. Validate US1 independently with Manager, API, CLI, and HostFS compatibility tests.
4. Only then add notices, export/share, and UI/watch layers.

### Incremental Delivery

1. US1: Generic actionable decisions plus HostFS compatibility.
2. US2: Notice class and ack semantics.
3. US3: Share/leaving-machine export decisions.
4. US4: CLI/TUI/WebUI watch and panels.
5. Polish: docs, schemas, smoke, Gate 0.
