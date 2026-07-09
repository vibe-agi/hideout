# Tasks: Doctor Diagnostics And Recovery

<!-- markdownlint-disable MD013 -->

**Input**: Design documents from `specs/015-doctor-diagnostics-recovery/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

**Tests**: Required. 015 touches lifecycle diagnostics, recovery, package/helper evidence, profile state, daemon status, HostFS/DNS/privilege readiness, export evidence, and redaction.

**Organization**: Tasks are grouped by user story to enable independent implementation and testing of each story.

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Create shared doctor artifacts and schema hooks.

- [X] T001 Create `internal/doctor/doc.go`, `internal/doctor/report.go`, `internal/doctor/runner.go`, `internal/doctor/checks.go`, `internal/doctor/render.go`, and `internal/doctor/recovery.go` package skeletons
- [X] T002 Add `schemas/doctor-report.schema.json` for `hideout.doctor-report/v1`
- [X] T003 Register `schemas/doctor-report.schema.json` existence/schema validation in `scripts/test-gate0.sh`
- [X] T004 Add executable `scripts/test-doctor-smoke.sh` skeleton and wire it into `scripts/test-gate0.sh`

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Core model and selection logic that all stories depend on.

**CRITICAL**: No user story work can begin until this phase is complete.

- [X] T005 [P] Add Doctor Request, Check, Finding, Report, Summary, and FeatureSet model tests in `internal/doctor/report_test.go` (FR-001, FR-014)
- [X] T006 [P] Add doctor report schema validation tests in `internal/doctor/report_schema_test.go` (FR-002, SC-002)
- [X] T007 Implement Doctor Request, Check, Finding, Report, Summary, redaction, and exit-classification model in `internal/doctor/report.go` (FR-001, FR-014, FR-018)
- [X] T008 Implement level/feature parsing, supported feature validation, required-failure exit classification, and check selection in `internal/doctor/runner.go` (FR-003, FR-004, FR-014)
- [X] T009 Implement shared human and JSON renderers over the same report object in `internal/doctor/render.go` (FR-002, SC-002)

**Checkpoint**: Foundation ready; user story implementation can start.

---

## Phase 3: User Story 1 - Diagnose Local Readiness Quickly (Priority: P1)

**Goal**: Default `hideout doctor` produces a local/light human or JSON report with stable check ids, actionable findings, and correct exit semantics.

**Independent Test**: Run doctor on fresh and broken local fixtures, verify human output, JSON output, required-failure exit, warning/degraded exit, and no hidden deep probes.

### Tests for User Story 1

- [X] T010 [P] [US1] Add default light doctor pass/warn/error report tests in `internal/doctor/checks_test.go` (FR-001, FR-003, SC-001, SC-003)
- [X] T011 [P] [US1] Add CLI human/JSON parity and exit-code tests in `internal/app/app_test.go` (FR-002, FR-014, SC-002, SC-004, SC-005)
- [X] T012 [P] [US1] Add package/helper/profile fixture tests for required failure and actionable next actions in `internal/doctor/checks_test.go` (FR-005, FR-006, SC-003, SC-004)
- [X] T013 [P] [US1] Add redaction tests for doctor human and JSON output in `internal/doctor/report_test.go` (FR-018, SC-009)

### Implementation for User Story 1

- [X] T014 [US1] Implement light/local checks for store writability, package/install integrity, helper presence, profile/schema/template metadata, daemon basics, and evidence basics in `internal/doctor/checks.go` (FR-005, FR-006, FR-007)
- [X] T015 [US1] Refactor `internal/app/app.go` doctor command to delegate report generation/rendering to `internal/doctor` while preserving existing human output essentials (FR-001, FR-002)
- [X] T016 [US1] Add `--format human|json`, `--level light|deep`, and repeatable `--feature` parsing to doctor CLI in `internal/app/app.go` (FR-002, FR-004)
- [X] T017 [US1] Ensure default light doctor performs no guest start, hidden DNS probe, HostFS mutation, or network probe via tests in `internal/doctor/checks_test.go` (FR-003, SC-001)

**Checkpoint**: US1 is independently functional as the MVP.

---

## Phase 4: User Story 2 - Run Explicit Deep Or Feature Diagnostics (Priority: P2)

**Goal**: Operators can opt into targeted DNS, HostFS, Lima/backend, privilege, adapter, package, daemon, decision, export/redaction, and cleanup diagnostics without surprise probes.

**Independent Test**: Run selected feature scopes against fixtures and verify selected checks run, unselected deep checks do not run, and missing prerequisites are actionable errors.

### Tests for User Story 2

- [X] T018 [P] [US2] Add feature-selection tests proving `--feature dns|hostfs|lima|privilege|adapters|packaging|daemon|decisions|export|cleanup` includes only selected checks in `internal/doctor/runner_test.go` (FR-004)
- [X] T019 [P] [US2] Add DNS prerequisite, gate-required marker, and no-weak-fallback tests in `internal/doctor/checks_test.go` (FR-011)
- [X] T020 [P] [US2] Add HostFS local-readiness, gate-required marker, reserved-root, and no-weak-fallback tests in `internal/doctor/checks_test.go` (FR-012)
- [X] T021 [P] [US2] Add adapter inventory, decision status, daemon status, export/redaction, and cleanup health tests in `internal/doctor/checks_test.go` (FR-007, FR-008, FR-009, FR-010, SC-003)

### Implementation for User Story 2

- [X] T022 [US2] Implement feature/deep check catalog entries in `internal/doctor/checks.go` (FR-004)
- [X] T023 [US2] Implement DNS/network diagnostic checks using existing dry-run plan facts and explicit gate-required markers in `internal/doctor/checks.go` (FR-011)
- [X] T024 [US2] Implement HostFS local-readiness checks and explicit gate-required markers in `internal/doctor/checks.go` (FR-012)
- [X] T025 [US2] Implement Lima/backend and privilege status checks preserving enforced/degraded/unknown non-claims in `internal/doctor/checks.go` (FR-013, SC-005)
- [X] T026 [US2] Implement adapter inventory, decision status, daemon, export/redaction, package, and cleanup checks from existing stores in `internal/doctor/checks.go` (FR-007, FR-008, FR-009, FR-010)
- [X] T027 [US2] Update CLI feature/deep output ordering and summaries in `internal/app/app.go`

**Checkpoint**: US1 and US2 both work independently.

---

## Phase 5: User Story 3 - Apply Safe Recovery Deliberately (Priority: P3)

**Goal**: `doctor --fix --dry-run` previews safe repairs and `doctor --fix --apply` applies only typed safe repairs with audit evidence.

**Independent Test**: Run dry-run/apply against safe and unsafe fixtures; verify no durable writes in dry-run, typed repairs only in apply, and high-risk recovery refusal.

### Tests for User Story 3

- [X] T028 [P] [US3] Add recovery dry-run no-durable-write tests in `internal/doctor/recovery_test.go` (FR-015, SC-006)
- [X] T029 [P] [US3] Add recovery apply typed InitTask/audit evidence tests in `internal/doctor/recovery_test.go` (FR-015, SC-007)
- [X] T030 [P] [US3] Add unsafe recovery refusal tests for purge/evidence deletion/environment recreation/authority broadening in `internal/doctor/recovery_test.go` (FR-016, SC-008)
- [X] T031 [P] [US3] Add CLI `doctor --fix --dry-run|--apply` compatibility tests in `internal/app/app_test.go`

### Implementation for User Story 3

- [X] T032 [US3] Implement Recovery Plan, eligible/refused finding mapping, dry-run, and apply delegation in `internal/doctor/recovery.go` (FR-015, FR-016)
- [X] T033 [US3] Refactor `internal/app/app.go` `doctorFix` to use `internal/doctor` recovery while preserving Manager `PlanDoctorFix`/`ApplyDoctorFix` (FR-015)
- [X] T034 [US3] Enforce invalid `--fix` without `--dry-run` or `--apply` rejection in `internal/app/app.go` (FR-015)

**Checkpoint**: Safe recovery is independently functional.

---

## Phase 6: User Story 4 - Export Diagnostic Evidence On Demand (Priority: P4)

**Goal**: Doctor reports can be saved and explicitly selected for export/share evidence without being silently bundled into unrelated exports.

**Independent Test**: Save a doctor report, validate schema/redaction/provenance, explicitly include it in export, and verify unrelated exports omit it.

### Tests for User Story 4

- [X] T035 [P] [US4] Add `--evidence-out` report write and schema/redaction tests in `internal/app/app_test.go` (FR-017, FR-018, SC-009)
- [X] T036 [P] [US4] Add export source tests proving doctor reports are included only when explicitly selected in `internal/export/export_test.go` (FR-017)
- [X] T037 [P] [US4] Add smoke redaction scan for saved doctor reports in `scripts/test-doctor-smoke.sh` (FR-018, SC-009)

### Implementation for User Story 4

- [X] T038 [US4] Implement `--evidence-out <path>` doctor report save path in `internal/app/app.go` and `internal/doctor/report.go` (FR-017, FR-018)
- [X] T039 [US4] Add explicit doctor-report source selection to export contracts/implementation in `internal/export` (FR-017)
- [X] T040 [US4] Ensure saved/exported doctor reports include provenance and deterministic redaction in `internal/doctor/report.go` (FR-018, SC-009)

**Checkpoint**: Doctor evidence selection is independently functional.

---

## Phase 7: Polish & Cross-Cutting Concerns

**Purpose**: Docs, smoke, Gate 0, and final verification.

- [X] T041 [P] Update `README.md` with default doctor, JSON, feature-scope, and safe recovery examples (FR-019)
- [X] T042 [P] Update `docs/STATUS.md`, `docs/privacy-run-design.md`, and `docs/privacy-run-test-plan.md` for 015 implemented status, gates, and non-claims (FR-019)
- [X] T043 [P] Update `docs/tui-webui-experience.md` or manager docs if doctor report facts become future UI inputs
- [X] T044 Complete `scripts/test-doctor-smoke.sh` for human output, JSON schema, required failure, warning/degraded exit, redaction, and dry-run recovery (SC-010)
- [X] T045 Run `npx --yes markdownlint-cli2 README.md 'docs/*.md' 'specs/015-doctor-diagnostics-recovery/**/*.md'`
- [X] T046 Run `go build ./...`, `go vet ./...`, `gofmt -l internal cmd`, `git diff --check`, and `go test ./...`
- [X] T047 Run `scripts/test-doctor-smoke.sh` and `scripts/test-gate0.sh`
- [X] T048 Mark all completed 015 tasks as checked in `specs/015-doctor-diagnostics-recovery/tasks.md`

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies.
- **Foundational (Phase 2)**: Depends on Setup; blocks all user stories.
- **US1 (Phase 3)**: Depends on Foundational; MVP.
- **US2 (Phase 4)**: Depends on Foundational and integrates with US1 report model.
- **US3 (Phase 5)**: Depends on Foundational and existing InitTask repair.
- **US4 (Phase 6)**: Depends on Foundational and report redaction; export integration can start after report save exists.
- **Polish (Phase 7)**: Depends on implemented stories.

### User Story Dependencies

- **US1**: MVP and first implementation target.
- **US2**: Can start after Foundational, but should preserve US1 default-light behavior.
- **US3**: Can start after Foundational; must preserve existing doctor fix behavior.
- **US4**: Can start after report model exists; export integration should not alter unrelated export defaults.

### Parallel Opportunities

- T005/T006 can run in parallel.
- US1 tests T010-T013 can run in parallel.
- US2 tests T018-T021 can run in parallel.
- US3 tests T028-T031 can run in parallel.
- US4 tests T035-T037 can run in parallel.
- Docs T041-T043 can run in parallel.

## Implementation Strategy

### MVP First

1. Complete Setup and Foundational phases.
2. Complete US1 so default doctor has structured human/JSON local readiness.
3. Validate with targeted doctor tests before deep checks/recovery/export.

### Incremental Delivery

1. US1: local/light report model and CLI JSON.
2. US2: feature/deep checks.
3. US3: safe recovery.
4. US4: explicit doctor report evidence.
5. Polish: docs, smoke, Gate 0, full battery.
