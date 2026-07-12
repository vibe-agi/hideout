# Tasks: Test And Evidence Spine

<!-- markdownlint-disable MD013 -->

**Input**: Design documents from `/specs/026-test-evidence-spine/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

**Tests**: Required. This feature touches release evidence, docs truth, and
gate behavior.

**Organization**: Tasks are grouped by user story so each story can be
implemented and tested independently.

## Phase 1: Setup

**Purpose**: Prepare shared registry/evaluator files and docs placeholders.

- [X] T001 Create registry/evaluator source placeholders in `internal/productevidence/registry.go` and `internal/productevidence/evaluate.go`
- [X] T002 Create registry/evaluator test placeholders in `internal/productevidence/registry_test.go` and `internal/productevidence/evaluate_test.go`
- [X] T003 [P] Add 026 docs/status placeholders in `docs/privacy-run-test-plan.md` and `docs/STATUS.md`

---

## Phase 2: Foundational

**Purpose**: Define shared types and constants before user stories.

- [X] T004 Define registry enum constants and public result statuses in `internal/productevidence/registry.go`
- [X] T005 Define evaluator option/result structs in `internal/productevidence/evaluate.go`
- [X] T006 Add helper fixtures for product-hardening manifests in `internal/productevidence/evaluate_test.go`

**Checkpoint**: Registry/evaluator types exist; story work can begin.

---

## Phase 3: User Story 1 - Evaluate Required Proofs From One Registry (Priority: P1) 🎯 MVP

**Goal**: Replace per-feature required proof slices as the truth source with a
Go-owned proof requirement registry.

**Independent Test**: `go test ./internal/productevidence -run 'Registry|EvaluateMissing|Require02'`

### Tests for User Story 1

- [X] T007 [P] [US1] Add registry uniqueness and determinism tests in `internal/productevidence/registry_test.go`
- [X] T008 [P] [US1] Add missing-proof evaluator test proving featureId/proofId come from registry in `internal/productevidence/evaluate_test.go`
- [X] T009 [P] [US1] Add compatibility tests for existing `Require021Complete` through `Require025Complete` in `internal/productevidence/aggregate_test.go`

### Implementation for User Story 1

- [X] T010 [US1] Implement `ProofRequirement` registry rows for 021-025 in `internal/productevidence/registry.go`
- [X] T011 [US1] Implement registry validation and deterministic ordering in `internal/productevidence/registry.go`
- [X] T012 [US1] Update completion helpers in `internal/productevidence/aggregate.go` to use registry-derived requirements
- [X] T013 [US1] Implement base evaluator missing/failed/not-run/redaction-failed results in `internal/productevidence/evaluate.go`
- [X] T014 [US1] Run `go test ./internal/productevidence -run 'Registry|EvaluateMissing|Require02'`

**Checkpoint**: Existing 021-025 completion checks are registry-backed and
missing-proof diagnostics carry feature metadata.

---

## Phase 4: User Story 2 - Detect Stale Or False Evidence Without New Proof Status (Priority: P2)

**Goal**: Add evaluator-only freshness and artifact verification while keeping
manifest proof status stable.

**Independent Test**: `go test ./internal/productevidence -run 'Evaluate.*Stale|Artifact|ProofStatus'`

### Tests for User Story 2

- [X] T015 [P] [US2] Add stale-by-commit and stale-by-package fixture tests in `internal/productevidence/evaluate_test.go`
- [X] T016 [P] [US2] Add missing artifact and digest mismatch fixture tests in `internal/productevidence/evaluate_test.go`
- [X] T017 [P] [US2] Add proof-status stability test rejecting `stale` as manifest status in `internal/productevidence/schema_test.go`

### Implementation for User Story 2

- [X] T018 [US2] Implement freshness policy evaluation in `internal/productevidence/evaluate.go`
- [X] T019 [US2] Implement artifact policy evaluation in `internal/productevidence/evaluate.go`
- [X] T020 [US2] Implement human-readable evaluator summary errors in `internal/productevidence/evaluate.go`
- [X] T021 [US2] Run `go test ./internal/productevidence -run 'Evaluate.*Stale|Artifact|ProofStatus'`

**Checkpoint**: False-green proof cases fail evaluator checks without changing
manifest schema.

---

## Phase 5: User Story 3 - Let Shell Gates And Docs Truth Consume The Same Registry (Priority: P3)

**Goal**: Expose deterministic registry JSON and remove duplicated proof lists
from shell/docs/release consumers.

**Independent Test**: `scripts/test-doc-truth-smoke.sh --out <tmp>` plus
release readiness smoke.

### Tests for User Story 3

- [X] T022 [P] [US3] Add registry JSON contract test in `internal/productevidence/registry_test.go`
- [X] T023 [P] [US3] Add docs truth smoke regression test or shell check for registry consumption in `scripts/test-doc-truth-smoke.sh`
- [X] T024 [P] [US3] Add release readiness supporting-evidence test in `internal/releasecompat/readiness_test.go`

### Implementation for User Story 3

- [X] T025 [US3] Implement deterministic registry JSON view in `internal/productevidence/registry.go`
- [X] T026 [US3] Add CLI/test helper access to registry JSON in `internal/productevidence/registry.go` or existing smoke-test entrypoint
- [X] T027 [US3] Update `scripts/test-doc-truth-smoke.sh` to consume registry JSON or Go helper output instead of a separate 021-025 proof list
- [X] T028 [US3] Update `internal/releasecompat/readiness.go` to report product-hardening evidence as supporting context without satisfying real gates
- [X] T029 [US3] Update `docs/claim-boundaries.md`, `docs/privacy-run-test-plan.md`, and `docs/STATUS.md`
- [X] T030 [US3] Run `go test ./internal/productevidence ./internal/releasecompat`
- [X] T031 [US3] Run `scripts/test-doc-truth-smoke.sh --out <tmp>`
- [X] T032 [US3] Run `scripts/test-release-hardening-smoke.sh`

**Checkpoint**: Shell/docs/release consumers no longer need separate 021-025
required-proof lists.

---

## Phase 6: Polish & Cross-Cutting Concerns

**Purpose**: Validate specs, docs, gates, and final cleanliness.

- [X] T033 [P] Run `npx --yes markdownlint-cli2 'specs/026-test-evidence-spine/**/*.md' docs/claim-boundaries.md docs/privacy-run-test-plan.md docs/STATUS.md`
- [X] T034 Run `gofmt -l internal/productevidence internal/releasecompat`
- [X] T035 Run `go build ./...`
- [X] T036 Run `go vet ./...`
- [X] T037 Run `git diff --check`
- [X] T038 Run `go test ./...`
- [X] T039 Run `scripts/test-gate0.sh`
- [X] T040 Mark all tasks complete in `specs/026-test-evidence-spine/tasks.md`

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies.
- **Foundational (Phase 2)**: Depends on Setup and blocks all stories.
- **US1 (Phase 3)**: MVP; required before US2/US3.
- **US2 (Phase 4)**: Depends on base evaluator from US1.
- **US3 (Phase 5)**: Depends on registry and evaluator from US1/US2.
- **Polish (Phase 6)**: Depends on selected stories complete.

### User Story Dependencies

- **US1**: Independent MVP.
- **US2**: Depends on US1 evaluator structure.
- **US3**: Depends on US1 registry and US2 result semantics.

### Parallel Opportunities

- T003 can run in parallel with source placeholders.
- T007-T009 can run in parallel.
- T015-T017 can run in parallel.
- T022-T024 can run in parallel.
- T033 can run while non-doc code checks are prepared after implementation.

## Implementation Strategy

1. Build the registry and evaluator first.
2. Preserve existing manifests and completion helpers while moving them to the
   registry-backed path.
3. Add freshness/artifact checks as evaluator behavior only.
4. Expose registry JSON to shell/docs/release consumers.
5. Validate with targeted tests, docs truth smoke, release smoke, and Gate 0.
