# Tasks: Error Codes And Recovery Hints

<!-- markdownlint-disable MD013 -->

**Input**: Design documents from `/specs/028-error-recovery-hints/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

**Tests**: Required. This feature stabilizes support-visible contracts.

**Organization**: Tasks are grouped by user story for independent validation.

## Phase 1: Setup

**Purpose**: Add registry/report placeholders.

- [X] T001 Create `internal/recovery/registry.go`
- [X] T002 Create `internal/recovery/registry_test.go`
- [X] T003 [P] Add 028 docs/status placeholders in `docs/STATUS.md`, `docs/privacy-run-test-plan.md`, and `docs/first-run-alpha.md`

---

## Phase 2: Foundational

**Purpose**: Define public code shape before surfaces consume it.

- [X] T004 Define recovery code constants and record shape in `internal/recovery/registry.go`
- [X] T005 Define deterministic registry JSON view in `internal/recovery/registry.go`
- [X] T006 Add registry validation tests for uniqueness, shape, and redaction-sensitive next actions in `internal/recovery/registry_test.go`

---

## Phase 3: User Story 1 - Read A Stable Code In Doctor Output (Priority: P1) 🎯 MVP

**Goal**: Doctor human/JSON reports carry optional stable recovery metadata.

**Independent Test**: `go test ./internal/doctor -run 'Recovery|Schema|Human'`

### Tests for User Story 1

- [X] T007 [P] [US1] Add doctor schema test for coded and uncoded findings in `internal/doctor/report_schema_test.go`
- [X] T008 [P] [US1] Add doctor human/JSON parity test for recovery code fields in `internal/doctor/report_test.go`

### Implementation for User Story 1

- [X] T009 [US1] Add optional `code`, `reason`, and `hint` fields to `doctor.Finding`
- [X] T010 [US1] Add `doctor.WithRecovery` finding option using the registry
- [X] T011 [US1] Update `schemas/doctor-report.schema.json`
- [X] T012 [US1] Update doctor human renderer to print code/reason/hint
- [X] T013 [US1] Run `go test ./internal/doctor -run 'Recovery|Schema|Human'`

**Checkpoint**: Doctor is the primary structured coded surface.

---

## Phase 4: User Story 2 - Surface Codes On Selected CLI Failures (Priority: P2)

**Goal**: Selected package/init/release failures print stable recovery codes.

**Independent Test**: `go test ./internal/app ./internal/releasecompat -run 'RecoveryCode|Package|Init|Readiness'`

### Tests for User Story 2

- [X] T014 [P] [US2] Add package obsolete/prerequisite recovery-code assertions in `internal/app/app_test.go`
- [X] T015 [P] [US2] Add privacy init prerequisite recovery-code assertions in `internal/app/app_test.go`
- [X] T016 [P] [US2] Add release missing/stale evidence recovery-code assertions in `internal/releasecompat/readiness_test.go`

### Implementation for User Story 2

- [X] T017 [US2] Add `package.obsolete-leftover` and `package.prerequisite.missing` output wiring
- [X] T018 [US2] Add `init.proxy-secret.missing` and `init.mediated-resolver.missing` output wiring
- [X] T019 [US2] Add release evidence code wiring in `internal/releasecompat/readiness.go`
- [X] T020 [US2] Run `go test ./internal/app ./internal/releasecompat -run 'RecoveryCode|Package|Init|Readiness'`

**Checkpoint**: Selected host-visible CLI paths expose stable codes without
changing behavior.

---

## Phase 5: User Story 3 - Keep Error Code Truth Central And Documented (Priority: P3)

**Goal**: Docs truth validates public recovery-code references against the Go
registry.

**Independent Test**: `scripts/test-doc-truth-smoke.sh --out <tmp>`

### Tests for User Story 3

- [X] T021 [P] [US3] Add registry JSON/helper test in `internal/recovery/registry_test.go`
- [X] T022 [P] [US3] Add docs truth recovery-code reference check in `scripts/test-doc-truth-smoke.sh`

### Implementation for User Story 3

- [X] T023 [US3] Add CLI/helper access to recovery code registry if needed by shell
- [X] T024 [US3] Update docs to reference selected public codes
- [X] T025 [US3] Run `scripts/test-doc-truth-smoke.sh --out <tmp>`

**Checkpoint**: Docs cannot reference nonexistent public recovery codes.

---

## Phase 6: Polish & Cross-Cutting Concerns

**Purpose**: Final docs, smoke gates, and cleanliness.

- [X] T026 Update `scripts/test-doctor-smoke.sh` and Gate 0 coverage comments for 028
- [X] T027 Run `npx --yes markdownlint-cli2 'specs/028-error-recovery-hints/**/*.md' docs/STATUS.md docs/privacy-run-test-plan.md docs/first-run-alpha.md`
- [X] T028 Run `gofmt -l internal/recovery internal/doctor internal/app internal/releasecompat`
- [X] T029 Run `go build ./...`
- [X] T030 Run `go vet ./...`
- [X] T031 Run `git diff --check`
- [X] T032 Run `go test ./...`
- [X] T033 Run `scripts/test-gate0.sh`
- [X] T034 Mark all tasks complete in `specs/028-error-recovery-hints/tasks.md`

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies.
- **Foundational (Phase 2)**: Blocks all stories.
- **US1 (Phase 3)**: MVP and feeds doctor/docs tests.
- **US2 (Phase 4)**: Depends on registry from Foundational.
- **US3 (Phase 5)**: Depends on registry and docs references.
- **Polish (Phase 6)**: Depends on selected stories complete.

### Parallel Opportunities

- T003 can run in parallel with registry placeholders.
- T007-T008 can run in parallel.
- T014-T016 can run in parallel.
- T021-T022 can run in parallel.

## Implementation Strategy

1. Create the registry first.
2. Wire doctor schema/rendering.
3. Add selected CLI/release code output.
4. Make docs truth consume the registry.
5. Validate with targeted tests, full Go tests, and Gate 0.
