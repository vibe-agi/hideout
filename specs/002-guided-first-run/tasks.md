<!-- markdownlint-disable MD013 -->

# Tasks: Tool Model Cleanup

**Input**: Design documents from `/specs/002-guided-first-run/`

**Prerequisites**: `plan.md`, `spec.md`, `research.md`, `data-model.md`,
`contracts/tool-model-cleanup.md`, `quickstart.md`, `.specify/memory/constitution.md`

**Tests**: Required. This feature touches profile validation, app command
parsing, schema validation, Manager/profile summaries, Lima tool setup removal,
diagnostic evidence, and docs/status gates.

**Organization**: Tasks are grouped by user story so each story remains
independently implementable and testable. US1 is the MVP because it removes the
old authority path.

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Establish cleanup-specific validation helpers and make the old
tool-supply vocabulary auditable during implementation.

- [X] T001 Add `scripts/test-tool-model-cleanup.sh` with strict shell mode, repository scans for live npm/provider/preset product surfaces, and the contract allowlist for negative tests, unsupported diagnostics, 002 removal prose, and inert history in `scripts/test-tool-model-cleanup.sh`
- [X] T002 [P] Add shared test fixtures for legacy `tools.npmGlobals`, legacy `tools.presets`, mixed legacy/new fields, and valid `tools.expectedCommands` profiles in `internal/profile/testdata/tool_model/`
- [X] T003 [P] Add shared assertions for unsupported legacy tool-supply diagnostics and no-install wording in `internal/app/app_test.go`

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Define the replacement profile/schema vocabulary before removing
old execution paths.

**Critical**: No user story implementation should start until the
expected-command shape and legacy rejection behavior are testable.

### Tests First

- [X] T004 [P] Add profile validation tests for valid `tools.expectedCommands`, malformed command names, path-like names, argument-bearing names, and mixed legacy/new tool fields in `internal/profile/profile_test.go`
- [X] T005 [P] Add schema validation tests or fixtures for valid `tools.expectedCommands` and rejected legacy `npmGlobals`/`presets` fields in `schemas/profile.schema.json`
- [X] T006 [P] Add init-plan schema tests or fixture expectations proving `tools.npm-global.add` is no longer an accepted task kind in `schemas/init-plan.schema.json`

### Implementation

- [X] T007 Replace `Tools.Presets` and `Tools.NPMGlobals` profile storage with `Tools.ExpectedCommands` diagnostic storage in `internal/profile/profile.go`
- [X] T008 Implement conservative expected-command name validation and explicit legacy-field rejection diagnostics in `internal/profile/profile.go`
- [X] T009 Update profile schema to accept `tools.expectedCommands` and reject `tools.npmGlobals`/`tools.presets` in `schemas/profile.schema.json`
- [X] T010 Update init-plan schema to remove `tools.npm-global.add` and any preset/package-manager task kind from `schemas/init-plan.schema.json`

**Checkpoint**: The profile and schema model now accepts expected commands and
rejects old installer fields before user-story implementation proceeds.

---

## Phase 3: User Story 1 - Remove Package-Manager Tool Authority (Priority: P1) MVP

**Goal**: npm/provider/preset tool installation is no longer reachable through
CLI, profile, schema, init tasks, Manager summaries, Lima backend setup, or
WebUI/API payloads.

**Independent Test**: Old npm/provider/preset inputs fail closed with a clear
unsupported legacy tool-supply diagnostic and no package-manager execution,
profile mutation, backend preparation, or environment setup.

### Tests for User Story 1

