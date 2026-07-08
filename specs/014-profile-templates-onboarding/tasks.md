# Tasks: Profile Templates And Onboarding

<!-- markdownlint-disable MD013 -->

**Input**: Design artifacts from `specs/014-profile-templates-onboarding/`

## Phase 1: Setup

- [X] T001 Create `internal/profiletemplate/doc.go` package skeleton for built-in template catalog, request validation, evidence summaries, and review text
- [X] T002 Add `schemas/onboarding-evidence.schema.json` for `hideout.onboarding-evidence/v1`
- [X] T003 [P] Add `scripts/test-onboarding-smoke.sh` skeleton covering privacy, hardened enforced, hardened degraded denial, dev/debug, evidence schema, and docs checks
- [X] T004 [P] Register onboarding smoke and evidence schema checks in `scripts/test-gate0.sh`

## Phase 2: Foundational

- [X] T005 Implement `internal/profiletemplate` data types for Template, Request, PrivilegeFact, RenderedProfile, EvidenceSummary, and Review
- [X] T006 Implement static catalog for `privacy`, `hardened`, `dev`, and `debug` with `privacy` as the only recommended template for FR-001/FR-002
- [X] T007 [P] Add catalog invariant tests proving exactly four templates, one recommendation, zero HostFS grants, zero command adapters, and schema-valid rendered profiles for FR-003/FR-009/FR-010/SC-001/SC-002
- [X] T008 Implement request validation for explicit non-interactive choices, network/proxy/resolver combinations, existing profile collisions, and unknown template ids
- [X] T009 [P] Add request validation tests for FR-011, FR-016, and network fail-closed behavior
- [X] T010 Implement hardened privilege decision rules for enforced, degraded, unknown, and explicit degraded fallback
- [X] T011 [P] Add hardened privilege tests for SC-003 and SC-004
- [X] T012 Implement deterministic evidence summary rendering and control-plane redaction
- [X] T013 [P] Add evidence schema/redaction tests with injected control-plane-looking values for FR-015 and SC-007

## Phase 3: User Story 1 - Privacy Recommended Profile (P1)

**Goal**: Non-interactive `privacy` onboarding creates a usable privacy profile with no default HostFS grants or adapter-pack bindings.

**Independent Test**: Run the manager/app non-interactive path with explicit privacy flags and inspect profile plus evidence.

- [X] T014 [P] [US1] Add `internal/inittask` tests proving privacy template plan/apply creates `tun2socks` profile, mediated resolver, metadata, evidence path, and next steps for FR-004/FR-014
- [X] T015 [P] [US1] Add `internal/app` CLI test for `hideout init --template privacy --no-input` output and no secret-value leakage for FR-015/SC-007
- [X] T016 [US1] Extend `inittask.Options` and `Plan` with template, mediated resolver, privilege fact, degraded fallback, warnings, non-claims, and evidence path fields
- [X] T017 [US1] Modify `PlanMachine` to call `profiletemplate` validation/rendering before task creation and to fail before tasks on invalid onboarding requests
- [X] T018 [US1] Modify `applyTask` profile creation/network selection so template-rendered profile settings and metadata are applied through the existing InitTask path
- [X] T019 [US1] Write onboarding evidence only after successful apply and include init audit path when available
- [X] T020 [US1] Extend `hideout init` parsing with `--template`, `--mediated-resolver`, `--privilege-status`, and `--allow-degraded-template`
- [X] T021 [US1] Extend Manager init API request/handler with template fields and add CLI/Manager parity tests
- [X] T022 [US1] Update init success output to include template, effective posture, evidence path, and next steps
- [X] T023 [US1] Expand onboarding smoke privacy path and schema validation

## Phase 4: User Story 2 - Hardened Enforced Only (P2)

**Goal**: Hardened succeeds only with enforced privilege facts and degraded fallback is explicit and visibly marked.

**Independent Test**: Run hardened enforced, degraded denial, unknown denial, and explicit degraded fallback without real Lima.

