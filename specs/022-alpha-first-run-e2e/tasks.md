# Tasks: Alpha First-Run E2E

<!-- markdownlint-disable MD013 -->

**Input**: Design documents from `/specs/022-alpha-first-run-e2e/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

**Tests**: Required. 022 touches package/install lifecycle, profile init,
backend proof labeling, audit/Boundary evidence, and release-adjacent evidence.

**Organization**: Tasks are grouped by user story so the local-fast MVP can be
implemented and validated independently before failure fixtures and real-backend
proof mode.

## Phase 1: Setup

**Purpose**: Establish proof IDs, script shell, and docs contract anchors.

- [X] T001 Add 022 proof ID constants and covered-claim helpers in `internal/productevidence/claims.go`
- [X] T002 [P] Add 022 proof completeness tests in `internal/productevidence/aggregate_test.go`
- [X] T003 Create `scripts/test-first-run-e2e.sh` with argument parsing for `--local-fast`, `--real-backend`, `--require-real`, `--fixture`, `--out`, and `--package`
- [X] T004 [P] Add first-run docs order fixture assertions in `scripts/test-first-run-docs-smoke.sh`
- [X] T005 [P] Add 022 evidence contract fixture in `internal/productevidence/schema_test.go`

---

## Phase 2: Foundational

**Purpose**: Shared script and evidence plumbing used by all stories.

**CRITICAL**: No story can pass until these helpers exist.

- [X] T006 Implement first-run evidence entry helpers in `internal/productevidence/first_run.go`
- [X] T007 Add product-evidence validation for required 022 local-fast proof IDs in `internal/productevidence/aggregate.go`
- [X] T008 Implement package build/extract helper functions in `scripts/test-first-run-e2e.sh`
- [X] T009 Implement installed-binary command runner and log capture helpers in `scripts/test-first-run-e2e.sh`
- [X] T010 Implement artifact checksum and redaction scan helpers in `scripts/test-first-run-e2e.sh`
- [X] T011 Implement manifest write and schema validation helpers in `scripts/test-first-run-e2e.sh`
- [X] T012 Implement docs-order scanner that checks `./install.sh --skip-init` before explicit init in `scripts/test-first-run-e2e.sh`
- [X] T013 Add shell cleanup traps that preserve evidence on failure in `scripts/test-first-run-e2e.sh`

**Checkpoint**: The script can parse arguments, create an output directory, and
write failed/not-run schema-valid evidence without running a package.

---

## Phase 3: User Story 1 - Install Package And Complete First Run (P1) MVP

**Goal**: Prove package install to first command in local-fast mode without
claiming real isolation.

**Independent Test**:
`scripts/test-first-run-e2e.sh --local-fast --out <tmp>` validates schema,
installs with `--skip-init`, verifies the installed package, initializes one
weak/dev profile, runs one command, captures audit/Boundary evidence, and writes
passing local-fast proof entries.

### Tests for User Story 1

- [X] T014 [P] [US1] Add unit test for 022 local-fast proof aggregation in `internal/productevidence/aggregate_test.go`
- [X] T015 [P] [US1] Add script self-check for `--skip-init` install output in `scripts/test-first-run-e2e.sh`
- [X] T016 [P] [US1] Add installed-binary assertion that rejects `go run` or source-tree binary use in `scripts/test-first-run-e2e.sh`
- [X] T017 [P] [US1] Add audit and `Hideout boundary:` positive assertions in `scripts/test-first-run-e2e.sh`
- [X] T018 [P] [US1] Add evidence schema and redaction assertions for local-fast artifacts in `scripts/test-first-run-e2e.sh`
- [X] T019 [P] [US1] Add docs-order assertion that fails if `docs/first-run-alpha.md` omits `./install.sh --skip-init` in `scripts/test-first-run-docs-smoke.sh`

### Implementation for User Story 1

- [X] T020 [US1] Build or accept a package artifact in `scripts/test-first-run-e2e.sh`
- [X] T021 [US1] Extract package and run packaged `install.sh --skip-init` into a temp prefix/store in `scripts/test-first-run-e2e.sh`
- [X] T022 [US1] Run installed `hideout package verify <prefix>` plus support/doctor checks before any pass evidence in `scripts/test-first-run-e2e.sh`
- [X] T023 [US1] Initialize one local-fast weak/dev `default` profile with installed `hideout init` in `scripts/test-first-run-e2e.sh`
- [X] T024 [US1] Run one installed-binary native first command with explicit weak-harness labeling in `scripts/test-first-run-e2e.sh`
- [X] T025 [US1] Capture audit log output and Boundary output into referenced artifacts in `scripts/test-first-run-e2e.sh`
- [X] T026 [US1] Write local-fast proof entries with package identity and weak/native notes in `scripts/test-first-run-e2e.sh`
- [X] T027 [US1] Update `docs/first-run-alpha.md` install section to use `./install.sh --skip-init` before the explicit init step
- [X] T028 [US1] Update `internal/productevidence/quickstart_test.go` to require the 022 local-fast proof IDs when requested

**Checkpoint**: US1 is independently complete when local-fast first-run proof
passes and docs no longer contain the install/init collision.

---

## Phase 4: User Story 2 - Fail With Actionable Diagnostics (P2)

**Goal**: Prove common first-run failures are failed or `not-run`, never
misleading success.

**Independent Test**:
Run `scripts/test-first-run-e2e.sh --local-fast --fixture <name> --out <tmp>`
for each fixture and verify the affected proof is failed or `not-run`, with
diagnostic artifact references.

### Tests for User Story 2

- [X] T029 [P] [US2] Add bad checksum fixture assertion in `scripts/test-first-run-e2e.sh`
- [X] T030 [P] [US2] Add missing manifest fixture assertion in `scripts/test-first-run-e2e.sh`
- [X] T031 [P] [US2] Add missing package-owned helper fixture assertion in `scripts/test-first-run-e2e.sh`
- [X] T032 [P] [US2] Add stale obsolete package file fixture assertion in `scripts/test-first-run-e2e.sh`
- [X] T033 [P] [US2] Add duplicate profile fixture assertion in `scripts/test-first-run-e2e.sh`
- [X] T034 [P] [US2] Add unsafe workspace/store fixture assertion in `scripts/test-first-run-e2e.sh`
- [X] T035 [P] [US2] Add control-plane redaction injection assertion in `scripts/test-first-run-e2e.sh`

### Implementation for User Story 2

- [X] T036 [US2] Implement package corruption fixtures for checksum, manifest, and helper failures in `scripts/test-first-run-e2e.sh`
- [X] T037 [US2] Implement stale package upgrade fixture and repair hint capture in `scripts/test-first-run-e2e.sh`
- [X] T038 [US2] Implement duplicate profile precondition fixture in `scripts/test-first-run-e2e.sh`
- [X] T039 [US2] Implement unsafe workspace/store fixture using existing reserved-root checks in `scripts/test-first-run-e2e.sh`
- [X] T040 [US2] Ensure every fixture writes failed or `not-run` product-hardening evidence in `scripts/test-first-run-e2e.sh`
- [X] T041 [US2] Ensure failed fixture artifacts include actionable next-step text in `scripts/test-first-run-e2e.sh`

**Checkpoint**: US2 is independently complete when every fixture produces
non-passing evidence and no partial pass claim.

---

## Phase 5: User Story 3 - Distinguish Real Backend Proof (P3)

**Goal**: Make real Lima/privacy first-run proof explicit and prevent native
fallback from satisfying real claims.

**Independent Test**:
Run `scripts/test-first-run-e2e.sh --real-backend --out <tmp>` on a host
without prerequisites and verify `not-run`; run with `--require-real` and
verify missing prerequisites exit non-zero. On a capable host, a manual real run
may pass only after Lima/privacy execution.

### Tests for User Story 3

- [X] T042 [P] [US3] Add real-backend missing-prerequisite `not-run` assertion in `scripts/test-first-run-e2e.sh`
- [X] T043 [P] [US3] Add `--require-real` non-zero assertion for missing real prerequisites in `scripts/test-first-run-e2e.sh`
- [X] T044 [P] [US3] Add assertion that real-backend mode never passes through native fallback in `scripts/test-first-run-e2e.sh`
- [X] T045 [P] [US3] Add optional real-backend pass assertion gated by Lima/privacy prerequisites in `scripts/test-first-run-e2e.sh`

### Implementation for User Story 3

- [X] T046 [US3] Implement real-backend prerequisite detection for Lima, proxy secret, mediated resolver, and external `tun2socks` in `scripts/test-first-run-e2e.sh`
- [X] T047 [US3] Implement real-backend init path with installed `hideout init --template privacy --backend lima --network tun2socks` in `scripts/test-first-run-e2e.sh`
- [X] T048 [US3] Implement real-backend first command and audit/Boundary capture with installed binary in `scripts/test-first-run-e2e.sh`
- [X] T049 [US3] Write `022.first-run.real-backend` or `022.first-run.real-backend.not-run` evidence in `scripts/test-first-run-e2e.sh`
- [X] T050 [US3] Document real-backend not-run and require-real behavior in `docs/first-run-alpha.md`

**Checkpoint**: US3 is complete when real-backend mode is explicit, honest, and
cannot pass through native fallback.

---

## Phase 6: Polish & Cross-Cutting

**Purpose**: Wire the proof into normal gates and docs.

- [X] T051 [P] Update `docs/privacy-run-test-plan.md` with 022 first-run E2E proof boundaries and local-fast vs real-backend distinction
- [X] T052 [P] Update `docs/STATUS.md` with Alpha first-run E2E status and non-release boundary
- [X] T053 [P] Update `README.md` and `docs/README.md` if first-run docs links or commands changed
- [X] T054 Add `scripts/test-first-run-e2e.sh --local-fast` to `scripts/test-gate0.sh`
- [X] T055 Run `npx --yes markdownlint-cli2 specs/022-alpha-first-run-e2e/**/*.md docs/first-run-alpha.md docs/privacy-run-test-plan.md docs/STATUS.md README.md docs/README.md`
- [X] T056 Run `go test ./internal/productevidence ./...`
- [X] T057 Run final battery: `go build ./...`, `go vet ./...`, `gofmt -l internal test`, `git diff --check`, `scripts/test-first-run-e2e.sh --local-fast --out <tmp>`, and `scripts/test-gate0.sh`

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: no dependencies.
- **Foundational (Phase 2)**: depends on Setup and blocks all stories.
- **US1 (Phase 3)**: MVP, depends on Foundational.
- **US2 (Phase 4)**: depends on Foundational and may reuse US1 package helpers.
- **US3 (Phase 5)**: depends on Foundational and may reuse US1 package helpers.
- **Polish (Phase 6)**: depends on selected story completion.

### Story Dependencies

- **US1**: first MVP; independently proves local-fast package first-run.
- **US2**: can be implemented after shared script helpers; reuses evidence
  writer and package fixture helpers.
- **US3**: can be implemented after shared script helpers; real pass is host
  prerequisite-gated.

### Parallel Opportunities

- T002, T004, and T005 can run in parallel.
- US1 tests T014-T019 can be prepared in parallel.
- US2 fixture assertions T029-T035 can be prepared in parallel.
- US3 assertions T042-T045 can be prepared in parallel.
- Polish docs T051-T053 can run in parallel after behavior is stable.

## Parallel Example: US1

```text
Task: "T014 [US1] Add unit test for 022 local-fast proof aggregation"
Task: "T015 [US1] Add script self-check for --skip-init install output"
Task: "T017 [US1] Add audit and Boundary positive assertions"
```

## Implementation Strategy

### MVP First

1. Complete Setup and Foundational.
2. Complete US1.
3. Validate with `scripts/test-first-run-e2e.sh --local-fast --out <tmp>`.
4. Stop for review before adding fixture and real-backend complexity.

### Incremental Delivery

1. US1 proves package install to first command.
2. US2 proves failures cannot masquerade as success.
3. US3 proves real-backend mode is explicit and honest.
4. Polish wires 022 into Gate 0 and docs.
