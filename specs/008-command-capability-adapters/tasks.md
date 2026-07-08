# Tasks: Command Capability Adapters

<!-- markdownlint-disable MD013 -->

**Input**: Design documents from `specs/008-command-capability-adapters/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

**Tests**: Required. This feature crosses command proxy, scripts, profile state, broker routing, audit, Manager, and evidence boundaries.

**Organization**: Tasks are grouped by user story so each story can be implemented and tested independently.

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Create feature-owned package and static contract placeholders.

- [X] T001 Create `internal/cmdadapter/doc.go`, `internal/cmdadapter/profile.go`, `internal/cmdadapter/outcome.go`, `internal/cmdadapter/evaluator.go`, `internal/cmdadapter/rootsensitive.go`, and `internal/cmdadapter/evidence.go`
- [X] T002 [P] Create `schemas/command-adapter.schema.json` for adapter profile and outcome fixture validation
- [X] T003 [P] Add command-adapter schema registration placeholder in `scripts/test-gate0.sh`
- [X] T004 [P] Create `scripts/test-command-adapter-smoke.sh` smoke script skeleton with usage and no-op feature check

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Shared model, validation, and routing foundations that block all user stories.

**CRITICAL**: No user story work can begin until this phase is complete.

- [X] T005 Add `CommandAdapters` profile fields and validation hooks in `internal/profile/profile.go`
- [X] T006 [P] Add profile schema coverage for `commandAdapters` in `schemas/profile.schema.json`
- [X] T007 [P] Implement adapter profile validation tests for IDs, command names, digest format, enabled-state rules, and duplicate command ownership in `internal/cmdadapter/profile_test.go`
- [X] T008 Implement adapter profile validation and command ownership compilation in `internal/cmdadapter/profile.go`
- [X] T009 [P] Implement strict adapter outcome contract tests for deny, simulate, rewriteGuest, proposeCapability, unknown fields, and undeclared capabilities in `internal/cmdadapter/outcome_test.go`
- [X] T010 Implement strict adapter outcome decoding, validation, and redaction-safe summaries in `internal/cmdadapter/outcome.go` and `internal/cmdadapter/evidence.go`
- [X] T011 Extend `internal/cmdproxy/cmdproxy.go` registration model to distinguish host-open owners from adapter owners while preserving simple shim names
- [X] T012 [P] Add command proxy registry tests for default `open` compatibility, adapter-owned symbols, alias conflicts, and duplicate owner rejection in `internal/cmdproxy/cmdproxy_test.go`
- [X] T013 Add policy evaluator entrypoint tests for `decideCommandAdapter` context restrictions and sandbox-denied file/network/process attempts in `internal/policy/policy_test.go`
- [X] T014 Extend `internal/policy/policy.go` with a command-adapter script runner that reuses existing Goja limits and strict JSON decoding

**Checkpoint**: Foundation ready. Adapter config can be validated, command ownership can compile, and adapter JS can run only through the constrained runtime.

---

## Phase 3: User Story 1 - Route Commands Through Adapters (Priority: P1) MVP

**Goal**: Registered command symbols route through enabled adapters, validated outcomes control the broker response, invalid outcomes fail closed, and unregistered/default commands keep existing behavior.

**Independent Test**: Configure one local adapter for `tool-x`, invoke `tool-x --version`, verify adapter outcome/audit, and verify unregistered commands plus default `open` behavior remain unchanged.

### Tests for User Story 1

- [X] T015 [P] [US1] Add broker adapter deny/simulate/rewrite/proposal positive path tests in `internal/broker/broker_adapter_test.go`
- [X] T016 [P] [US1] Add broker fail-closed tests for missing adapter, digest mismatch, script throw, timeout, invalid output, and unsafe rewrite in `internal/broker/broker_adapter_test.go`
- [X] T017 [P] [US1] Add default `host.open` compatibility regression tests in `internal/broker/broker_test.go`
- [X] T018 [P] [US1] Add adapter audit redaction tests for token-shaped values, `HIDEOUT_SECRET_*`, generated IDs, simulated output, and proposal resources in `internal/cmdadapter/evidence_test.go`

### Implementation for User Story 1

- [X] T019 [US1] Add adapter fields to `broker.Server`, request context construction, and verified adapter lookup in `internal/broker/broker.go`
- [X] T020 [US1] Implement registered adapter command routing before target command execution in `internal/broker/broker.go`
- [X] T021 [US1] Implement deny, simulate, rewriteGuest, and non-applied proposeCapability broker responses in `internal/broker/broker.go`
- [X] T022 [US1] Ensure unsafe adapter failures deny before side effects and emit audit through existing `auditContext` redaction in `internal/broker/broker.go`
- [X] T023 [US1] Materialize adapter-owned command shims with existing `hideout-shim` path in `internal/manager/run_dataplane.go`
- [X] T024 [US1] Extend `cmd/hideout-shim/main.go` request handling only if needed to preserve raw argv/cwd for adapter context
- [X] T025 [US1] Add manager run plan wiring so broker receives compiled adapter config in `internal/manager/run_plan.go` and `internal/manager/run_apply.go`

**Checkpoint**: US1 is independently functional and testable as the MVP.

---

## Phase 4: User Story 2 - Capture Root-Sensitive Intent (Priority: P2)

**Goal**: Built-in root-sensitive adapter classifies common privileged command attempts as intent capture, denies or proposes bounded non-applied capability intent, and never claims 008 blocks root.

**Independent Test**: Enable the built-in adapter, invoke representative root-sensitive command names, and verify classification, deny/proposal behavior, intent-only evidence, and no root-containment claim.

### Tests for User Story 2

- [X] T026 [P] [US2] Add root-sensitive classification tests for escalation, package-manager, network mutation, resolver, service-manager, mount, and system-management commands in `internal/cmdadapter/rootsensitive_test.go`
- [X] T027 [P] [US2] Add tests proving root-sensitive adapters cannot simulate successful system mutation in `internal/cmdadapter/outcome_test.go`
- [X] T028 [P] [US2] Add evidence wording tests that reject root-blocking claims and require intent-only or unknown separation status in `internal/cmdadapter/evidence_test.go`
- [X] T029 [P] [US2] Add native command-name smoke coverage for built-in root-sensitive intent capture through shim/broker/audit in `scripts/test-command-adapter-smoke.sh`
- [X] T030 [P] [US2] Add docs/status scan test or gate0 check rejecting 008 root-containment overclaims in `scripts/test-gate0.sh`

### Implementation for User Story 2

- [X] T031 [US2] Implement built-in root-sensitive adapter source or Go-embedded adapter provider in `internal/cmdadapter/rootsensitive.go`
- [X] T032 [US2] Register root-sensitive built-in adapter profile materialization and digest reporting in `internal/cmdadapter/profile.go`
- [X] T033 [US2] Validate root-sensitive intent payload categories and bounded argv summaries in `internal/cmdadapter/rootsensitive.go`
- [X] T034 [US2] Enforce root-sensitive no-successful-system-mutation simulation in `internal/cmdadapter/outcome.go`
- [X] T035 [US2] Surface intent-only separation status in broker audit and local decision summaries in `internal/broker/broker.go` and `internal/cmdadapter/evidence.go`

**Checkpoint**: US2 is independently functional without claiming a root boundary.

---

## Phase 5: User Story 3 - Enable Local Adapters Safely (Priority: P3)

**Goal**: Operators can add, enable, disable, refresh, remove, list, and review local digest-pinned adapters through Manager/CLI surfaces without raw profile writes.

**Independent Test**: Add a local adapter through plan/apply, review digest/commands/capabilities, enable it, verify digest drift fails closed, refresh digest explicitly, and verify recent decisions are visible.

### Tests for User Story 3

- [X] T036 [P] [US3] Add Manager command-adapter plan/apply tests for add-local, enable, disable, refresh-digest, remove, duplicate command, and digest drift in `internal/manager/command_adapters_test.go`
- [X] T037 [P] [US3] Add CLI command-adapter tests for list/add-local/enable/disable/refresh-digest/remove output and errors in `internal/app/app_test.go`
- [X] T038 [P] [US3] Confirm no new Manager HTTP API routes are required for v1; Core plan/apply and CLI tests cover command-adapter management without expanding `internal/manager/api.go`
- [X] T039 [P] [US3] Add TUI/WebUI decision visibility tests or reducer fixtures for adapter config and recent decisions in `internal/manager/server_liveconsole_test.go`
- [X] T040 [P] [US3] Add schema tests for adapter profile and adapter outcome fixtures in `schemas/command-adapter.schema.json` and `schemas/profile.schema.json`

### Implementation for User Story 3

- [X] T041 [US3] Implement `Core.PlanCommandAdapter` and `Core.ApplyCommandAdapter` in `internal/manager/command_adapters.go`
- [X] T042 [US3] Keep command-adapter management on Core plan/apply plus CLI for v1; do not add `internal/manager/api.go` routes without a consumer
- [X] T043 [US3] Add CLI dispatch `hideout profile command-adapter` in `internal/app/app.go`
- [X] T044 [US3] Add local management summary fields for configured adapters and recent decisions in `internal/manager/manager.go` and `internal/manager/boundary_summary.go`
- [X] T045 [US3] Add WebUI/TUI display wiring for adapter configuration and recent decisions in `internal/manager/server.go` and `internal/app/app.go`

**Checkpoint**: US3 is independently functional and uses typed plan/apply for adapter enablement.

---

## Phase 6: Polish & Cross-Cutting Concerns

**Purpose**: Documentation, gates, smoke, and final verification.

- [X] T046 [P] Update `docs/script-extension-architecture.md` with adapter ABI, JS restrictions, and local-only ecosystem scope
- [X] T047 [P] Update `docs/privacy-run-design.md`, `docs/threat-model.md`, and `docs/privacy-run-test-plan.md` with 008 claims, non-claims, root-intent wording, and required gates
- [X] T048 [P] Update `docs/manager-control-plane.md`, `docs/tui-webui-experience.md`, `docs/README.md`, and `docs/STATUS.md` with adapter management and current implementation status
- [X] T049 Wire `schemas/command-adapter.schema.json` and `scripts/test-command-adapter-smoke.sh` into `scripts/test-gate0.sh`
- [X] T050 Run quickstart scenario validation from `specs/008-command-capability-adapters/quickstart.md`
- [X] T051 Run final battery: `go build ./...`, `go vet ./...`, `gofmt -l internal cmd`, `git diff --check`, `go test ./...`, `scripts/test-gate0.sh`, and `scripts/test-command-adapter-smoke.sh`

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies.
- **Foundational (Phase 2)**: Depends on Setup and blocks all user stories.
- **US1 (Phase 3)**: Depends on Foundational. MVP.
- **US2 (Phase 4)**: Depends on Foundational and can run after or beside US1, but broker evidence integration is easier after US1.
- **US3 (Phase 5)**: Depends on Foundational and uses US1 runtime for end-to-end validation.
- **Polish (Phase 6)**: Depends on selected user stories being complete.

### User Story Dependencies

- **US1**: Required MVP. No dependency on US2 or US3.
- **US2**: Uses the adapter runtime from Foundational; product integration benefits from US1 broker routing.
- **US3**: Uses validation from Foundational and runtime behavior from US1.

### Parallel Opportunities

- T002, T003, and T004 can run in parallel.
- T006, T007, T009, T012, and T013 can run in parallel after T001.
- US1 test tasks T015 through T018 can run in parallel.
- US2 test tasks T026 through T030 can run in parallel.
- US3 test tasks T036 through T040 can run in parallel.
- Documentation tasks T046 through T048 can run in parallel once behavior is stable.

---

## Parallel Example: User Story 1

```bash
Task: "T015 [US1] Add broker adapter deny/simulate/rewrite/proposal positive path tests in internal/broker/broker_adapter_test.go"
Task: "T016 [US1] Add broker fail-closed tests for missing adapter, digest mismatch, script throw, timeout, invalid output, and unsafe rewrite in internal/broker/broker_adapter_test.go"
Task: "T017 [US1] Add default host.open compatibility regression tests in internal/broker/broker_test.go"
Task: "T018 [US1] Add adapter audit redaction tests for token-shaped values, HIDEOUT_SECRET_*, generated IDs, simulated output, and proposal resources in internal/cmdadapter/evidence_test.go"
```

---

## Implementation Strategy

### MVP First: US1

1. Complete Setup and Foundational tasks.
2. Implement US1 tests first and confirm they fail.
3. Implement adapter runtime routing through broker.
4. Validate default command proxy compatibility and fail-closed behavior.

### Incremental Delivery

1. US1: local adapter runtime MVP.
2. US2: built-in root-sensitive intent capture with honest non-claims.
3. US3: typed enablement and management surfaces.
4. Polish: docs, smoke, Gate 0, and full battery.

### Verification Contract

008 is complete only when:

- all tasks are checked;
- Gate 0 passes;
- all Go tests pass;
- command-adapter smoke passes;
- docs contain no claim that 008 blocks root escalation;
- default `open`/`xdg-open` behavior is unchanged unless explicitly reconfigured.
