# Tasks: Doctor And Package Recovery E2E

<!-- markdownlint-disable MD013 -->

**Input**: Design documents from `/specs/024-doctor-package-recovery-e2e/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

**Tests**: Required. 024 exists to prove recovery behavior end to end; existing
smokes remain authoritative, but evidence must be stable and schema-valid.

## Phase 1: Setup

- [X] T001 Add 024 proof ID constants and covered claims in `internal/productevidence/claims.go`
- [X] T002 [P] Add 024 aggregate completeness tests in `internal/productevidence/aggregate_test.go`
- [X] T003 [P] Add 024 schema fixture test in `internal/productevidence/schema_test.go`
- [X] T004 Create `scripts/test-doctor-package-recovery-e2e.sh` with `--local-fast`, `--out`, and `--package` parsing
- [X] T005 [P] Add 024 quickstart proof-id test in `internal/productevidence/quickstart_test.go`

## Phase 2: Foundational

- [X] T006 Implement 024 product-hardening proof helper functions in `internal/productevidence/doctor_package_recovery.go`
- [X] T007 Implement required 024 local-fast completeness validation in `internal/productevidence/aggregate.go`
- [X] T008 Implement schema validation and redaction scan helpers in `scripts/test-doctor-package-recovery-e2e.sh`
- [X] T009 Implement public artifact reference generation in `scripts/test-doctor-package-recovery-e2e.sh`

## Phase 3: User Story 1 - Recover Stale Package State (P1) MVP

**Goal**: Prove stale package-owned leftovers are detected, previewed, repaired,
and verified clean using existing package paths.

- [X] T010 [P] [US1] Add package repair proof coverage tests in `internal/productevidence/aggregate_test.go`
- [X] T011 [US1] Run or reuse existing package smoke from `scripts/test-doctor-package-recovery-e2e.sh`
- [X] T012 [US1] Capture package stale verify, dry-run, apply, and repaired verify artifacts in `scripts/test-doctor-package-recovery-e2e.sh`
- [X] T013 [US1] Assert durable state and unrelated file preservation in `scripts/test-doctor-package-recovery-e2e.sh`
- [X] T014 [US1] Write package repair loop proof entry in `scripts/test-doctor-package-recovery-e2e.sh`

## Phase 4: User Story 2 - Recover Safe Doctor State (P2)

**Goal**: Prove doctor deep diagnostics and safe fix dry-run/apply behavior.

- [X] T015 [P] [US2] Add doctor safe-fix proof coverage tests in `internal/productevidence/aggregate_test.go`
- [X] T016 [US2] Run or reuse existing doctor smoke from `scripts/test-doctor-package-recovery-e2e.sh`
- [X] T017 [US2] Capture doctor deep, fix dry-run, fix apply, and rerun artifacts in `scripts/test-doctor-package-recovery-e2e.sh`
- [X] T018 [US2] Assert dry-run non-mutation and explicit apply mutation in `scripts/test-doctor-package-recovery-e2e.sh`
- [X] T019 [US2] Write doctor safe-fix proof entry in `scripts/test-doctor-package-recovery-e2e.sh`

## Phase 5: User Story 3 - Preserve Guidance And Redaction Boundaries (P3)

**Goal**: Prove guidance-only findings are not repairs and public artifacts are
clean.

- [X] T020 [P] [US3] Add recovery redaction proof coverage tests in `internal/productevidence/schema_test.go`
- [X] T021 [US3] Capture guidance-only doctor feature diagnostics in `scripts/test-doctor-package-recovery-e2e.sh`
- [X] T022 [US3] Export a selected doctor report and validate export schema in `scripts/test-doctor-package-recovery-e2e.sh`
- [X] T023 [US3] Implement public artifact redaction scan in `scripts/test-doctor-package-recovery-e2e.sh`
- [X] T024 [US3] Write guidance-only and redaction proof entries in `scripts/test-doctor-package-recovery-e2e.sh`

## Phase 6: Polish & Cross-Cutting

- [X] T025 [P] Update `docs/privacy-run-test-plan.md` with 024 recovery proof boundaries
- [X] T026 [P] Update `docs/STATUS.md` with doctor/package recovery E2E evidence status
- [X] T027 Add `scripts/test-doctor-package-recovery-e2e.sh --local-fast` to `scripts/test-gate0.sh`
- [X] T028 Run `npx --yes markdownlint-cli2 specs/024-doctor-package-recovery-e2e/**/*.md docs/privacy-run-test-plan.md docs/STATUS.md`
- [X] T029 Run `go test ./internal/productevidence ./internal/packagekit ./internal/doctor ./internal/export`
- [X] T030 Run `scripts/test-doctor-package-recovery-e2e.sh --local-fast --out <tmp>`
- [X] T031 Run final battery: `go build ./...`, `go vet ./...`, `gofmt -l internal test`, `git diff --check`, `go test ./...`, and `scripts/test-gate0.sh`

## Dependencies & Execution Order

- Setup and Foundational block all user stories.
- US1 is MVP because package repair is the first install recovery path.
- US2 can run after Foundational and reuses doctor smoke behavior.
- US3 depends on artifacts from US1/US2.
- Polish runs after all story proofs pass.

## Parallel Opportunities

- T002, T003, and T005 can run in parallel.
- T010, T015, and T020 can run in parallel.
- T025 and T026 can run in parallel.

## Implementation Strategy

1. Add product evidence vocabulary.
2. Build the recovery E2E runner as a thin orchestrator over existing smoke
   paths and focused fixtures.
3. Validate product-hardening evidence and redaction.
4. Wire the runner into Gate 0 and docs.
