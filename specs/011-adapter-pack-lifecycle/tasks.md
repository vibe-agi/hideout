# Tasks: Adapter Pack Lifecycle And Local Registry

<!-- markdownlint-disable MD013 -->

**Input**: Design documents from `specs/011-adapter-pack-lifecycle/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

**Tests**: Required. This feature touches scripts, profile authority, Manager
plan/apply, audit/export evidence, and command routing.

**Organization**: Tasks are grouped by user story to enable independent implementation and testing.

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Establish files, schemas, and smoke entry points.

- [X] T001 Create `internal/adapterpack/doc.go`, `internal/adapterpack/types.go`, `internal/adapterpack/manifest.go`, `internal/adapterpack/source.go`, `internal/adapterpack/registry.go`, `internal/adapterpack/test.go`, and `internal/adapterpack/evidence.go`
- [X] T002 [P] Add `schemas/adapter-pack-manifest.schema.json` and `schemas/adapter-pack-registry.schema.json`
- [X] T003 [P] Add initial smoke script `scripts/test-adapter-pack-smoke.sh`
- [X] T004 Register adapter-pack schema and smoke script in `scripts/test-gate0.sh`

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Core pack model and validation that MUST exist before stories.

**CRITICAL**: No user story work can begin until this phase is complete.

- [X] T005 [P] Add adapter pack manifest/type validation tests in `internal/adapterpack/manifest_test.go` for ids, versions, adapter ids, commands, entrypoints, script paths, capabilities, and test vector references
- [X] T006 [P] Add adapter pack source/digest tests in `internal/adapterpack/source_test.go` for local source lock, exact git commit acceptance, floating ref rejection, submodule non-recursion, and tree digest stability
- [X] T007 [P] Add adapter pack registry atomic-write, write-failure, no-partial-file, and schema validation tests in `internal/adapterpack/registry_test.go`
- [X] T008 [P] Add adapter pack evidence redaction and evidence-writer failure tests in `internal/adapterpack/evidence_test.go`
- [X] T009 Implement core data types and manifest validation in `internal/adapterpack/types.go` and `internal/adapterpack/manifest.go`
- [X] T010 Implement local source locking, digest calculation, and exact-commit git source checks in `internal/adapterpack/source.go`
- [X] T011 Implement file-backed registry load/save with atomic writes in `internal/adapterpack/registry.go`
- [X] T012 Implement lifecycle evidence shaping and deterministic redaction integration in `internal/adapterpack/evidence.go`
- [X] T013 Extend `profile.CommandAdapter` with optional pack id, revision id, pack adapter id, and lock digest fields in `internal/profile/profile.go`
- [X] T014 Extend profile validation and `schemas/profile.schema.json` for pack-backed bindings in `internal/profile/profile.go` and `schemas/profile.schema.json`
- [X] T015 Add pack-backed compile tests in `internal/cmdadapter/profile_test.go` proving existing local and built-in adapters still compile unchanged
- [X] T016 Extend `internal/cmdadapter/profile.go` to resolve pack-backed bindings into the existing `RuntimeAdapter` shape while preserving local path and built-in compatibility

**Checkpoint**: Foundation ready - user story implementation can now begin.

---

## Phase 3: User Story 1 - Install And Enable A Locked Adapter Pack (Priority: P1) MVP

**Goal**: Install a pack into a store-wide registry, keep it authority-free until explicit profile enablement, then route one profile through an exact pinned revision.

**Independent Test**: Install a valid pack into a fresh store, verify it is listed but inactive, enable one profile with a pinned revision, and verify only that profile routes the command through the pack.

### Tests for User Story 1

- [X] T017 [P] [US1] Add registry install/list tests in `internal/adapterpack/registry_test.go` proving install creates lock evidence and changes no profile authority
- [X] T018 [P] [US1] Add Manager install/list/inspect contract tests in `internal/manager/adapter_packs_test.go`
- [X] T019 [P] [US1] Add Manager enable tests in `internal/manager/adapter_packs_test.go` proving profile binding pins pack id, revision id, adapter id, commands, and capabilities
- [X] T020 [P] [US1] Add runtime routing test in `internal/broker/broker_adapter_test.go` proving only the enabled profile uses the pack-backed adapter
- [X] T021 [P] [US1] Add CLI smoke coverage for install/list/enable/run in `scripts/test-adapter-pack-smoke.sh`

### Implementation for User Story 1

- [X] T022 [US1] Implement pack install/list/inspect Manager plan/read types in `internal/manager/adapter_packs.go`
- [X] T023 [US1] Implement pack enable Manager plan/apply with exact revision pinning in `internal/manager/adapter_packs.go`
- [X] T024 [US1] Add Manager API routes for adapter-pack install/list/inspect/enable in `internal/manager/api.go`
- [X] T025 [US1] Add CLI commands for `hideout adapter-pack install|list|inspect|enable` in `internal/app/app.go`
- [X] T026 [US1] Integrate profile pack bindings into run data-plane adapter compilation in `internal/manager/run_dataplane.go`
- [X] T027 [US1] Add adapter pack registry summaries to Manager overview/TUI/WebUI data in `internal/manager/manager.go`, `internal/manager/server.go`, and `internal/app/app.go`
- [X] T028 [US1] Update `schemas/manager-api.schema.json` with adapter pack lifecycle response shapes

**Checkpoint**: User Story 1 is independently functional and testable.

---

## Phase 4: User Story 2 - Prove Pack Safety Before Enablement (Priority: P2)

**Goal**: Enforce Core validation and mandatory deterministic tests before any pack can affect runtime routing.

**Independent Test**: Try to enable packs with schema errors, digest drift, unsupported outcomes, undeclared capabilities, failing tests, and passing self-authored tests that still violate Core constraints; verify all invalid cases fail closed.

### Tests for User Story 2

- [X] T029 [P] [US2] Add deterministic pack test harness tests in `internal/adapterpack/test_test.go` for pass, fail, missing test, malformed test, timeout, exception, and forbidden API attempts
- [X] T030 [P] [US2] Add Core-validation-beats-pack-tests tests in `internal/adapterpack/test_test.go`
- [X] T031 [P] [US2] Add digest drift runtime fail-closed tests in `internal/cmdadapter/profile_test.go` and `internal/broker/broker_adapter_test.go`
- [X] T032 [P] [US2] Add exact git source rejection tests in `internal/adapterpack/source_test.go` for branch, tag, ambiguous ref, missing commit, submodule expectation, and local hook/filter configuration not being treated as authority
- [X] T033 [P] [US2] Add Manager enable rejection tests in `internal/manager/adapter_packs_test.go` for failed tests, undeclared capabilities, duplicate command ownership, unsupported authority, timeout/exception failures, and registry/profile/audit write failures leaving profile authority unchanged
- [X] T034 [P] [US2] Add redaction tests for pack test output and validation failure evidence in `internal/adapterpack/evidence_test.go`

### Implementation for User Story 2

- [X] T035 [US2] Implement deterministic pack test harness in `internal/adapterpack/test.go` using the existing adapter ABI and outcome validator
- [X] T036 [US2] Wire pack test status into registry revisions in `internal/adapterpack/registry.go`
- [X] T037 [US2] Enforce Core validation plus mandatory pack test status before Manager enable in `internal/manager/adapter_packs.go`
- [X] T038 [US2] Enforce pack-backed runtime digest drift fail-closed behavior in `internal/cmdadapter/profile.go`
- [X] T039 [US2] Add CLI commands for `hideout adapter-pack test` and clear failure output in `internal/app/app.go`
- [X] T040 [US2] Extend adapter pack smoke to cover failing tests, digest drift, and Core validation rejection in `scripts/test-adapter-pack-smoke.sh`

**Checkpoint**: User Stories 1 and 2 work independently and invalid packs cannot enable.

---

## Phase 5: User Story 3 - Upgrade, Disable, Revoke, And Inspect Packs (Priority: P3)

**Goal**: Maintain pack lifecycle safely over time without sticky or implicit authority.

**Independent Test**: Install, enable, upgrade, disable, revoke, inspect, and export pack lifecycle evidence; verify active profile behavior changes only after explicit re-enable and revoked references fail closed.

### Tests for User Story 3

- [X] T041 [P] [US3] Add upgrade candidate tests in `internal/adapterpack/registry_test.go` and `internal/manager/adapter_packs_test.go` proving active profile behavior remains pinned to the old revision
- [X] T042 [P] [US3] Add disable and revoke tests in `internal/manager/adapter_packs_test.go`
- [X] T043 [P] [US3] Add built-in metadata inspection and mutation rejection tests in `internal/adapterpack/registry_test.go`
- [X] T044 [P] [US3] Add export evidence tests for pack lifecycle events in `internal/export/export_test.go`
- [X] T045 [P] [US3] Add runtime revoked-reference fail-closed tests in `internal/cmdadapter/profile_test.go`

### Implementation for User Story 3

- [X] T046 [US3] Implement pack upgrade candidate lifecycle in `internal/adapterpack/registry.go` and `internal/manager/adapter_packs.go`
- [X] T047 [US3] Implement profile disable and store-wide revoke lifecycle in `internal/manager/adapter_packs.go`
- [X] T048 [US3] Implement built-in adapter metadata list/inspect with mutation rejection in `internal/adapterpack/registry.go`
- [X] T049 [US3] Add CLI commands for `hideout adapter-pack upgrade|disable|revoke` in `internal/app/app.go`
- [X] T050 [US3] Add pack lifecycle audit/export details in `internal/adapterpack/evidence.go`, `internal/audit/audit.go`, and export integration
- [X] T051 [US3] Extend Manager overview/TUI/WebUI summaries for disabled, revoked, candidate, and built-in pack states in `internal/manager/manager.go`, `internal/manager/server.go`, and `internal/app/app.go`
- [X] T052 [US3] Extend adapter pack smoke to cover upgrade, disable, revoke, built-in metadata, and export evidence in `scripts/test-adapter-pack-smoke.sh`

**Checkpoint**: All user stories are independently functional.

---

## Phase 6: Polish & Cross-Cutting Concerns

**Purpose**: Documentation, gates, and compatibility checks.

- [X] T053 [P] Update `docs/STATUS.md` to mark adapter pack lifecycle as implemented and distinguish it from public marketplace trust
- [X] T054 [P] Update `docs/script-extension-architecture.md` with adapter pack lifecycle, Core validation primary gate, and proposal-only authority
- [X] T055 [P] Update `docs/privacy-run-design.md` and `docs/manager-control-plane.md` with registry/profile binding and Manager lifecycle semantics
- [X] T056 [P] Update `docs/privacy-run-test-plan.md` with adapter pack smoke, Gate 0 schema checks, and no-real-Lima requirement for 011
- [X] T057 [P] Update `docs/tui-webui-experience.md` and `docs/README.md` with pack listing/inspection surfaces and CLI-first workflow
- [X] T058 [P] Add or update schema validation tests for `schemas/adapter-pack-manifest.schema.json`, `schemas/adapter-pack-registry.schema.json`, `schemas/profile.schema.json`, and `schemas/manager-api.schema.json`
- [X] T059 Run `gofmt -l internal cmd` and fix formatting issues
- [X] T060 Run `go build ./...` and `go vet ./...`
- [X] T061 Run `go test ./...`
- [X] T062 Run `scripts/test-adapter-pack-smoke.sh`
- [X] T063 Run `scripts/test-gate0.sh`
- [X] T064 Run `npx --yes markdownlint-cli2 README.md 'docs/**/*.md' 'specs/011-adapter-pack-lifecycle/**/*.md'`
- [X] T065 Run `git diff --check`
- [X] T066 Confirm 011 task completion count, commit only 011 changes, and leave `.tmp/011-016-plan.md` updated for the next feature

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies.
- **Foundational (Phase 2)**: Depends on Setup; blocks every user story.
- **US1 (Phase 3)**: Depends on Foundational.
- **US2 (Phase 4)**: Depends on Foundational and can run after US1 contracts exist; implementation should not weaken US1.
- **US3 (Phase 5)**: Depends on Foundational and pack registry state from US1.
- **Polish (Phase 6)**: Depends on all implemented stories.

### User Story Dependencies

- **US1**: MVP. Install/list/inspect/enable with pinned revision and no implicit profile authority.
- **US2**: Adds validation/test hardening. Invalid packs cannot enable.
- **US3**: Adds lifecycle maintenance. Upgrade/disable/revoke/export evidence.

### Parallel Opportunities

- Setup schema/script tasks T002-T003 can run in parallel.
- Foundational tests T005-T008 can run in parallel before implementation.
- US1 tests T017-T021 can run in parallel.
- US2 tests T029-T034 can run in parallel.
- US3 tests T041-T045 can run in parallel.
- Documentation tasks T053-T057 can run in parallel after behavior stabilizes.

## Parallel Example: User Story 1

```bash
Task: "T017 [US1] Add registry install/list tests in internal/adapterpack/registry_test.go"
Task: "T018 [US1] Add Manager install/list/inspect contract tests in internal/manager/adapter_packs_test.go"
Task: "T019 [US1] Add Manager enable tests in internal/manager/adapter_packs_test.go"
Task: "T020 [US1] Add runtime routing test in internal/broker/broker_adapter_test.go"
```

## Implementation Strategy

### MVP First (US1 Only)

1. Complete Phase 1 Setup.
2. Complete Phase 2 Foundational.
3. Complete Phase 3 US1.
4. Validate install/list/inspect/enable smoke and relevant Go tests.
5. Stop if US1 cannot prove store-wide install has no authority until profile
   binding pins an exact revision.

### Incremental Delivery

1. US1: install and enable one pinned revision.
2. US2: harden validation, digest drift, mandatory tests, and git-source rules.
3. US3: upgrade, disable, revoke, built-in metadata, and export evidence.
4. Polish: docs, schemas, gates, commit 011 only.

## Notes

- [P] tasks touch different files or can be performed independently.
- Every authority-changing path has tests before implementation.
- Real Lima Gate 2/Gate 3 is not required unless implementation changes
  backend, HostFS, DNS/network, or privilege separation behavior.
