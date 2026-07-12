# Tasks: Doctor Deep Diagnostics

<!-- markdownlint-disable MD013 -->

**Input**: Design documents from `specs/018-doctor-deep-diagnostics/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

**Tests**: Required. This feature changes doctor evidence/redaction and troubleshooting contracts.

**Organization**: Tasks are grouped by user story to enable independent implementation and testing.

## Phase 1: Setup

**Purpose**: Lock the 018 contract and test fixtures before implementation.

- [X] T001 [P] Verify current doctor-report schema accepts details/nextActions conventions in schemas/doctor-report.schema.json
- [X] T002 [P] Add 018 docs links and feature status placeholders in docs/STATUS.md and docs/privacy-run-test-plan.md
- [X] T003 [P] Extend scripts/test-doctor-smoke.sh fixture setup to allow deep/feature/redaction smoke assertions

---

## Phase 2: Foundational

**Purpose**: Shared report rendering and feature-finding helpers needed by all stories.

- [X] T004 Add a shared doctor finding emit/render helper for human and JSON parity in internal/app/app.go
- [X] T005 Add structured finding detail helpers for observedFacts/candidateCauses/gateRequired in internal/app/app.go
- [X] T006 Add report parity and redaction helper tests in internal/app/app_test.go and internal/doctor/report_test.go

**Checkpoint**: Shared report rendering and structured details are ready.

---

## Phase 3: User Story 1 - Get Useful Deep Diagnostics (Priority: P1) 🎯 MVP

**Goal**: `hideout doctor --level deep` emits useful local diagnostics for every supported feature without starting hidden probes or claiming gates.

**Independent Test**: Run deep doctor on a controlled store and verify every supported feature emits additional findings with observed facts, next actions, and gate-required markers where appropriate.

### Tests for User Story 1

- [X] T007 [P] [US1] Add deep-vs-light coverage test for all supported feature findings in internal/app/app_test.go
- [X] T008 [P] [US1] Add no-hidden-probe test proving deep doctor does not start backend, Gate 2/3, network, HostFS mutation, or package repair in internal/app/app_test.go
- [X] T009 [P] [US1] Add packaging deep diagnostic test covering package verification, obsolete state, repair guidance, and external tun2socks prerequisite in internal/app/app_test.go

### Implementation for User Story 1

- [X] T010 [US1] Expand addDoctorFeatureDiagnostics with observedFacts/gateRequired/nextActions for packaging, DNS, HostFS, Lima, privilege, daemon, export, cleanup, adapters, and decisions in internal/app/app.go
- [X] T011 [US1] Make deep mode include all supported feature diagnostics while keeping light mode local-only in internal/app/app.go
- [X] T012 [US1] Ensure gate-required DNS/HostFS/Lima/privilege findings do not claim release evidence in internal/app/app.go

**Checkpoint**: Deep diagnostics are independently useful and honest.

---

## Phase 4: User Story 2 - Diagnose One Feature Without Noise (Priority: P2)

**Goal**: `hideout doctor --feature <name>` produces focused diagnostics for one feature without unrelated feature-only findings.

**Independent Test**: Run each supported selector and verify one local finding or gate marker appears while unselected features remain absent.

### Tests for User Story 2

- [X] T013 [P] [US2] Add selector coverage test for all supported features in internal/app/app_test.go
- [X] T014 [P] [US2] Add unselected-feature absence test for single-feature mode in internal/app/app_test.go
- [X] T015 [P] [US2] Add unknown feature rejection or error test in internal/app/app_test.go

### Implementation for User Story 2

- [X] T016 [US2] Tighten selectedDoctorDiagnosticFeatures and option validation for supported feature names in internal/app/app.go
- [X] T017 [US2] Add feature-specific local facts and candidate causes for adapters and decisions in internal/app/app.go
- [X] T018 [US2] Add feature-specific local facts and gate-required markers for DNS, HostFS, Lima, and privilege in internal/app/app.go

**Checkpoint**: Feature-scoped diagnostics are focused and complete.

---

## Phase 5: User Story 3 - Trust Redacted Reports Across Output Modes (Priority: P3)

**Goal**: Human output, JSON output, evidence files, audit/recovery evidence, and export-selected doctor reports share one deterministic redaction boundary.

**Independent Test**: Inject representative control-plane-looking values into diagnostic fields and prove zero raw matches across output modes.

### Tests for User Story 3

- [X] T019 [P] [US3] Add human-vs-JSON parity test for check ids/status/severity/required/nextActions in internal/app/app_test.go
- [X] T020 [P] [US3] Add redaction injection test for summary/details/nextActions/evidenceRefs in internal/doctor/report_test.go
- [X] T021 [P] [US3] Add doctor evidence file redaction test in internal/app/app_test.go
- [X] T022 [P] [US3] Add export-selected doctor report redaction coverage or contract assertion in internal/export tests or internal/app/app_test.go
- [X] T023 [P] [US3] Add doctor recovery/audit evidence redaction test with injected control-plane-looking values in internal/app/app_test.go

### Implementation for User Story 3

- [X] T024 [US3] Render feature/deep findings in human output from the same builder data used for JSON in internal/app/app.go
- [X] T025 [US3] Ensure doctor report details, next actions, and evidence refs are redacted recursively enough for injected probes in internal/doctor/report.go
- [X] T026 [US3] Preserve non-secret user data while stripping control-plane probe values in internal/doctor/report.go

**Checkpoint**: Doctor reports can be shared intentionally without control-plane leaks.

---

## Phase 6: User Story 4 - Receive Safe Recovery Guidance (Priority: P4)

**Goal**: Doctor provides high-confidence commands and safe dry-run/apply behavior without expanding automatic recovery.

**Independent Test**: Run safe InitTask dry-run/apply fixtures and non-auto-fix findings; verify guidance appears and no unsafe action is applied.

### Tests for User Story 4

- [X] T027 [P] [US4] Add next-action guidance test for package repair, stale decisions, and gate-required proof in internal/app/app_test.go
- [X] T028 [P] [US4] Add warning/degraded exit-zero and required local error nonzero test in internal/app/app_test.go
- [X] T029 [P] [US4] Add safe dry-run recovery smoke assertion in scripts/test-doctor-smoke.sh

### Implementation for User Story 4

- [X] T030 [US4] Attach non-auto-fix command guidance for package leftovers, stale decisions, DNS/HostFS gates, and unsafe cleanup in internal/app/app.go
- [X] T031 [US4] Keep automatic doctor recovery limited to existing typed safe fixes and refuse high-risk recovery paths in internal/app/app.go
- [X] T032 [US4] Ensure warning/degraded feature findings are not required unless local required errors exist in internal/app/app.go

**Checkpoint**: Recovery guidance is useful without new authority.

---

## Phase 7: Polish & Cross-Cutting

**Purpose**: Documentation, smoke, and final verification.

- [X] T033 [P] Update README.md with deep/feature doctor examples and warning/error semantics
- [X] T034 [P] Update docs/STATUS.md to describe implemented 018 doctor deep diagnostics
- [X] T035 [P] Update docs/privacy-run-test-plan.md with Gate 0 doctor smoke coverage for deep, feature, redaction, and safe dry-run
- [X] T036 [P] Update specs/018-doctor-deep-diagnostics/quickstart.md with final command names and expected outputs if implementation differs
- [X] T037 Add doctor smoke assertions for deep mode, at least three feature selectors, redaction injection, warning/error, and safe dry-run in scripts/test-doctor-smoke.sh
- [X] T038 Mark all completed tasks in specs/018-doctor-deep-diagnostics/tasks.md
- [X] T039 Run gofmt -l internal cmd and ensure it prints nothing
- [X] T040 Run go build ./... and go vet ./...
- [X] T041 Run go test ./...
- [X] T042 Run scripts/test-gate0.sh
- [X] T043 Run npx --yes markdownlint-cli2 README.md docs/**/*.md specs/018-doctor-deep-diagnostics/**/*.md
- [X] T044 Run git diff --check

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies.
- **Foundational (Phase 2)**: Depends on Setup; blocks all user stories.
- **US1 (Phase 3)**: MVP after Foundational.
- **US2 (Phase 4)**: Can start after Foundational; uses shared helper from US1 if implemented first.
- **US3 (Phase 5)**: Can start after Foundational; redaction helper work affects all stories.
- **US4 (Phase 6)**: Can start after Foundational; depends on feature next-action shape.
- **Polish (Phase 7)**: Depends on implemented stories.

### User Story Dependencies

- **US1**: MVP, no dependency on other stories.
- **US2**: Independent selector behavior; benefits from US1 feature detail implementation.
- **US3**: Cross-cuts output modes; can be developed in parallel with US1/US2 tests.
- **US4**: Depends on next-action conventions from US1/US2.

### Parallel Opportunities

- T001-T003 can run in parallel.
- T007-T009, T013-T015, T019-T023, and T027-T029 are independent test tasks.
- Docs tasks T033-T036 can run in parallel after implementation.

## Parallel Example: User Story 3

```text
Task: "Add human-vs-JSON parity test in internal/app/app_test.go"
Task: "Add redaction injection test in internal/doctor/report_test.go"
Task: "Add doctor evidence file redaction test in internal/app/app_test.go"
```

## Implementation Strategy

### MVP First

1. Complete Setup and Foundational tasks.
2. Implement US1 deep diagnostics.
3. Run the US1 tests and doctor smoke subset.
4. Continue with feature scope, redaction parity, and recovery guidance.

### Completion Bar

018 is complete only when all 44 tasks are checked, Gate 0 includes the expanded doctor smoke, and final build/test/lint/diff checks are green.
