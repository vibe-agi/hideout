# Tasks: UI E2E Proof

<!-- markdownlint-disable MD013 -->

**Input**: Design documents from `/specs/021-ui-e2e-proof/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/, quickstart.md

**Tests**: Required. This feature touches UI/daemon control, evidence, docs truth, and redaction surfaces.

**Organization**: Tasks are grouped by user story to enable independent implementation and testing.

## Requirement Coverage

- **US1 Browser proof**: FR-001, FR-002, FR-003, FR-004, FR-005, FR-014, FR-015, FR-017; SC-001, SC-002, SC-003, SC-008, SC-010.
- **US2 Terminal proof**: FR-006, FR-007, FR-008, FR-009, FR-010, FR-014, FR-015, FR-017; SC-004, SC-005, SC-008, SC-010.
- **US3 Evidence reuse**: FR-011, FR-012, FR-013, FR-014, FR-016, FR-017; SC-006, SC-007, SC-008, SC-009, SC-010.

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Establish evidence schema, package skeleton, and proof runner entrypoints.

- [X] T001 Add `schemas/product-hardening-evidence.schema.json` for `hideout.product-hardening-evidence/v1`
- [X] T002 Add `internal/productevidence/` package skeleton for manifest models, artifact refs, redaction status, and writer helpers
- [X] T003 Add `scripts/test-ui-e2e.sh` proof runner skeleton with `--browser`, `--tui`, `--all`, `--manifest-only`, `--out`, and `--require-executed` flags
- [X] T004 [P] Add `test/e2e/webui/` placeholder assets for browser proof driver and README note
- [X] T005 [P] Add `test/e2e/tui/` placeholder assets for terminal proof harness and README note
- [X] T006 Register product-hardening evidence schema validation in `scripts/test-gate0.sh` without requiring browser/PTY execution

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Core evidence and redaction behavior that MUST be complete before UI proof lanes.

**CRITICAL**: No user story proof work can be marked complete until this phase is complete.

- [X] T007 [P] Add manifest model and validation tests in `internal/productevidence/manifest_test.go`
- [X] T008 [P] Add schema round-trip tests for `schemas/product-hardening-evidence.schema.json` in `internal/productevidence/schema_test.go`
- [X] T009 [P] Add redaction canary tests for manifest fields and artifact summaries in `internal/productevidence/redaction_test.go`
- [X] T010 Implement manifest model, proof-entry validation, `not-run` validation, artifact ref normalization, and redaction checks in `internal/productevidence/manifest.go`
- [X] T011 Implement atomic manifest write/append helpers in `internal/productevidence/writer.go`
- [X] T012 Implement script-level manifest generation for `--manifest-only` and missing-prerequisite `not-run` modes in `scripts/test-ui-e2e.sh`
- [X] T013 Add claim id constants for 021 proof entries in `internal/productevidence/claims.go`
- [X] T014 Verify foundational behavior with `go test ./internal/productevidence` and `go run ./cmd/hideout-schema-validate schemas/product-hardening-evidence.schema.json <fixture>`

**Checkpoint**: Evidence manifest is reusable and can record pass/fail/not-run without browser or TUI work.

---

## Phase 3: User Story 1 - Prove WebUI Console In A Real Browser (Priority: P1) MVP

**Goal**: Open the served WebUI in a real browser, prove visible console state, live update, no hidden polling, notice acknowledgement, and auth refusal.

**Independent Test**: `scripts/test-ui-e2e.sh --browser --require-executed --out <tmp>` produces passed browser proof entries and redacted artifacts.

### Tests for User Story 1

- [X] T015 [P] [US1] Add browser prerequisite discovery and `not-run` contract tests in `test/e2e/webui/prereq_test.go`
- [X] T016 [P] [US1] Add WebUI browser page-load and required-panel test in `test/e2e/webui/webui_browser_test.go` with runtime assertions in `test/e2e/webui/proof.mjs`
- [X] T017 [P] [US1] Add browser live-event no-hidden-polling test in `test/e2e/webui/webui_browser_test.go` with runtime assertions in `test/e2e/webui/proof.mjs`
- [X] T018 [P] [US1] Add browser notice acknowledgement round-trip test covering token, payload, response, and visible state in `test/e2e/webui/webui_browser_test.go` with runtime assertions in `test/e2e/webui/proof.mjs`
- [X] T019 [P] [US1] Add wrong/missing token visible-refusal test in `test/e2e/webui/webui_browser_test.go` with runtime assertions in `test/e2e/webui/proof.mjs`
- [X] T020 [P] [US1] Add browser artifact redaction canary test in `test/e2e/webui/webui_redaction_test.go`

### Implementation for User Story 1

- [X] T021 [US1] Implement browser proof dependency discovery and `not-run` writer in `test/e2e/webui/prereq.go` and `scripts/test-ui-e2e.sh`
- [X] T022 [US1] Implement local daemon/WebUI proof fixture startup and cleanup in `test/e2e/webui/fixture.go`
- [X] T023 [US1] Implement browser driver for real page load, stable selectors, screenshot/DOM summary capture, and artifact hashing in `test/e2e/webui/browser.go` and `test/e2e/webui/proof.mjs`
- [X] T024 [US1] Implement live-event injection and hidden-polling instrumentation for browser proof in `test/e2e/webui/proof.mjs`
- [X] T025 [US1] Implement notice acknowledgement fixture and browser action round-trip verification in `test/e2e/webui/fixture.go` and `test/e2e/webui/proof.mjs`
- [X] T026 [US1] Wire browser proof lane into `scripts/test-ui-e2e.sh` and write proof ids `021.webui.browser.*`
- [X] T027 [US1] Update `docs/tui-webui-experience.md` with the browser E2E boundary and no product browser-control claim

**Checkpoint**: WebUI browser proof is independently runnable and cannot pass via static grep or reducer-only proof.

---

## Phase 4: User Story 2 - Prove TUI Console In A Real Terminal Process (Priority: P2)

**Goal**: Launch the real `hideout tui` process under a terminal harness and prove visible output, live event update, no hidden interval polling, and fallback state.

**Independent Test**: `scripts/test-ui-e2e.sh --tui --require-executed --out <tmp>` produces passed TUI proof entries and redacted terminal artifacts.

### Tests for User Story 2

- [X] T028 [P] [US2] Add PTY prerequisite discovery and `not-run` contract tests in `test/e2e/tui/prereq_test.go`
- [X] T029 [P] [US2] Add real `hideout tui` process launch and required-section output test in `test/e2e/tui/tui_process_test.go`
- [X] T030 [P] [US2] Add TUI live-event no-interval-polling test in `test/e2e/tui/tui_process_test.go`
- [X] T031 [P] [US2] Add stream closure or credential invalidation visible fallback test in `test/e2e/tui/tui_process_test.go`
- [X] T032 [P] [US2] Add terminal artifact redaction canary test in `test/e2e/tui/tui_process_test.go`

### Implementation for User Story 2

- [X] T033 [US2] Implement PTY or terminal-process harness for the real `hideout tui` command in `test/e2e/tui/harness.go`
- [X] T034 [US2] Implement daemon event source or deterministic event seam that still exercises the real TUI process in `test/e2e/tui/harness.go`
- [X] T035 [US2] Implement terminal capture parsing, stable section assertions, and artifact hashing in `test/e2e/tui/harness.go`
- [X] T036 [US2] Implement hidden polling detection for healthy-stream TUI proof in `test/e2e/tui/harness.go`
- [X] T037 [US2] Wire TUI proof lane into `scripts/test-ui-e2e.sh` and write proof ids `021.tui.pty.*`
- [X] T038 [US2] Update `docs/tui-webui-experience.md` with the terminal E2E boundary and render-test non-claim

**Checkpoint**: TUI proof launches a real command process and cannot be satisfied by renderer unit tests alone.

---

## Phase 5: User Story 3 - Record Reusable Product-Hardening Evidence (Priority: P3)

**Goal**: Produce stable, redacted, claim-mapped evidence entries that 022-025 and release-readiness tooling can consume without overclaiming.

**Independent Test**: Full UI proof writes a schema-valid manifest with stable proof ids, covered claims, artifacts, redaction status, and correct pass/fail/not-run semantics.

### Tests for User Story 3

- [X] T039 [P] [US3] Add full manifest aggregation test for browser and TUI lanes in `internal/productevidence/aggregate_test.go`
- [X] T040 [P] [US3] Add `--require-executed` failure test when browser or TUI lanes are `not-run` in `internal/productevidence/quickstart_test.go`
- [X] T041 [P] [US3] Add docs overclaim scan for browser E2E, terminal E2E, reducer harness, fixture server, local-fast, and release-readiness distinctions in `scripts/test-ui-e2e.sh`
- [X] T042 [P] [US3] Add quickstart scenario validation test for evidence manifest examples in `internal/productevidence/quickstart_test.go`

### Implementation for User Story 3

- [X] T043 [US3] Implement proof aggregation and covered-claim mapping for all 021 proof ids in `internal/productevidence/aggregate.go`
- [X] T044 [US3] Implement `--require-executed` semantics in `scripts/test-ui-e2e.sh`
- [X] T045 [US3] Add product-hardening evidence consumption note to `docs/privacy-run-test-plan.md`
- [X] T046 [US3] Update `docs/STATUS.md` with UI E2E proof status and explicit local-only/release-readiness boundary
- [X] T047 [US3] Validate all quickstart scenarios in `specs/021-ui-e2e-proof/quickstart.md`

**Checkpoint**: 021 proof output is reusable by 022-025 and cannot mark skipped proof lanes as passed.

---

## Phase 6: Polish & Cross-Cutting Concerns

**Purpose**: Final cleanup, docs alignment, and verification.

- [X] T048 [P] Update `specs/021-ui-e2e-proof/spec.md` if implementation choices refine proof ids, harness names, or prerequisite behavior
- [X] T049 [P] Update `specs/021-ui-e2e-proof/contracts/product-hardening-evidence.md` with final schema field names
- [X] T050 [P] Update `specs/021-ui-e2e-proof/contracts/ui-e2e-proof.md` with final browser/PTY provider details
- [X] T051 [P] Update `.tmp/021-025-product-hardening-plan.md` if 021 final evidence manifest choices affect 022-025
- [X] T052 Run `npx --yes markdownlint-cli2 specs/021-ui-e2e-proof/**/*.md docs/STATUS.md docs/privacy-run-test-plan.md docs/tui-webui-experience.md`
- [X] T053 Run `go test ./...`
- [X] T054 Run `scripts/test-ui-e2e.sh --all --require-executed --out <tmp>` on a host with browser and PTY prerequisites
- [X] T055 Run `scripts/test-gate0.sh`
- [X] T056 Run `go build ./...`, `go vet ./...`, `gofmt -l internal test`, and `git diff --check`

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies.
- **Foundational (Phase 2)**: Depends on Setup and blocks all user stories.
- **US1 WebUI Browser Proof (Phase 3)**: Depends on Foundational.
- **US2 TUI Terminal Proof (Phase 4)**: Depends on Foundational.
- **US3 Evidence Reuse (Phase 5)**: Depends on Foundational and integrates US1/US2 lane outputs.
- **Polish (Phase 6)**: Depends on selected user stories being complete.

### User Story Dependencies

- **US1 (P1)**: MVP after Foundational; independent browser proof lane.
- **US2 (P2)**: Can start after Foundational; independent terminal proof lane.
- **US3 (P3)**: Can start after Foundational for manifest aggregation basics, but final acceptance depends on US1 and US2 proof outputs.

### Parallel Opportunities

- T004 and T005 can run in parallel.
- T007, T008, and T009 can run in parallel before T010.
- US1 tests T015-T020 can run in parallel.
- US2 tests T028-T032 can run in parallel.
- US3 tests T039-T042 can run in parallel.
- Documentation tasks T048-T051 can run in parallel after implementation stabilizes.

---

## Parallel Example: User Story 1

```text
Task: "T016 [US1] Add WebUI browser page-load and required-panel test in test/e2e/webui/webui_browser_test.go"
Task: "T017 [US1] Add browser live-event no-hidden-polling runtime assertions in test/e2e/webui/proof.mjs"
Task: "T018 [US1] Add browser notice acknowledgement round-trip runtime assertions in test/e2e/webui/proof.mjs"
Task: "T019 [US1] Add wrong/missing token visible-refusal runtime assertions in test/e2e/webui/proof.mjs"
```

## Parallel Example: User Story 2

```text
Task: "T029 [US2] Add real hideout tui process launch and required-section output test in test/e2e/tui/tui_process_test.go"
Task: "T030 [US2] Add TUI live-event no-interval-polling assertions in test/e2e/tui/harness.go"
Task: "T031 [US2] Add stream closure visible fallback assertions in test/e2e/tui/harness.go"
Task: "T032 [US2] Add terminal artifact redaction canary test in test/e2e/tui/tui_process_test.go"
```

---

## Implementation Strategy

### MVP First

1. Complete Setup and Foundational phases.
2. Complete US1 browser proof.
3. Run `scripts/test-ui-e2e.sh --browser --require-executed --out <tmp>`.
4. Stop and review evidence before implementing US2/US3.

### Incremental Delivery

1. Foundation: schema, manifest writer, redaction, not-run behavior.
2. US1: browser E2E with notice acknowledgement.
3. US2: TUI E2E with real process output.
4. US3: aggregation, docs truth, and 022-025 evidence reuse.
5. Polish: docs, Gate 0, full verification.

### Completion Guard

021 is not complete until `scripts/test-ui-e2e.sh --all --require-executed --out <tmp>` runs on a host with browser and PTY prerequisites and produces passed, schema-valid, redacted browser and terminal proof entries.
