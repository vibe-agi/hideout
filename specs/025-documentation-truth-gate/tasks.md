# Tasks: Documentation Truth Gate

<!-- markdownlint-disable MD013 -->

**Input**: Design documents from `/specs/025-documentation-truth-gate/`

## Phase 1: Setup

- [X] T001 Add 025 proof IDs and claims in `internal/productevidence/claims.go`
- [X] T002 [P] Add 025 aggregate completeness tests in `internal/productevidence/aggregate_test.go`
- [X] T003 [P] Add 025 schema fixture test in `internal/productevidence/schema_test.go`
- [X] T004 Create `docs/claim-boundaries.md`
- [X] T005 Create `docs/command-examples.json`

## Phase 2: Foundational

- [X] T006 Implement 025 product evidence helpers in `internal/productevidence/docs_truth.go`
- [X] T007 Implement required 025 completeness validation in `internal/productevidence/aggregate.go`
- [X] T008 Create `scripts/test-doc-truth-smoke.sh` with `--out` parsing and report directories
- [X] T009 Implement schema validation and redaction scan in `scripts/test-doc-truth-smoke.sh`

## Phase 3: User Story 1 - Map Claims To Proof (P1)

- [X] T010 [US1] Validate `docs/claim-boundaries.md` includes required 021-024 proof ids in `scripts/test-doc-truth-smoke.sh`
- [X] T011 [US1] Validate claim rows include owner/proof/non-claim fields in `scripts/test-doc-truth-smoke.sh`
- [X] T012 [US1] Write claim-boundary proof entry in `scripts/test-doc-truth-smoke.sh`

## Phase 4: User Story 2 - Catch Known Overclaims (P2)

- [X] T013 [US2] Implement banned overclaim scan in `scripts/test-doc-truth-smoke.sh`
- [X] T014 [US2] Exclude `.tmp` drafts from current-product scan set in `scripts/test-doc-truth-smoke.sh`
- [X] T015 [US2] Write overclaim-scan proof entry in `scripts/test-doc-truth-smoke.sh`

## Phase 5: User Story 3 - Keep Commands And Localized Entrypoints Honest (P3)

- [X] T016 [US3] Update `README.zh-CN.md` with canonical English README statement and current package path wording
- [X] T017 [US3] Implement curated command fixture checks in `scripts/test-doc-truth-smoke.sh`
- [X] T018 [US3] Validate README/support/first-run/test-plan/Gate 0 cross-links in `scripts/test-doc-truth-smoke.sh`
- [X] T019 [US3] Write command and cross-doc proof entries in `scripts/test-doc-truth-smoke.sh`

## Phase 6: Polish & Cross-Cutting

- [X] T020 [P] Update `docs/privacy-run-test-plan.md` with 025 docs truth smoke
- [X] T021 [P] Update `docs/STATUS.md` with docs truth gate status
- [X] T022 Add `scripts/test-doc-truth-smoke.sh` to `scripts/test-gate0.sh`
- [X] T023 Run `npx --yes markdownlint-cli2 specs/025-documentation-truth-gate/**/*.md docs/claim-boundaries.md docs/privacy-run-test-plan.md docs/STATUS.md README.md README.zh-CN.md`
- [X] T024 Run `go test ./internal/productevidence`
- [X] T025 Run `scripts/test-doc-truth-smoke.sh --out <tmp>`
- [X] T026 Run final battery: `go build ./...`, `go vet ./...`, `gofmt -l internal test`, `git diff --check`, `go test ./...`, and `scripts/test-gate0.sh`

## Dependencies & Execution Order

- Setup and Foundational block all stories.
- US1 is MVP because the registry defines proof mapping.
- US2 and US3 can run after Foundational.
- Polish runs last.