- [X] T011 [P] [US1] Add app tests proving `hideout init --npm-package`, `hideout init --npm-command`, `hideout doctor --fix --npm-package`, and `hideout doctor --fix --tool-preset` fail with unsupported legacy diagnostics in `internal/app/app_test.go`
- [X] T012 [P] [US1] Add app tests proving `hideout profile tools <name> npm add` and `hideout profile tools <name> preset add` fail without mutating profile JSON in `internal/app/app_test.go`
- [X] T013 [P] [US1] Add InitTask tests proving `ToolPresets`, `NPMGlobals`, and `tools.npm-global.add` are not planned or applied in `internal/inittask/inittask_test.go`
- [X] T014 [P] [US1] Add Lima backend tests proving tool presets and npm global tools are not resolved into guest provisioning/configuration in `internal/backend/lima/lima_test.go`
- [X] T015 [P] [US1] Add Manager/API tests proving profile summaries no longer expose `toolPresets` or `npmGlobals` as live setup state in `internal/manager/api_test.go`
- [X] T016 [P] [US1] Add WebUI smoke tests or static assertions proving setup forms and profile cards do not submit or render npm globals/tool presets in `internal/manager/server.go`

### Implementation for User Story 1

- [X] T017 [US1] Remove npm/package/preset flags from init and doctor help/success examples while keeping legacy flag recognition only to emit unsupported legacy diagnostics in `internal/app/app.go`
- [X] T018 [US1] Replace `profile tools npm` and `profile tools preset` subcommands with unsupported legacy diagnostics or removed-command handling in `internal/app/app.go`
- [X] T019 [US1] Remove npm global and tool preset planning/apply logic from InitTask normalization, validation, task creation, and apply paths in `internal/inittask/inittask.go`
- [X] T020 [US1] Remove Lima `ToolPreset`, `NPMGlobalTool`, `ResolveToolPresets`, and related provisioning inputs from backend configuration generation in `internal/backend/lima/lima.go`
- [X] T021 [US1] Remove tool preset/npm global fields from Manager profile summaries, API request/response structs, run environment fingerprinting, and run dataplane audit details in `internal/manager/manager.go`, `internal/manager/api.go`, `internal/manager/run_environment.go`, and `internal/manager/run_dataplane.go`
- [X] T022 [US1] Remove npm global setup form logic, npm rendering helpers, and tool preset display rows from WebUI code in `internal/manager/server.go`
- [X] T023 [US1] Update app tests that previously expected npm/preset setup success so they assert unsupported legacy diagnostics instead in `internal/app/app_test.go`
- [X] T024 [US1] Update manager/profile/backend tests that previously expected default `base-dev`/`node-dev` presets or npm global setup so they assert expected-command or no-tool-provider behavior in `internal/manager/manager_test.go`, `internal/profile/profile_test.go`, and `internal/backend/lima/lima_test.go`

**Checkpoint**: US1 is independently complete when old npm/provider/preset
surfaces fail closed and no live product path can install or provision tools.

---

## Phase 4: User Story 2 - Declare Expected Commands For Diagnosis (Priority: P2)

**Goal**: Operators can declare expected guest commands for diagnostics and
readiness evidence without requesting installation or repair.

**Independent Test**: Valid expected-command declarations validate, appear in
diagnostic output as expectations, report present/missing/not-checkable status
from controlled check contexts, and never invoke package managers, setup
providers, or host commands.

### Tests for User Story 2

- [X] T025 [P] [US2] Add app tests for `hideout profile tools <name> expected add`, `remove`, and `list` behavior in `internal/app/app_test.go`
- [X] T026 [P] [US2] Add Manager tests for expected-command diagnostic states `present`, `missing`, `not-checkable`, and `blocked` using controlled check contexts in `internal/manager/manager_test.go`
- [X] T027 [P] [US2] Add run/app tests proving a missing expected command required for the target command fails closed without host fallback or package-manager setup in `internal/app/app_test.go`
- [X] T028 [P] [US2] Add environment fingerprint tests proving expected-command declarations affect readiness/fingerprint input as expectations, not provisioning results, in `internal/manager/run_environment_test.go`
- [X] T029 [P] [US2] Add run/app non-regression tests proving an already-present target command still runs after tool-model cleanup in `internal/app/app_test.go`

### Implementation for User Story 2

