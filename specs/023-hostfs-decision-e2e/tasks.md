# Tasks: HostFS And Decision E2E

<!-- markdownlint-disable MD013 -->

**Input**: Design documents from `/specs/023-hostfs-decision-e2e/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

**Tests**: Required. 023 exists to prove sensitive HostFS mutation and decision
semantics end to end; green unit tests alone are insufficient.

**Organization**: Tasks are grouped by user story. US1 is the MVP because it
proves the core staged-write contract. US2 and US3 add decision-center
concurrency/outcomes and visibility/redaction.

**Traceability**:

- **US1**: FR-001, FR-002, FR-003, FR-004, FR-005, FR-009, FR-010,
  FR-012, FR-013, SC-002, SC-003, SC-005, SC-007.
- **US2**: FR-007, FR-008, FR-009, SC-001, SC-004.
- **US3**: FR-006, FR-011, FR-014, SC-001, SC-006.
- **Polish**: SC-008 and cross-doc status/test-plan alignment.

## Phase 1: Setup

**Purpose**: Establish stable evidence vocabulary and script shell.

- [X] T001 Add 023 proof ID constants and covered-claim helpers in `internal/productevidence/claims.go`
- [X] T002 [P] Add 023 required local-fast proof aggregation tests in `internal/productevidence/aggregate_test.go`
- [X] T003 [P] Add 023 schema fixture tests in `internal/productevidence/schema_test.go`
- [X] T004 Create `scripts/test-hostfs-decision-e2e.sh` with argument parsing for `--local-fast`, `--real-gate2`, `--require-real`, `--operation`, and `--out`
- [X] T005 [P] Add quickstart test coverage for 023 proof IDs in `internal/productevidence/quickstart_test.go`

---

## Phase 2: Foundational

**Purpose**: Shared evidence, fixture, redaction, and coverage helpers.

**CRITICAL**: No story can pass until these helpers exist.

- [X] T006 Implement 023 product-hardening evidence helper functions in `internal/productevidence/hostfs_decision.go`
- [X] T007 Implement required 023 local-fast completeness validation in `internal/productevidence/aggregate.go`
- [X] T008 Implement manifest writer and schema validation helpers in `scripts/test-hostfs-decision-e2e.sh`
- [X] T009 Implement redaction scanner for public artifacts in `scripts/test-hostfs-decision-e2e.sh`
- [X] T010 Implement HostFS operation coverage matrix writer in `scripts/test-hostfs-decision-e2e.sh`
- [X] T011 Implement temp store, fixture root, and artifact cleanup/preservation helpers in `scripts/test-hostfs-decision-e2e.sh`
- [X] T012 Add shell syntax and evidence content self-checks for `scripts/test-hostfs-decision-e2e.sh`

**Checkpoint**: The script can write schema-valid failed/not-run evidence with
an operation coverage matrix before running HostFS or decision logic.

---

## Phase 3: User Story 1 - Prove Staged HostFS Write Approval (P1) MVP

**Goal**: Prove staged write semantics and apply/conflict behavior without
mutating host lower state before operator approval.

**Independent Test**:
`scripts/test-hostfs-decision-e2e.sh --local-fast --out <tmp>` writes passing
local lifecycle evidence, and `scripts/test-hostfs-decision-e2e.sh --real-gate2
--out <tmp>` writes either real pass evidence or explicit not-run evidence.

### Tests for User Story 1

- [X] T013 [P] [US1] Add local-fast lifecycle proof test in `internal/productevidence/aggregate_test.go`
- [X] T014 [P] [US1] Add overlay staged-view unit coverage for create/replace/append/truncate/mkdir/delete/rename/chmod/chown in `internal/hostfs/overlay/overlay_test.go`
- [X] T015 [P] [US1] Add conflict apply fail-closed test in `internal/hostfs/overlay/overlay_test.go`
- [X] T016 [P] [US1] Add script assertions that host lower remains unchanged before apply in `scripts/test-hostfs-decision-e2e.sh`
- [X] T017 [P] [US1] Add script assertions that real Gate 2 pass requires guest-read staged evidence in `scripts/test-hostfs-decision-e2e.sh`
- [X] T018 [P] [US1] Add operation coverage matrix assertions in `scripts/test-hostfs-decision-e2e.sh`

### Implementation for User Story 1

- [X] T019 [US1] Implement local-fast replace lifecycle fixture in `scripts/test-hostfs-decision-e2e.sh`
- [X] T020 [US1] Implement local-fast broader operation coverage matrix in `scripts/test-hostfs-decision-e2e.sh`
- [X] T021 [US1] Implement local-fast conflict/stale lower mutation fixture in `scripts/test-hostfs-decision-e2e.sh`
- [X] T022 [US1] Implement real Gate 2 prerequisite detection and not-run evidence in `scripts/test-hostfs-decision-e2e.sh`
- [X] T023 [US1] Extend or wrap `scripts/test-gate2-lima.sh` HostFS write output to produce 023 real Gate 2 evidence artifacts
- [X] T024 [US1] Ensure real Gate 2 mode never falls back to native/local-fast pass evidence in `scripts/test-hostfs-decision-e2e.sh`

**Checkpoint**: US1 is complete when local-fast lifecycle/conflict proof passes
and real Gate 2 mode either passes with guest/host assertions or records
explicit not-run evidence.

---

## Phase 4: User Story 2 - Prove Decision Center Concurrency And Outcomes (P2)

**Goal**: Prove generic decision claim/resolve/timeout behavior used by HostFS
approval workflows.

**Independent Test**:
The local-fast script creates deterministic decisions, proves one claim winner
and one loser, resolves approve/deny paths, simulates or expires timeout
default-deny, and writes passing evidence.

### Tests for User Story 2

- [X] T025 [P] [US2] Add cross-store claim race test for generic decisions in `internal/decision/store_test.go`
- [X] T026 [P] [US2] Add Manager claim/resolve parity test for HostFS and generic decisions in `internal/manager/decisions_test.go`
- [X] T027 [P] [US2] Add timeout/default-deny test that proves no provider side effect in `internal/decision/store_test.go`
- [X] T028 [P] [US2] Add script assertion for exactly one winning claimant in `scripts/test-hostfs-decision-e2e.sh`
- [X] T029 [P] [US2] Add script assertion for deny and timeout audit/status artifacts in `scripts/test-hostfs-decision-e2e.sh`

### Implementation for User Story 2

- [X] T030 [US2] Implement generic decision fixture creation in `scripts/test-hostfs-decision-e2e.sh`
- [X] T031 [US2] Implement two-client claim winner/loser proof in `scripts/test-hostfs-decision-e2e.sh`
- [X] T032 [US2] Implement approve and deny resolution proof in `scripts/test-hostfs-decision-e2e.sh`
- [X] T033 [US2] Implement timeout/default-deny proof in `scripts/test-hostfs-decision-e2e.sh`
- [X] T034 [US2] Add local audit/status artifact references for decision outcomes in `scripts/test-hostfs-decision-e2e.sh`

**Checkpoint**: US2 is complete when decision concurrency and outcomes are
independently proven by local-fast evidence.

---

## Phase 5: User Story 3 - Prove Visibility Without New Approval Surfaces (P3)

**Goal**: Prove pending and resolved HostFS/decision state is visible through
existing surfaces without leaking private material or adding UI authority.

**Independent Test**:
The local-fast script generates a pending decision and verifies CLI/API,
WebUI-model, and TUI-model artifacts agree on id/state and omit private data.

### Tests for User Story 3

- [X] T035 [P] [US3] Add liveconsole reducer visibility test for HostFS write and decision rows in `internal/liveconsole/reducer_test.go`
- [X] T036 [P] [US3] Add Manager list/inspect redaction test for claim tokens and provider refs in `internal/manager/decisions_test.go`
- [X] T037 [P] [US3] Add script redaction injection assertion for claim tokens, `hfwobj_`, provider refs, and `HIDEOUT_SECRET_*` in `scripts/test-hostfs-decision-e2e.sh`
- [X] T038 [P] [US3] Add script assertion that UI/TUI artifacts are model visibility proof, not browser click proof, in `scripts/test-hostfs-decision-e2e.sh`

### Implementation for User Story 3

- [X] T039 [US3] Implement CLI/API decision visibility artifact capture in `scripts/test-hostfs-decision-e2e.sh`
- [X] T040 [US3] Implement WebUI-model and TUI-model artifact capture using existing liveconsole state in `scripts/test-hostfs-decision-e2e.sh`
- [X] T041 [US3] Implement public artifact redaction scan and failure reporting in `scripts/test-hostfs-decision-e2e.sh`
- [X] T042 [US3] Write visibility and redaction product-hardening proof entries in `scripts/test-hostfs-decision-e2e.sh`

**Checkpoint**: US3 is complete when every public visibility artifact agrees on
decision state and passes redaction scanning.

---

## Phase 6: Polish & Cross-Cutting

**Purpose**: Wire proof into normal gates and docs.

- [X] T043 [P] Update `docs/privacy-run-test-plan.md` with 023 local-fast versus real Gate 2 proof boundaries
- [X] T044 [P] Update `docs/STATUS.md` with HostFS/decision E2E evidence status and non-release boundary
- [X] T045 [P] Update `docs/hostfs-overlay-design.md` if proof scope or operation coverage language changed
- [X] T046 Add `scripts/test-hostfs-decision-e2e.sh --local-fast` to `scripts/test-gate0.sh`
- [X] T047 Run `npx --yes markdownlint-cli2 specs/023-hostfs-decision-e2e/**/*.md docs/privacy-run-test-plan.md docs/STATUS.md docs/hostfs-overlay-design.md`
- [X] T048 Run `go test ./internal/productevidence ./internal/hostfs/overlay ./internal/decision ./internal/manager ./internal/liveconsole`
- [X] T049 Run `scripts/test-hostfs-decision-e2e.sh --local-fast --out <tmp>` and `scripts/test-hostfs-decision-e2e.sh --real-gate2 --out <tmp>`
- [X] T050 Run final battery: `go build ./...`, `go vet ./...`, `gofmt -l internal test`, `git diff --check`, `go test ./...`, and `scripts/test-gate0.sh`

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: no dependencies.
- **Foundational (Phase 2)**: depends on Setup and blocks all stories.
- **US1 (Phase 3)**: MVP, depends on Foundational.
- **US2 (Phase 4)**: depends on Foundational and may reuse decision fixtures
  from US1.
- **US3 (Phase 5)**: depends on Foundational and may reuse US1/US2 artifacts.
- **Polish (Phase 6)**: depends on selected story completion.

### Story Dependencies

- **US1**: first MVP; proves HostFS staged write lifecycle and conflict.
- **US2**: independent decision-center proof; can run after shared helpers.
- **US3**: visibility/redaction proof; depends on decision fixtures from US1/US2.

### Parallel Opportunities

- T002, T003, and T005 can run in parallel.
- T013-T018 can be prepared in parallel.
- T025-T029 can be prepared in parallel.
- T035-T038 can be prepared in parallel.
- Polish docs T043-T045 can run in parallel.

## Parallel Example: US1

```text
Task: "T014 [US1] Add overlay staged-view unit coverage"
Task: "T016 [US1] Add host lower unchanged script assertion"
Task: "T018 [US1] Add operation coverage matrix assertions"
```

## Implementation Strategy

### MVP First

1. Complete Setup and Foundational tasks.
2. Complete US1 local-fast lifecycle and conflict proof.
3. Validate with `scripts/test-hostfs-decision-e2e.sh --local-fast --out <tmp>`.
4. Add real Gate 2 not-run/pass mode without allowing native fallback.

### Incremental Delivery

1. US1 proves staged HostFS lifecycle.
2. US2 proves decision claim/resolve/timeout semantics.
3. US3 proves visibility and redaction.
4. Polish wires 023 into Gate 0 and docs.