- [X] T024 [P] [US2] Add `internal/inittask` tests for hardened enforced success and degraded/unknown fail-before-profile for FR-005/SC-003
- [X] T025 [P] [US2] Add `internal/app` CLI tests for hardened error guidance and explicit degraded fallback output for FR-006/SC-004
- [X] T026 [US2] Wire hardened failure guidance to recreate/base-image/no-sudo help without creating profile/evidence
- [X] T027 [US2] Render explicit degraded fallback metadata/evidence with `templateDegraded=true` and no hardened boundary claim
- [X] T028 [US2] Expand onboarding smoke hardened enforced, hardened degraded denial, and degraded fallback checks

## Phase 5: User Story 3 - CI Non-Interactive Profiles (P3)

**Goal**: CI can create privacy/dev/debug with explicit choices and missing choices fail closed.

**Independent Test**: Run `--no-input` with complete and incomplete flag sets.

- [X] T029 [P] [US3] Add missing-choice tests for every required non-interactive flag and assert no profile/evidence files for FR-011/SC-005
- [X] T030 [P] [US3] Add dev/debug non-interactive tests proving weaker posture labels and no privacy/hardened claims for FR-007/FR-008
- [X] T031 [US3] Track whether CLI flags were explicitly supplied so defaults do not satisfy `--no-input`
- [X] T032 [US3] Ensure dev/debug evidence labels native backend and direct network as weak/local when selected
- [X] T033 [US3] Expand onboarding smoke dev/debug and missing-choice paths

## Phase 6: User Story 4 - Interactive Onboarding (P4)

**Goal**: TTY onboarding recommends privacy, shows high-impact defaults, and requires confirmation before profile creation.

**Independent Test**: Simulate stdin/stdout for confirm and cancel paths.

- [X] T034 [P] [US4] Add `profiletemplate` review tests proving prompt text names recommendation, HostFS default, adapter-pack default, privilege requirement, and non-claims for FR-012
- [X] T035 [P] [US4] Add `internal/app` interactive cancel test proving no profile and no evidence summary for FR-013/SC-006
- [X] T036 [P] [US4] Add `internal/app` interactive confirm test proving generated profile equals equivalent non-interactive request
- [X] T037 [US4] Add stdin-aware interactive review/confirmation support to `internal/app` while keeping `Main(args, stdout, stderr)` compatibility
- [X] T038 [US4] Ensure dry-run prints the onboarding plan and writes no profile/evidence

## Phase 7: Documentation And Polish

- [X] T039 [P] Update `README.md` recommended first-run command and advanced customization command for FR-017/SC-009
- [X] T040 [P] Update `docs/README.md`, `docs/STATUS.md`, and first-run docs for 014 template/onboarding status
- [X] T041 [P] Update `docs/privacy-run-test-plan.md` with onboarding Gate 0 smoke and no-real-Lima rationale
- [X] T042 [P] Add docs assertions to `scripts/test-onboarding-smoke.sh` for recommended and advanced commands for SC-008/SC-009
- [X] T043 Run markdownlint for README, docs, and `specs/014-profile-templates-onboarding/**/*.md`
- [X] T044 Run `go build ./...`, `go vet ./...`, `gofmt -l internal cmd`, `git diff --check`, and `go test ./...`
- [X] T045 Run `scripts/test-onboarding-smoke.sh` and `scripts/test-gate0.sh`
- [X] T046 Mark all completed 014 tasks as checked in `specs/014-profile-templates-onboarding/tasks.md`

## Dependencies

- Setup and Foundational tasks block all stories.
- US1 is the MVP and must land before US2/US3 because it wires template plan/apply and evidence.
- US2 depends on foundational privilege decision rules and US1 apply wiring.
- US3 depends on CLI explicit-flag tracking from US1.
- US4 depends on shared review/evidence builders from foundational work.
- Documentation and polish run after command names and evidence format stabilize.

## Parallel Opportunities

- T003/T004 can run after T001/T002.
- T007, T009, T011, and T013 can run in parallel after T005/T006.
- T014 and T015 can run in parallel before implementation.
- T024 and T025 can run in parallel.
- T029 and T030 can run in parallel.
- T034, T035, and T036 can run in parallel after review contracts stabilize.
- T039/T040/T041/T042 can run in parallel after CLI output stabilizes.

## Implementation Strategy

MVP first: complete US1 so packaged alpha users can run one explicit privacy
onboarding command and receive a valid profile plus evidence. Then add hardened
enforced-only behavior, CI failure coverage, interactive confirmation, docs,
and Gate 0 smoke.