- [X] T030 [US2] Implement `profile tools expected add|remove|list` command parsing and profile plan/apply behavior in `internal/app/app.go`
- [X] T031 [US2] Add expected-command diagnostic data structures and present/missing/not-checkable/blocked result construction from controlled check contexts in `internal/manager/manager.go`
- [X] T032 [US2] Surface expected-command diagnostics in profile summary/API output without installer claims in `internal/manager/api.go`
- [X] T033 [US2] Include expected-command declarations in environment fingerprint input and remove npm global package coordinates from the fingerprint shape in `internal/manager/run_environment.go`
- [X] T034 [US2] Render expected-command diagnostics in TUI/WebUI profile summaries without npm/preset wording in `internal/app/app.go` and `internal/manager/server.go`
- [X] T035 [US2] Ensure target command readiness derives required-command failure from the requested target command and expected-command diagnostics, never from a per-command required flag, and never installs or repairs in `internal/app/app.go`

**Checkpoint**: US2 is independently complete when expected commands can be
declared, diagnosed, and used as readiness/fingerprint input without any tool
materialization.

---

## Phase 5: User Story 3 - Keep Documentation And Status Honest (Priority: P3)

**Goal**: Specs, docs, status files, user-facing help, and validation scripts
all describe the same diagnostic-only tool model.

**Independent Test**: Repository scans find no live npm/provider/preset
tool-supply product paths outside removal notes, migration diagnostics, or
negative tests.

### Tests for User Story 3

- [X] T036 [P] [US3] Extend `scripts/test-tool-model-cleanup.sh` to fail on live `npmGlobals`, `npm-global`, `tools.presets`, `tool preset`, `npm-package`, `npm-command`, and provider-execution references outside the contract allowlist in `scripts/test-tool-model-cleanup.sh`
- [X] T037 [P] [US3] Add docs/status scan assertions for expected-command vocabulary and absence of package-manager setup instructions in `scripts/test-tool-model-cleanup.sh`

### Implementation for User Story 3

- [X] T038 [US3] Replace the full Tool supply ownership-split/in-progress wording and related `(002)` transition notes with the final cleanup status once provider execution/storage/subcommands are removed in `docs/STATUS.md`
- [X] T039 [P] [US3] Update public English and Chinese setup text to avoid npm/provider/preset installation instructions and point materialization to base images or in-boundary setup runs in `README.md` and `README.zh-CN.md`
- [X] T040 [P] [US3] Update architecture and product docs to remove stale package-manager/provider wording, define expected-command diagnostics as string arrays, and update the PRD profile tools CLI/Required editors blocks for `expected add|remove|list` in `docs/privacy-run-design.md`, `docs/architecture-principles.md`, and `docs/distribution-bootstrap.md`
- [X] T041 [P] [US3] Update test-plan/status docs to list `scripts/test-tool-model-cleanup.sh` as the cleanup evidence and remove guided-first-run onboarding claims from 002 in `docs/privacy-run-test-plan.md` and `docs/STATUS.md`
- [X] T042 [US3] Update 002 spec artifacts only for wording/example drift while preserving `tools.expectedCommands` as the canonical profile spelling in `specs/002-guided-first-run/spec.md`, `specs/002-guided-first-run/data-model.md`, and `specs/002-guided-first-run/contracts/tool-model-cleanup.md`

**Checkpoint**: US3 is independently complete when docs/status/help/scans tell
the same story as code.

---

## Phase 6: Polish & Cross-Cutting Concerns

**Purpose**: Validate the cleanup end to end and remove incidental drift.

- [X] T043 [P] Run `go test ./...` from `/Users/null/Code/ym/nodejs/node-v26.4.0` and fix any test fallout in touched Go packages
- [X] T044 [P] Run `scripts/test-gate0.sh` from `/Users/null/Code/ym/nodejs/node-v26.4.0` and fix schema/install/package smoke fallout
- [X] T045 [P] Run `scripts/test-tool-model-cleanup.sh` from `/Users/null/Code/ym/nodejs/node-v26.4.0` and update allowlists only for explicit negative tests or removal notes
- [X] T046 [P] Run `markdownlint-cli2 README.md README.zh-CN.md docs specs/002-guided-first-run` from `/Users/null/Code/ym/nodejs/node-v26.4.0` and fix markdown issues
- [X] T047 Review `rg -n "npmGlobals|npm-global|npm package|npm-package|npmCommand|npm-command|tools\\.presets|tool preset|package-manager provider|provider execution" .` output against the contract allowlist and document any intentional residual negative-test or removal-note hits in `specs/002-guided-first-run/quickstart.md`
- [X] T048 Update generated schema fixtures or API snapshots affected by removing `toolPresets`/`npmGlobals` in `schemas/` and `internal/manager/`

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies; can start immediately.
- **Foundational (Phase 2)**: Depends on Setup; blocks user stories because it
  defines the replacement profile/schema model.
- **US1 (Phase 3)**: Depends on Foundational; MVP.
- **US2 (Phase 4)**: Depends on Foundational; can proceed in parallel with US1
  only after agreed shared profile structs exist, but should land after US1 to
  avoid preserving compatibility accidentally.
- **US3 (Phase 5)**: Depends on US1 and US2 implementation details for final
  wording, but tests/scans can be prepared earlier.
- **Polish (Phase 6)**: Depends on desired stories being complete.

### User Story Dependencies

- **US1 Remove Package-Manager Tool Authority**: MVP; no dependency on US2/US3
  after Foundational.
- **US2 Declare Expected Commands For Diagnosis**: Depends on Foundational and
  should not reintroduce any US1-removed installer path.
- **US3 Keep Documentation And Status Honest**: Depends on actual final code
  spelling from US1/US2.

### Within Each User Story

- Write tests first and confirm they fail for the old behavior.
- Remove authority/execution paths before updating user-facing status to
  "done".
- Keep expected-command diagnostics separate from installer/provider logic.
- Validate each checkpoint before moving to the next story.

### Parallel Opportunities

- T002 and T003 can run in parallel.
- T004, T005, and T006 can run in parallel.
- T011 through T016 can run in parallel because they touch separate packages or
  surfaces.
- T025 through T029 can run in parallel after the shared profile shape is
  agreed.
- T039, T040, and T041 can run in parallel after code behavior is settled.
- T043 through T046 can run in parallel after implementation is complete.

---

## Parallel Example: User Story 1

```bash
Task: "T011 [P] [US1] Add app tests proving removed init/doctor flags fail in internal/app/app_test.go"
Task: "T013 [P] [US1] Add InitTask tests proving npm task kinds are not planned in internal/inittask/inittask_test.go"
Task: "T014 [P] [US1] Add Lima backend tests proving presets are not resolved in internal/backend/lima/lima_test.go"
Task: "T015 [P] [US1] Add Manager/API tests proving old summary fields are gone in internal/manager/api_test.go"
```

## Parallel Example: User Story 2

```bash
Task: "T025 [P] [US2] Add profile tools expected CLI tests in internal/app/app_test.go"
Task: "T026 [P] [US2] Add diagnostic state tests in internal/manager/manager_test.go"
Task: "T028 [P] [US2] Add fingerprint input tests in internal/manager/run_environment_test.go"
Task: "T029 [P] [US2] Add existing target command non-regression tests in internal/app/app_test.go"
```

---

## Implementation Strategy

### MVP First (US1 Only)

1. Complete Phase 1 setup and Phase 2 profile/schema foundation.
2. Complete US1 removal tasks.
3. Validate old npm/provider/preset inputs fail closed and no installation path
   remains live.
4. Stop and review before adding expected-command diagnostics.

### Incremental Delivery

1. Foundation: expected-command schema shape and legacy rejection.
2. US1: remove old authority path.
3. US2: add diagnostic expected-command UX and evidence.
4. US3: align docs/status/scans.
5. Polish: run tests, Gate 0, markdownlint, and cleanup scan.

### Scope Guard

- Do not add package-manager installation, provider execution, base-image
  builder UX, named/global environment creation, daemon mode, TUI/WebUI
  onboarding, marketplace trust, or product-specific agent setup in this
  feature.
